package sessionconsumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/session"
)

const (
	ProcessorName     = "page_wiki"
	ProcessorVersion  = "knowledge-llm-v1"
	failureBackoffCap = 10 * time.Minute
)

// failureRecord tracks one stream's consecutive injection failures at a
// given head. It lives only in process memory: a restart resets all backoff,
// which is the desired behavior on a single-team workstation deployment.
type failureRecord struct {
	head        int64
	attempts    int
	nextRetryAt time.Time
}

func streamKey(stream Stream) string {
	return stream.ScopeID + "/" + stream.Actor.AgentID + "/" + stream.Actor.SessionID
}

var ErrSessionNotFound = errors.New("session not found")

type Progress struct {
	PendingSessions int
	LastProcessedAt *time.Time
}

type Status struct {
	AutoInject bool
	// Progress is nil when the progress query failed; ingestion status
	// stays available so the toggle keeps working (spec section 4).
	Progress *Progress
}

type InjectResult struct {
	ProcessedStreams int
}

type Stream struct {
	ScopeID string
	Actor   session.Actor
	Head    int64
}

type Store interface {
	AutoInjectEnabled(context.Context, string) (bool, error)
	SetAutoInjectEnabled(context.Context, string, bool) error
	PendingStreams(context.Context) ([]Stream, error)
	StreamsBySessionID(context.Context, string, string) ([]Stream, error)
	SessionEvents(context.Context, Stream) ([]session.SessionEvent, error)
	AdvanceCursor(context.Context, Stream) error
	Progress(context.Context, string) (Progress, error)
}

type Injector interface {
	InjectSession(context.Context, pagewiki.InjectSessionRequest) (pagewiki.InjectResult, error)
}

type Rebuilder interface {
	RebuildPageWiki(context.Context, string, string, string, time.Time) error
}

type Controller struct {
	store     Store
	injector  Injector
	rebuilder Rebuilder
	logger    *slog.Logger
	interval  time.Duration
	trigger   chan struct{}
	mu        sync.Mutex
	failures  map[string]failureRecord
	now       func() time.Time
}

func New(
	store Store,
	injector Injector,
	rebuilder Rebuilder,
	logger *slog.Logger,
	interval time.Duration,
) (*Controller, error) {
	if store == nil || injector == nil || rebuilder == nil || logger == nil {
		return nil, fmt.Errorf(
			"create Page Wiki session consumer: store, injector, rebuilder, and logger are required",
		)
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return &Controller{
		store: store, injector: injector, rebuilder: rebuilder,
		logger: logger, interval: interval, trigger: make(chan struct{}, 1),
		failures: make(map[string]failureRecord),
		now:      time.Now,
	}, nil
}

func (c *Controller) Start(ctx context.Context) {
	go func() {
		c.scan(ctx)
		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-c.trigger:
				c.scan(ctx)
			case <-ticker.C:
				c.scan(ctx)
			}
		}
	}()
}

func (c *Controller) Status(ctx context.Context, scopeID string) (Status, error) {
	enabled, err := c.store.AutoInjectEnabled(ctx, scopeID)
	if err != nil {
		return Status{}, fmt.Errorf("read Page Wiki ingestion status: %w", err)
	}
	status := Status{AutoInject: enabled}
	progress, err := c.store.Progress(ctx, scopeID)
	if err != nil {
		c.logger.WarnContext(ctx, "read Page Wiki ingestion progress", "error", err)
		return status, nil
	}
	status.Progress = &progress
	return status, nil
}

func (c *Controller) SetAutoInject(ctx context.Context, scopeID string, enabled bool) (Status, error) {
	if strings.TrimSpace(scopeID) == "" {
		return Status{}, fmt.Errorf("set Page Wiki auto injection: scope is required")
	}
	if err := c.store.SetAutoInjectEnabled(ctx, scopeID, enabled); err != nil {
		return Status{}, fmt.Errorf("set Page Wiki auto injection: %w", err)
	}
	return Status{AutoInject: enabled}, nil
}

func (c *Controller) Rebuild(ctx context.Context, scopeID string, since time.Time) (Status, error) {
	if strings.TrimSpace(scopeID) == "" {
		return Status{}, fmt.Errorf("rebuild Page Wiki: scope is required")
	}
	c.mu.Lock()
	err := c.rebuilder.RebuildPageWiki(ctx, scopeID, ProcessorName, ProcessorVersion, since)
	if err == nil {
		c.failures = make(map[string]failureRecord)
	}
	c.mu.Unlock()
	if err != nil {
		return Status{}, fmt.Errorf("rebuild Page Wiki: %w", err)
	}
	select {
	case c.trigger <- struct{}{}:
	default:
	}
	return Status{AutoInject: true}, nil
}

