package groupmembench_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
	"github.com/pax-beehive/pax-nexus/internal/session"
	"github.com/stretchr/testify/suite"
)

type filesSuite struct{ suite.Suite }

func TestFilesSuite(t *testing.T) { suite.Run(t, new(filesSuite)) }

func (s *filesSuite) TestWriteCasesPublishesAcceptanceAndThreeScenarioSmokeManifests() {
	directory := s.T().TempDir()
	cases := make([]groupmembench.Case, 0)
	for _, category := range groupmembench.Categories() {
		cases = append(cases, groupmembench.Case{
			Category: category,
			Question: groupmembench.Question{ID: category + "_1", Question: "question", Answer: "answer", AskingUserID: "user"},
			Messages: []groupmembench.Message{{
				NodeID: "message-" + category, Author: "User_1", Role: "Data Analyst", Content: "context",
				Channel: "Finance", PhaseName: "Planning", Topic: "Controls", Timestamp: "2025-07-05T07:54:13Z",
			}},
		})
	}

	s.Require().NoError(groupmembench.WriteCases(directory, "revision", "Finance", "seed", cases))
	acceptance := loadManifest(s.T(), filepath.Join(directory, "manifest.json"))
	smoke := loadManifest(s.T(), filepath.Join(directory, "manifest.smoke.json"))
	s.Len(acceptance.Cases, 6)
	s.Require().Len(smoke.Cases, 3)
	s.Equal([]string{"multi_hop", "knowledge_update", "abstention"}, []string{
		smoke.Cases[0].Category, smoke.Cases[1].Category, smoke.Cases[2].Category,
	})
	s.Equal("native_sessions", acceptance.Cases[0].IngestMode)
	s.Equal(1, acceptance.Cases[0].SessionBatches)
	s.Equal(1, acceptance.Cases[0].Actors)
	batches := loadSessionBatches(s.T(), filepath.Join(directory, "cases", "multi_hop_1", "producer", "session-batches.json"))
	s.Require().Len(batches, 1)
	s.Require().Len(batches[0].Events, 1)
	s.Equal("User_1", batches[0].Events[0].Actor.UserID)
	s.Equal("groupmembench-User_1", batches[0].Events[0].Actor.AgentID)
	s.Equal("Data Analyst", batches[0].Events[0].Metadata["role"])
}

func (s *filesSuite) TestWriteCasesPartitionsNativeSessionsByAuthorChannelAndPhase() {
	directory := s.T().TempDir()
	messages := []groupmembench.Message{
		{NodeID: "Msg_1", Author: "User_3", Role: "PM", Channel: "Finance", PhaseName: "Plan", Topic: "Controls", Timestamp: "2025-07-05T07:00:00Z", Content: "First decision.", IsDecisionPoint: true, DecisionChangeMetadata: map[string]any{"supersedes": "Msg_0"}},
		{NodeID: "Msg_2", Author: "User_3", Role: "PM", Channel: "Finance", PhaseName: "Plan", Topic: "Controls", Timestamp: "2025-07-05T07:01:00Z", Content: "Follow-up.", ReplyTo: "Msg_1"},
		{NodeID: "Msg_3", Author: "User_3", Role: "PM", Channel: "Ops", PhaseName: "Execute", Topic: "Controls", Timestamp: "2025-07-05T07:02:00Z", Content: "Same author, later phase."},
		{NodeID: "Msg_4", Author: "User_7", Role: "Analyst", Channel: "Finance", PhaseName: "Plan", Topic: "Controls", Timestamp: "2025-07-05T07:03:00Z", Content: "Second author."},
		{NodeID: "Msg_5", Author: "User_3", Role: "PM", Channel: "Finance", PhaseName: "Plan", Topic: "Controls", Timestamp: "2025-07-05T08:00:00Z", Content: "Same context after an activity gap."},
	}
	cases := make([]groupmembench.Case, 0, len(groupmembench.Categories()))
	for _, category := range groupmembench.Categories() {
		cases = append(cases, groupmembench.Case{
			Category: category, Question: groupmembench.Question{ID: category, Question: "q", Answer: "a", AskingUserID: "User_3"}, Messages: messages,
		})
	}
	s.Require().NoError(groupmembench.WriteCases(directory, "revision", "Finance", "seed", cases))
	batches := loadSessionBatches(s.T(), filepath.Join(directory, "cases", "multi_hop", "producer", "session-batches.json"))
	s.Require().Len(batches, 3)
	s.Len(batches[0].Events, 3)
	s.Equal(int64(1), batches[0].Events[0].Sequence)
	s.Equal(int64(2), batches[0].Events[1].Sequence)
	s.Equal("Msg_1", batches[0].Events[1].Metadata["reply_to"])
	s.Equal("true", batches[0].Events[0].Metadata["decision_point"])
	s.JSONEq(`{"supersedes":"Msg_0"}`, batches[0].Events[0].Metadata["decision_change_metadata"])
	s.Empty(batches[0].Events[0].TaskRef)
	s.Empty(batches[0].Events[0].ThreadRef)
	s.Equal("Plan", batches[0].Events[0].Metadata["phase"])
	s.Equal("Finance", batches[0].Events[0].Metadata["channel"])
	s.Equal("Execute", batches[1].Events[0].Metadata["phase"])
	s.Equal(time.Date(2025, time.July, 5, 7, 3, 0, 0, time.UTC), batches[2].Events[0].OccurredAt)
	s.Equal(int64(3), batches[0].Events[2].Sequence)
	s.Equal(batches[0].Events[0].Actor.SessionID, batches[0].Events[2].Actor.SessionID)
}

func loadManifest(t *testing.T, path string) groupmembench.Manifest {
	t.Helper()
	input, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest groupmembench.Manifest
	if err := json.Unmarshal(input, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func loadSessionBatches(t *testing.T, path string) []session.SessionBatch {
	t.Helper()
	input, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var batches []session.SessionBatch
	if err := json.Unmarshal(input, &batches); err != nil {
		t.Fatal(err)
	}
	return batches
}
