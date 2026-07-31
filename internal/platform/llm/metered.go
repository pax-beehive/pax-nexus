package llm

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/session"
)

// UsageEvent reports the token usage of a single ChatClient.Complete call,
// attributed to a scope and a component for downstream metering.
type UsageEvent struct {
	ScopeID   string
	Component string
	Model     string
	Usage     TokenUsage
}

// UsageSink receives UsageEvents emitted by MeteredChatClient.
type UsageSink interface {
	RecordLLMUsage(ctx context.Context, event UsageEvent) error
}

// LLMUsageRow is a per-(component, model) aggregate over a requested window,
// as returned by a UsageSink's windowed summary (e.g.
// postgres.LLMUsageStore.UsageSummary). It lives here, next to UsageEvent,
// so that consumers of usage summaries (such as the HTTP handler package)
// depend on shared LLM types instead of the Postgres-backed store.
type LLMUsageRow struct {
	Component       string
	Model           string
	Calls           int64
	InputTokens     int64
	CacheHitTokens  int64
	CacheMissTokens int64
	OutputTokens    int64
}

// MeteredConfig configures a MeteredChatClient.
type MeteredConfig struct {
	Client       ChatClient   // required
	Sink         UsageSink    // required
	Component    string       // required, e.g. "wiki-planner"
	DefaultScope string       // required fallback when ctx carries no scope
	Logger       *slog.Logger // optional; nil → discard
}

// MeteredChatClient decorates a ChatClient, reporting each successful
// call's token usage to a UsageSink. Metering is best-effort: sink
// failures are logged and never propagated to the caller.
type MeteredChatClient struct {
	client       ChatClient
	sink         UsageSink
	component    string
	defaultScope string
	logger       *slog.Logger
}

func NewMeteredChatClient(config MeteredConfig) (*MeteredChatClient, error) {
	if config.Client == nil || config.Sink == nil ||
		strings.TrimSpace(config.Component) == "" ||
		strings.TrimSpace(config.DefaultScope) == "" {
		return nil, errors.New(
			"create metered chat client: client, sink, component, and default scope are required",
		)
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &MeteredChatClient{
		client: config.Client, sink: config.Sink,
		component: config.Component, defaultScope: config.DefaultScope,
		logger: logger,
	}, nil
}

func (m *MeteredChatClient) Complete(
	ctx context.Context,
	request ChatRequest,
) (ChatResponse, error) {
	response, err := m.client.Complete(ctx, request)
	if err != nil {
		return response, err
	}
	event := UsageEvent{
		ScopeID: m.defaultScope, Component: m.component,
		Model: request.Model, Usage: response.Usage,
	}
	if scoped, scopeErr := session.ScopeFromContext(ctx); scopeErr == nil && scoped != "" {
		event.ScopeID = scoped
	}
	if recordErr := m.sink.RecordLLMUsage(ctx, event); recordErr != nil {
		m.logger.WarnContext(ctx, "record LLM usage failed",
			"component", m.component, "scope_id", event.ScopeID, "error", recordErr)
	}
	return response, nil
}
