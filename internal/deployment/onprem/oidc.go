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
}

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
	config   oauth2.Config
	verifier *oidc.IDTokenVerifier
	aead     cipher.AEAD
	clock    func() time.Time
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
		verifier: provider.Verifier(&oidc.Config{ClientID: config.ClientID}),
		aead:     aead,
		clock:    time.Now,
	}, nil
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
	url := a.config.AuthCodeURL(
		state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(verifier),
	)
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
