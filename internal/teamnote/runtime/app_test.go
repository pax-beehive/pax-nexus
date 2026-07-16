package runtime_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
	teamruntime "github.com/pax-beehive/pax-nexus/internal/teamnote/runtime"
	"github.com/stretchr/testify/suite"
)

type appSuite struct {
	suite.Suite
	repository *runtimeRepository
	extractor  *runtimeExtractor
	app        *teamruntime.App
	logs       bytes.Buffer
}

func TestAppSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(appSuite))
}

func (s *appSuite) SetupTest() {
	s.repository = newRuntimeRepository()
	s.extractor = new(runtimeExtractor)
	logger := slog.New(slog.NewJSONHandler(&s.logs, nil))
	app, err := teamruntime.New(sessionlake.New(s.repository), s.extractor, teamruntime.Config{Logger: logger})
	s.Require().NoError(err)
	s.app = app
}

func (s *appSuite) TestObserveQueuesExtractionAndRecallWithinScope() {
	ctx := teamnote.WithScope(context.Background(), "scope-a")
	actor := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "producer-session"}
	event := runtimeEvent("event-1", actor)
	s.extractor.result = extractor.Result{Candidates: []teamnote.Candidate{{
		ID: "candidate-1", Action: teamnote.ActionCreate, Kind: teamnote.KindBlocker,
		Subject: "release", Body: "Tests are failing.", TaskRef: "release-42",
		Origin: actor, EvidenceEventIDs: []string{event.ID},
	}}}

	receipt, err := s.app.ObserveSession(ctx, teamnote.SessionBatch{Events: []teamnote.SessionEvent{event}, Complete: true})
	s.Require().NoError(err)
	s.Equal(1, receipt.Accepted)
	s.Zero(s.extractor.calls)

	beforeExtraction, err := s.app.RecallNotes(ctx, teamnote.RecallRequest{
		Actor:   teamnote.Actor{UserID: "owner", AgentID: "before", SessionID: "before-session"},
		TaskRef: "release-42", TokenBudget: 256,
	})
	s.Require().NoError(err)
	s.Empty(beforeExtraction.Items)

	more, err := s.app.ProcessExtraction(ctx, actor, receipt.Cursor, false)
	s.Require().NoError(err)
	s.False(more)
	s.Equal(1, s.extractor.calls)
	s.Contains(s.logs.String(), `"msg":"extraction slice completed"`)
	s.Contains(s.logs.String(), `"agent_id":"producer"`)
	s.Contains(s.logs.String(), `"candidates":1`)

	envelope, err := s.app.RecallNotes(ctx, teamnote.RecallRequest{
		Actor:   teamnote.Actor{UserID: "owner", AgentID: "consumer", SessionID: "consumer-session"},
		TaskRef: "release-42", TokenBudget: 256,
	})
	s.Require().NoError(err)
	s.Equal([]string{"[blocker certainty=unresolved] Tests are failing."}, envelope.Items)

	otherScope := teamnote.WithScope(context.Background(), "scope-b")
	envelope, err = s.app.RecallNotes(otherScope, teamnote.RecallRequest{
		Actor:   teamnote.Actor{UserID: "owner", AgentID: "consumer", SessionID: "other-session"},
		TaskRef: "release-42", TokenBudget: 256,
	})
	s.Require().NoError(err)
	s.Empty(envelope.Items)
}

func (s *appSuite) TestIncompleteBatchDoesNotExtract() {
	ctx := teamnote.WithScope(context.Background(), "scope-incomplete")
	actor := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "producer-session"}
	_, err := s.app.ObserveSession(ctx, teamnote.SessionBatch{Events: []teamnote.SessionEvent{runtimeEvent("event", actor)}})
	s.Require().NoError(err)
	s.Equal(0, s.extractor.calls)
}

func (s *appSuite) TestProcessExtractionIsIdempotentAfterCursorAdvance() {
	ctx := teamnote.WithScope(context.Background(), "scope-idempotent")
	actor := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "producer-session"}
	event := runtimeEvent("event", actor)
	_, err := s.app.ObserveSession(ctx, teamnote.SessionBatch{Events: []teamnote.SessionEvent{event}, Complete: true})
	s.Require().NoError(err)
	more, err := s.app.ProcessExtraction(ctx, actor, 1, false)
	s.Require().NoError(err)
	s.False(more)
	more, err = s.app.ProcessExtraction(ctx, actor, 1, false)
	s.Require().NoError(err)
	s.False(more)
	s.Equal(1, s.extractor.calls)
}