func (c *Controller) InjectSession(
	ctx context.Context,
	scopeID string,
	sessionID string,
) (InjectResult, error) {
	if strings.TrimSpace(scopeID) == "" || strings.TrimSpace(sessionID) == "" {
		return InjectResult{}, fmt.Errorf("inject Page Wiki session: scope and session ID are required")
	}
	streams, err := c.store.StreamsBySessionID(ctx, scopeID, sessionID)
	if err != nil {
		return InjectResult{}, fmt.Errorf("find Page Wiki session: %w", err)
	}
	if len(streams) == 0 {
		return InjectResult{}, ErrSessionNotFound
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, stream := range streams {
		delete(c.failures, streamKey(stream))
		if err := c.consume(ctx, stream); err != nil {
			return InjectResult{}, err
		}
	}
	return InjectResult{ProcessedStreams: len(streams)}, nil
}

func (c *Controller) scan(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	streams, err := c.store.PendingStreams(ctx)
	if err != nil {
		c.logger.ErrorContext(ctx, "Page Wiki scan failed", "error", err)
		return
	}
	now := c.now()
	for _, stream := range streams {
		if c.backedOff(stream, now) {
			continue
		}
		if err := c.consume(ctx, stream); err != nil {
			record := c.recordFailure(stream)
			c.logger.WarnContext(ctx, "Page Wiki session injection failed",
				"scope_id", stream.ScopeID,
				"agent_id", stream.Actor.AgentID,
				"session_id", stream.Actor.SessionID,
				"attempts", record.attempts,
				"next_retry_at", record.nextRetryAt,
				"error", err,
			)
			continue
		}
		delete(c.failures, streamKey(stream))
	}
}

func (c *Controller) consume(ctx context.Context, stream Stream) error {
	events, err := c.store.SessionEvents(ctx, stream)
	if err != nil {
		return fmt.Errorf("read Page Wiki session events: %w", err)
	}
	if len(events) == 0 {
		return nil
	}
	request := injectionRequest(stream, events)
	result, err := c.injector.InjectSession(ctx, request)
	if err != nil {
		return fmt.Errorf("inject Page Wiki session: %w", err)
	}
	if result.Run.Status != pagewiki.RunStatusSucceeded {
		return fmt.Errorf("inject Page Wiki session: maintenance run ended with %q", result.Run.Status)
	}
	if err := c.store.AdvanceCursor(ctx, stream); err != nil {
		return fmt.Errorf("commit Page Wiki session cursor: %w", err)
	}
	c.logger.InfoContext(ctx, "Page Wiki session injected",
		"scope_id", stream.ScopeID,
		"agent_id", stream.Actor.AgentID,
		"session_id", stream.Actor.SessionID,
		"cursor", stream.Head,
	)
	return nil
}

func injectionRequest(stream Stream, events []session.SessionEvent) pagewiki.InjectSessionRequest {
	var raw strings.Builder
	inputs := make([]pagewiki.SourceEventInput, 0, len(events))
	for index, event := range events {
		if index > 0 {
			raw.WriteString("\n\n")
		}
		start := raw.Len()
		fmt.Fprintf(&raw, "[event:%s sequence:%d type:%s] %s", event.ID, event.Sequence, event.Type, event.Content)
		inputs = append(inputs, pagewiki.SourceEventInput{
			ID: event.ID, StartByte: start, EndByte: raw.Len(),
		})
	}
	sourceID := fmt.Sprintf("session:%s:%s:%s", stream.ScopeID, stream.Actor.AgentID, stream.Actor.SessionID)
	return pagewiki.InjectSessionRequest{
		SourceID:       sourceID,
		IdempotencyKey: fmt.Sprintf("%s/%s/%d", ProcessorName, ProcessorVersion, stream.Head),
		Raw:            []byte(raw.String()), Events: inputs,
	}
}

// backedOff reports whether the stream is still inside its retry window. A
// head advance (new session events) always clears the way immediately.
func (c *Controller) backedOff(stream Stream, now time.Time) bool {
	record, found := c.failures[streamKey(stream)]
	if !found || record.head != stream.Head {
		return false
	}
	return now.Before(record.nextRetryAt)
}

func (c *Controller) recordFailure(stream Stream) failureRecord {
	key := streamKey(stream)
	record := c.failures[key]
	if record.head != stream.Head {
		record = failureRecord{head: stream.Head}
	}
	record.attempts++
	record.nextRetryAt = c.now().Add(c.backoffDelay(record.attempts))
	c.failures[key] = record
	return record
}

func (c *Controller) backoffDelay(attempts int) time.Duration {
	if attempts >= 16 {
		return failureBackoffCap
	}
	delay := c.interval << attempts
	if delay <= 0 || delay > failureBackoffCap {
		return failureBackoffCap
	}
	return delay
}
