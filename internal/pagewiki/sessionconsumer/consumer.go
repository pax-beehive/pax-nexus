package sessionconsumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
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

type RebuildState string

const (
	RebuildIdle    RebuildState = "idle"
	RebuildQueued  RebuildState = "queued"
	RebuildRunning RebuildState = "running"
	RebuildFailed  RebuildState = "failed"
)

// RebuildStatus is the in-memory rebuild state machine snapshot. Error is
// set only while State is RebuildFailed; FinishedAt records the last
// successful completion. It lives only in process memory: a restart resets
// it, matching the failureRecord policy above.
type RebuildStatus struct {
	State      RebuildState
	Error      string
	FinishedAt *time.Time
}

type Status struct {
	AutoInject bool
	// Progress is nil when the progress query failed; ingestion status
	// stays available so the toggle keeps working (spec section 4).
	Progress *Progress
	Rebuild  RebuildStatus
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

// InjectorFor and RebuilderFor resolve one scope's collaborators. The
// consumer sweeps every scope from a single loop, so it holds resolvers
// rather than instances: wiring builds them over the Page Wiki service and
// repository managers, which hydrate a scope on first use and cache it.
type InjectorFor func(ctx context.Context, scopeID string) (Injector, error)

type RebuilderFor func(ctx context.Context, scopeID string) (Rebuilder, error)

// scopeRebuild is one scope's rebuild slot: the state machine snapshot the
// status endpoint reports plus the cutoff the queued run was armed with.
// Every scope has its own, so one tenant's rebuild never swallows another's.
type scopeRebuild struct {
	status RebuildStatus
	since  time.Time
}

type Controller struct {
	store        Store
	injectorFor  InjectorFor
	rebuilderFor RebuilderFor
	logger       *slog.Logger
	interval     time.Duration
	trigger      chan struct{}
	mu           sync.Mutex
	failures     map[string]failureRecord
	now          func() time.Time
	// stateMu guards rebuilds below. It is separate from mu so status reads
	// never wait behind a minutes-long injection scan.
	stateMu  sync.Mutex
	rebuilds map[string]*scopeRebuild
}

func New(
	store Store,
	injectorFor InjectorFor,
	rebuilderFor RebuilderFor,
	logger *slog.Logger,
	interval time.Duration,
) (*Controller, error) {
	if store == nil || injectorFor == nil || rebuilderFor == nil || logger == nil {
		return nil, fmt.Errorf(
			"create Page Wiki session consumer: store, injector resolver, " +
				"rebuilder resolver, and logger are required",
		)
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return &Controller{
		store: store, injectorFor: injectorFor, rebuilderFor: rebuilderFor,
		logger: logger, interval: interval, trigger: make(chan struct{}, 1),
		failures: make(map[string]failureRecord),
		now:      time.Now,
		rebuilds: make(map[string]*scopeRebuild),
	}, nil
}

func (c *Controller) Start(ctx context.Context) {
	go func() {
		c.tick(ctx)
		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-c.trigger:
				c.tick(ctx)
			case <-ticker.C:
				c.tick(ctx)
			}
		}
	}()
}

func (c *Controller) tick(ctx context.Context) {
	c.maybeRebuild(ctx)
	c.scan(ctx)
}

func (c *Controller) Status(ctx context.Context, scopeID string) (Status, error) {
	enabled, err := c.store.AutoInjectEnabled(ctx, scopeID)
	if err != nil {
		return Status{}, fmt.Errorf("read Page Wiki ingestion status: %w", err)
	}
	status := Status{AutoInject: enabled, Rebuild: c.rebuildSnapshot(scopeID)}
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
	return Status{AutoInject: enabled, Rebuild: c.rebuildSnapshot(scopeID)}, nil
}

// Rebuild arms scopeID's own rebuild slot. A scope whose rebuild is already
// queued or running keeps its armed cutoff and reads back its current state,
// so repeated requests merge — per scope, independently of every other one.
func (c *Controller) Rebuild(_ context.Context, scopeID string, since time.Time) (Status, error) {
	if strings.TrimSpace(scopeID) == "" {
		return Status{}, fmt.Errorf("rebuild Page Wiki: scope is required")
	}
	c.stateMu.Lock()
	entry, found := c.rebuilds[scopeID]
	if !found {
		entry = &scopeRebuild{status: RebuildStatus{State: RebuildIdle}}
		c.rebuilds[scopeID] = entry
	}
	if entry.status.State != RebuildQueued && entry.status.State != RebuildRunning {
		entry.status = RebuildStatus{State: RebuildQueued, FinishedAt: entry.status.FinishedAt}
		entry.since = since
	}
	snapshot := entry.status
	c.stateMu.Unlock()
	select {
	case c.trigger <- struct{}{}:
	default:
	}
	return Status{AutoInject: true, Rebuild: snapshot}, nil
}

