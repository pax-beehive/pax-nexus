package agentsessions

import (
	"strings"
	"testing"
)

func TestToSessionBatchShape(t *testing.T) {
	s := Session{
		SessionID:   "User_1/2025-07-19/s1",
		Agent:       Persona{UserID: "User_1"},
		WindowStart: "2025-07-19T01:00:00",
		WindowEnd:   "2025-07-19T02:00:00",
		Observations: []Observation{{Channel: "A", MsgNode: "Msg_1",
			Author: "User_2", Timestamp: "2025-07-19T01:00:00"}},
		Trajectory: []Action{{Type: "memory_write",
			SourceMsgs: []string{"Msg_1"}, Content: "note"}},
	}
	batch, err := ToSessionBatch(s, map[string]string{"Msg_1": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !batch.Complete || len(batch.Events) != 2 {
		t.Fatalf("want complete batch with 2 events, got %+v", batch)
	}
	obs, act := batch.Events[0], batch.Events[1]
	if obs.Type != "observation" || !strings.Contains(obs.Content, "hello") ||
		!strings.Contains(obs.Content, "Msg_1") {
		t.Fatalf("bad observation event: %+v", obs)
	}
	if act.Type != "memory_write" || act.Content != "note" ||
		act.Sequence != obs.Sequence+1 {
		t.Fatalf("bad action event: %+v", act)
	}
	if obs.Actor.UserID != "User_1" || obs.Actor.AgentID != "groupmembench-agent" ||
		obs.Actor.SessionID != "User_1/2025-07-19/s1" {
		t.Fatalf("bad actor: %+v", obs.Actor)
	}
	if obs.OccurredAt.IsZero() || act.OccurredAt.Before(obs.OccurredAt) {
		t.Fatalf("bad occurred_at: obs=%v act=%v", obs.OccurredAt, act.OccurredAt)
	}
}

func TestToSessionBatchRejectsMissingText(t *testing.T) {
	s := Session{SessionID: "User_1/2025-07-19/s1",
		Agent:     Persona{UserID: "User_1"},
		WindowEnd: "2025-07-19T02:00:00",
		Observations: []Observation{{MsgNode: "Msg_missing",
			Timestamp: "2025-07-19T01:00:00"}}}
	if _, err := ToSessionBatch(s, map[string]string{}); err == nil {
		t.Fatal("want error for missing message text")
	}
}
