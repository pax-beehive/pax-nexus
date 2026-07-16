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
