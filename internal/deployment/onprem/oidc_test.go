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
}

func TestOIDCSuite(t *testing.T) {
	suite.Run(t, new(oidcSuite))
}

func (s *oidcSuite) SetupTest() {
	var issuer string
	s.provider = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
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

func (s *oidcSuite) TestConfigurationIsRequired() {
	_, err := onprem.NewOIDCAuthenticator(context.Background(), onprem.OIDCConfig{})
	s.Require().Error(err)
}
