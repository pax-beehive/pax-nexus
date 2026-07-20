package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
	"github.com/stretchr/testify/suite"
)

type storeSuite struct {
	suite.Suite
	store    *postgres.Store
	sessions *postgres.SessionRepository
	episodes *postgres.EpisodeStore
}

func TestStoreSuite(t *testing.T) {
	suite.Run(t, new(storeSuite))
}

func (s *storeSuite) SetupSuite() {
	dsn := testDSN(s.T())
	store, err := postgres.Open(context.Background(), dsn)
	s.Require().NoError(err)
	s.Require().NoError(store.Migrate(context.Background()))
	s.store = store
	s.sessions = store.Sessions()
	s.episodes = store.Episodes()
}

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEAM_MEMORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEAM_MEMORY_TEST_POSTGRES_DSN is not set")
	}
	return dsn
}

func (s *storeSuite) TearDownSuite() {
	if s.store != nil {
		s.store.Close()
	}
}

func (s *storeSuite) TestAppendReplayAndOrderedRead() {
	scopeID := uniqueScope("ordered")
	actor := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "session-1"}
	batch := teamnote.SessionBatch{Complete: true, Events: []teamnote.SessionEvent{
		event("event-2", actor, 2),
		event("event-1", actor, 1),
	}}

	receipt, err := s.sessions.AppendSession(context.Background(), scopeID, batch)
	s.Require().NoError(err)
	s.Equal(2, receipt.Accepted)
	s.Equal(0, receipt.Duplicate)
	s.Equal(int64(2), receipt.Cursor)

	replayed, err := s.sessions.AppendSession(context.Background(), scopeID, batch)
	s.Require().NoError(err)
	s.Equal(0, replayed.Accepted)
	s.Equal(2, replayed.Duplicate)

	events, err := s.sessions.SessionEvents(context.Background(), scopeID, actor, 0, 10)
	s.Require().NoError(err)
	s.Require().Len(events, 2)
	s.Equal("event-1", events[0].ID)
	s.Equal("event-2", events[1].ID)
	s.Equal("value", events[0].Metadata["key"])
	head, err := s.sessions.SessionLatestSequence(context.Background(), scopeID, actor)
	s.Require().NoError(err)
	s.Equal(int64(2), head)
	overlap, err := s.sessions.SessionEventsBefore(context.Background(), scopeID, actor, 2, 1)
	s.Require().NoError(err)
	s.Require().Len(overlap, 1)
	s.Equal("event-2", overlap[0].ID)
}

func (s *storeSuite) TestExtractionEpisodePersistsAndRejectsStaleVersion() {
	key := extractor.EpisodeKey{ScopeID: uniqueScope("episode"), TaskRef: "release-42", ThreadRef: "thread-a"}
	episode := extractor.Episode{
		Key: key, EstimatedTokens: 120, EventCount: 2, ProtocolVersion: extractor.ExtractionVersionV2,
		Model: "model", PromptVersion: "v2-rolling",
		Messages:   []extractor.EpisodeMessage{{Role: "user", Content: "events"}},
		Runs:       map[string]extractor.EpisodeRun{"checksum": {Response: `{"candidates":[]}`, Ordinal: 2}},
		Checkpoint: extractor.Checkpoint{SourceCursors: map[string]int64{"owner/agent/session": 2}},
	}
	s.Require().NoError(s.episodes.SaveEpisode(context.Background(), episode, 0))

	loaded, ok, err := s.episodes.LoadEpisode(context.Background(), key)
	s.Require().NoError(err)
	s.True(ok)
	s.Equal(int64(1), loaded.Version)
	s.Equal(2, loaded.EventCount)
	s.Equal(extractor.ExtractionVersionV2, loaded.ProtocolVersion)
	s.Equal("events", loaded.Messages[0].Content)
	s.JSONEq(`{"candidates":[]}`, loaded.Runs["checksum"].Response)

	loaded.EventCount = 3
	s.Require().NoError(s.episodes.SaveEpisode(context.Background(), loaded, loaded.Version))
	s.ErrorIs(s.episodes.SaveEpisode(context.Background(), loaded, loaded.Version), extractor.ErrEpisodeConflict)
}

