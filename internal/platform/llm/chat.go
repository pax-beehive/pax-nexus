// Package llm provides the shared LLM chat-completion client used by
// product contexts. It is technical infrastructure, peer to
// platform/observability: domain packages may import it.
package llm

import "context"

type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ChatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolDefinition struct {
	Type     string             `json:"type"`
	Function ToolFunctionSchema `json:"function"`
}

type ToolFunctionSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ChatRequest struct {
	Model    string
	Messages []ChatMessage
	Tools    []ToolDefinition
	// MaxTokens caps the completion length; zero keeps the provider default.
	MaxTokens int
}

type TokenUsage struct {
	InputTokens           int
	OutputTokens          int
	PromptCacheHitTokens  int
	PromptCacheMissTokens int
}

type ChatResponse struct {
	Message ChatMessage
	Usage   TokenUsage
	// FinishReason is the provider's stop cause for the first choice, e.g.
	// "stop" or "length"; it distinguishes truncation from normal completion.
	FinishReason string
}

type ChatClient interface {
	Complete(context.Context, ChatRequest) (ChatResponse, error)
}
