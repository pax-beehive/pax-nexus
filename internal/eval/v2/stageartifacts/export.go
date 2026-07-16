// Package stageartifacts connects live Team Note observations to Eval v2
// artifacts without coupling the stage scorer to product storage.
package stageartifacts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pax-beehive/pax-nexus/internal/eval/stagecapture"
	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	v2 "github.com/pax-beehive/pax-nexus/internal/eval/v2"
)

type Observer interface {
	Capture(context.Context, stagecapture.Target) (stageeval.Observation, stageeval.Observation, error)
}

type Request struct {
	RunID           string
	OutputDirectory string
	Config          v2.StageCaptureConfig
	Fixtures        stageeval.FixtureSet
	Cases           []v2.Case
	Results         []v2.TrialResult
}

func Export(ctx context.Context, observer Observer, request Request) (returnedErr error) {
	if observer == nil || request.RunID == "" || request.OutputDirectory == "" {
		return fmt.Errorf("export stage artifacts: observer, run ID, and output directory are required")
	}
	if err := os.MkdirAll(request.OutputDirectory, 0o755); err != nil {
		return fmt.Errorf("create stage artifact output directory: %w", err)
	}
	temporaryRoot, err := os.MkdirTemp(request.OutputDirectory, ".stage-artifacts-")
	if err != nil {
		return fmt.Errorf("create temporary stage artifact directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(temporaryRoot); err != nil {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("remove temporary stage artifacts: %w", err))
		}
	}()
	request.OutputDirectory = temporaryRoot
	cases := caseIndex(request.Cases)
	results := resultIndex(request.Results)
	for _, arm := range request.Config.Arms {
		observations, err := captureArm(ctx, observer, request, arm, cases, results)
		if err != nil {
			return err
		}
		if err := exportArm(request.OutputDirectory, arm, request.Fixtures, observations); err != nil {
			return err
		}
	}
	manifest := struct {
		SchemaVersion string              `json:"schema_version"`
		Fixtures      string              `json:"fixtures"`
		Arms          map[string][]string `json:"arms"`
	}{SchemaVersion: stageeval.SchemaVersion, Fixtures: request.Config.Fixtures, Arms: make(map[string][]string)}
	for _, arm := range request.Config.Arms {
		manifest.Arms[arm] = []string{
			filepath.Join(arm, "observations.jsonl"),
			filepath.Join(arm, "stage-results.jsonl"),
			filepath.Join(arm, "stage-summary.json"),
		}
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode stage artifact manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(temporaryRoot, "stage", "artifacts.json"), append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("write stage artifact manifest: %w", err)
	}
	return replaceStageDirectory(temporaryRoot, filepath.Dir(temporaryRoot))
}

func captureArm(
	ctx context.Context,
	observer Observer,
	request Request,
	arm string,
	cases map[string]v2.Case,
	results map[resultKey]v2.TrialResult,
) ([]stageeval.Observation, error) {
	observations := make([]stageeval.Observation, 0, len(request.Fixtures.Cases)*2)
	for _, fixture := range request.Fixtures.Cases {
		extraction, recall, err := captureFixture(ctx, observer, request.RunID, arm, fixture, cases, results)
		if err != nil {
			return nil, err
		}
		observations = append(observations, extraction, recall)
	}
	return observations, nil
}

func captureFixture(
	ctx context.Context,
	observer Observer,
	runID, arm string,
	fixture stageeval.Fixture,
	cases map[string]v2.Case,
	results map[resultKey]v2.TrialResult,
) (stageeval.Observation, stageeval.Observation, error) {
	evalCase, exists := cases[fixture.CaseID]
	if !exists {
		return stageeval.Observation{}, stageeval.Observation{}, fmt.Errorf("export stage artifacts: fixture case %q is absent from eval manifest", fixture.CaseID)
	}
	if err := validateSourceRevision(evalCase, fixture.SourceRevision); err != nil {
		return stageeval.Observation{}, stageeval.Observation{}, err
	}
	if fixture.RecallContext.ConsumerUserID != evalCase.AskingUserID || fixture.RecallContext.Query != evalCase.Question {
		return stageeval.Observation{}, stageeval.Observation{}, fmt.Errorf("export stage artifacts: fixture %q recall controls differ from eval manifest", fixture.CaseID)
	}
	trial, exists := results[resultKey{caseID: fixture.CaseID, arm: arm}]
	if !exists || trial.Status != "completed" || trial.SessionID == "" {
		extraction, recall := unavailableObservations(fixture, "completed trial with a consumer session is unavailable")
		addRunProvenance(&extraction, runID, arm)
		addRunProvenance(&recall, runID, arm)
		return extraction, recall, nil
	}
	if fixture.RecallContext.ConsumerAgentID == "" {
		return stageeval.Observation{}, stageeval.Observation{}, fmt.Errorf("export stage artifacts: fixture %q consumer_agent_id is required", fixture.CaseID)
	}
	extraction, recall, err := observer.Capture(ctx, stagecapture.Target{
		Fixture: fixture, ScopeID: scopeID(runID, evalCase.ScopeID, arm),
		RecipientAgentID: fixture.RecallContext.ConsumerAgentID, RecipientSessionID: trial.SessionID,
	})
	if err != nil {
		return stageeval.Observation{}, stageeval.Observation{}, fmt.Errorf("export stage artifacts for %s/%s: %w", fixture.CaseID, arm, err)
	}
	addRunProvenance(&extraction, runID, arm)
	addRunProvenance(&recall, runID, arm)
	return extraction, recall, nil
}