func (s *storeSuite) TestRejectsInvalidBatches() {
	actor := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "session-1"}
	tests := []struct {
		name  string
		scope string
		batch teamnote.SessionBatch
	}{
		{name: "empty scope", batch: teamnote.SessionBatch{Events: []teamnote.SessionEvent{event("event", actor, 1)}}},
		{name: "empty events", scope: uniqueScope("empty")},
		{
			name: "mixed actors", scope: uniqueScope("mixed"),
			batch: teamnote.SessionBatch{Events: []teamnote.SessionEvent{
				event("one", actor, 1),
				event("two", teamnote.Actor{UserID: "owner", AgentID: "other", SessionID: "session-1"}, 2),
			}},
		},
	}

	for _, test := range tests {
		s.Run(test.name, func() {
			_, err := s.sessions.AppendSession(context.Background(), test.scope, test.batch)
			s.ErrorIs(err, postgres.ErrInvalidSessionBatch)
		})
	}
}

func (s *storeSuite) TestSequenceCollisionFailsWithoutPartialWrite() {
	scopeID := uniqueScope("collision")
	actor := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "session-1"}
	_, err := s.sessions.AppendSession(context.Background(), scopeID, teamnote.SessionBatch{
		Events: []teamnote.SessionEvent{event("first", actor, 1)},
	})
	s.Require().NoError(err)

	_, err = s.sessions.AppendSession(context.Background(), scopeID, teamnote.SessionBatch{
		Events: []teamnote.SessionEvent{event("different", actor, 1)},
	})
	s.Require().Error(err)
	s.Require().NotErrorIs(err, postgres.ErrInvalidSessionBatch)

	events, readErr := s.sessions.SessionEvents(context.Background(), scopeID, actor, 0, 10)
	s.Require().NoError(readErr)
	s.Require().Len(events, 1)
	s.Equal("first", events[0].ID)
}

func (s *storeSuite) TestExtractionCursorLifecycle() {
	ctx := context.Background()
	scopeID := uniqueScope("cursor")
	actor := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "cursor-session"}
	cursor, err := s.sessions.ExtractionCursor(ctx, scopeID, actor)
	s.Require().NoError(err)
	s.Zero(cursor)

	_, err = s.sessions.AppendSession(ctx, scopeID, teamnote.SessionBatch{Events: []teamnote.SessionEvent{
		event("cursor-event-1", actor, 1), event("cursor-event-2", actor, 2),
	}})
	s.Require().NoError(err)
	events, err := s.sessions.SessionEvents(ctx, scopeID, actor, 0, 10)
	s.Require().NoError(err)
	s.Require().Len(events, 2)
	for _, current := range events {
		s.False(current.CapturedAt.IsZero())
		s.Nil(current.ExtractedAt)
	}
	s.Require().NoError(s.sessions.AdvanceExtractionCursor(ctx, scopeID, actor, 1))
	cursor, err = s.sessions.ExtractionCursor(ctx, scopeID, actor)
	s.Require().NoError(err)
	s.Equal(int64(1), cursor)
	events, err = s.sessions.SessionEvents(ctx, scopeID, actor, 0, 10)
	s.Require().NoError(err)
	s.Require().Len(events, 2)
	s.NotNil(events[0].ExtractedAt)
	s.Nil(events[1].ExtractedAt)

	s.Require().NoError(s.sessions.AdvanceExtractionCursor(ctx, scopeID, actor, 2))
	cursor, err = s.sessions.ExtractionCursor(ctx, scopeID, actor)
	s.Require().NoError(err)
	s.Equal(int64(2), cursor)
	events, err = s.sessions.SessionEvents(ctx, scopeID, actor, 0, 10)
	s.Require().NoError(err)
	s.Require().Len(events, 2)
	s.NotNil(events[0].ExtractedAt)
	s.NotNil(events[1].ExtractedAt)

	err = s.sessions.AdvanceExtractionCursor(ctx, scopeID, actor, 3)
	s.ErrorIs(err, postgres.ErrInvalidSessionBatch)
}

