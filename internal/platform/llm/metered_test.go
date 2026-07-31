package llm

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/session"
)

type recordingSink struct {
	events []UsageEvent
	err    error
}

func (s *recordingSink) RecordLLMUsage(_ context.Context, event UsageEvent) error {
	s.events = append(s.events, event)
	return s.err
}

type stubChatClient struct {
	response ChatResponse
	err      error
}

func (c stubChatClient) Complete(context.Context, ChatRequest) (ChatResponse, error) {
	return c.response, c.err
}

func TestMeteredClientRecordsUsageWithComponentAndDefaultScope(t *testing.T) {
	inner := stubChatClient{response: ChatResponse{
		Usage: TokenUsage{
			InputTokens: 100, OutputTokens: 40,
			PromptCacheHitTokens: 70, PromptCacheMissTokens: 30,
		},
	}}
	sink := &recordingSink{}
	client, err := NewMeteredChatClient(MeteredConfig{
		Client: inner, Sink: sink,
		Component: "wiki-editor", DefaultScope: "local-team",
	})
	if err != nil {
		t.Fatalf("NewMeteredChatClient: %v", err)
	}

	response, err := client.Complete(
		context.Background(), ChatRequest{Model: "deepseek-chat"},
	)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !reflect.DeepEqual(response, inner.response) {
		t.Fatalf("response passthrough mismatch: got %+v want %+v", response, inner.response)
	}
	if len(sink.events) != 1 {
		t.Fatalf("expected 1 recorded event, got %d", len(sink.events))
	}
	want := UsageEvent{
		ScopeID: "local-team", Component: "wiki-editor", Model: "deepseek-chat",
		Usage: inner.response.Usage,
	}
	if sink.events[0] != want {
		t.Fatalf("event mismatch: got %+v want %+v", sink.events[0], want)
	}
}

func TestMeteredClientPrefersContextScope(t *testing.T) {
	inner := stubChatClient{response: ChatResponse{
		Usage: TokenUsage{InputTokens: 1, OutputTokens: 1},
	}}
	sink := &recordingSink{}
	client, err := NewMeteredChatClient(MeteredConfig{
		Client: inner, Sink: sink,
		Component: "wiki-editor", DefaultScope: "local-team",
	})
	if err != nil {
		t.Fatalf("NewMeteredChatClient: %v", err)
	}

	ctx := session.WithScope(context.Background(), "scoped-team")
	_, err = client.Complete(ctx, ChatRequest{Model: "deepseek-chat"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(sink.events) != 1 {
		t.Fatalf("expected 1 recorded event, got %d", len(sink.events))
	}
	if sink.events[0].ScopeID != "scoped-team" {
		t.Fatalf("expected scoped-team scope, got %q", sink.events[0].ScopeID)
	}
}

func TestMeteredClientSwallowsSinkErrors(t *testing.T) {
	inner := stubChatClient{response: ChatResponse{
		Message: ChatMessage{Content: "hello"},
	}}
	sink := &recordingSink{err: errors.New("sink unavailable")}
	client, err := NewMeteredChatClient(MeteredConfig{
		Client: inner, Sink: sink,
		Component: "wiki-editor", DefaultScope: "local-team",
	})
	if err != nil {
		t.Fatalf("NewMeteredChatClient: %v", err)
	}

	response, err := client.Complete(context.Background(), ChatRequest{Model: "deepseek-chat"})
	if err != nil {
		t.Fatalf("Complete should swallow sink errors, got: %v", err)
	}
	if !reflect.DeepEqual(response, inner.response) {
		t.Fatalf("response passthrough mismatch: got %+v want %+v", response, inner.response)
	}
}

func TestMeteredClientSkipsRecordingOnClientError(t *testing.T) {
	innerErr := errors.New("provider failure")
	inner := stubChatClient{err: innerErr}
	sink := &recordingSink{}
	client, err := NewMeteredChatClient(MeteredConfig{
		Client: inner, Sink: sink,
		Component: "wiki-editor", DefaultScope: "local-team",
	})
	if err != nil {
		t.Fatalf("NewMeteredChatClient: %v", err)
	}

	_, err = client.Complete(context.Background(), ChatRequest{Model: "deepseek-chat"})
	if !errors.Is(err, innerErr) {
		t.Fatalf("expected inner error to propagate, got %v", err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("expected 0 recorded events, got %d", len(sink.events))
	}
}

func TestNewMeteredChatClientValidatesConfig(t *testing.T) {
	validClient := stubChatClient{}
	validSink := &recordingSink{}

	cases := []struct {
		name   string
		config MeteredConfig
	}{
		{
			name: "nil client",
			config: MeteredConfig{
				Client: nil, Sink: validSink,
				Component: "wiki-editor", DefaultScope: "local-team",
			},
		},
		{
			name: "nil sink",
			config: MeteredConfig{
				Client: validClient, Sink: nil,
				Component: "wiki-editor", DefaultScope: "local-team",
			},
		},
		{
			name: "empty component",
			config: MeteredConfig{
				Client: validClient, Sink: validSink,
				Component: "", DefaultScope: "local-team",
			},
		},
		{
			name: "empty default scope",
			config: MeteredConfig{
				Client: validClient, Sink: validSink,
				Component: "wiki-editor", DefaultScope: "",
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client, err := NewMeteredChatClient(testCase.config)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", testCase.name)
			}
			if client != nil {
				t.Fatalf("expected nil client for %s, got %+v", testCase.name, client)
			}
		})
	}
}
