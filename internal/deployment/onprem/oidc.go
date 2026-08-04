package onprem

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const oidcFlowTTL = 10 * time.Minute

type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	FlowSecret   string
	// AuthorizationParameters are extra query parameters appended to the
	// authorization request. Standard OIDC needs none, but some providers
	// require one to select an authentication method: WorkOS AuthKit rejects
	// a request without provider=authkit as an invalid connection selector,
	// so login never reaches the sign-in page.
	AuthorizationParameters map[string]string
	// IdentitySource selects where the authenticated identity is read from.
	// Empty means the ID token, which is what OIDC requires. See
	// OIDCIdentitySourceTokenResponseUser for the exception.
	IdentitySource OIDCIdentitySource
}

// OIDCIdentitySource names where CompleteLogin reads the identity from.
type OIDCIdentitySource string

const (
	// OIDCIdentitySourceIDToken is the OIDC-conformant default: identity
	// comes from the verified ID token.
	OIDCIdentitySourceIDToken OIDCIdentitySource = ""
	// OIDCIdentitySourceTokenResponseUser reads identity from a "user"
	// object in the token response instead.
	//
	// This exists for providers that publish OIDC discovery but whose token
	// endpoint is not OIDC-conformant. WorkOS AuthKit is the case in hand:
	// it returns access_token, refresh_token, and a user object, never an
	// id_token, and its access token carries no email claim.
	//
	// It is deliberately opt-in. A provider that merely fails to return an
	// ID token must not silently degrade to this path, because the nonce
	// binding is lost with the ID token. That loss is acceptable only here:
	// the token response arrives over TLS directly from the token endpoint
	// rather than through the browser, so it is not attacker-controllable,
	// and nonce exists to stop a front-channel ID token replay that cannot
	// happen when there is no ID token. State and PKCE still bind the
	// exchange to this browser and this flow.
	OIDCIdentitySourceTokenResponseUser OIDCIdentitySource = "token_response_user"
)

type OIDCFlow struct {
	AuthorizationURL string
	CookieValue      string
	ExpiresAt        time.Time
}

