package extractionshadow

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
	teamruntime "github.com/pax-beehive/pax-nexus/internal/teamnote/runtime"
)

// SliceRecord captures one extraction call's products and usage for the
// shadow telemetry.
type SliceRecord struct {
	SessionID             string             `json:"session_id"`
	FromSequence          int64              `json:"from_sequence"`
	ToSequence            int64              `json:"to_sequence"`
	Events                int                `json:"events"`
	Candidates            int                `json:"candidates"`
	Rejections            int                `json:"rejections"`
	Claims                int                `json:"claims,omitempty"`
	StateDecisions        int                `json:"state_decisions,omitempty"`
	ClaimRejections       int                `json:"claim_rejections,omitempty"`
	DecisionRejections    int                `json:"decision_rejections,omitempty"`
	InteractionRejections int                `json:"interaction_rejections,omitempty"`
	NoStateEvents         int                `json:"no_state_events,omitempty"`
	UnreviewedEvents      int                `json:"unreviewed_events,omitempty"`
	InvalidNoStateEvents  int                `json:"invalid_no_state_events,omitempty"`
	OrphanClaims          int                `json:"orphan_claims,omitempty"`
	WouldVerify           []string           `json:"would_verify,omitempty"`
	Trace                 *extractor.TraceV2 `json:"trace,omitempty"`
	Usage                 extractor.Usage    `json:"usage"`
	DurationMS            int64              `json:"duration_ms"`
	Error                 string             `json:"error,omitempty"`
}

// recordingExtractor wraps the protocol extractor and records every slice
// call, so usage, latency, and v2 products are observable without changing
// the pipeline.
type recordingExtractor struct {
	delegate extractor.Extractor
	mu       sync.Mutex
	slices   []SliceRecord
}

func (r *recordingExtractor) Extract(ctx context.Context, slice sessionlake.Slice) (extractor.Result, error) {
	startedAt := time.Now()
	result, err := r.delegate.Extract(ctx, slice)
	record := SliceRecord{
		SessionID: slice.Actor.SessionID, FromSequence: slice.FromSequence, ToSequence: slice.ToSequence,
		Events: len(slice.Events), Candidates: len(result.Candidates), Rejections: len(result.Rejections),
		Usage: result.Usage, DurationMS: time.Since(startedAt).Milliseconds(),
	}
	if result.Trace != nil {
		record.Claims = len(result.Trace.Claims)
		record.StateDecisions = len(result.Trace.StateDecisions)
		record.ClaimRejections = len(result.Trace.ClaimRejections)
		record.DecisionRejections = len(result.Trace.DecisionRejections)
		record.InteractionRejections = len(result.Trace.InteractionRejections)
		record.NoStateEvents = len(result.Trace.NoStateEventIDs)
		record.UnreviewedEvents = len(result.Trace.UnreviewedEventIDs)
		record.InvalidNoStateEvents = len(result.Trace.InvalidNoStateEventIDs)
		record.OrphanClaims = len(result.Trace.OrphanClaimIDs)
		record.WouldVerify = result.Trace.WouldVerify
		record.Trace = result.Trace
	}
	if err != nil {
		record.Error = err.Error()
	}
	r.mu.Lock()
	r.slices = append(r.slices, record)
	r.mu.Unlock()
	return result, err
}

func (r *recordingExtractor) recorded() []SliceRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]SliceRecord(nil), r.slices...)
}

// CaseRun is the shadow replay outcome for one case.
type CaseRun struct {
	CaseID  string
	ScopeID string
	Streams int
	Events  int
	Notes   []teamnote.Note
	Slices  []SliceRecord
}

