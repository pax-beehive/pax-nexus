package session_test

import (
	"errors"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/session"
)

func validEvent() session.StreamEvent {
	return session.StreamEvent{
		ID:         "evt-1",
		Stream:     session.Stream{Source: session.SourceIMChannel, StreamID: "channel-9"},
		Author:     session.Author{Kind: "user", NativeID: "U0AB12"},
		Kind:       session.KindText,
		Type:       "message",
		Content:    "ship the rollback pack by Friday",
		Visibility: session.VisibilityTeam,
		OccurredAt: time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC),
	}
}

func TestValidateStreamBatchAcceptsRegisteredTeamEvent(t *testing.T) {
	batch := session.StreamBatch{Events: []session.StreamEvent{validEvent()}}
	if err := session.ValidateStreamBatch(batch); err != nil {
		t.Fatalf("expected valid batch, got %v", err)
	}
}

func TestValidateStreamBatchRejections(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*session.StreamEvent)
		target error
	}{
		{"unregistered source", func(e *session.StreamEvent) { e.Stream.Source = "carrier-pigeon" }, session.ErrUnregisteredValue},
		{"unregistered kind", func(e *session.StreamEvent) { e.Kind = "hologram" }, session.ErrUnregisteredValue},
		{"unregistered type", func(e *session.StreamEvent) { e.Type = "poke" }, session.ErrUnregisteredValue},
		{"unregistered author kind", func(e *session.StreamEvent) { e.Author.Kind = "robot" }, session.ErrUnregisteredValue},
		{"non-team visibility", func(e *session.StreamEvent) { e.Visibility = "private" }, session.ErrVisibilityRejected},
		{"media kind without plan 4", func(e *session.StreamEvent) { e.Kind = "audio" }, session.ErrMediaNotEnabled},
		{"missing native id", func(e *session.StreamEvent) { e.Author.NativeID = " " }, session.ErrInvalidStreamBatch},
		{"missing id", func(e *session.StreamEvent) { e.ID = "" }, session.ErrInvalidStreamBatch},
		{"zero occurred_at", func(e *session.StreamEvent) { e.OccurredAt = time.Time{} }, session.ErrInvalidStreamBatch},
		{"caller-set sequence", func(e *session.StreamEvent) { e.Sequence = 7 }, session.ErrInvalidStreamBatch},
		{"agent-session source", func(e *session.StreamEvent) { e.Stream.Source = session.SourceAgentSession }, session.ErrInvalidStreamBatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := validEvent()
			test.mutate(&event)
			err := session.ValidateStreamBatch(session.StreamBatch{Events: []session.StreamEvent{event}})
			if !errors.Is(err, test.target) {
				t.Fatalf("expected %v, got %v", test.target, err)
			}
		})
	}
}

func TestValidateStreamBatchRejectsEmptyAndMixedStreams(t *testing.T) {
	if err := session.ValidateStreamBatch(session.StreamBatch{}); !errors.Is(err, session.ErrInvalidStreamBatch) {
		t.Fatalf("expected empty batch rejection, got %v", err)
	}
	first, second := validEvent(), validEvent()
	second.ID = "evt-2"
	second.Stream.StreamID = "channel-other"
	err := session.ValidateStreamBatch(session.StreamBatch{Events: []session.StreamEvent{first, second}})
	if !errors.Is(err, session.ErrInvalidStreamBatch) {
		t.Fatalf("expected mixed-stream rejection, got %v", err)
	}
}

func TestValidateStreamBatchRejectsEmptyStreamID(t *testing.T) {
	event := validEvent()
	event.Stream.StreamID = "   "
	err := session.ValidateStreamBatch(session.StreamBatch{Events: []session.StreamEvent{event}})
	if !errors.Is(err, session.ErrInvalidStreamBatch) {
		t.Fatalf("expected empty stream id rejection, got %v", err)
	}
}

func TestValidateStreamBatchAcceptsAppTodoSource(t *testing.T) {
	batch := session.StreamBatch{Events: []session.StreamEvent{{
		ID:         "app-todo-evt-1",
		Stream:     session.Stream{Source: session.SourceAppTodo, StreamID: "app-todo"},
		Author:     session.Author{Kind: "user", NativeID: "user-1", UserID: "user-1"},
		Kind:       session.KindText,
		Type:       "message",
		Content:    "User completed todo fix-provider-credential.",
		Visibility: session.VisibilityTeam,
		OccurredAt: time.Now().UTC(),
	}}}
	if err := session.ValidateStreamBatch(batch); err != nil {
		t.Fatalf("expected app:todo batch to validate, got %v", err)
	}
}

func TestStreamFromActorDerivesLegacyIdentity(t *testing.T) {
	actor := session.Actor{UserID: "todd", AgentID: "agent-7", SessionID: "sess-42"}
	stream := session.StreamFromActor(actor)
	if stream.Source != session.SourceAgentSession || stream.StreamID != "agent-7:sess-42" {
		t.Fatalf("unexpected stream %+v", stream)
	}
	author := session.AuthorFromActor(actor)
	if author.Kind != "agent" || author.NativeID != "agent-7" || author.UserID != "todd" {
		t.Fatalf("unexpected author %+v", author)
	}
}