func (s *storeSuite) TestCompleteBatchEnqueuesExtractionInSameOperation() {
	store, err := postgres.Open(context.Background(), testDSN(s.T()))
	s.Require().NoError(err)
	defer store.Close()
	enqueuer := &recordingEnqueuer{jobID: "river-42"}
	sessions := store.Sessions()
	s.Require().NoError(sessions.ConfigureExtractionEnqueuer(enqueuer))
	s.Require().Error(sessions.ConfigureExtractionEnqueuer(enqueuer))

	scopeID := uniqueScope("enqueue")
	actor := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "session"}
	receipt, err := sessions.AppendSession(context.Background(), scopeID, teamnote.SessionBatch{
		Complete: true, Events: []teamnote.SessionEvent{event("enqueue-event", actor, 1)},
	})
	s.Require().NoError(err)
	s.Equal("river-42", receipt.RunID)
	s.Equal(scopeID, enqueuer.scopeID)
	s.Equal(actor, enqueuer.actor)
	s.Equal(int64(1), enqueuer.cursor)
	s.True(enqueuer.complete)
	s.Equal(1, enqueuer.calls)

	incompleteReceipt, err := sessions.AppendSession(context.Background(), scopeID, teamnote.SessionBatch{
		Events: []teamnote.SessionEvent{event("incomplete-event", actor, 2)},
	})
	s.Require().NoError(err)
	s.Equal("river-42", incompleteReceipt.RunID)
	s.Equal(int64(2), enqueuer.cursor)
	s.False(enqueuer.complete)
	s.Equal(2, enqueuer.calls)
}

func (s *storeSuite) TestEnqueueFailureRollsBackEvents() {
	store, err := postgres.Open(context.Background(), testDSN(s.T()))
	s.Require().NoError(err)
	defer store.Close()
	expected := errors.New("queue unavailable")
	sessions := store.Sessions()
	s.Require().NoError(sessions.ConfigureExtractionEnqueuer(&recordingEnqueuer{err: expected}))

	scopeID := uniqueScope("enqueue-rollback")
	actor := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "session"}
	_, err = sessions.AppendSession(context.Background(), scopeID, teamnote.SessionBatch{
		Complete: true, Events: []teamnote.SessionEvent{event("rollback-event", actor, 1)},
	})
	s.Require().ErrorIs(err, expected)
	events, readErr := sessions.SessionEvents(context.Background(), scopeID, actor, 0, 10)
	s.Require().NoError(readErr)
	s.Empty(events)
}

type recordingEnqueuer struct {
	jobID    string
	err      error
	scopeID  string
	actor    teamnote.Actor
	calls    int
	cursor   int64
	complete bool
}

func (e *recordingEnqueuer) EnqueueTx(_ context.Context, _ pgx.Tx, scopeID string, actor teamnote.Actor, cursor int64, complete bool) (string, error) {
	e.calls++
	e.scopeID = scopeID
	e.actor = actor
	e.cursor = cursor
	e.complete = complete
	return e.jobID, e.err
}

func event(id string, actor teamnote.Actor, sequence int64) teamnote.SessionEvent {
	return teamnote.SessionEvent{
		ID: id, Actor: actor, Sequence: sequence, Type: "assistant", Content: "content",
		TaskRef: "task-1", Visibility: "team_note_eligible",
		OccurredAt: time.Date(2026, time.July, 14, 12, 0, int(sequence), 0, time.UTC),
		Metadata:   map[string]string{"key": "value"},
	}
}

func uniqueScope(label string) string {
	return fmt.Sprintf("%s-%d", label, time.Now().UnixNano())
}