func (s *appSuite) TestProcessExtractionCommitsZeroCandidateRunProvenance() {
	store := newRecordingNoteStore()
	app, err := teamruntime.New(sessionlake.New(s.repository), s.extractor, teamruntime.Config{NoteStore: store})
	s.Require().NoError(err)
	ctx := teamnote.WithScope(context.Background(), "scope-zero-candidate")
	actor := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "producer-session"}
	event := runtimeEvent("event-zero-candidate", actor)
	s.extractor.result = extractor.Result{
		Usage: extractor.Usage{InputTokens: 41, OutputTokens: 3},
		Model: "extractor-model", PromptVersion: "prompt-v2",
	}
	_, err = app.ObserveSession(ctx, teamnote.SessionBatch{Events: []teamnote.SessionEvent{event}, Complete: true})
	s.Require().NoError(err)

	more, err := app.ProcessExtraction(ctx, actor, 1, false)
	s.Require().NoError(err)
	s.False(more)
	s.Require().Len(store.runs, 1)
	run := store.runs[0]
	s.Equal("scope-zero-candidate", store.scopeIDs[0])
	s.Equal(event.Sequence, run.FromSequence)
	s.Equal(event.Sequence, run.ToSequence)
	s.NotEmpty(run.ID)
	s.Equal(run.InputChecksum, run.ID)
	s.Equal("extractor-model", run.Model)
	s.Equal("prompt-v2", run.PromptVersion)
	s.Equal(41, run.InputTokens)
	s.Equal(3, run.OutputTokens)
	s.Empty(run.Candidates)
}

func (s *appSuite) TestTimeoutJobSkipsWhenSessionHasAdvanced() {
	ctx := teamnote.WithScope(context.Background(), "scope-timeout")
	actor := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "producer-session"}
	first := runtimeEvent("event-1", actor)
	second := runtimeEvent("event-2", actor)
	second.Sequence = 2
	_, err := s.app.ObserveSession(ctx, teamnote.SessionBatch{Events: []teamnote.SessionEvent{first}})
	s.Require().NoError(err)
	_, err = s.app.ObserveSession(ctx, teamnote.SessionBatch{Events: []teamnote.SessionEvent{second}})
	s.Require().NoError(err)

	more, err := s.app.ProcessExtraction(ctx, actor, 1, true)
	s.Require().NoError(err)
	s.False(more)
	s.Zero(s.extractor.calls)

	more, err = s.app.ProcessExtraction(ctx, actor, 2, true)
	s.Require().NoError(err)
	s.False(more)
	s.Equal(1, s.extractor.calls)
}

func (s *appSuite) TestCompleteJobStopsAtCapturedBatchCursor() {
	ctx := teamnote.WithScope(context.Background(), "scope-boundary")
	actor := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "producer-session"}
	first := runtimeEvent("event-1", actor)
	second := runtimeEvent("event-2", actor)
	second.Sequence = 2
	_, err := s.app.ObserveSession(ctx, teamnote.SessionBatch{Events: []teamnote.SessionEvent{first, second}})
	s.Require().NoError(err)

	more, err := s.app.ProcessExtraction(ctx, actor, 1, false)
	s.Require().NoError(err)
	s.False(more)
	s.Equal(int64(1), s.repository.cursors[runtimeStreamKey("scope-boundary", actor)])

	more, err = s.app.ProcessExtraction(ctx, actor, 2, false)
	s.Require().NoError(err)
	s.False(more)
	s.Equal(int64(2), s.repository.cursors[runtimeStreamKey("scope-boundary", actor)])
	s.Equal(2, s.extractor.calls)
}

func (s *appSuite) TestCapsSlicesAndReportsContinuation() {
	app, err := teamruntime.New(sessionlake.New(s.repository), s.extractor, teamruntime.Config{
		SliceEventLimit: 1, SliceTokenLimit: 1024, SliceOverlap: 1, MaxSlicesPerJob: 2,
	})
	s.Require().NoError(err)
	ctx := teamnote.WithScope(context.Background(), "scope-continuation")
	actor := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "producer-session"}
	events := []teamnote.SessionEvent{
		runtimeEvent("event-1", actor), runtimeEvent("event-2", actor), runtimeEvent("event-3", actor),
	}
	for index := range events {
		events[index].Sequence = int64(index + 1)
	}
	_, err = app.ObserveSession(ctx, teamnote.SessionBatch{Events: events})
	s.Require().NoError(err)

	more, err := app.ProcessExtraction(ctx, actor, 3, false)
	s.Require().NoError(err)
	s.True(more)
	s.Equal(2, s.extractor.calls)
	s.Equal(int64(2), s.repository.cursors[runtimeStreamKey("scope-continuation", actor)])

	more, err = app.ProcessExtraction(ctx, actor, 3, false)
	s.Require().NoError(err)
	s.False(more)
	s.Equal(3, s.extractor.calls)
}

