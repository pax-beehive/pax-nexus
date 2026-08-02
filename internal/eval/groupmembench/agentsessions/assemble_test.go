package agentsessions

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
)

func TestBuildSessionShapesOutput(t *testing.T) {
	m, _, _ := Normalize([]groupmembench.Message{{NodeID: "Msg_1", Channel: "A",
		Author: "User_2", Timestamp: "2025-07-19T01:00:00", Content: "x"}})
	w := Window{User: "User_1", Date: "2025-07-19", Part: 1, Msgs: m}
	s := BuildSession(w, Persona{UserID: "User_1", Role: "Business Analyst"},
		[]Action{{Type: "memory_write", SourceMsgs: []string{"Msg_1"}, Content: "n"}})
	if s.SessionID != "User_1/2025-07-19/s1" ||
		len(s.Observations) != 1 || s.Observations[0].MsgNode != "Msg_1" ||
		len(s.Trajectory) != 1 {
		t.Fatalf("bad session: %+v", s)
	}
}

func TestAttachEvidenceSessions(t *testing.T) {
	sessions := []Session{
		{SessionID: "User_7/2025-07-19/s1", Agent: Persona{UserID: "User_7"},
			Observations: []Observation{{MsgNode: "Msg_1"}}},
		{SessionID: "User_2/2025-07-19/s1", Agent: Persona{UserID: "User_2"},
			Observations: []Observation{{MsgNode: "Msg_1"}}},
	}
	questions := []EnhancedQuestion{{
		Question: groupmembench.Question{ID: "q1", AskingUserID: "User_7"},
		Category: "multi_hop", EvidenceMsgIDs: []string{"Msg_1"}}}
	got := AttachEvidenceSessions(questions, sessions)
	if len(got[0].EvidenceSessionIDs) != 1 ||
		got[0].EvidenceSessionIDs[0] != "User_7/2025-07-19/s1" {
		t.Fatalf("want asker session only, got %v", got[0].EvidenceSessionIDs)
	}
}

func TestWriteJSONLRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rows.jsonl")
	if err := WriteJSONL(path, []MessageRow{{MsgNode: "Msg_1", Content: "a"},
		{MsgNode: "Msg_2", Content: "b"}}); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var count int
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var row MessageRow
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		count++
	}
	if count != 2 {
		t.Fatalf("want 2 rows, got %d", count)
	}
}