// RunCase replays one case's fixed event streams through the real extraction
// pipeline (session lake, runtime, in-memory episode store, in-memory note
// ledger) with the given protocol extractor and returns the admitted notes
// plus per-slice telemetry.
func RunCase(ctx context.Context, caseID, scopeID string, streams []StreamEvents, delegate extractor.Extractor) (CaseRun, error) {
	if delegate == nil {
		return CaseRun{}, fmt.Errorf("run extraction shadow case %q: extractor is required", caseID)
	}
	recorder := &recordingExtractor{delegate: delegate}
	noteStore := teamnote.NewScopedLedgerStore(teamnote.DefaultTTLPolicy(), teamnote.SystemClock{})
	app, err := teamruntime.New(sessionlake.New(newMemoryRepository()), recorder, teamruntime.Config{NoteStore: noteStore})
	if err != nil {
		return CaseRun{}, fmt.Errorf("run extraction shadow case %q: %w", caseID, err)
	}
	ctx = teamnote.WithScope(ctx, scopeID)
	eventCount := 0
	for _, stream := range streams {
		eventCount += len(stream.Events)
		receipt, err := app.ObserveSession(ctx, teamnote.SessionBatch{Events: stream.Events, Complete: true})
		if err != nil {
			return CaseRun{}, fmt.Errorf("run extraction shadow case %q observe %q: %w", caseID, stream.Actor.SessionID, err)
		}
		for {
			more, err := app.ProcessExtraction(ctx, stream.Actor, receipt.Cursor, false)
			if err != nil {
				return CaseRun{}, fmt.Errorf("run extraction shadow case %q process %q: %w", caseID, stream.Actor.SessionID, err)
			}
			if !more {
				break
			}
		}
	}
	return CaseRun{
		CaseID: caseID, ScopeID: scopeID, Streams: len(streams), Events: eventCount,
		Notes: noteStore.SnapshotNotes(scopeID), Slices: recorder.recorded(),
	}, nil
}

// memoryRepository is an in-memory sessionlake.Repository for shadow replay.
type memoryRepository struct {
	events  map[string][]teamnote.SessionEvent
	cursors map[string]int64
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{events: make(map[string][]teamnote.SessionEvent), cursors: make(map[string]int64)}
}

func (r *memoryRepository) streamKey(scopeID string, actor teamnote.Actor) string {
	return scopeID + "/" + actor.AgentID + "/" + actor.SessionID
}

func (r *memoryRepository) AppendSession(_ context.Context, scopeID string, batch teamnote.SessionBatch) (teamnote.IngestReceipt, error) {
	if len(batch.Events) == 0 {
		return teamnote.IngestReceipt{}, fmt.Errorf("append shadow session: events are required")
	}
	key := r.streamKey(scopeID, batch.Events[0].Actor)
	r.events[key] = append(r.events[key], batch.Events...)
	return teamnote.IngestReceipt{Accepted: len(batch.Events), Cursor: batch.Events[len(batch.Events)-1].Sequence}, nil
}

func (r *memoryRepository) SessionEvents(_ context.Context, scopeID string, actor teamnote.Actor, after int64, limit int) ([]teamnote.SessionEvent, error) {
	var result []teamnote.SessionEvent
	for _, event := range r.events[r.streamKey(scopeID, actor)] {
		if event.Sequence > after && len(result) < limit {
			result = append(result, event)
		}
	}
	return result, nil
}

func (r *memoryRepository) SessionEventsBefore(_ context.Context, scopeID string, actor teamnote.Actor, atOrBefore int64, limit int) ([]teamnote.SessionEvent, error) {
	var result []teamnote.SessionEvent
	for _, event := range r.events[r.streamKey(scopeID, actor)] {
		if event.Sequence <= atOrBefore {
			result = append(result, event)
		}
	}
	if len(result) > limit {
		result = result[len(result)-limit:]
	}
	return result, nil
}

func (r *memoryRepository) SessionLatestSequence(_ context.Context, scopeID string, actor teamnote.Actor) (int64, error) {
	events := r.events[r.streamKey(scopeID, actor)]
	if len(events) == 0 {
		return 0, nil
	}
	return events[len(events)-1].Sequence, nil
}

func (r *memoryRepository) ExtractionCursor(_ context.Context, scopeID string, actor teamnote.Actor) (int64, error) {
	return r.cursors[r.streamKey(scopeID, actor)], nil
}

func (r *memoryRepository) AdvanceExtractionCursor(_ context.Context, scopeID string, actor teamnote.Actor, cursor int64) error {
	r.cursors[r.streamKey(scopeID, actor)] = cursor
	return nil
}
