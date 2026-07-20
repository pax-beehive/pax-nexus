package v3_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	v2 "github.com/pax-beehive/pax-nexus/internal/eval/v2"
	v3 "github.com/pax-beehive/pax-nexus/internal/eval/v3"
	"github.com/stretchr/testify/suite"
)

type validitySuite struct{ suite.Suite }

func TestValiditySuite(t *testing.T) { suite.Run(t, new(validitySuite)) }

func (s *validitySuite) TestCompleteRunIsValid() {
	directory, run, cases, results := s.validRunFixture()

	report, err := v3.EvaluateValidity(directory, run, cases, results)

	s.Require().NoError(err)
	s.True(report.Valid)
	s.Equal("valid", report.Status)
	s.Empty(report.Failures)
	s.Equal(3, report.ExpectedTrials)
	s.Equal(3, report.ObservedTrials)
}

func (s *validitySuite) TestPartialPrivateSQLiteMutationInvalidatesComparison() {
	directory, run, cases, results := s.validRunFixture()
	s.writeJSON(filepath.Join(directory, "memory", "private-sqlite-ingest.json"), map[string]any{
		"provider": "private_sqlite", "source_events": 4, "accepted": 4, "created": 1,
	})

	report, err := v3.EvaluateValidity(directory, run, cases, results)

	s.Require().NoError(err)
	s.False(report.Valid)
	s.Equal("invalid", report.Status)
	s.Contains(report.Failures, v3.ValidityFailure{
		Check: "memory_mutation", Arm: v3.ArmPrivateSQLiteTeamNote,
		Reason: "ingest did not materialize every full-domain source Event",
	})
}

func (s *validitySuite) TestAttemptFromAnotherRunInvalidatesComparison() {
	directory, run, cases, results := s.validRunFixture()
	attempts := s.loadAttempts(filepath.Join(directory, "attempts.jsonl"))
	attempts[0].RunID = "another-run"
	s.Require().NoError(v2.ExportTrialAttempts(directory, attempts))

	report, err := v3.EvaluateValidity(directory, run, cases, results)

	s.Require().NoError(err)
	s.False(report.Valid)
	s.Contains(report.Failures, v3.ValidityFailure{
		Check: "attempt_artifacts", CaseID: "case", Arm: attempts[0].Arm,
		Reason: "Attempt run_id does not match Run",
	})
}

func (s *validitySuite) TestRecallProviderEvidenceMustMatchTheArm() {
	tests := []struct {
		name      string
		configure func([]v2.TrialResult)
	}{
		{name: "memory provider was not called", configure: func(results []v2.TrialResult) {
			results[1].MemoryRecallProviderCalls = 0
		}},
		{name: "memory provider type is wrong", configure: func(results []v2.TrialResult) {
			results[2].MemoryRecallProviderType = "mem0"
		}},
		{name: "no-memory arm reports a provider type", configure: func(results []v2.TrialResult) {
			results[0].MemoryRecallProviderType = "mem0"
		}},
		{name: "no-memory arm reports provider counts", configure: func(results []v2.TrialResult) {
			results[0].MemoryRecallProviders = map[string]int{"memory": 1}
		}},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			directory, run, cases, results := s.validRunFixture()
			test.configure(results)

			report, err := v3.EvaluateValidity(directory, run, cases, results)

			s.Require().NoError(err)
			s.False(report.Valid)
			s.True(hasFailureCheck(report, "recall_observation"), "failures: %#v", report.Failures)
		})
	}
}

func (s *validitySuite) TestAttemptMustBeCompletedInItsCanonicalDirectory() {
	tests := []struct {
		name      string
		configure func(*v2.TrialAttempt)
	}{
		{name: "stage is not completed", configure: func(attempt *v2.TrialAttempt) {
			attempt.Stage = v2.TrialStageJudge
		}},
		{name: "completion timestamp is missing", configure: func(attempt *v2.TrialAttempt) {
			attempt.CompletedAt = nil
		}},
		{name: "artifact belongs to another attempt", configure: func(attempt *v2.TrialAttempt) {
			attempt.ArtifactRefs["artifact_dir"] = filepath.Join("trials", "case", attempt.Arm, "attempts", "002")
		}},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			directory, run, cases, results := s.validRunFixture()
			attempts := s.loadAttempts(filepath.Join(directory, "attempts.jsonl"))
			test.configure(&attempts[0])
			s.Require().NoError(v2.ExportTrialAttempts(directory, attempts))

			report, err := v3.EvaluateValidity(directory, run, cases, results)

			s.Require().NoError(err)
			s.False(report.Valid)
			s.True(hasFailureCheck(report, "attempt_artifacts"), "failures: %#v", report.Failures)
		})
	}
}

