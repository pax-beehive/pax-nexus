package postgres_test

import (
	"context"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/session"
)

func (s *storeSuite) TestAppendStreamAssignsSequencesAndDedupes() {
	ctx := context.Background()
	scopeID := "scope-evidence"
	event := func(id, content string) session.StreamEvent {
		return session.StreamEvent{
			ID:         id,
			Stream:     session.Stream{Source: session.SourceIMChannel, StreamID: "channel-9"},
			Author:     session.Author{Kind: "user", NativeID: "U0AB12"},
			Kind:       session.KindText,
			Type:       "message",
			Content:    content,
			Visibility: session.VisibilityTeam,
			OccurredAt: time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC),
		}
	}

	receipt, err := s.sessions.AppendStream(ctx, scopeID, session.StreamBatch{
		Events: []session.StreamEvent{event("evt-1", "first"), event("evt-2", "second")},
	})
	s.Require().NoError(err)
	s.Equal(2, receipt.Accepted)
	s.Equal(int64(2), receipt.Cursor)

	// Replay of evt-2 plus one new event: duplicate is not re-sequenced.
	receipt, err = s.sessions.AppendStream(ctx, scopeID, session.StreamBatch{
		Events: []session.StreamEvent{event("evt-2", "second"), event("evt-3", "third")},
	})
	s.Require().NoError(err)
	s.Equal(1, receipt.Accepted)
	s.Equal(1, receipt.Duplicate)
	s.Equal(int64(3), receipt.Cursor)

	events, err := s.sessions.StreamEvents(ctx, scopeID,
		session.Stream{Source: session.SourceIMChannel, StreamID: "channel-9"}, 0, 10)
	s.Require().NoError(err)
	s.Require().Len(events, 3)
	for index, event := range events {
		s.Equal(int64(index+1), event.Sequence)
		s.Equal("U0AB12", event.Author.NativeID)
		s.Equal(session.SourceIMChannel, event.Stream.Source)
	}
}

func (s *storeSuite) TestAppendSessionPopulatesEvidenceColumns() {
	ctx := context.Background()
	scopeID := "scope-legacy"
	actor := session.Actor{UserID: "todd", AgentID: "agent-7", SessionID: "sess-42"}
	_, err := s.sessions.AppendSession(ctx, scopeID, session.SessionBatch{Events: []session.SessionEvent{{
		ID: "legacy-1", Actor: actor, Sequence: 1, Type: "message",
		Content: "hello", OccurredAt: time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC),
	}}})
	s.Require().NoError(err)

	events, err := s.sessions.StreamEvents(ctx, scopeID, session.StreamFromActor(actor), 0, 10)
	s.Require().NoError(err)
	s.Require().Len(events, 1)
	s.Equal("agent-7:sess-42", events[0].Stream.StreamID)
	s.Equal("agent-7", events[0].Author.NativeID)
	s.Equal("todd", events[0].Author.UserID)
}
