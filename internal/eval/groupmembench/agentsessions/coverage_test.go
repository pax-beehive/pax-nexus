package agentsessions

import (
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
)

func coverageFixture() ([]EnhancedQuestion, []Session) {
	questions := []EnhancedQuestion{
		{Question: groupmembench.Question{ID: "q_ok", AskingUserID: "User_1"},
			Category: "multi_hop", EvidenceMsgIDs: []string{"Msg_1"}},
		{Question: groupmembench.Question{ID: "q_unseen", AskingUserID: "User_1"},
			Category: "temporal", EvidenceMsgIDs: []string{"Msg_ghost"}},
		{Question: groupmembench.Question{ID: "q_abstain", AskingUserID: "User_1"},
			Category: "abstention"},
	}
	sessions := []Session{{SessionID: "User_1/2025-07-19/s1",
		Agent:        Persona{UserID: "User_1"},
		Observations: []Observation{{MsgNode: "Msg_1"}, {MsgNode: "Msg_2"}},
		Trajectory: []Action{{Type: "memory_write", SourceMsgs: []string{"Msg_1"}},
			{Type: "todo", SourceMsgs: []string{"Msg_2"}}}}}
	return questions, sessions
}

func TestVerifyCoverage(t *testing.T) {
	questions, sessions := coverageFixture()
	got := VerifyCoverage(questions, sessions)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 exception, got %+v", got)
	}
	if got[0].QuestionID != "q_unseen" || got[0].Reason != "evidence_not_observed" {
		t.Fatalf("bad exception: %+v", got[0])
	}
}

func TestVerifyCoverageFlagsMissingMemoryWrite(t *testing.T) {
	questions := []EnhancedQuestion{
		{Question: groupmembench.Question{ID: "q1", AskingUserID: "User_1"},
			Category: "multi_hop", EvidenceMsgIDs: []string{"Msg_2"}}}
	_, sessions := coverageFixture()
	got := VerifyCoverage(questions, sessions)
	// Msg_2 被观察到但只有 todo 没有 memory_write
	if len(got) != 1 || got[0].Reason != "no_memory_write" {
		t.Fatalf("want no_memory_write, got %+v", got)
	}
}