// maybeRebuild drains the scopes with a queued rebuild on the consumer
// goroutine, using the loop's context so a disconnected HTTP client cannot
// cancel it.
func (c *Controller) maybeRebuild(ctx context.Context) {
	for _, scopeID := range c.queuedRebuildScopes() {
		c.runScopeRebuild(ctx, scopeID)
	}
}

// queuedRebuildScopes snapshots the scopes waiting for a rebuild in a
// deterministic order. Scopes queued after the snapshot are picked up by the
// next tick: Rebuild always pings the trigger, and the trigger's one slot of
// buffer means that wakeup survives an in-flight tick.
func (c *Controller) queuedRebuildScopes() []string {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	scopes := make([]string, 0, len(c.rebuilds))
	for scopeID, entry := range c.rebuilds {
		if entry.status.State == RebuildQueued {
			scopes = append(scopes, scopeID)
		}
	}
	sort.Strings(scopes)
	return scopes
}

// runScopeRebuild moves one scope from queued to running, executes it, and
// records its terminal state. Only that scope's slot is touched, so a
// concurrent request for another scope keeps its own queued state.
func (c *Controller) runScopeRebuild(ctx context.Context, scopeID string) {
	c.stateMu.Lock()
	entry, found := c.rebuilds[scopeID]
	if !found || entry.status.State != RebuildQueued {
		c.stateMu.Unlock()
		return
	}
	entry.status.State = RebuildRunning
	since := entry.since
	c.stateMu.Unlock()

	err := c.rebuildScope(ctx, scopeID, since)

	c.stateMu.Lock()
	if err != nil {
		entry.status = RebuildStatus{
			State: RebuildFailed, Error: err.Error(), FinishedAt: entry.status.FinishedAt,
		}
	} else {
		finished := c.now()
		entry.status = RebuildStatus{State: RebuildIdle, FinishedAt: &finished}
	}
	c.stateMu.Unlock()
	if err != nil {
		c.logger.ErrorContext(ctx, "Page Wiki rebuild failed", "scope_id", scopeID, "error", err)
	}
}

// rebuildScope resolves the scope's rebuilder before taking mu — hydrating a
// cold scope must not block the injection scan — then rebuilds under mu so a
// rebuild never races an in-flight injection. A successful rebuild clears
// only that scope's injection backoff.
func (c *Controller) rebuildScope(ctx context.Context, scopeID string, since time.Time) error {
	rebuilder, err := c.rebuilderFor(ctx, scopeID)
	if err != nil {
		return fmt.Errorf("resolve Page Wiki rebuilder: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := rebuilder.RebuildPageWiki(ctx, scopeID, ProcessorName, ProcessorVersion, since); err != nil {
		return err
	}
	prefix := scopeID + "/"
	for key := range c.failures {
		if strings.HasPrefix(key, prefix) {
			delete(c.failures, key)
		}
	}
	return nil
}

// rebuildSnapshot reports scopeID's rebuild state. A scope that never
// requested one reads back idle, the state every scope starts in.
func (c *Controller) rebuildSnapshot(scopeID string) RebuildStatus {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if entry, found := c.rebuilds[scopeID]; found {
		return entry.status
	}
	return RebuildStatus{State: RebuildIdle}
}

// rebuildQueued reports whether any scope is waiting for a rebuild: the scan
// yields to a queued rebuild whichever scope asked for it, because the
// rebuild needs the same lock the scan holds.
func (c *Controller) rebuildQueued() bool {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	for _, entry := range c.rebuilds {
		if entry.status.State == RebuildQueued {
			return true
		}
	}
	return false
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
		// A queued rebuild wipes everything this pass would build; yield
		// the lock now instead of after the whole backlog. The remaining
		// streams are picked up by the next tick.
		if c.rebuildQueued() {
			return
		}
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
	ctx = session.WithScope(ctx, stream.ScopeID)
	// Resolve per stream: one loop serves every scope, and a scope whose
	// service cannot be resolved must fail only its own streams.
	injector, err := c.injectorFor(ctx, stream.ScopeID)
	if err != nil {
		return fmt.Errorf("resolve Page Wiki injector: %w", err)
	}
	result, err := injector.InjectSession(ctx, request)
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