func (s *validitySuite) TestInvalidReportIsExportedAndRejected() {
	directory, run, cases, results := s.validRunFixture()
	results[1].MemoryRecallObserved = false
	report, err := v3.EvaluateValidity(directory, run, cases, results)
	s.Require().NoError(err)

	s.Require().NoError(v3.ExportValidity(directory, report))
	s.Require().ErrorIs(v3.RequireValid(report), v3.ErrInvalidRun)
	input, err := os.ReadFile(filepath.Join(directory, "validity.json"))
	s.Require().NoError(err)
	s.Contains(string(input), `"status": "invalid"`)
	s.Contains(string(input), `"check": "recall_observation"`)
}

func (s *validitySuite) TestManifestWithoutSourceCountFailsSourceCoverage() {
	directory, run, cases, results := s.validRunFixture()
	s.writeJSON(run.Config.Run.Manifest, map[string]any{
		"protocol": v3.ManifestProtocol, "domain_session_batches": "sessions.json", "full_domain_messages": 0,
	})

	report, err := v3.EvaluateValidity(directory, run, cases, results)

	s.Require().NoError(err)
	s.False(report.Valid)
	s.Contains(report.Failures, v3.ValidityFailure{
		Check: "source_coverage", Reason: "manifest has no full-domain source message count",
	})
}

func (s *validitySuite) TestManifestRevisionDriftInvalidatesSourceCoverage() {
	directory, run, cases, results := s.validRunFixture()
	s.writeJSON(run.Config.Run.Manifest, map[string]any{
		"protocol": v3.ManifestProtocol, "domain_session_batches": "sessions.json", "full_domain_messages": 4,
		"dataset_revision": "different-revision",
	})

	report, err := v3.EvaluateValidity(directory, run, cases, results)

	s.Require().NoError(err)
	s.False(report.Valid)
	s.Contains(report.Failures, v3.ValidityFailure{
		Check: "source_coverage", Reason: "manifest protocol or dataset revision does not match the durable Run",
	})
}

func (s *validitySuite) TestValidityFailureMatrix() {
	tests := []struct {
		name      string
		configure func(string, v2.RunRecord, []v2.TrialResult)
		wantCheck string
	}{
		{name: "zero Team Note mutation", wantCheck: "memory_mutation", configure: func(directory string, _ v2.RunRecord, _ []v2.TrialResult) {
			s.writeJSON(filepath.Join(directory, "memory", "team-note-ingest.json"), map[string]any{
				"provider": "team_note", "source_events": 4, "accepted": 4, "memory_items": 0,
			})
		}},
		{name: "missing recall observation", wantCheck: "recall_observation", configure: func(_ string, _ v2.RunRecord, results []v2.TrialResult) {
			results[1].MemoryRecallObserved = false
		}},
		{name: "no-memory recall leakage", wantCheck: "recall_observation", configure: func(_ string, _ v2.RunRecord, results []v2.TrialResult) {
			results[0].MemoryRecallObserved = true
		}},
		{name: "failed Trial", wantCheck: "trial_matrix", configure: func(_ string, _ v2.RunRecord, results []v2.TrialResult) {
			results[2].Status = "failed"
		}},
		{name: "missing consumer artifact", wantCheck: "attempt_artifacts", configure: func(directory string, _ v2.RunRecord, _ []v2.TrialResult) {
			path := filepath.Join(directory, "trials", "case", v3.ArmGroupMemBenchMem0, "attempts", "001", "consumer.jsonl")
			s.Require().NoError(os.Remove(path))
		}},
		{name: "truncated consumer artifact", wantCheck: "attempt_artifacts", configure: func(directory string, _ v2.RunRecord, _ []v2.TrialResult) {
			path := filepath.Join(directory, "trials", "case", v3.ArmGroupMemBenchMem0, "attempts", "001", "consumer.jsonl")
			s.Require().NoError(os.WriteFile(path, []byte(`{"truncated"`), 0o600))
		}},
		{name: "missing resolved config", wantCheck: "resolved_config_provenance", configure: func(directory string, _ v2.RunRecord, _ []v2.TrialResult) {
			s.Require().NoError(os.Remove(filepath.Join(directory, "config.resolved.json")))
		}},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			directory, run, cases, results := s.validRunFixture()
			test.configure(directory, run, results)

			report, err := v3.EvaluateValidity(directory, run, cases, results)

			s.Require().NoError(err)
			s.False(report.Valid)
			s.True(hasFailureCheck(report, test.wantCheck), "failures: %#v", report.Failures)
		})
	}
}