func unavailableObservations(fixture stageeval.Fixture, message string) (stageeval.Observation, stageeval.Observation) {
	recallContext := fixture.RecallContext
	return stageeval.Observation{
			CaseID: fixture.CaseID, Stage: stageeval.StageExtraction,
			SourceRevision: fixture.SourceRevision, Error: message,
		}, stageeval.Observation{
			CaseID: fixture.CaseID, Stage: stageeval.StageRecall,
			SourceRevision: fixture.SourceRevision, RecallContext: &recallContext, Error: message,
		}
}

func replaceStageDirectory(temporaryRoot, outputDirectory string) error {
	candidate := filepath.Join(temporaryRoot, "stage")
	target := filepath.Join(outputDirectory, "stage")
	backup := filepath.Join(outputDirectory, ".stage-artifacts-previous")
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("remove prior stage artifact backup: %w", err)
	}
	if err := os.Rename(target, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("backup existing stage artifacts: %w", err)
	}
	if err := os.Rename(candidate, target); err != nil {
		if restoreErr := os.Rename(backup, target); restoreErr != nil && !errors.Is(restoreErr, os.ErrNotExist) {
			return errors.Join(fmt.Errorf("publish stage artifacts: %w", err), fmt.Errorf("restore stage artifacts: %w", restoreErr))
		}
		return fmt.Errorf("publish stage artifacts: %w", err)
	}
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("remove stage artifact backup: %w", err)
	}
	return nil
}

func exportArm(outputDirectory, arm string, fixtures stageeval.FixtureSet, observations []stageeval.Observation) error {
	directory := filepath.Join(outputDirectory, "stage", arm)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create stage artifact directory: %w", err)
	}
	var observationData bytes.Buffer
	encoder := json.NewEncoder(&observationData)
	for _, observation := range observations {
		if err := encoder.Encode(observation); err != nil {
			return fmt.Errorf("encode live stage observation: %w", err)
		}
	}
	if err := os.WriteFile(filepath.Join(directory, "observations.jsonl"), observationData.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write live stage observations: %w", err)
	}
	results, summary, err := stageeval.Run(fixtures, bytes.NewReader(observationData.Bytes()))
	if err != nil {
		return fmt.Errorf("score live stage observations: %w", err)
	}
	var resultData bytes.Buffer
	if err := stageeval.WriteResultsJSONL(&resultData, results); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "stage-results.jsonl"), resultData.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write live stage results: %w", err)
	}
	var summaryData bytes.Buffer
	if err := stageeval.WriteSummaryJSON(&summaryData, summary); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "stage-summary.json"), summaryData.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write live stage summary: %w", err)
	}
	return nil
}

func validateSourceRevision(evalCase v2.Case, expected string) error {
	data, err := os.ReadFile(filepath.Join(evalCase.ProducerWorkspace, "session-batches.json"))
	if err != nil {
		return fmt.Errorf("read stage source for case %q: %w", evalCase.ID, err)
	}
	digest := sha256.Sum256(data)
	actual := hex.EncodeToString(digest[:])
	if actual != expected {
		return fmt.Errorf("validate stage source for case %q: revision drifted", evalCase.ID)
	}
	return nil
}

func addRunProvenance(observation *stageeval.Observation, runID, arm string) {
	if observation.Provenance == nil {
		observation.Provenance = make(map[string]string)
	}
	observation.Provenance["eval_run_id"] = runID
	observation.Provenance["eval_arm"] = arm
}

func scopeID(runID, caseScopeID, arm string) string {
	result := runID + "-" + caseScopeID
	if arm == "team_note_hybrid" {
		result += "-team-note-hybrid"
	}
	return result
}

func caseIndex(cases []v2.Case) map[string]v2.Case {
	result := make(map[string]v2.Case, len(cases))
	for _, evalCase := range cases {
		result[evalCase.ID] = evalCase
	}
	return result
}

type resultKey struct {
	caseID string
	arm    string
}

func resultIndex(results []v2.TrialResult) map[resultKey]v2.TrialResult {
	indexed := make(map[resultKey]v2.TrialResult, len(results))
	for _, result := range results {
		indexed[resultKey{caseID: result.CaseID, arm: result.Arm}] = result
	}
	return indexed
}
