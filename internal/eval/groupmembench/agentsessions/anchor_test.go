package agentsessions

import (
	"context"
	"errors"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
)

func anchorMsgs() []Msg {
	out, _, err := Normalize([]groupmembench.Message{
		{NodeID: "Msg_1", Channel: "A", Author: "User_2",
			Timestamp: "2025-07-19T01:00:00",
			Content:   "the ESG policy deadline is 2025-07-18 for reporting"},
		{NodeID: "Msg_2", Channel: "A", Author: "User_3",
			Timestamp: "2025-07-19T02:00:00", Content: "pizza friday"},
	})
	if err != nil {
		panic(err)
	}
	return out
}

func TestRecoverAnchorsBindsEvidence(t *testing.T) {
	client := &cannedClient{content: `{"evidence_msg_ids":["Msg_1"],"confident":true}`}
	questions := map[string][]groupmembench.Question{
		"multi_hop": {{ID: "q1", Question: "ESG deadline?", Answer: "2025-07-18",
			AskingUserID: "User_7"}},
	}
	got, err := RecoverAnchors(context.Background(), client, "m", questions,
		anchorMsgs(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Confidence != "high" ||
		len(got[0].EvidenceMsgIDs) != 1 || got[0].EvidenceMsgIDs[0] != "Msg_1" ||
		got[0].Category != "multi_hop" {
		t.Fatalf("bad result: %+v", got)
	}
}

func TestRecoverAnchorsSkipsAbstention(t *testing.T) {
	client := &cannedClient{}
	questions := map[string][]groupmembench.Question{
		"abstention": {{ID: "a1", Question: "unknown?", Answer: "Unknown",
			AskingUserID: "User_1"}},
	}
	got, err := RecoverAnchors(context.Background(), client, "m", questions,
		anchorMsgs(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 0 {
		t.Fatal("abstention must not call LLM")
	}
	if got[0].Confidence != "none" || len(got[0].EvidenceMsgIDs) != 0 {
		t.Fatalf("bad abstention result: %+v", got)
	}
}

func TestRecoverAnchorsLowConfidenceOnLLMFailure(t *testing.T) {
	client := &cannedClient{err: errors.New("down")}
	questions := map[string][]groupmembench.Question{
		"temporal": {{ID: "t1", Question: "ESG deadline?", Answer: "2025-07-18",
			AskingUserID: "User_7"}},
	}
	got, err := RecoverAnchors(context.Background(), client, "m", questions,
		anchorMsgs(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Confidence != "low" || len(got[0].EvidenceMsgIDs) != 1 ||
		got[0].EvidenceMsgIDs[0] != "Msg_1" {
		t.Fatalf("want low-confidence BM25 top-1 fallback, got %+v", got)
	}
}