type runtimeExtractor struct {
	result extractor.Result
	calls  int
}

type recordingNoteStore struct {
	delegate *teamnote.ScopedLedgerStore
	scopeIDs []string
	runs     []teamnote.ExtractionRun
}

func newRecordingNoteStore() *recordingNoteStore {
	return &recordingNoteStore{delegate: teamnote.NewScopedLedgerStore(teamnote.DefaultTTLPolicy(), teamnote.SystemClock{})}
}

func (s *recordingNoteStore) ApplyExtractionRun(ctx context.Context, scopeID string, run teamnote.ExtractionRun) ([]teamnote.Note, error) {
	s.scopeIDs = append(s.scopeIDs, scopeID)
	s.runs = append(s.runs, run)
	return s.delegate.ApplyExtractionRun(ctx, scopeID, run)
}

func (s *recordingNoteStore) RecallNotes(ctx context.Context, scopeID string, request teamnote.RecallRequest) (teamnote.NoteEnvelope, error) {
	return s.delegate.RecallNotes(ctx, scopeID, request)
}

func (e *runtimeExtractor) Extract(_ context.Context, _ sessionlake.Slice) (extractor.Result, error) {
	e.calls++
	return e.result, nil
}

type runtimeRepository struct {
	events  map[string][]teamnote.SessionEvent
	cursors map[string]int64
}

func newRuntimeRepository() *runtimeRepository {
	return &runtimeRepository{events: make(map[string][]teamnote.SessionEvent), cursors: make(map[string]int64)}
}

func (r *runtimeRepository) AppendSession(_ context.Context, scopeID string, batch teamnote.SessionBatch) (teamnote.IngestReceipt, error) {
	key := runtimeStreamKey(scopeID, batch.Events[0].Actor)
	r.events[key] = append(r.events[key], batch.Events...)
	return teamnote.IngestReceipt{Accepted: len(batch.Events), Cursor: batch.Events[len(batch.Events)-1].Sequence}, nil
}

func (r *runtimeRepository) SessionEvents(_ context.Context, scopeID string, actor teamnote.Actor, after int64, limit int) ([]teamnote.SessionEvent, error) {
	var result []teamnote.SessionEvent
	for _, event := range r.events[runtimeStreamKey(scopeID, actor)] {
		if event.Sequence > after && len(result) < limit {
			result = append(result, event)
		}
	}
	return result, nil
}

func (r *runtimeRepository) SessionEventsBefore(_ context.Context, scopeID string, actor teamnote.Actor, atOrBefore int64, limit int) ([]teamnote.SessionEvent, error) {
	var result []teamnote.SessionEvent
	for _, event := range r.events[runtimeStreamKey(scopeID, actor)] {
		if event.Sequence <= atOrBefore {
			result = append(result, event)
		}
	}
	if len(result) > limit {
		result = result[len(result)-limit:]
	}
	return result, nil
}

func (r *runtimeRepository) SessionLatestSequence(_ context.Context, scopeID string, actor teamnote.Actor) (int64, error) {
	events := r.events[runtimeStreamKey(scopeID, actor)]
	if len(events) == 0 {
		return 0, nil
	}
	return events[len(events)-1].Sequence, nil
}

func (r *runtimeRepository) ExtractionCursor(_ context.Context, scopeID string, actor teamnote.Actor) (int64, error) {
	return r.cursors[runtimeStreamKey(scopeID, actor)], nil
}

func (r *runtimeRepository) AdvanceExtractionCursor(_ context.Context, scopeID string, actor teamnote.Actor, cursor int64) error {
	r.cursors[runtimeStreamKey(scopeID, actor)] = cursor
	return nil
}

func runtimeEvent(id string, actor teamnote.Actor) teamnote.SessionEvent {
	return teamnote.SessionEvent{
		ID: id, Actor: actor, Sequence: 1, Type: "assistant", Content: "Tests are failing.",
		TaskRef: "release-42", Visibility: "team_note_eligible",
		OccurredAt: time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC),
	}
}

func runtimeStreamKey(scopeID string, actor teamnote.Actor) string {
	return fmt.Sprintf("%s/%s/%s", scopeID, actor.AgentID, actor.SessionID)
}
