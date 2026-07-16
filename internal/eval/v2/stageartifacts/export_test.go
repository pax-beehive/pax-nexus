package stageartifacts_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/stagecapture"
	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	v2 "github.com/pax-beehive/pax-nexus/internal/eval/v2"
	"github.com/pax-beehive/pax-nexus/internal/eval/v2/stageartifacts"
	"github.com/stretchr/testify/suite"
)

type ExportSuite struct {
	suite.Suite
}

func TestExportSuite(t *testing.T) {
	suite.Run(t, new(ExportSuite))
}

func (s *ExportSuite) TestExportCapturesAndScoresConfiguredArm() {
	directory := s.T().TempDir()
	producer := filepath.Join(directory, "producer")
	s.Require().NoError(os.MkdirAll(producer, 0o755))
	source := []byte(`[{"events":[]}]`)
	s.Require().NoError(os.WriteFile(filepath.Join(producer, "session-batches.json"), source, 0o600))
	digest := sha256.Sum256(source)
	fixture := stageeval.Fixture{
		CaseID: "case-1", SourceRevision: hex.EncodeToString(digest[:]),
		RecallContext: stageeval.RecallContext{
			ConsumerUserID: "User_3", ConsumerAgentID: "groupmembench-User_3",
			Query: "Who owns rollback evidence?", TokenBudget: 500,
		},
		RequiredAtoms: []stageeval.Atom{{ID: "owner", Patterns: []string{"Ops Lead"}}},
	}
	observer := &recordingObserver{}

	err := stageartifacts.Export(context.Background(), observer, stageartifacts.Request{
		RunID: "run-1", OutputDirectory: directory,
		Config:   v2.StageCaptureConfig{Arms: []string{"team_note"}},
		Fixtures: stageeval.FixtureSet{SchemaVersion: stageeval.SchemaVersion, Cases: []stageeval.Fixture{fixture}},
		Cases: []v2.Case{{
			ID: "case-1", ScopeID: "scope-1", ProducerWorkspace: producer,
			AskingUserID: "User_3", Question: "Who owns rollback evidence?",
		}},
		Results: []v2.TrialResult{{CaseID: "case-1", Arm: "team_note", AskingUserID: "User_3", Status: "completed", SessionID: "consumer-session"}},
	})

	s.Require().NoError(err)
	s.Require().Len(observer.targets, 1)
	s.Equal("run-1-scope-1", observer.targets[0].ScopeID)
	s.Equal("groupmembench-User_3", observer.targets[0].RecipientAgentID)
	s.Equal("consumer-session", observer.targets[0].RecipientSessionID)
	for _, name := range []string{"observations.jsonl", "stage-results.jsonl", "stage-summary.json"} {
		_, statErr := os.Stat(filepath.Join(directory, "stage", "team_note", name))
		s.Require().NoError(statErr)
	}
	_, err = os.Stat(filepath.Join(directory, "stage", "artifacts.json"))
	s.Require().NoError(err)
}

func (s *ExportSuite) TestExportKeepsDiagnosticsForUnavailableTrial() {
	directory, producer, fixture := s.fixtureSource()
	observer := &recordingObserver{}
	err := stageartifacts.Export(context.Background(), observer, stageartifacts.Request{
		RunID: "run", OutputDirectory: directory, Config: v2.StageCaptureConfig{Arms: []string{"team_note"}},
		Fixtures: stageeval.FixtureSet{SchemaVersion: stageeval.SchemaVersion, Cases: []stageeval.Fixture{fixture}},
		Cases: []v2.Case{{
			ID: fixture.CaseID, ScopeID: "scope", ProducerWorkspace: producer,
			AskingUserID: fixture.RecallContext.ConsumerUserID, Question: fixture.RecallContext.Query,
		}},
	})

	s.Require().NoError(err)
	s.Empty(observer.targets)
	data, readErr := os.ReadFile(filepath.Join(directory, "stage", "team_note", "observations.jsonl"))
	s.Require().NoError(readErr)
	s.Contains(string(data), "completed trial with a consumer session is unavailable")
}

