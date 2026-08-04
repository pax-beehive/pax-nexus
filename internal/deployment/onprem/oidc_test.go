package onprem_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/stretchr/testify/suite"
)

type oidcSuite struct {
	suite.Suite
	provider *httptest.Server
	// tokenResponse, when set, is what the stub token endpoint returns, so a
	// test can reproduce a provider's exact response shape.
	tokenResponse map[string]any
}

func TestOIDCSuite(t *testing.T) {
	suite.Run(t, new(oidcSuite))
}

func (s *oidcSuite) SetupTest() {
	s.tokenResponse = nil
	var issuer string
	s.provider = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/token" {
			if s.tokenResponse == nil {
				http.Error(response, "no token response configured", http.StatusBadRequest)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(response).Encode(s.tokenResponse); err != nil {
				http.Error(response, "encode token", http.StatusInternalServerError)
			}
			return
		}
		if request.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(response).Encode(map[string]any{
			"issuer": issuer, "authorization_endpoint": issuer + "/authorize",
			"token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		}); err != nil {
			http.Error(response, "encode discovery", http.StatusInternalServerError)
		}
	}))
	issuer = s.provider.URL
}

func (s *oidcSuite) TearDownTest() {
	if s.provider != nil {
		s.provider.Close()
	}
}

func (s *oidcSuite) TestAuthorizationFlowUsesStateNonceAndPKCE() {
	authenticator, err := onprem.NewOIDCAuthenticator(context.Background(), onprem.OIDCConfig{
		Issuer: s.provider.URL, ClientID: "client", ClientSecret: "secret",
		RedirectURL: "https://portal.example/callback", FlowSecret: "flow-secret",
	})
	s.Require().NoError(err)
	flow, err := authenticator.BeginLogin()
	s.Require().NoError(err)
	s.NotEmpty(flow.CookieValue)
	parsed, err := url.Parse(flow.AuthorizationURL)
	s.Require().NoError(err)
	s.Equal(s.provider.URL+"/authorize", parsed.Scheme+"://"+parsed.Host+parsed.Path)
	s.NotEmpty(parsed.Query().Get("state"))
	s.NotEmpty(parsed.Query().Get("nonce"))
	s.Equal("S256", parsed.Query().Get("code_challenge_method"))
	s.NotEmpty(parsed.Query().Get("code_challenge"))

	_, err = authenticator.CompleteLogin(
		context.Background(), "code", "wrong-state", flow.CookieValue,
	)
	s.Require().ErrorIs(err, onprem.ErrUnauthorized)
	_, err = authenticator.CompleteLogin(context.Background(), "code", parsed.Query().Get("state"), "tampered")
	s.Require().ErrorIs(err, onprem.ErrUnauthorized)
}

// TestAuthorizationParametersAreForwarded covers providers that require an
// extra authorization parameter the standard OIDC set does not carry. WorkOS
// AuthKit is the case in hand: without provider=authkit its authorize
// endpoint cannot tell which connection to use and rejects the request as an
// invalid connection selector, so login never reaches the sign-in page.
func (s *oidcSuite) TestAuthorizationParametersAreForwarded() {
	tests := []struct {
		name       string
		parameters map[string]string
		want       map[string]string
	}{
		{name: "none configured", parameters: nil, want: map[string]string{"provider": ""}},
		{
			name:       "single parameter",
			parameters: map[string]string{"provider": "authkit"},
			want:       map[string]string{"provider": "authkit"},
		},
		{
			name:       "several parameters",
			parameters: map[string]string{"provider": "authkit", "prompt": "login"},
			want:       map[string]string{"provider": "authkit", "prompt": "login"},
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			authenticator, err := onprem.NewOIDCAuthenticator(context.Background(), onprem.OIDCConfig{
				Issuer: s.provider.URL, ClientID: "client", ClientSecret: "secret",
				RedirectURL: "https://portal.example/callback", FlowSecret: "flow-secret",
				AuthorizationParameters: test.parameters,
			})
			s.Require().NoError(err)

			flow, err := authenticator.BeginLogin()

			s.Require().NoError(err)
			parsed, err := url.Parse(flow.AuthorizationURL)
			s.Require().NoError(err)
			for name, value := range test.want {
				s.Equal(value, parsed.Query().Get(name))
			}
			// The standard parameters must survive alongside the extras.
			s.NotEmpty(parsed.Query().Get("state"))
			s.NotEmpty(parsed.Query().Get("code_challenge"))
		})
	}
}

