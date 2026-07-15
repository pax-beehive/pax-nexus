package sessionlake_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/session"
	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
	"github.com/stretchr/testify/suite"
)

type lakeSuite struct {
	suite.Suite
	repository *memoryRepository
	lake       *sessionlake.Lake
}

func TestLakeSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(lakeSuite))
}

func (s *lakeSuite) SetupTest() {
	s.repository = newMemoryRepository()
	s.lake = sessionlake.New(s.repository)
}

func (s *lakeSuite) TestObservePlanAndCommit() {
	ctx := session.WithScope(context.Background(), "scope-1")
	actor := session.Actor{UserID: "owner", AgentID: "producer", SessionID: "session-1"}
	batch := session.SessionBatch{Complete: true, Events: []session.SessionEvent{
		lakeEvent("event-1", actor, 1), lakeEvent("event-2", actor, 2),
	}}

	receipt, err := s.lake.Observe(ctx, batch)
	s.Require().NoError(err)
	s.Equal(2, receipt.Accepted)

	policy := sessionlake.SlicePolicy{EventLimit: 10, TokenLimit: 1000}
	planned, err := s.lake.NextSlice(ctx, actor, policy)
	s.Require().NoError(err)
	s.Equal(int64(1), planned.FromSequence)
	s.Equal(int64(2), planned.ToSequence)
	s.NotEmpty(planned.InputChecksum)

	s.Require().NoError(s.lake.CommitSlice(ctx, planned))
	next, err := s.lake.NextSlice(ctx, actor, policy)
	s.Require().NoError(err)
	s.Empty(next.Events)
}

func (s *lakeSuite) TestScopeIsRequired() {
	_, err := s.lake.Observe(context.Background(), session.SessionBatch{})
	s.Require().ErrorIs(err, session.ErrMissingScope)

	_, err = s.lake.NextSlice(context.Background(), session.Actor{}, sessionlake.SlicePolicy{EventLimit: 10, TokenLimit: 1000})
	s.Require().ErrorIs(err, session.ErrMissingScope)
}

func (s *lakeSuite) TestPlansBoundedMultiTurnSlicesWithOverlap() {
	ctx := session.WithScope(context.Background(), "scope-overlap")
	actor := session.Actor{UserID: "owner", AgentID: "producer", SessionID: "session"}
	events := make([]session.SessionEvent, 6)
	for index := range events {
		events[index] = lakeEvent(fmt.Sprintf("event-%d", index+1), actor, int64(index+1))
	}
	_, err := s.lake.Observe(ctx, session.SessionBatch{Events: events, Complete: true})
	s.Require().NoError(err)
	policy := sessionlake.SlicePolicy{EventLimit: 3, TokenLimit: 1000, Overlap: 2}

	first, err := s.lake.NextSlice(ctx, actor, policy)
	s.Require().NoError(err)
	s.Equal([]string{"event-1", "event-2", "event-3"}, first.NewEventIDs)
	s.Empty(first.OverlapEventIDs)
	s.Require().NoError(s.lake.CommitSlice(ctx, first))

	second, err := s.lake.NextSlice(ctx, actor, policy)
	s.Require().NoError(err)
	s.Equal([]string{"event-2", "event-3"}, second.OverlapEventIDs)
	s.Equal([]string{"event-4", "event-5", "event-6"}, second.NewEventIDs)
	s.Equal([]string{"event-2", "event-3", "event-4", "event-5", "event-6"}, eventIDs(second.Events))
}

func (s *lakeSuite) TestTokenBudgetAlwaysMakesProgress() {
	ctx := session.WithScope(context.Background(), "scope-tokens")
	actor := session.Actor{UserID: "owner", AgentID: "producer", SessionID: "session"}
	first := lakeEvent("large-1", actor, 1)
	first.Content = strings.Repeat("a", 400)
	second := lakeEvent("large-2", actor, 2)
	second.Content = strings.Repeat("b", 400)
	_, err := s.lake.Observe(ctx, session.SessionBatch{Events: []session.SessionEvent{first, second}, Complete: true})
	s.Require().NoError(err)

	slice, err := s.lake.NextSlice(ctx, actor, sessionlake.SlicePolicy{EventLimit: 25, TokenLimit: 64, Overlap: 3})
	s.Require().NoError(err)
	s.Equal([]string{"large-1"}, slice.NewEventIDs)
	s.Equal(int64(1), slice.ToSequence)
}

