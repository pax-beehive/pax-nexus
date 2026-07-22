package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type providerSuite struct {
	suite.Suite
	provider *provider
}

func TestProviderSuite(t *testing.T) {
	suite.Run(t, new(providerSuite))
}

func (s *providerSuite) SetupTest() {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	s.Require().NoError(err)
	s.provider = &provider{key: key}
}

func (s *providerSuite) TestProviderConstructionAndHealthCheck() {
	constructed, err := newProvider()
	s.Require().NoError(err)
	s.NotNil(constructed.key)
	s.NotNil(newHandler(constructed))
	s.Equal(listenAddress, newServer(constructed).Addr)
	tests := []struct {
		name      string
		transport roundTripFunc
		wantError bool
	}{
		{
			name: "healthy", transport: func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
			},
		},
		{
			name: "unhealthy status", wantError: true, transport: func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader(""))}, nil
			},
		},
		{
			name: "transport error", wantError: true, transport: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("unavailable")
			},
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			err := checkHealth(&http.Client{Transport: test.transport}, "http://oidc.test/healthz")
			if test.wantError {
				s.Require().Error(err)
				return
			}
			s.Require().NoError(err)
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func (s *providerSuite) TestDiscoveryHealthAndKeys() {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		path    string
	}{
		{name: "health", handler: s.provider.health, path: "/healthz"},
		{name: "discovery", handler: s.provider.discovery, path: "/.well-known/openid-configuration"},
		{name: "keys", handler: s.provider.keys, path: "/keys"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			response := httptest.NewRecorder()
			test.handler(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			s.Equal(http.StatusOK, response.Code)
			if test.name != "health" {
				s.Contains(response.Header().Get("Content-Type"), "application/json")
			}
		})
	}
}

func (s *providerSuite) TestAuthorizationCodeProducesSignedIDToken() {
	request := httptest.NewRequest(http.MethodGet,
		"/authorize?redirect_uri=http%3A%2F%2Fteam-memory%3A8080%2Fv1%2Fauth%2Fcallback&state=state-1&nonce=nonce-1", nil)
	response := httptest.NewRecorder()
	s.provider.authorize(response, request)
	s.Equal(http.StatusFound, response.Code)
	location, err := url.Parse(response.Header().Get("Location"))
	s.Require().NoError(err)
	s.Equal("state-1", location.Query().Get("code"))
	s.Equal("state-1", location.Query().Get("state"))

	form := url.Values{"code": []string{"state-1"}}
	tokenRequest := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	tokenRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenResponse := httptest.NewRecorder()
	s.provider.token(tokenResponse, tokenRequest)
	s.Equal(http.StatusOK, tokenResponse.Code)
	var payload map[string]any
	s.Require().NoError(json.Unmarshal(tokenResponse.Body.Bytes(), &payload))
	s.NotEmpty(payload["id_token"])
	s.Equal("Bearer", payload["token_type"])
}

func (s *providerSuite) TestRejectsInvalidAuthorizationAndTokenRequests() {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		request *http.Request
	}{
		{
			name: "authorization without state", handler: s.provider.authorize,
			request: httptest.NewRequest(http.MethodGet, "/authorize?redirect_uri=http%3A%2F%2Fcallback&nonce=nonce", nil),
		},
		{
			name: "unknown authorization code", handler: s.provider.token,
			request: httptest.NewRequest(http.MethodPost, "/token", strings.NewReader("code=unknown")),
		},
		{
			name: "malformed token form", handler: s.provider.token,
			request: httptest.NewRequest(http.MethodPost, "/token", strings.NewReader("%")),
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			if strings.Contains(test.name, "token") {
				test.request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			response := httptest.NewRecorder()
			test.handler(response, test.request)
			s.Equal(http.StatusBadRequest, response.Code)
		})
	}
}

func (s *providerSuite) TestRejectsInvalidNonceAndSigningKey() {
	s.provider.nonces.Store("bad-nonce", 42)
	request := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader("code=bad-nonce"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	s.provider.token(response, request)
	s.Equal(http.StatusBadRequest, response.Code)

	_, err := (&provider{}).signIDToken("nonce")
	s.Require().Error(err)
	s.provider.writeJSON(errorResponseWriter{header: make(http.Header)}, map[string]string{"ok": "true"})
}

type errorResponseWriter struct {
	header http.Header
}

func (writer errorResponseWriter) Header() http.Header {
	return writer.header
}

func (errorResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func (errorResponseWriter) WriteHeader(int) {}
