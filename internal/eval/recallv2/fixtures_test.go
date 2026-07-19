package recallv2_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/recallreplay"
	"github.com/pax-beehive/pax-nexus/internal/eval/recallv2"
	"github.com/stretchr/testify/suite"
	"gopkg.in/yaml.v3"
)

type fixtureSuite struct{ suite.Suite }

func TestFixtureSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(fixtureSuite))
}

func (s *fixtureSuite) TestBuildAndValidateThirtyIndependentHardCases() {
	manifest, annotations := s.writeCohort(30)

	cases, revision, cohort, err := recallv2.LoadCases(manifest, annotations, "seed", 30, 50)
	s.Require().NoError(err)
	s.Len(cases, 30)
	s.Equal("revision-1", revision)
	s.Len(cohort.Categories, 6)
	s.Equal(10, cohort.TemporalCases)
	s.Equal(5, cohort.IdentityDependentCases)
	s.Equal(2, cohort.ReviewedSourceCases)
	s.Equal(2, cohort.StrictCrossAgentCases)

	fixtures, err := recallv2.BuildAnswerAtomFixtures(manifest)
	s.Require().NoError(err)
	s.Len(fixtures.Cases, 30)
	s.Empty(fixtures.Cases[5].RequiredAtoms)
	s.NotEmpty(fixtures.Cases[5].ForbiddenAtoms)

	replay, err := recallv2.BuildSyntheticHardReplay(fixtures)
	s.Require().NoError(err)
	s.Len(replay.Cases, 30)
	path := filepath.Join(s.T().TempDir(), "replay.json")
	s.Require().NoError(recallreplay.WriteFixtureSet(path, replay))
	s.NoError(recallv2.ValidateReplay(path, 30))

	report, err := recallreplay.Run(replay, replay.Policy)
	s.Require().NoError(err)
	s.NoError(recallv2.ValidateReplayReport(report))
}

func (s *fixtureSuite) TestLoadCasesRejectsCohortDefects() {
	tests := []struct {
		name    string
		count   int
		mutate  func(string, string)
		wantErr string
	}{
		{name: "too small", count: 29, wantErr: "require 30 to 50"},
		{name: "annotations absent", count: 30, mutate: func(_, annotations string) { s.Require().NoError(os.Remove(annotations)) }, wantErr: "read recall eval v2 annotations"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			manifest, annotations := s.writeCohort(test.count)
			if test.mutate != nil {
				test.mutate(manifest, annotations)
			}
			_, _, _, err := recallv2.LoadCases(manifest, annotations, "seed", 30, 50)
			s.ErrorContains(err, test.wantErr)
		})
	}
}

func (s *fixtureSuite) TestLoadConfigRoundTrip() {
	config := validConfig()
	directory := s.T().TempDir()
	config.Recall.DeterministicReplay = filepath.Join(directory, "replay.json")
	config.Recall.CaseAnnotations = filepath.Join(directory, "annotations.json")
	s.Require().NoError(os.WriteFile(config.Recall.DeterministicReplay, []byte("replay"), 0o600))
	s.Require().NoError(os.WriteFile(config.Recall.CaseAnnotations, []byte("annotations"), 0o600))
	encoded, err := yaml.Marshal(config)
	s.Require().NoError(err)
	path := filepath.Join(directory, "config.yaml")
	s.Require().NoError(os.WriteFile(path, encoded, 0o600))

	loaded, err := recallv2.LoadConfig(path)
	s.Require().NoError(err)
	s.Equal(recallv2.ConfigVersion, loaded.Version)
	s.Len(loaded.ProtocolMetadata["deterministic_replay_sha256"], 64)
}

func (s *fixtureSuite) TestReplayAcceptanceMatrix() {
	tests := []struct {
		name    string
		mutate  func(*recallreplay.Report)
		wantErr string
	}{
		{name: "valid"},
		{name: "candidate", mutate: func(report *recallreplay.Report) { report.RecallEval.CandidateRecallAtLimit = 0.79 }, wantErr: "candidate recall"},
		{name: "relation", mutate: func(report *recallreplay.Report) { report.RecallEval.RelationExpandedRecall = 0.94 }, wantErr: "relation-expanded"},
		{name: "conditional", mutate: func(report *recallreplay.Report) { report.RecallEval.DeliveredConditionalRecall = 0.89 }, wantErr: "conditional recall"},
		{name: "superseded", mutate: func(report *recallreplay.Report) { report.RecallEval.SupersededLeakageItems = 1 }, wantErr: "superseded"},
		{name: "budget", mutate: func(report *recallreplay.Report) {
			report.RecallEval.AvailableAtoms = 10
			report.RecallEval.BudgetDroppedAtoms = 1
		}, wantErr: "five percent"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			report := recallreplay.Report{}
			report.RecallEval.CandidateRecallAtLimit = 1
			report.RecallEval.RelationExpandedRecall = 1
			report.RecallEval.DeliveredConditionalRecall = 1
			if test.mutate != nil {
				test.mutate(&report)
			}
			err := recallv2.ValidateReplayReport(report)
			if test.wantErr == "" {
				s.NoError(err)
			} else {
				s.ErrorContains(err, test.wantErr)
			}
		})
	}
}

func (s *fixtureSuite) writeCohort(count int) (string, string) {
	directory := s.T().TempDir()
	categories := []string{"multi_hop", "knowledge_update", "temporal", "user_implicit", "term_ambiguity", "abstention"}
	cases := make([]map[string]any, 0, count)
	for index := 0; index < count; index++ {
		caseID := fmt.Sprintf("case-%02d", index)
		category := categories[index%len(categories)]
		cases = append(cases, map[string]any{
			"id": caseID, "category": category, "question": fmt.Sprintf("Question %d?", index),
			"answer": fmt.Sprintf("Answer %d", index), "asking_user_id": "User_1", "scope_id": "domain",
			"participant_agent_ids": []string{"groupmembench-User_1", "groupmembench-User_2", "groupmembench-User_3"},
		})
		producer := filepath.Join(directory, "cases", caseID, "producer")
		s.Require().NoError(os.MkdirAll(producer, 0o755))
		s.Require().NoError(os.WriteFile(filepath.Join(producer, "session-batches.json"), []byte("[]\n"), 0o600))
	}
	domain := filepath.Join(directory, "domain", "producer")
	s.Require().NoError(os.MkdirAll(domain, 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(domain, "session-batches.json"), []byte("[]\n"), 0o600))
	manifest := map[string]any{
		"protocol": "multi-agent-groupmembench-v3", "dataset_revision": "revision-1",
		"domain_session_batches": "domain/producer/session-batches.json", "full_domain_messages": 1, "cases": cases,
	}
	manifestPath := filepath.Join(directory, "manifest.json")
	s.writeJSON(manifestPath, manifest)
	annotationsPath := filepath.Join(directory, "annotations.json")
	s.writeJSON(annotationsPath, map[string]any{"cases": map[string]any{
		"case-00": map[string]any{"knowledge_source_status": "reviewed", "supporting_agent_ids": []string{"groupmembench-User_2"}},
		"case-01": map[string]any{"knowledge_source_status": "reviewed", "supporting_agent_ids": []string{"groupmembench-User_2"}},
	}})
	return manifestPath, annotationsPath
}

func (s *fixtureSuite) writeJSON(path string, value any) {
	encoded, err := json.Marshal(value)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(path, encoded, 0o600))
}
