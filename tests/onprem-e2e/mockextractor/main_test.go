package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type extractorHandlerSuite struct {
	suite.Suite
}

func TestExtractorHandlerSuite(t *testing.T) {
	suite.Run(t, new(extractorHandlerSuite))
}

func (s *extractorHandlerSuite) TestHealthAndAuthorizedCompletion() {
	s.Equal(listenAddress, newServer().Addr)
	s.NotNil(newHandler())
	healthResponse := httptest.NewRecorder()
	health(healthResponse, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	s.Equal(http.StatusOK, healthResponse.Code)

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer e2e-extractor-key")
	response := httptest.NewRecorder()
	complete(response, request)
	s.Equal(http.StatusOK, response.Code)
	var payload map[string]any
	s.Require().NoError(json.Unmarshal(response.Body.Bytes(), &payload))
	s.NotEmpty(payload["choices"])
	s.NotEmpty(payload["usage"])
}

func (s *extractorHandlerSuite) TestHealthCheckResponses() {
	tests := []struct {
		name      string
		transport extractorRoundTripFunc
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
			err := checkHealth(&http.Client{Transport: test.transport}, "http://extractor.test/healthz")
			if test.wantError {
				s.Require().Error(err)
				return
			}
			s.Require().NoError(err)
		})
	}
}

type extractorRoundTripFunc func(*http.Request) (*http.Response, error)

func (function extractorRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func (s *extractorHandlerSuite) TestCompletionRejectsMissingCredential() {
	response := httptest.NewRecorder()
	complete(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	s.Equal(http.StatusUnauthorized, response.Code)
}