type oidcFlowState struct {
	State        string    `json:"state"`
	Nonce        string    `json:"nonce"`
	PKCEVerifier string    `json:"pkce_verifier"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type OIDCAuthenticator struct {
	config              oauth2.Config
	verifier            *oidc.IDTokenVerifier
	aead                cipher.AEAD
	clock               func() time.Time
	authorizationExtras []oauth2.AuthCodeOption
	issuer              string
	identitySource      OIDCIdentitySource
}

// tokenResponseUser mirrors the user object providers such as WorkOS return
// alongside the tokens. Only the fields the identity contract needs are
// decoded.
type tokenResponseUser struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	FirstName     string `json:"first_name"`
	LastName      string `json:"last_name"`
}

// identityFromTokenResponse builds the identity from the token response's
// user object, for providers that never issue an ID token. See
// OIDCIdentitySourceTokenResponseUser for why this is safe and why it is
// opt-in.
func (a *OIDCAuthenticator) identityFromTokenResponse(token *oauth2.Token) (ExternalIdentity, error) {
	raw := token.Extra("user")
	if raw == nil {
		return ExternalIdentity{}, fmt.Errorf("exchange OIDC authorization code: token response has no user")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return ExternalIdentity{}, fmt.Errorf("encode token response user: %w", err)
	}
	var user tokenResponseUser
	if err := json.Unmarshal(encoded, &user); err != nil {
		return ExternalIdentity{}, fmt.Errorf("decode token response user: %w", err)
	}
	if strings.TrimSpace(user.ID) == "" {
		return ExternalIdentity{}, fmt.Errorf("exchange OIDC authorization code: token response user has no id")
	}
	return ExternalIdentity{
		Issuer: a.issuer, Subject: user.ID, Email: user.Email,
		EmailVerified: user.EmailVerified,
		DisplayName:   strings.TrimSpace(user.FirstName + " " + user.LastName),
	}, nil
}

func NewOIDCAuthenticator(ctx context.Context, config OIDCConfig) (*OIDCAuthenticator, error) {
	if strings.TrimSpace(config.Issuer) == "" || strings.TrimSpace(config.ClientID) == "" ||
		strings.TrimSpace(config.ClientSecret) == "" || strings.TrimSpace(config.RedirectURL) == "" ||
		strings.TrimSpace(config.FlowSecret) == "" {
		return nil, fmt.Errorf("create OIDC authenticator: issuer, client, redirect, and flow secret are required")
	}
	provider, err := oidc.NewProvider(ctx, config.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC provider: %w", err)
	}
	key := sha256.Sum256([]byte(config.FlowSecret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("create OIDC flow cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create OIDC flow AEAD: %w", err)
	}
	return &OIDCAuthenticator{
		config: oauth2.Config{
			ClientID: config.ClientID, ClientSecret: config.ClientSecret,
			Endpoint: provider.Endpoint(), RedirectURL: config.RedirectURL,
			Scopes: []string{oidc.ScopeOpenID, "profile", "email"},
		},
		verifier:            provider.Verifier(&oidc.Config{ClientID: config.ClientID}),
		aead:                aead,
		clock:               time.Now,
		authorizationExtras: authorizationExtras(config.AuthorizationParameters),
		issuer:              config.Issuer,
		identitySource:      config.IdentitySource,
	}, nil
}

// authorizationExtras converts the configured parameters into options, in a
// stable order so the authorization URL is deterministic for a given
// configuration.
func authorizationExtras(parameters map[string]string) []oauth2.AuthCodeOption {
	names := make([]string, 0, len(parameters))
	for name := range parameters {
		names = append(names, name)
	}
	sort.Strings(names)
	options := make([]oauth2.AuthCodeOption, 0, len(names))
	for _, name := range names {
		options = append(options, oauth2.SetAuthURLParam(name, parameters[name]))
	}
	return options
}

func (a *OIDCAuthenticator) BeginLogin() (OIDCFlow, error) {
	state, err := randomToken()
	if err != nil {
		return OIDCFlow{}, fmt.Errorf("create OIDC state: %w", err)
	}
	nonce, err := randomToken()
	if err != nil {
		return OIDCFlow{}, fmt.Errorf("create OIDC nonce: %w", err)
	}
	verifier := oauth2.GenerateVerifier()
	flow := oidcFlowState{
		State: state, Nonce: nonce, PKCEVerifier: verifier,
		ExpiresAt: a.clock().UTC().Add(oidcFlowTTL),
	}
	cookieValue, err := a.sealFlow(flow)
	if err != nil {
		return OIDCFlow{}, err
	}
	options := append(
		[]oauth2.AuthCodeOption{oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)},
		a.authorizationExtras...,
	)
	url := a.config.AuthCodeURL(state, options...)
	return OIDCFlow{AuthorizationURL: url, CookieValue: cookieValue, ExpiresAt: flow.ExpiresAt}, nil
}

func (a *OIDCAuthenticator) CompleteLogin(
	ctx context.Context,
	code string,
	state string,
	cookieValue string,
) (ExternalIdentity, error) {
	flow, err := a.openFlow(cookieValue)
	if err != nil {
		return ExternalIdentity{}, err
	}
	if a.clock().UTC().After(flow.ExpiresAt) || !secureStringEqual(state, flow.State) {
		return ExternalIdentity{}, ErrUnauthorized
	}
	token, err := a.config.Exchange(ctx, code, oauth2.VerifierOption(flow.PKCEVerifier))
	if err != nil {
		return ExternalIdentity{}, fmt.Errorf("exchange OIDC authorization code: %w", err)
	}
	if a.identitySource == OIDCIdentitySourceTokenResponseUser {
		return a.identityFromTokenResponse(token)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return ExternalIdentity{}, fmt.Errorf("exchange OIDC authorization code: ID token is missing")
	}
	idToken, err := a.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return ExternalIdentity{}, fmt.Errorf("verify OIDC ID token: %w", err)
	}
	var claims struct {
		Nonce         string `json:"nonce"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return ExternalIdentity{}, fmt.Errorf("decode OIDC ID token claims: %w", err)
	}
	if !secureStringEqual(claims.Nonce, flow.Nonce) {
		return ExternalIdentity{}, ErrUnauthorized
	}
	return ExternalIdentity{
		Issuer: idToken.Issuer, Subject: idToken.Subject, Email: claims.Email,
		EmailVerified: claims.EmailVerified, DisplayName: claims.Name,
	}, nil
}

func (a *OIDCAuthenticator) sealFlow(flow oidcFlowState) (string, error) {
	plaintext, err := json.Marshal(flow)
	if err != nil {
		return "", fmt.Errorf("encode OIDC flow: %w", err)
	}
	nonce := make([]byte, a.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("create OIDC flow nonce: %w", err)
	}
	sealed := a.aead.Seal(nonce, nonce, plaintext, nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (a *OIDCAuthenticator) openFlow(value string) (oidcFlowState, error) {
	sealed, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(sealed) < a.aead.NonceSize() {
		return oidcFlowState{}, ErrUnauthorized
	}
	nonce := sealed[:a.aead.NonceSize()]
	plaintext, err := a.aead.Open(nil, nonce, sealed[a.aead.NonceSize():], nil)
	if err != nil {
		return oidcFlowState{}, ErrUnauthorized
	}
	var flow oidcFlowState
	if err := json.Unmarshal(plaintext, &flow); err != nil {
		return oidcFlowState{}, ErrUnauthorized
	}
	return flow, nil
}

func secureStringEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