func (s *lakeSuite) TestRejectsInvalidSlicePolicy() {
	ctx := session.WithScope(context.Background(), "scope-invalid")
	tests := []sessionlake.SlicePolicy{
		{},
		{EventLimit: 1, TokenLimit: 1, Overlap: -1},
	}
	for _, policy := range tests {
		_, err := s.lake.NextSlice(ctx, session.Actor{}, policy)
		s.Require().Error(err)
	}
}

func (s *lakeSuite) TestChecksExpectedSessionHead() {
	ctx := session.WithScope(context.Background(), "scope-head")
	actor := session.Actor{UserID: "owner", AgentID: "producer", SessionID: "session"}
	_, err := s.lake.Observe(ctx, session.SessionBatch{Events: []session.SessionEvent{lakeEvent("event-1", actor, 1)}})
	s.Require().NoError(err)
	current, err := s.lake.IsCurrent(ctx, actor, 1)
	s.Require().NoError(err)
	s.True(current)
	current, err = s.lake.IsCurrent(ctx, actor, 2)
	s.Require().NoError(err)
	s.False(current)
}

type memoryRepository struct {
	events  map[string][]session.SessionEvent
	cursors map[string]int64
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{events: make(map[string][]session.SessionEvent), cursors: make(map[string]int64)}
}

func (r *memoryRepository) AppendSession(_ context.Context, scopeID string, batch session.SessionBatch) (session.IngestReceipt, error) {
	key := streamKey(scopeID, batch.Events[0].Actor)
	r.events[key] = append(r.events[key], batch.Events...)
	return session.IngestReceipt{Accepted: len(batch.Events), Cursor: batch.Events[len(batch.Events)-1].Sequence}, nil
}

func (r *memoryRepository) SessionEvents(_ context.Context, scopeID string, actor session.Actor, after int64, limit int) ([]session.SessionEvent, error) {
	var result []session.SessionEvent
	for _, event := range r.events[streamKey(scopeID, actor)] {
		if event.Sequence > after && len(result) < limit {
			result = append(result, event)
		}
	}
	return result, nil
}

func (r *memoryRepository) SessionEventsBefore(_ context.Context, scopeID string, actor session.Actor, atOrBefore int64, limit int) ([]session.SessionEvent, error) {
	var result []session.SessionEvent
	for _, event := range r.events[streamKey(scopeID, actor)] {
		if event.Sequence <= atOrBefore {
			result = append(result, event)
		}
	}
	if len(result) > limit {
		result = result[len(result)-limit:]
	}
	return result, nil
}

func (r *memoryRepository) SessionLatestSequence(_ context.Context, scopeID string, actor session.Actor) (int64, error) {
	events := r.events[streamKey(scopeID, actor)]
	if len(events) == 0 {
		return 0, nil
	}
	return events[len(events)-1].Sequence, nil
}

func (r *memoryRepository) ExtractionCursor(_ context.Context, scopeID string, actor session.Actor) (int64, error) {
	return r.cursors[streamKey(scopeID, actor)], nil
}

func (r *memoryRepository) AdvanceExtractionCursor(_ context.Context, scopeID string, actor session.Actor, cursor int64) error {
	r.cursors[streamKey(scopeID, actor)] = cursor
	return nil
}

func lakeEvent(id string, actor session.Actor, sequence int64) session.SessionEvent {
	return session.SessionEvent{
		ID: id, Actor: actor, Sequence: sequence, Type: "assistant", Content: id,
		OccurredAt: time.Date(2026, time.July, 14, 12, 0, int(sequence), 0, time.UTC),
	}
}

func streamKey(scopeID string, actor session.Actor) string {
	return scopeID + "/" + actor.AgentID + "/" + actor.SessionID
}

func eventIDs(events []session.SessionEvent) []string {
	ids := make([]string, len(events))
	for index, event := range events {
		ids[index] = event.ID
	}
	return ids
}
