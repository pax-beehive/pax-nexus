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
	// scopeLocks serializes, per scope, the three operations that mutate a
	// scope's wiki: scan-driven injection, manual InjectSession, and rebuild.
	// Scopes never contend with each other; within a scope injection stays
	// strictly serial (the wiki mirror is per-scope in-memory state).
	scopeLocksMu sync.Mutex
	scopeLocks   map[string]*sync.Mutex
	// failuresMu guards failures; it is its own small lock so backoff
	// bookkeeping never waits behind an in-flight injection.
	failuresMu sync.Mutex
	failures   map[string]failureRecord
	now        func() time.Time
	// injectWaiters counts, per scope, the manual InjectSession callers
	// waiting on that scope's lock. A scope's job yields to them between
	// streams, exactly like it yields to a queued rebuild: a user-facing
	// inject must never sit behind a whole backlog sweep of multi-minute
	// LLM injections.
	waitersMu     sync.Mutex
	injectWaiters map[string]int
	// stateMu guards rebuilds below. It is separate from the scope locks so
	// status reads never wait behind a minutes-long injection scan.
	stateMu  sync.Mutex
	rebuilds map[string]*scopeRebuild
	// slots is the injection worker pool: a job (one scope's rebuild + its
	// pending streams, in order) holds one slot for its whole run. Capacity
	// is WithInjectConcurrency's K — the process-wide ceiling on concurrent
	// LLM injection spend. Within a scope everything stays serial.
	slots chan struct{}
	// inFlight dedups jobs per scope: a tick firing while a scope's job is
	// still running skips that scope instead of queueing behind it.
	inFlightMu sync.Mutex
	inFlight   map[string]bool
	jobs       sync.WaitGroup
	cancel     context.CancelFunc
	done       chan struct{}
}

// Option configures a Controller at construction. See WithInjectConcurrency.
type Option func(*Controller) error

// WithInjectConcurrency caps how many scope jobs run at once (default 2).
// K is a spend ceiling, not a fairness knob: a single-scope deployment
// only ever produces one job regardless of K.
func WithInjectConcurrency(k int) Option {
	return func(c *Controller) error {
		if k < 1 {
			return fmt.Errorf("create Page Wiki session consumer: inject concurrency must be >= 1, got %d", k)
		}
		c.slots = make(chan struct{}, k)
		return nil
	}
}