// TestTokenResponseIdentitySource covers providers whose token endpoint is
// not OIDC-conformant. WorkOS AuthKit publishes a discovery document but its
// token endpoint returns access_token, refresh_token, and a user object --
// no id_token, and its access token carries no email claim -- so the only
// identity available is the user object in the back-channel response.
//
// That response arrives over TLS straight from the token endpoint rather
// than through the browser, so it is not attacker-controllable and the
// missing nonce check costs nothing: nonce exists to stop an ID token being
// replayed through the front channel, and there is no ID token here. State
// and PKCE still bind the exchange to this browser and this flow.
func (s *oidcSuite) TestTokenResponseIdentitySource() {
	s.tokenResponse = map[string]any{
		"access_token": "access-token-value",
		"token_type":   "Bearer",
		"user": map[string]any{
			"id":             "user_01ABC",
			"email":          "owner@example.com",
			"email_verified": true,
			"first_name":     "Ada",
			"last_name":      "Lovelace",
		},
	}
	authenticator, err := onprem.NewOIDCAuthenticator(context.Background(), onprem.OIDCConfig{
		Issuer: s.provider.URL, ClientID: "client", ClientSecret: "secret",
		RedirectURL: "https://portal.example/callback", FlowSecret: "flow-secret",
		IdentitySource: onprem.OIDCIdentitySourceTokenResponseUser,
	})
	s.Require().NoError(err)
	flow, err := authenticator.BeginLogin()
	s.Require().NoError(err)
	parsed, err := url.Parse(flow.AuthorizationURL)
	s.Require().NoError(err)

	identity, err := authenticator.CompleteLogin(
		context.Background(), "code", parsed.Query().Get("state"), flow.CookieValue,
	)

	s.Require().NoError(err)
	s.Equal(s.provider.URL, identity.Issuer)
	s.Equal("user_01ABC", identity.Subject)
	s.Equal("owner@example.com", identity.Email)
	s.True(identity.EmailVerified)
	s.Equal("Ada Lovelace", identity.DisplayName)
}

// TestTokenResponseIdentityRequiresAUser guards the failure mode: if the
// provider stops returning a user object, login must fail loudly rather than
// admit an identity with an empty subject.
func (s *oidcSuite) TestTokenResponseIdentityRequiresAUser() {
	s.tokenResponse = map[string]any{"access_token": "access-token-value", "token_type": "Bearer"}
	authenticator, err := onprem.NewOIDCAuthenticator(context.Background(), onprem.OIDCConfig{
		Issuer: s.provider.URL, ClientID: "client", ClientSecret: "secret",
		RedirectURL: "https://portal.example/callback", FlowSecret: "flow-secret",
		IdentitySource: onprem.OIDCIdentitySourceTokenResponseUser,
	})
	s.Require().NoError(err)
	flow, err := authenticator.BeginLogin()
	s.Require().NoError(err)
	parsed, err := url.Parse(flow.AuthorizationURL)
	s.Require().NoError(err)

	_, err = authenticator.CompleteLogin(
		context.Background(), "code", parsed.Query().Get("state"), flow.CookieValue,
	)

	s.Require().Error(err)
	s.ErrorContains(err, "user")
}

// TestDefaultIdentitySourceStillRequiresAnIDToken pins that the concession
// above is opt-in: a provider that simply fails to return an ID token must
// not silently fall back to the weaker path.
func (s *oidcSuite) TestDefaultIdentitySourceStillRequiresAnIDToken() {
	s.tokenResponse = map[string]any{
		"access_token": "access-token-value", "token_type": "Bearer",
		"user": map[string]any{"id": "user_01ABC", "email": "owner@example.com"},
	}
	authenticator, err := onprem.NewOIDCAuthenticator(context.Background(), onprem.OIDCConfig{
		Issuer: s.provider.URL, ClientID: "client", ClientSecret: "secret",
		RedirectURL: "https://portal.example/callback", FlowSecret: "flow-secret",
	})
	s.Require().NoError(err)
	flow, err := authenticator.BeginLogin()
	s.Require().NoError(err)
	parsed, err := url.Parse(flow.AuthorizationURL)
	s.Require().NoError(err)

	_, err = authenticator.CompleteLogin(
		context.Background(), "code", parsed.Query().Get("state"), flow.CookieValue,
	)

	s.Require().Error(err)
	s.ErrorContains(err, "ID token is missing")
}

func (s *oidcSuite) TestConfigurationIsRequired() {
	_, err := onprem.NewOIDCAuthenticator(context.Background(), onprem.OIDCConfig{})
	s.Require().Error(err)
}
