package llm_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
	"github.com/stretchr/testify/suite"
)

type deepSeekSuite struct {
	suite.Suite
}

func TestDeepSeekSuite(t *testing.T) {
	suite.Run(t, new(deepSeekSuite))
}

func (s *deepSeekSuite) TestCallsOfficialCompatibleEndpointAndDecodesTools() {
	httpClient := &http.Client{Transport: roundTripFunc(func(
		request *http.Request,
	) (*http.Response, error) {
		s.Equal("/chat/completions", request.URL.Path)
		s.Equal("Bearer secret", request.Header.Get("Authorization"))
		body, err := io.ReadAll(request.Body)
		s.Require().NoError(err)
		s.Contains(string(body), `"model":"deepseek-v4-pro"`)
		s.Contains(string(body), `"tool_choice":"auto"`)
		return response(http.StatusOK, `{
		  "choices": [{"message": {
		    "role": "assistant",
		    "content": "",
		    "tool_calls": [{
		      "id": "call-1",
		      "type": "function",
		      "function": {"name": "read_file", "arguments": "{\"path\":\"AGENTS.md\"}"}
		    }]
		  }}],
		  "usage": {"prompt_tokens": 12, "completion_tokens": 4}
		}`), nil
	})}

	client := llm.NewDeepSeekClient(llm.DeepSeekConfig{
		BaseURL: "https://deepseek.example", APIKey: "secret", HTTPClient: httpClient,
	})
	response, err := client.Complete(context.Background(), llm.ChatRequest{
		Model:    "deepseek-v4-pro",
		Messages: []llm.ChatMessage{{Role: "user", Content: "work"}},
		Tools: []llm.ToolDefinition{{
			Type:     "function",
			Function: llm.ToolFunctionSchema{Name: "read_file"},
		}},
	})
	s.Require().NoError(err)
	s.Equal(12, response.Usage.InputTokens)
	s.Equal(4, response.Usage.OutputTokens)
	s.Require().Len(response.Message.ToolCalls, 1)
	s.Equal("read_file", response.Message.ToolCalls[0].Function.Name)
}

func (s *deepSeekSuite) TestOmitsToolChoiceWhenNoToolsAreSent() {
	httpClient := &http.Client{Transport: roundTripFunc(func(
		request *http.Request,
	) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		s.Require().NoError(err)
		// OpenAI-compatible endpoints reject tool_choice without tools;
		// only DeepSeek's own endpoint tolerates the combination.
		s.NotContains(string(body), "tool_choice")
		s.NotContains(string(body), `"tools"`)
		return response(http.StatusOK, `{
		  "choices": [{"message": {"role": "assistant", "content": "done"}}],
		  "usage": {"prompt_tokens": 3, "completion_tokens": 1}
		}`), nil
	})}

	client := llm.NewDeepSeekClient(llm.DeepSeekConfig{
		BaseURL: "https://deepseek.example", APIKey: "secret", HTTPClient: httpClient,
	})
	chatResponse, err := client.Complete(context.Background(), llm.ChatRequest{
		Model:    "deepseek-v4-pro",
		Messages: []llm.ChatMessage{{Role: "user", Content: "work"}},
	})
	s.Require().NoError(err)
	s.Equal("done", chatResponse.Message.Content)
}

func (s *deepSeekSuite) TestReportsProviderErrorWithoutLeakingAPIKey() {
	httpClient := &http.Client{Transport: roundTripFunc(func(
		_ *http.Request,
	) (*http.Response, error) {
		return response(
			http.StatusUnauthorized,
			`{"error":{"message":"invalid credentials"}}`,
		), nil
	})}

	client := llm.NewDeepSeekClient(llm.DeepSeekConfig{
		BaseURL: "https://deepseek.example", APIKey: "do-not-leak", HTTPClient: httpClient,
	})
	_, err := client.Complete(context.Background(), llm.ChatRequest{
		Model: "deepseek-v4-pro",
	})
	s.Require().ErrorContains(err, "status 401")
	s.NotContains(err.Error(), "do-not-leak")
}

func (s *deepSeekSuite) TestRejectsMissingCredentialsAndMalformedResponse() {
	client := llm.NewDeepSeekClient(llm.DeepSeekConfig{})
	_, err := client.Complete(context.Background(), llm.ChatRequest{})
	s.Require().ErrorContains(err, "API key")

	httpClient := &http.Client{Transport: roundTripFunc(func(
		_ *http.Request,
	) (*http.Response, error) {
		return response(http.StatusOK, `{"choices":[]}`), nil
	})}
	client = llm.NewDeepSeekClient(llm.DeepSeekConfig{
		BaseURL: "https://deepseek.example", APIKey: "secret", HTTPClient: httpClient,
	})
	_, err = client.Complete(context.Background(), llm.ChatRequest{})
	s.Require().ErrorContains(err, "no choices")
}

func (s *deepSeekSuite) TestChatTypesRoundTripToolResults() {
	message := llm.ChatMessage{
		Role:       "tool",
		Content:    `{"ok":true}`,
		ToolCallID: "call-1",
	}
	encoded, err := json.Marshal(message)
	s.Require().NoError(err)
	s.Contains(string(encoded), `"tool_call_id":"call-1"`)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}
