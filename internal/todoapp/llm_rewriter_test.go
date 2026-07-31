package todoapp

import (
	"context"
	"errors"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
)

type fakeChatClient struct {
	responses []string
	errs      []error
	calls     int
}

func (f *fakeChatClient) Complete(_ context.Context, _ llm.ChatRequest) (llm.ChatResponse, error) {
	index := f.calls
	f.calls++
	if index < len(f.errs) && f.errs[index] != nil {
		return llm.ChatResponse{}, f.errs[index]
	}
	return llm.ChatResponse{Message: llm.ChatMessage{Role: "assistant", Content: f.responses[index]}}, nil
}

func TestLLMRewriter_Rewrite(t *testing.T) {
	tests := []struct {
		name          string
		responses     []string
		errs          []error
		expectedTitle string
		expectedBody  string
		wantErr       bool
		expectedCalls int
	}{
		{
			name:          "valid JSON",
			responses:     []string{`{"title":"Fix database","body":"Fix the database schema issue."}`},
			errs:          []error{nil},
			expectedTitle: "Fix database",
			expectedBody:  "Fix the database schema issue.",
			wantErr:       false,
			expectedCalls: 1,
		},
		{
			name:          "fenced JSON block",
			responses:     []string{"```json\n{\"title\":\"Deploy service\",\"body\":\"Deploy the service to production.\"}\n```"},
			errs:          []error{nil},
			expectedTitle: "Deploy service",
			expectedBody:  "Deploy the service to production.",
			wantErr:       false,
			expectedCalls: 1,
		},
		{
			name:          "first attempt garbage second valid",
			responses:     []string{"invalid", `{"title":"Update docs","body":"Update documentation."}`},
			errs:          []error{nil, nil},
			expectedTitle: "Update docs",
			expectedBody:  "Update documentation.",
			wantErr:       false,
			expectedCalls: 2,
		},
		{
			name:          "both attempts garbage degrade to verbatim",
			responses:     []string{"invalid1", "invalid2"},
			errs:          []error{nil, nil},
			expectedTitle: "Original Subject",
			expectedBody:  "Original body text.",
			wantErr:       false,
			expectedCalls: 2,
		},
		{
			name:          "client error twice degrade to verbatim",
			responses:     []string{"", ""},
			errs:          []error{errors.New("network error"), errors.New("network error")},
			expectedTitle: "Error Item",
			expectedBody:  "Something went wrong.",
			wantErr:       false,
			expectedCalls: 2,
		},
		{
			name:          "blank title counted as failed attempt",
			responses:     []string{`{"title":"","body":"No title"}`, `{"title":"Real title","body":"Real body"}`},
			errs:          []error{nil, nil},
			expectedTitle: "Real title",
			expectedBody:  "Real body",
			wantErr:       false,
			expectedCalls: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeChatClient{
				responses: tt.responses,
				errs:      tt.errs,
			}

			rewriter, err := NewLLMRewriter(LLMRewriterConfig{
				Client: client,
				Model:  "gpt-4",
				Logger: nil,
			})
			if err != nil {
				t.Fatalf("unexpected error creating rewriter: %v", err)
			}

			item := ActionItem{
				NoteID:  "note-123",
				Kind:    "task",
				Subject: "Original Subject",
				Body:    "Original body text.",
			}

			// For error handling test cases
			if tt.name == "client error twice degrade to verbatim" {
				item.Subject = "Error Item"
				item.Body = "Something went wrong."
			}

			title, body, err := rewriter.Rewrite(context.Background(), item)

			if (err != nil) != tt.wantErr {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err != nil)
			}

			if title != tt.expectedTitle {
				t.Errorf("expected title %q, got %q", tt.expectedTitle, title)
			}

			if body != tt.expectedBody {
				t.Errorf("expected body %q, got %q", tt.expectedBody, body)
			}

			if client.calls != tt.expectedCalls {
				t.Errorf("expected %d calls, got %d", tt.expectedCalls, client.calls)
			}
		})
	}
}

func TestNewLLMRewriter(t *testing.T) {
	tests := []struct {
		name    string
		config  LLMRewriterConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: LLMRewriterConfig{
				Client: &fakeChatClient{},
				Model:  "gpt-4",
				Logger: nil,
			},
			wantErr: false,
		},
		{
			name: "nil client",
			config: LLMRewriterConfig{
				Client: nil,
				Model:  "gpt-4",
				Logger: nil,
			},
			wantErr: true,
		},
		{
			name: "blank model",
			config: LLMRewriterConfig{
				Client: &fakeChatClient{},
				Model:  "",
				Logger: nil,
			},
			wantErr: true,
		},
		{
			name: "model with whitespace",
			config: LLMRewriterConfig{
				Client: &fakeChatClient{},
				Model:  "   ",
				Logger: nil,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewLLMRewriter(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("expected error %v, got %v", tt.wantErr, err != nil)
			}
		})
	}
}