func New(
	store Store,
	injectorFor InjectorFor,
	rebuilderFor RebuilderFor,
	logger *slog.Logger,
	interval time.Duration,
	options ...Option,
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
	c := &Controller{
		store: store, injectorFor: injectorFor, rebuilderFor: rebuilderFor,
		logger: logger, interval: interval, trigger: make(chan struct{}, 1),
		scopeLocks:    make(map[string]*sync.Mutex),
		failures:      make(map[string]failureRecord),
		injectWaiters: make(map[string]int),
		now:           time.Now,
		rebuilds:      make(map[string]*scopeRebuild),
		slots:         make(chan struct{}, 2),
		inFlight:      make(map[string]bool),
	}
	for _, option := range options {
		if err := option(c); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Start launches the background consume loop. It stops when ctx is
// cancelled or Stop is called.
func (c *Controller) Start(ctx context.Context) {
	ctx, c.cancel = context.WithCancel(ctx)
	c.done = make(chan struct{})
	go func(done chan struct{}) {
		defer close(done)
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
	}(c.done)
}

// Stop cancels the consume loop and waits for the background goroutine to
// exit, bounded by ctx. An in-flight injection or rebuild observes the same
// cancelled context. Stopping a controller that was never started is a
// no-op.
func (c *Controller) Stop(ctx context.Context) error {
	if c.cancel == nil {
		return nil
	}
	c.cancel()
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop Page Wiki session consumer: %w", ctx.Err())
	}
}

type scopeJob struct {
	scopeID string
	streams []Stream
}

func (c *Controller) tick(ctx context.Context) {
	streams, err := c.store.PendingStreams(ctx)
	if err != nil {
		c.logger.ErrorContext(ctx, "Page Wiki scan failed", "error", err)
		// Queued rebuilds must still run even when the scan query fails.
		streams = nil
	}
	for _, job := range c.buildScopeJobs(streams) {
		if !c.markInFlight(job.scopeID) {
			continue
		}
		c.jobs.Add(1)
		// Claim a free slot here, on the dispatch goroutine, rather than
		// leaving every job's goroutine to race for it: two freshly spawned
		// goroutines have no ordering guarantee (the Go scheduler may run
		// the most recently spawned one first), so a naive race would let a
		// later scope jump an earlier one. Grabbing what's available up
		// front makes dispatch order equal slot-acquisition order for
		// however many jobs fit; only the overflow blocks inside the job.
		held := false
		select {
		case c.slots <- struct{}{}:
			held = true
		default:
		}
		go c.runScopeJob(ctx, job, held)
	}
}

// buildScopeJobs groups the pending streams by scope, preserving the
// store's fairness-interleaved order both across jobs (first appearance)
// and within each job. Scopes with a queued rebuild but no pending streams
// get a rebuild-only job.
func (c *Controller) buildScopeJobs(streams []Stream) []scopeJob {
	jobs := make([]scopeJob, 0, len(streams))
	index := make(map[string]int)
	for _, stream := range streams {
		at, seen := index[stream.ScopeID]
		if !seen {
			at = len(jobs)
			index[stream.ScopeID] = at
			jobs = append(jobs, scopeJob{scopeID: stream.ScopeID})
		}
		jobs[at].streams = append(jobs[at].streams, stream)
	}
	for _, scopeID := range c.queuedRebuildScopes() {
		if _, seen := index[scopeID]; !seen {
			jobs = append(jobs, scopeJob{scopeID: scopeID})
		}
	}
	return jobs
}

func (c *Controller) markInFlight(scopeID string) bool {
	c.inFlightMu.Lock()
	defer c.inFlightMu.Unlock()
	if c.inFlight[scopeID] {
		return false
	}
	c.inFlight[scopeID] = true
	return true
}

func (c *Controller) clearInFlight(scopeID string) {
	c.inFlightMu.Lock()
	defer c.inFlightMu.Unlock()
	delete(c.inFlight, scopeID)
}

// runScopeJob is one scope's turn: rebuild first if queued, then the
// scope's streams in order. It holds one pool slot for its whole run and
// yields to a rebuild queued mid-pass. slotHeld reports whether tick already
// claimed the slot on the job's behalf; otherwise the job blocks here until
// one frees up.
//
// The final rebuildQueuedFor recheck-and-ping is deferred, and registered
// before clearInFlight's defer, so it runs after clearInFlight on every exit
// path (including ctx cancellation) — defers run LIFO. Pinging first would
// let the woken tick's markInFlight see this scope as still in-flight and
// skip it, consuming the trigger's one-slot buffer with nobody left to ping
// again until the next ticker fire (see queuedRebuildScopes).
func (c *Controller) runScopeJob(ctx context.Context, job scopeJob, slotHeld bool) {
	defer c.jobs.Done()
	defer func() {
		if c.rebuildQueuedFor(job.scopeID) {
			c.ping()
		}
	}()
	defer c.clearInFlight(job.scopeID)
	if !slotHeld {
		select {
		case c.slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
	}
	defer func() { <-c.slots }()

	if c.rebuildQueuedFor(job.scopeID) {
		c.runScopeRebuild(ctx, job.scopeID)
	}
	now := c.now()
	for _, stream := range job.streams {
		if ctx.Err() != nil {
			return
		}
		// A queued rebuild wipes everything this pass would build, and a
		// waiting manual inject is a user staring at a spinner; yield the
		// scope's lock now instead of after the whole backlog. The remaining
		// streams are picked up by the next tick.
		if c.rebuildQueuedFor(job.scopeID) || c.injectWaitingFor(job.scopeID) {
			break
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
		c.clearFailure(stream)
	}
}

// ping wakes the consumer loop; the one-slot buffer means a wakeup during
// an in-flight tick is remembered, not lost. Callers that ping because a
// scope's rebuild is still queued (runScopeJob, Rebuild) must not do so
// while that scope is still marked in-flight — see runScopeJob's defer
// ordering — or the tick this wakes will skip the scope via markInFlight
// and consume the buffered wakeup for nothing.
func (c *Controller) ping() {
	select {
	case c.trigger <- struct{}{}:
	default:
	}
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
//
// The response's AutoInject reflects the store: the rebuild itself enables
// ingestion only when its commit succeeds (postgres Repository
// RebuildPageWiki), so a queued — or later failed — rebuild must not report
// auto_inject as already on.
func (c *Controller) Rebuild(ctx context.Context, scopeID string, since time.Time) (Status, error) {
	if strings.TrimSpace(scopeID) == "" {
		return Status{}, fmt.Errorf("rebuild Page Wiki: scope is required")
	}
	enabled, err := c.store.AutoInjectEnabled(ctx, scopeID)
	if err != nil {
		return Status{}, fmt.Errorf("rebuild Page Wiki: read ingestion status: %w", err)
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
	c.ping()
	return Status{AutoInject: enabled, Rebuild: snapshot}, nil
}

// maybeRebuild drains every queued scope rebuild on the caller's
// goroutine. Production ticks fold rebuilds into scope jobs; this serial
// path remains for the deterministic test driver.
func (c *Controller) maybeRebuild(ctx context.Context) {
	for _, scopeID := range c.queuedRebuildScopes() {
		c.runScopeRebuild(ctx, scopeID)
	}
}

// queuedRebuildScopes snapshots the scopes waiting for a rebuild in a
// deterministic order. Scopes queued after the snapshot are picked up by the
// next tick: Rebuild always pings the trigger, and the trigger's one slot of
// buffer means that wakeup survives an in-flight tick — as long as the scope
// it names is no longer marked in-flight by the time that tick runs
// markInFlight, which is why runScopeJob defers its own re-check-and-ping to
// run after clearInFlight.
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

// rebuildScope resolves the scope's rebuilder before taking its lock —
// hydrating a cold scope must not block the injection scan — then rebuilds
// under that scope's lock so a rebuild never races an in-flight injection.
// A successful rebuild clears only that scope's injection backoff.
func (c *Controller) rebuildScope(ctx context.Context, scopeID string, since time.Time) error {
	rebuilder, err := c.rebuilderFor(ctx, scopeID)
	if err != nil {
		return fmt.Errorf("resolve Page Wiki rebuilder: %w", err)
	}
	lock := c.scopeLock(scopeID)
	lock.Lock()
	defer lock.Unlock()
	if err := rebuilder.RebuildPageWiki(ctx, scopeID, ProcessorName, ProcessorVersion, since); err != nil {
		return err
	}
	c.clearScopeFailures(scopeID)
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

// scopeLock returns scopeID's lock, creating it on first use. It serializes
// scan-driven injection, manual InjectSession, and rebuild for that scope
// only; other scopes never contend on it.
func (c *Controller) scopeLock(scopeID string) *sync.Mutex {
	c.scopeLocksMu.Lock()
	defer c.scopeLocksMu.Unlock()
	lock, ok := c.scopeLocks[scopeID]
	if !ok {
		lock = &sync.Mutex{}
		c.scopeLocks[scopeID] = lock
	}
	return lock
}

// rebuildQueuedFor reports whether scopeID's own rebuild is waiting. The
// scan yields only that scope's streams to it; other scopes are unaffected.
func (c *Controller) rebuildQueuedFor(scopeID string) bool {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	entry, found := c.rebuilds[scopeID]
	return found && entry.status.State == RebuildQueued
}

func (c *Controller) clearFailure(stream Stream) {
	c.failuresMu.Lock()
	defer c.failuresMu.Unlock()
	delete(c.failures, streamKey(stream))
}

func (c *Controller) clearScopeFailures(scopeID string) {
	c.failuresMu.Lock()
	defer c.failuresMu.Unlock()
	prefix := scopeID + "/"
	for key := range c.failures {
		if strings.HasPrefix(key, prefix) {
			delete(c.failures, key)
		}
	}
}

// addInjectWaiter registers a manual InjectSession caller as waiting for
// scopeID's lock and returns the func that deregisters it.
func (c *Controller) addInjectWaiter(scopeID string) func() {
	c.waitersMu.Lock()
	c.injectWaiters[scopeID]++
	c.waitersMu.Unlock()
	return func() {
		c.waitersMu.Lock()
		c.injectWaiters[scopeID]--
		c.waitersMu.Unlock()
	}
}

// injectWaitingFor reports whether a manual InjectSession call is waiting
// for scopeID's lock; the scope's job yields to it exactly like it yields
// to a queued rebuild.
func (c *Controller) injectWaitingFor(scopeID string) bool {
	c.waitersMu.Lock()
	defer c.waitersMu.Unlock()
	return c.injectWaiters[scopeID] > 0
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
	// Register as a waiter before contending for the scope lock (consume
	// takes it per stream) so this scope's in-flight job yields at its next
	// between-streams checkpoint instead of finishing the whole sweep first.
	deregister := c.addInjectWaiter(scopeID)
	defer deregister()
	for _, stream := range streams {
		c.clearFailure(stream)
		if err := c.consume(ctx, stream); err != nil {
			return InjectResult{}, err
		}
	}
	return InjectResult{ProcessedStreams: len(streams)}, nil
}

func (c *Controller) consume(ctx context.Context, stream Stream) error {
	ctx = session.WithScope(ctx, stream.ScopeID)
	// Resolve before locking: resolution may hydrate a cold scope (a
	// startup-class cost), and per-scope single-flight in the managers means
	// only this scope's callers wait on it — never the lock's other holders.
	injector, err := c.injectorFor(ctx, stream.ScopeID)
	if err != nil {
		return fmt.Errorf("resolve Page Wiki injector: %w", err)
	}
	lock := c.scopeLock(stream.ScopeID)
	lock.Lock()
	defer lock.Unlock()
	events, err := c.store.SessionEvents(ctx, stream)
	if err != nil {
		return fmt.Errorf("read Page Wiki session events: %w", err)
	}
	if len(events) == 0 {
		return nil
	}
	request := injectionRequest(stream, events)
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
	c.failuresMu.Lock()
	defer c.failuresMu.Unlock()
	record, found := c.failures[streamKey(stream)]
	if !found || record.head != stream.Head {
		return false
	}
	return now.Before(record.nextRetryAt)
}

func (c *Controller) recordFailure(stream Stream) failureRecord {
	c.failuresMu.Lock()
	defer c.failuresMu.Unlock()
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
