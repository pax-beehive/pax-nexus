package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	v2 "github.com/pax-beehive/pax-nexus/internal/eval/v2"
	v3 "github.com/pax-beehive/pax-nexus/internal/eval/v3"
	"github.com/stretchr/testify/suite"
)

type mainSuite struct{ suite.Suite }

func TestMainSuite(t *testing.T) { suite.Run(t, new(mainSuite)) }

func (s *mainSuite) TestInvalidRunRetainsArtifactsAndReturnsGateError() {
	directory := s.T().TempDir()
	manifestPath := filepath.Join(directory, "manifest.json")
	s.writeJSON(manifestPath, map[string]any{
		"protocol": v3.ManifestProtocol, "dataset_revision": "revision", "domain_session_batches": "sessions.json", "full_domain_messages": 1,
	})
	config := v2.Config{
		Version:     v3.ConfigVersion,
		Run:         v2.RunConfig{ID: "run", Dataset: "GroupMemBench", Manifest: manifestPath, OutputDir: directory},
		BaselineArm: v3.ArmNoMemoryTeam, Output: v2.OutputConfig{Formats: []string{"jsonl"}},
	}
	hash, err := config.HashWithRuntime(nil)
	s.Require().NoError(err)
	run := v2.RunRecord{ID: "run", Dataset: "GroupMemBench", DatasetRevision: "revision", ConfigHash: hash, Config: config}
	cases := []v2.Case{{ID: "case"}}
	arms := []string{v3.ArmNoMemoryTeam, v3.ArmGroupMemBenchMem0, v3.ArmPrivateSQLiteTeamNote}
	results := make([]v2.TrialResult, 0, len(arms))
	attempts := make([]v2.TrialAttempt, 0, len(arms))
	for _, arm := range arms {
		artifactRef := filepath.Join("trials", "case", arm, "attempts", "001")
		s.Require().NoError(os.MkdirAll(filepath.Join(directory, artifactRef), 0o755))
		s.Require().NoError(os.WriteFile(filepath.Join(directory, artifactRef, "consumer.jsonl"), []byte("{}\n"), 0o600))
		results = append(results, v2.TrialResult{RunID: run.ID, Dataset: run.Dataset, DatasetRevision: run.DatasetRevision, CaseID: "case", Arm: arm, Status: "completed", Judged: true})
		completed := time.Now().UTC()
		attempts = append(attempts, v2.TrialAttempt{
			TrialAttemptHandle: v2.TrialAttemptHandle{RunID: run.ID, CaseID: "case", Arm: arm, Number: 1},
			Status:             "completed", Stage: v2.TrialStageCompleted, ArtifactRefs: map[string]string{"artifact_dir": artifactRef}, CompletedAt: &completed,
		})
	}
	s.Require().NoError(v2.ExportTrialAttempts(directory, attempts))
	s.Require().NoError(os.MkdirAll(filepath.Join(directory, "memory"), 0o755))
	s.writeJSON(filepath.Join(directory, "memory", "team-note-ingest.json"), map[string]any{"provider": "team_note", "source_events": 1, "accepted": 1, "memory_items": 1})
	s.writeJSON(filepath.Join(directory, "memory", "mem0-ingest.json"), map[string]any{"provider": "mem0_messages", "source_events": 1, "accepted": 1, "created": 1})
	s.writeJSON(filepath.Join(directory, "memory", "private-sqlite-ingest.json"), map[string]any{"provider": "private_sqlite", "source_events": 1, "accepted": 1, "created": 1})

	err = exportResults(config, run, cases, results)

	s.Require().ErrorIs(err, v3.ErrInvalidRun)
	validityInput, readErr := os.ReadFile(filepath.Join(directory, "validity.json"))
	s.Require().NoError(readErr)
	s.Contains(string(validityInput), `"valid": false`)
	manifestInput, readErr := os.ReadFile(filepath.Join(directory, "artifacts.json"))
	s.Require().NoError(readErr)
	s.Contains(string(manifestInput), `"validity_report": "validity.json"`)
}

func (s *mainSuite) writeJSON(path string, value any) {
	s.T().Helper()
	encoded, err := json.Marshal(value)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(path, append(encoded, '\n'), 0o600))
}