func (s *ExportSuite) TestExportRejectsRecallControlDriftAndPreservesPriorArtifacts() {
	directory, producer, fixture := s.fixtureSource()
	prior := filepath.Join(directory, "stage", "artifacts.json")
	s.Require().NoError(os.MkdirAll(filepath.Dir(prior), 0o755))
	s.Require().NoError(os.WriteFile(prior, []byte("prior"), 0o600))
	err := stageartifacts.Export(context.Background(), &recordingObserver{}, stageartifacts.Request{
		RunID: "run", OutputDirectory: directory, Config: v2.StageCaptureConfig{Arms: []string{"team_note"}},
		Fixtures: stageeval.FixtureSet{SchemaVersion: stageeval.SchemaVersion, Cases: []stageeval.Fixture{fixture}},
		Cases: []v2.Case{{
			ID: fixture.CaseID, ScopeID: "scope", ProducerWorkspace: producer,
			AskingUserID: fixture.RecallContext.ConsumerUserID, Question: "different question",
		}},
	})

	s.Require().Error(err)
	data, readErr := os.ReadFile(prior)
	s.Require().NoError(readErr)
	s.Equal("prior", string(data))
}

func (s *ExportSuite) fixtureSource() (string, string, stageeval.Fixture) {
	directory := s.T().TempDir()
	producer := filepath.Join(directory, "producer")
	s.Require().NoError(os.MkdirAll(producer, 0o755))
	source := []byte(`[{}]`)
	s.Require().NoError(os.WriteFile(filepath.Join(producer, "session-batches.json"), source, 0o600))
	digest := sha256.Sum256(source)
	return directory, producer, stageeval.Fixture{
		CaseID: "case-1", SourceRevision: hex.EncodeToString(digest[:]),
		RecallContext: stageeval.RecallContext{
			ConsumerUserID: "user", ConsumerAgentID: "agent", Query: "question", TokenBudget: 500,
		},
		RequiredAtoms: []stageeval.Atom{{ID: "fact", Patterns: []string{"fact"}}},
	}
}

func (s *ExportSuite) TestExportRejectsSourceDrift() {
	directory := s.T().TempDir()
	producer := filepath.Join(directory, "producer")
	s.Require().NoError(os.MkdirAll(producer, 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(producer, "session-batches.json"), []byte("changed"), 0o600))
	fixture := stageeval.Fixture{
		CaseID: "case-1", SourceRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		RecallContext: stageeval.RecallContext{ConsumerUserID: "user", ConsumerAgentID: "agent", Query: "question", TokenBudget: 500},
		RequiredAtoms: []stageeval.Atom{{ID: "fact", Patterns: []string{"fact"}}},
	}
	err := stageartifacts.Export(context.Background(), &recordingObserver{}, stageartifacts.Request{
		RunID: "run", OutputDirectory: directory, Config: v2.StageCaptureConfig{Arms: []string{"team_note"}},
		Fixtures: stageeval.FixtureSet{SchemaVersion: stageeval.SchemaVersion, Cases: []stageeval.Fixture{fixture}},
		Cases:    []v2.Case{{ID: "case-1", ScopeID: "scope", ProducerWorkspace: producer}},
		Results:  []v2.TrialResult{{CaseID: "case-1", Arm: "team_note", Status: "completed", SessionID: "session"}},
	})
	s.Error(err)
}

type recordingObserver struct {
	targets []stagecapture.Target
}

func (o *recordingObserver) Capture(_ context.Context, target stagecapture.Target) (stageeval.Observation, stageeval.Observation, error) {
	o.targets = append(o.targets, target)
	context := target.Fixture.RecallContext
	extraction := stageeval.Observation{
		CaseID: target.Fixture.CaseID, Stage: stageeval.StageExtraction, SourceRevision: target.Fixture.SourceRevision,
		Items: []stageeval.Item{{ID: "note-1", Text: "Ops Lead owns rollback evidence."}},
	}
	recall := stageeval.Observation{
		CaseID: target.Fixture.CaseID, Stage: stageeval.StageRecall, SourceRevision: target.Fixture.SourceRevision,
		RecallContext: &context, Items: []stageeval.Item{{ID: "note-1", Text: "Ops Lead owns rollback evidence."}},
	}
	return extraction, recall, nil
}
