package mem0config_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/v2/mem0config"
	"github.com/stretchr/testify/suite"
)

type configureSuite struct{ suite.Suite }

func TestConfigureSuite(t *testing.T) { suite.Run(t, new(configureSuite)) }

func (s *configureSuite) TestConfigureUsesDeepSeekAndKeepsOpenAIEmbedding() {
	var payload map[string]any
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		s.Equal(http.MethodPost, request.Method)
		s.Equal("application/json", request.Header.Get("Content-Type"))
		s.Require().NoError(json.NewDecoder(request.Body).Decode(&payload))
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"message":"Configuration set successfully"}`)),
			Header:     make(http.Header),
		}, nil
	})}

	err := mem0config.Configure(context.Background(), client, "http://mem0:8000/configure", mem0config.Settings{
		DeepSeekAPIKey:  "deepseek-key",
		DeepSeekBaseURL: "https://api.deepseek.example/v1",
		OpenAIAPIKey:    "openai-key",
		LLMModel:        "deepseek-v4-flash",
		EmbedderModel:   "text-embedding-3-small",
		PostgresHost:    "mem0-postgres",
	})
	s.Require().NoError(err)

	llm, ok := payload["llm"].(map[string]any)
	s.Require().True(ok)
	s.Equal("openai", llm["provider"])
	llmConfig, ok := llm["config"].(map[string]any)
	s.Require().True(ok)
	s.Equal("deepseek-v4-flash", llmConfig["model"])
	s.Equal("deepseek-key", llmConfig["api_key"])
	s.Equal("https://api.deepseek.example/v1", llmConfig["openai_base_url"])
	temperature, ok := llmConfig["temperature"].(float64)
	s.Require().True(ok)
	s.InDelta(0.2, temperature, 0.0001)

	embedder, ok := payload["embedder"].(map[string]any)
	s.Require().True(ok)
	s.Equal("openai", embedder["provider"])
	embedderConfig, ok := embedder["config"].(map[string]any)
	s.Require().True(ok)
	s.Equal("text-embedding-3-small", embedderConfig["model"])
	s.Equal("openai-key", embedderConfig["api_key"])

	vectorStore, ok := payload["vector_store"].(map[string]any)
	s.Require().True(ok)
	s.Equal("pgvector", vectorStore["provider"])
	vectorConfig, ok := vectorStore["config"].(map[string]any)
	s.Require().True(ok)
	s.Equal("mem0-postgres", vectorConfig["host"])
	s.NotContains(payload, "graph_store")
}

func (s *configureSuite) TestConfigureRejectsInvalidInputsAndUpstreamErrors() {
	successClient := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"message":"ok"}`)),
			Header:     make(http.Header),
		}, nil
	})}
	validSettings := mem0config.Settings{DeepSeekAPIKey: "deepseek-key", OpenAIAPIKey: "openai-key"}
	tests := []struct {
		name     string
		client   *http.Client
		endpoint string
		settings mem0config.Settings
		message  string
	}{
		{name: "missing client", endpoint: "http://mem0/configure", settings: validSettings, message: "HTTP client is required"},
		{name: "missing endpoint", client: successClient, settings: validSettings, message: "endpoint is required"},
		{name: "missing DeepSeek key", client: successClient, endpoint: "http://mem0/configure", settings: mem0config.Settings{OpenAIAPIKey: "openai-key"}, message: "DeepSeek API key is required"},
		{name: "invalid DeepSeek base URL", client: successClient, endpoint: "http://mem0/configure", settings: mem0config.Settings{DeepSeekAPIKey: "deepseek-key", DeepSeekBaseURL: "not-a-url", OpenAIAPIKey: "openai-key"}, message: "DeepSeek base URL is invalid"},
		{name: "missing OpenAI key", client: successClient, endpoint: "http://mem0/configure", settings: mem0config.Settings{DeepSeekAPIKey: "deepseek-key"}, message: "OpenAI API key is required"},
		{
			name: "upstream failure",
			client: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader("provider rejected model")),
					Header:     make(http.Header),
				}, nil
			})},
			endpoint: "http://mem0/configure",
			settings: validSettings,
			message:  "status 500: provider rejected model",
		},
	}

	for _, test := range tests {
		s.Run(test.name, func() {
			err := mem0config.Configure(context.Background(), test.client, test.endpoint, test.settings)
			s.ErrorContains(err, test.message)
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
