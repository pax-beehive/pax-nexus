package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type DeepSeekConfig struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

type DeepSeekClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewDeepSeekClient(config DeepSeekConfig) *DeepSeekClient {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	return &DeepSeekClient{
		baseURL: baseURL, apiKey: strings.TrimSpace(config.APIKey), httpClient: client,
	}
}

func (c *DeepSeekClient) Complete(
	ctx context.Context,
	request ChatRequest,
) (ChatResponse, error) {
	if c.apiKey == "" {
		return ChatResponse{}, errors.New("DeepSeek API key is required")
	}
	// OpenAI-compatible endpoints reject tool_choice without tools; only
	// DeepSeek's own endpoint tolerates the combination.
	toolChoice := ""
	if len(request.Tools) > 0 {
		toolChoice = "auto"
	}
	payload := struct {
		Model      string           `json:"model"`
		Messages   []ChatMessage    `json:"messages"`
		Tools      []ToolDefinition `json:"tools,omitempty"`
		ToolChoice string           `json:"tool_choice,omitempty"`
		MaxTokens  int              `json:"max_tokens,omitempty"`
		Stream     bool             `json:"stream"`
	}{
		Model: request.Model, Messages: request.Messages, Tools: request.Tools,
		ToolChoice: toolChoice, MaxTokens: request.MaxTokens, Stream: false,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("encode DeepSeek request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/chat/completions",
		bytes.NewReader(encoded),
	)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("create DeepSeek request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("call DeepSeek: %w", err)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	closeErr := response.Body.Close()
	if err != nil {
		return ChatResponse{}, fmt.Errorf("read DeepSeek response: %w", err)
	}
	if closeErr != nil {
		return ChatResponse{}, fmt.Errorf("close DeepSeek response: %w", closeErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ChatResponse{}, fmt.Errorf(
			"DeepSeek returned status %d: %s",
			response.StatusCode,
			strings.TrimSpace(string(body)),
		)
	}
	var decoded struct {
		Choices []struct {
			Message      ChatMessage `json:"message"`
			FinishReason string      `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens          int `json:"prompt_tokens"`
			CompletionTokens      int `json:"completion_tokens"`
			PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
			PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return ChatResponse{}, fmt.Errorf("decode DeepSeek response: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return ChatResponse{}, errors.New("DeepSeek response contains no choices")
	}
	choice := decoded.Choices[0]
	if choice.Message.Content == "" && len(choice.Message.ToolCalls) == 0 {
		return ChatResponse{}, fmt.Errorf(
			"DeepSeek returned empty content (finish_reason=%q)", choice.FinishReason,
		)
	}
	return ChatResponse{
		Message:      choice.Message,
		FinishReason: choice.FinishReason,
		Usage: TokenUsage{
			InputTokens:           decoded.Usage.PromptTokens,
			OutputTokens:          decoded.Usage.CompletionTokens,
			PromptCacheHitTokens:  decoded.Usage.PromptCacheHitTokens,
			PromptCacheMissTokens: decoded.Usage.PromptCacheMissTokens,
		},
	}, nil
}
