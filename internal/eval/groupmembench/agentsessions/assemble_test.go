package agentsessions

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	largeContent := strings.Repeat("x", 150*1024)
	path := filepath.Join(t.TempDir(), "rows.jsonl")
	if err := WriteJSONL(path, []MessageRow{
		{MsgNode: "Msg_1", Content: "a"},
		{MsgNode: "Msg_2", Content: "b"},
		{MsgNode: "Msg_3", Content: largeContent},
	}); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var count int
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var row MessageRow
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		if row.MsgNode == "Msg_3" && len(row.Content) != len(largeContent) {
			t.Fatalf("large content corrupted: want %d bytes, got %d", len(largeContent), len(row.Content))
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("want 3 rows, got %d", count)
	}
}

func TestBuildSessionEmptyWindow(t *testing.T) {
	w := Window{User: "User_1", Date: "2025-07-19", Part: 1, Msgs: []Msg{}}
	action := Action{Type: "memory_write", Content: "test"}
	s := BuildSession(w, Persona{UserID: "User_1"}, []Action{action})
	if s.SessionID != "User_1/2025-07-19/s1" {
		t.Fatalf("want SessionID User_1/2025-07-19/s1, got %s", s.SessionID)
	}
	if s.WindowStart != "" || s.WindowEnd != "" {
		t.Fatalf("want empty WindowStart/WindowEnd, got %q/%q", s.WindowStart, s.WindowEnd)
	}
	if len(s.Observations) != 0 {
		t.Fatalf("want 0 observations, got %d", len(s.Observations))
	}
	if len(s.Trajectory) != 1 || s.Trajectory[0].Type != "memory_write" {
		t.Fatalf("want trajectory preserved, got %+v", s.Trajectory)
	}
}
