package textembedding_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/platform/textembedding"
	"github.com/stretchr/testify/suite"
)

type teiSuite struct {
	suite.Suite
}

func TestTEISuite(t *testing.T) {
	suite.Run(t, new(teiSuite))
}

func (s *teiSuite) TestEmbedsAndNormalizesRequestedDimensions() {
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		s.Equal("/v1/embeddings", request.URL.Path)
		var body struct {
			Input []string `json:"input"`
			Model string   `json:"model"`
		}
		s.Require().NoError(json.NewDecoder(request.Body).Decode(&body))
		s.Equal([]string{"first", "second"}, body.Input)
		s.Equal("qwen", body.Model)
		return response(`{"data":[{"index":0,"embedding":[3,4,99]},{"index":1,"embedding":[0,5,42]}]}`), nil
	})}

	client, err := textembedding.NewOpenAI(textembedding.OpenAIConfig{
		BaseURL: "http://embedding.local", Model: "qwen", Dimensions: 2, Client: httpClient,
	})
	s.Require().NoError(err)

	vectors, err := client.Embed(context.Background(), []string{"first", "second"})
	s.Require().NoError(err)
	s.Equal([][]float32{{0.6, 0.8}, {0, 1}}, vectors)
}

// TestSendsAuthorizationOnlyWhenConfigured covers hosted embedding
// providers. A local runtime needs no credential and must not receive an
// empty Authorization header; a hosted one such as OpenAI rejects the
// request without a bearer token.
func (s *teiSuite) TestSendsAuthorizationOnlyWhenConfigured() {
	tests := []struct {
		name   string
		apiKey string
		want   string
	}{
		{name: "local runtime sends no credential", apiKey: "", want: ""},
		{name: "hosted provider sends a bearer token", apiKey: "sk-test", want: "Bearer sk-test"},
		{name: "surrounding whitespace is trimmed", apiKey: "  sk-test\n", want: "Bearer sk-test"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			var authorization string
			var present bool
			httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				authorization = request.Header.Get("Authorization")
				_, present = request.Header["Authorization"]
				return response(`{"data":[{"index":0,"embedding":[3,4]}]}`), nil
			})}
			client, err := textembedding.NewOpenAI(textembedding.OpenAIConfig{
				BaseURL: "https://api.openai.com", Model: "text-embedding-3-small",
				Dimensions: 2, Client: httpClient, APIKey: test.apiKey,
			})
			s.Require().NoError(err)

			_, err = client.Embed(context.Background(), []string{"first"})

			s.Require().NoError(err)
			s.Equal(test.want, authorization)
			if test.want == "" {
				s.False(present, "an unauthenticated runtime must not receive the header at all")
			}
		})
	}
}

func (s *teiSuite) TestRejectsShortAndInvalidResponses() {
	tests := []struct {
		name string
		body string
	}{
		{name: "too few dimensions", body: `{"data":[{"index":0,"embedding":[1]}]}`},
		{name: "wrong vector count", body: `{"data":[{"index":0,"embedding":[1,2]},{"index":1,"embedding":[3,4]}]}`},
		{name: "zero vector", body: `{"data":[{"index":0,"embedding":[0,0]}]}`},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			transport := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return response(test.body), nil
			})
			client, err := textembedding.NewOpenAI(textembedding.OpenAIConfig{
				BaseURL: "http://embedding.local", Model: "qwen", Dimensions: 2,
				Client: &http.Client{Transport: transport},
			})
			s.Require().NoError(err)

			_, err = client.Embed(context.Background(), []string{"only"})
			s.Require().Error(err)
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func response(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestModelDimensionsResolvesKnownModels pins the model-to-width mapping.
// Configuring both a model and a width invites them to disagree, so the
// model decides and an explicit width is only an override -- which a
// Matryoshka model still needs when a deployment wants it below native
// width.
func (s *teiSuite) TestModelDimensionsResolvesKnownModels() {
	tests := []struct {
		name      string
		model     string
		override  int
		want      int
		wantError string
	}{
		{name: "OpenAI small", model: "text-embedding-3-small", want: 1536},
		{name: "OpenAI large", model: "text-embedding-3-large", want: 3072},
		{name: "Qwen3 0.6B", model: "Qwen/Qwen3-Embedding-0.6B", want: 1024},
		{name: "bare Qwen3 name", model: "Qwen3-Embedding-0.6B", want: 1024},
		{name: "case and spacing are ignored", model: "  TEXT-EMBEDDING-3-SMALL ", want: 1536},
		{name: "override wins over the model", model: "text-embedding-3-small", override: 384, want: 384},
		{name: "override carries an unknown model", model: "some/new-model", override: 768, want: 768},
		{name: "unknown model without an override", model: "some/new-model", wantError: "some/new-model"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			width, err := textembedding.ModelDimensions(test.model, test.override)

			if test.wantError != "" {
				s.Require().Error(err)
				s.Require().ErrorContains(err, test.wantError)
				s.Require().ErrorContains(err, "TEAM_MEMORY_EMBEDDING_DIMENSIONS")
				return
			}
			s.Require().NoError(err)
			s.Equal(test.want, width)
		})
	}
}