func (s *validitySuite) validRunFixture() (string, v2.RunRecord, []v2.Case, []v2.TrialResult) {
	s.T().Helper()
	directory := s.T().TempDir()
	manifestPath := filepath.Join(directory, "manifest.json")
	s.writeJSON(manifestPath, map[string]any{
		"protocol": v3.ManifestProtocol, "domain_session_batches": "sessions.json", "full_domain_messages": 4,
		"dataset_revision": "revision",
	})
	s.Require().NoError(os.WriteFile(filepath.Join(directory, "sessions.json"), []byte("[]\n"), 0o600))
	config := baseConfig()
	config.Run.OutputDir = directory
	config.Run.Manifest = manifestPath
	config.Judge = &v2.CommandSpec{Program: "judge"}
	runtimeValues := map[string]string{"OPENCODE_MODEL": "test-model"}
	hash, err := config.HashWithRuntime(runtimeValues)
	s.Require().NoError(err)
	run := v2.RunRecord{ID: config.Run.ID, Dataset: config.Run.Dataset, DatasetRevision: "revision", ConfigHash: hash, Config: config, Runtime: runtimeValues}
	s.Require().NoError(v2.ExportResolvedConfig(filepath.Join(directory, "config.resolved.json"), config, runtimeValues))
	s.Require().NoError(os.MkdirAll(filepath.Join(directory, "memory"), 0o755))
	s.writeJSON(filepath.Join(directory, "memory", "team-note-ingest.json"), map[string]any{
		"provider": "team_note", "source_events": 4, "accepted": 4, "duplicate": 0, "memory_items": 2,
	})
	s.writeJSON(filepath.Join(directory, "memory", "mem0-ingest.json"), map[string]any{
		"provider": "mem0_messages", "source_events": 4, "accepted": 4, "created": 2,
	})
	s.writeJSON(filepath.Join(directory, "memory", "private-sqlite-ingest.json"), map[string]any{
		"provider": "private_sqlite", "source_events": 4, "accepted": 4, "created": 4,
	})

	cases := []v2.Case{{ID: "case", Question: "q", Expected: "a", AskingUserID: "user"}}
	arms := []string{v3.ArmNoMemoryTeam, v3.ArmGroupMemBenchMem0, v3.ArmPrivateSQLiteTeamNote}
	results := make([]v2.TrialResult, 0, len(arms))
	attempts := make([]v2.TrialAttempt, 0, len(arms))
	for _, arm := range arms {
		artifactRef := filepath.Join("trials", "case", arm, "attempts", "001")
		artifactDirectory := filepath.Join(directory, artifactRef)
		s.Require().NoError(os.MkdirAll(artifactDirectory, 0o755))
		s.Require().NoError(os.WriteFile(filepath.Join(artifactDirectory, "consumer.jsonl"), []byte("{}\n"), 0o600))
		s.Require().NoError(os.WriteFile(filepath.Join(artifactDirectory, "judge.jsonl"), []byte("{}\n"), 0o600))
		result := v2.TrialResult{RunID: run.ID, CaseID: "case", Arm: arm, Status: "completed", Judged: true}
		if arm != v3.ArmNoMemoryTeam {
			result.MemoryRecallObserved = true
			result.MemoryRecallSuccess = true
			result.MemoryRecallProviderCalls = 1
			result.MemoryRecallProviders = map[string]int{"memory": 1}
			result.MemoryRecallProviderType = "mem0"
			if arm == v3.ArmPrivateSQLiteTeamNote {
				result.MemoryRecallProviderType = "team-memory-sqlite"
			}
		}
		results = append(results, result)
		completed := time.Now().UTC()
		attempts = append(attempts, v2.TrialAttempt{
			TrialAttemptHandle: v2.TrialAttemptHandle{RunID: run.ID, CaseID: "case", Arm: arm, Number: 1},
			Status:             "completed", Stage: v2.TrialStageCompleted, ArtifactRefs: map[string]string{"artifact_dir": artifactRef}, CompletedAt: &completed,
		})
	}
	s.Require().NoError(v2.ExportTrialAttempts(directory, attempts))
	return directory, run, cases, results
}

func (s *validitySuite) writeJSON(path string, value any) {
	s.T().Helper()
	encoded, err := json.Marshal(value)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(path, append(encoded, '\n'), 0o600))
}

func (s *validitySuite) loadAttempts(path string) []v2.TrialAttempt {
	s.T().Helper()
	input, err := os.ReadFile(path)
	s.Require().NoError(err)
	lines := bytes.Split(bytes.TrimSpace(input), []byte{'\n'})
	attempts := make([]v2.TrialAttempt, 0, len(lines))
	for _, line := range lines {
		var attempt v2.TrialAttempt
		s.Require().NoError(json.Unmarshal(line, &attempt))
		attempts = append(attempts, attempt)
	}
	return attempts
}

func hasFailureCheck(report v3.ValidityReport, check string) bool {
	for _, failure := range report.Failures {
		if failure.Check == check {
			return true
		}
	}
	return false
}
