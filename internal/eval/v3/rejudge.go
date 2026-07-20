package v3

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	v2 "github.com/pax-beehive/pax-nexus/internal/eval/v2"
)

// RecordRejudgeEvidence appends immutable Attempt evidence for judge-only
// recovery without rewriting the original execution Attempt.
func RecordRejudgeEvidence(directory, runID string, before, after []v2.TrialResult) error {
	attempts, err := loadAttempts(filepath.Join(directory, "attempts.jsonl"))
	if err != nil {
		return fmt.Errorf("load rejudge Attempt ledger: %w", err)
	}
	latest := latestAttempts(attempts)
	afterByTrial := make(map[string]v2.TrialResult, len(after))
	for _, result := range after {
		afterByTrial[trialIdentity(result.CaseID, result.Arm)] = result
	}
	for _, original := range before {
		if original.Status != "completed" || original.Judged {
			continue
		}
		key := trialIdentity(original.CaseID, original.Arm)
		previous, exists := latest[key]
		if !exists {
			return fmt.Errorf("record rejudge evidence for %s/%s: prior Attempt is required", original.CaseID, original.Arm)
		}
		judged, exists := afterByTrial[key]
		if !exists {
			return fmt.Errorf("record rejudge evidence for %s/%s: judged Trial is missing", original.CaseID, original.Arm)
		}
		attempt, recordErr := createRejudgeAttempt(directory, runID, previous, judged)
		if recordErr != nil {
			return recordErr
		}
		attempts = append(attempts, attempt)
		latest[key] = attempt
	}
	if err := v2.ExportTrialAttempts(directory, attempts); err != nil {
		return fmt.Errorf("export rejudge Attempt ledger: %w", err)
	}
	return nil
}

func createRejudgeAttempt(directory, runID string, previous v2.TrialAttempt, result v2.TrialResult) (v2.TrialAttempt, error) {
	handle := v2.TrialAttemptHandle{
		RunID: runID, CaseID: result.CaseID, Arm: result.Arm, Number: previous.Number + 1,
	}
	reference := filepath.Join("trials", result.CaseID, result.Arm, "attempts", fmt.Sprintf("%03d", handle.Number))
	artifactDirectory := filepath.Join(directory, reference)
	if err := os.MkdirAll(artifactDirectory, 0o755); err != nil {
		return v2.TrialAttempt{}, fmt.Errorf("create rejudge artifact directory for %s/%s: %w", result.CaseID, result.Arm, err)
	}
	previousReference := previous.ArtifactRefs["artifact_dir"]
	if !safeArtifactReference(previousReference) {
		return v2.TrialAttempt{}, fmt.Errorf("record rejudge evidence for %s/%s: prior artifact reference is unsafe", result.CaseID, result.Arm)
	}
	if err := copyArtifact(
		filepath.Join(directory, previousReference, "consumer.jsonl"),
		filepath.Join(artifactDirectory, "consumer.jsonl"),
	); err != nil {
		return v2.TrialAttempt{}, fmt.Errorf("copy rejudge consumer evidence for %s/%s: %w", result.CaseID, result.Arm, err)
	}
	now := time.Now().UTC()
	attempt := v2.TrialAttempt{
		TrialAttemptHandle: handle,
		Status:             "failed",
		Stage:              v2.TrialStageJudge,
		Error:              result.JudgeError,
		ArtifactRefs:       map[string]string{"artifact_dir": reference},
		StartedAt:          now,
		CompletedAt:        &now,
	}
	if !result.Judged || result.JudgeError != "" {
		return attempt, nil
	}
	if err := copyArtifact(
		filepath.Join(directory, "trials", result.CaseID, result.Arm, "judge.jsonl"),
		filepath.Join(artifactDirectory, "judge.jsonl"),
	); err != nil {
		return v2.TrialAttempt{}, fmt.Errorf("copy rejudge judgment evidence for %s/%s: %w", result.CaseID, result.Arm, err)
	}
	attempt.Status = "completed"
	attempt.Stage = v2.TrialStageCompleted
	attempt.Error = ""
	return attempt, nil
}

func latestAttempts(attempts []v2.TrialAttempt) map[string]v2.TrialAttempt {
	latest := make(map[string]v2.TrialAttempt, len(attempts))
	for _, attempt := range attempts {
		key := trialIdentity(attempt.CaseID, attempt.Arm)
		if current, exists := latest[key]; !exists || attempt.Number > current.Number {
			latest[key] = attempt
		}
	}
	return latest
}

func trialIdentity(caseID, arm string) string { return caseID + "\x00" + arm }

func copyArtifact(source, destination string) error {
	input, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if len(input) == 0 {
		return fmt.Errorf("source artifact is empty")
	}
	if err := os.WriteFile(destination, input, 0o600); err != nil {
		return err
	}
	return nil
}
