package v3

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	v2 "github.com/pax-beehive/pax-nexus/internal/eval/v2"
)

const ValiditySchemaVersion = "pax-eval-v3-validity-v1"

const (
	ValidityStatusValid   = "valid"
	ValidityStatusInvalid = "invalid"
)

var ErrInvalidRun = errors.New("eval v3 run is invalid for comparison")

// ValidityFailure identifies one observable reason a Run cannot support a
// comparative score.
type ValidityFailure struct {
	Check  string `json:"check"`
	CaseID string `json:"case_id,omitempty"`
	Arm    string `json:"arm,omitempty"`
	Reason string `json:"reason"`
}

// ValidityCheck summarizes one independently inspectable acceptance rule.
type ValidityCheck struct {
	Name     string `json:"name"`
	Passed   bool   `json:"passed"`
	Failures int    `json:"failures"`
}

// ValidityReport is the durable Eval v3 product-use acceptance record.
type ValidityReport struct {
	SchemaVersion  string            `json:"schema_version"`
	RunID          string            `json:"run_id"`
	Status         string            `json:"status"`
	Valid          bool              `json:"valid"`
	ExpectedTrials int               `json:"expected_trials"`
	ObservedTrials int               `json:"observed_trials"`
	Checks         []ValidityCheck   `json:"checks"`
	Failures       []ValidityFailure `json:"failures,omitempty"`
	GeneratedAt    time.Time         `json:"generated_at"`
}

type validityCollector struct {
	failures []ValidityFailure
	checks   []ValidityCheck
}

type ingestReceipt struct {
	Provider     string `json:"provider"`
	SourceEvents int    `json:"source_events"`
	Accepted     int    `json:"accepted"`
	Duplicate    int    `json:"duplicate"`
	Created      int    `json:"created"`
	Updated      int    `json:"updated"`
	Deleted      int    `json:"deleted"`
	MemoryItems  int    `json:"memory_items"`
}

type resolvedConfig struct {
	Config  v2.Config         `json:"config"`
	Runtime map[string]string `json:"runtime"`
}

// EvaluateValidity inspects the complete Eval v3 evidence set through one
// protocol Module. Missing or malformed evidence produces an invalid report;
// operational inability to inspect the output directory returns an error.
func EvaluateValidity(directory string, run v2.RunRecord, cases []v2.Case, results []v2.TrialResult) (ValidityReport, error) {
	if strings.TrimSpace(directory) == "" {
		return ValidityReport{}, fmt.Errorf("evaluate eval v3 validity: output directory is required")
	}
	report := ValidityReport{
		SchemaVersion: ValiditySchemaVersion, RunID: run.ID,
		ExpectedTrials: len(cases) * len(architectureArms), ObservedTrials: len(results), GeneratedAt: time.Now().UTC(),
	}
	collector := &validityCollector{}
	evaluateTrialMatrix(collector, run, cases, results)
	evaluateIngestEvidence(collector, directory, run)
	evaluateRecallObservations(collector, results)
	evaluateAttemptArtifacts(collector, directory, run.ID, run.Config.Judge != nil, results)
	evaluateResolvedConfig(collector, directory, run)
	report.Checks = collector.checks
	report.Failures = collector.failures
	report.Valid = len(report.Failures) == 0
	report.Status = ValidityStatusInvalid
	if report.Valid {
		report.Status = ValidityStatusValid
	}
	return report, nil
}

// ExportValidity writes the protocol decision before comparison artifacts are
// published, so invalid Runs retain inspectable rejection evidence.
func ExportValidity(directory string, report ValidityReport) (returnedErr error) {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create eval v3 validity artifact directory: %w", err)
	}
	output, err := os.CreateTemp(directory, ".validity-*.json")
	if err != nil {
		return fmt.Errorf("create eval v3 validity artifact: %w", err)
	}
	temporaryPath := output.Name()
	defer func() {
		if returnedErr != nil {
			if removeErr := os.Remove(temporaryPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				returnedErr = errors.Join(returnedErr, fmt.Errorf("remove incomplete eval v3 validity artifact: %w", removeErr))
			}
		}
	}()
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		if closeErr := output.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("encode eval v3 validity artifact: %w", err), fmt.Errorf("close incomplete eval v3 validity artifact: %w", closeErr))
		}
		return fmt.Errorf("encode eval v3 validity artifact: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close eval v3 validity artifact before publish: %w", err)
	}
	if err := os.Rename(temporaryPath, filepath.Join(directory, "validity.json")); err != nil {
		return fmt.Errorf("publish eval v3 validity artifact: %w", err)
	}
	return nil
}

// RequireValid converts a durable invalid decision into the CLI acceptance
// error used to reject comparative scoring.
func RequireValid(report ValidityReport) error {
	if report.Valid {
		return nil
	}
	return fmt.Errorf("%w: %d validity failures", ErrInvalidRun, len(report.Failures))
}

func evaluateTrialMatrix(collector *validityCollector, run v2.RunRecord, cases []v2.Case, results []v2.TrialResult) {
	start := len(collector.failures)
	expected := make(map[string]struct{}, len(cases)*len(architectureArms))
	for _, evalCase := range cases {
		for _, arm := range architectureArms {
			expected[evalCase.ID+"\x00"+arm] = struct{}{}
		}
	}
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		key := result.CaseID + "\x00" + result.Arm
		if _, duplicate := seen[key]; duplicate {
			collector.fail("trial_matrix", result.CaseID, result.Arm, "duplicate Trial result")
			continue
		}
		seen[key] = struct{}{}
		if _, exists := expected[key]; !exists {
			collector.fail("trial_matrix", result.CaseID, result.Arm, "unexpected Case or Arm")
		}
		if result.RunID != run.ID {
			collector.fail("trial_matrix", result.CaseID, result.Arm, "Trial run_id does not match Run")
		}
		if result.Status != "completed" {
			collector.fail("trial_matrix", result.CaseID, result.Arm, "Trial is not completed")
		}
		if !result.Judged || result.JudgeError != "" {
			collector.fail("trial_matrix", result.CaseID, result.Arm, "Trial has no successful judgment")
		}
	}
	for key := range expected {
		if _, exists := seen[key]; exists {
			continue
		}
		caseID, arm, _ := strings.Cut(key, "\x00")
		collector.fail("trial_matrix", caseID, arm, "Trial result is missing")
	}
	collector.finishCheck("trial_matrix", start)
}

func evaluateIngestEvidence(collector *validityCollector, directory string, run v2.RunRecord) {
	coverageStart := len(collector.failures)
	var header manifestHeader
	if err := readJSON(run.Config.Run.Manifest, &header); err != nil {
		collector.fail("source_coverage", "", "", "manifest is unavailable or malformed: "+err.Error())
		collector.finishCheck("source_coverage", coverageStart)
		mutationStart := len(collector.failures)
		collector.fail("memory_mutation", "", "", "source coverage could not be established")
		collector.finishCheck("memory_mutation", mutationStart)
		return
	}
	if header.FullDomainMessages <= 0 {
		collector.fail("source_coverage", "", "", "manifest has no full-domain source message count")
		collector.finishCheck("source_coverage", coverageStart)
		mutationStart := len(collector.failures)
		collector.fail("memory_mutation", "", "", "source coverage could not be established")
		collector.finishCheck("memory_mutation", mutationStart)
		return
	}
	if header.Protocol != ManifestProtocol || header.DatasetRevision != run.DatasetRevision {
		collector.fail("source_coverage", "", "", "manifest protocol or dataset revision does not match the durable Run")
	}
	tests := []struct {
		name     string
		provider string
		arm      string
		valid    func(ingestReceipt, int) bool
		mutation func(ingestReceipt, int) string
	}{
		{name: "team-note-ingest.json", provider: "team_note", arm: ArmPrivateSQLiteTeamNote, valid: func(receipt ingestReceipt, expected int) bool {
			return receipt.SourceEvents == expected && receipt.Accepted+receipt.Duplicate == expected
		}, mutation: func(receipt ingestReceipt, _ int) string {
			if receipt.MemoryItems <= 0 {
				return "ingest produced no observable Team Note mutation"
			}
			return ""
		}},
		{name: "mem0-ingest.json", provider: "mem0_messages", arm: ArmGroupMemBenchMem0, valid: func(receipt ingestReceipt, expected int) bool {
			return receipt.SourceEvents == expected && receipt.Accepted == expected
		}, mutation: func(receipt ingestReceipt, _ int) string {
			if !hasMutation(receipt) {
				return "ingest produced no observable Mem0 mutation"
			}
			return ""
		}},
		{name: "private-sqlite-ingest.json", provider: "private_sqlite", arm: ArmPrivateSQLiteTeamNote, valid: func(receipt ingestReceipt, expected int) bool {
			return receipt.SourceEvents == expected && receipt.Accepted == expected
		}, mutation: func(receipt ingestReceipt, expected int) string {
			if receipt.Created != expected {
				return "ingest did not materialize every full-domain source Event"
			}
			return ""
		}},
	}
	receipts := make([]ingestReceipt, 0, len(tests))
	for _, test := range tests {
		var receipt ingestReceipt
		if err := readJSON(filepath.Join(directory, "memory", test.name), &receipt); err != nil {
			collector.fail("source_coverage", "", test.provider, "ingest receipt is unavailable or malformed: "+err.Error())
			receipts = append(receipts, receipt)
			continue
		}
		if receipt.Provider != test.provider || !test.valid(receipt, header.FullDomainMessages) {
			collector.fail("source_coverage", "", test.provider, "ingest receipt does not cover the full-domain source")
		}
		receipts = append(receipts, receipt)
	}
	collector.finishCheck("source_coverage", coverageStart)
	mutationStart := len(collector.failures)
	for index, test := range tests {
		if reason := test.mutation(receipts[index], header.FullDomainMessages); reason != "" {
			collector.fail("memory_mutation", "", test.arm, reason)
		}
	}
	collector.finishCheck("memory_mutation", mutationStart)
}

func hasMutation(receipt ingestReceipt) bool {
	return receipt.Created+receipt.Updated+receipt.Deleted > 0
}

func evaluateRecallObservations(collector *validityCollector, results []v2.TrialResult) {
	start := len(collector.failures)
	for _, result := range results {
		if result.Arm == ArmNoMemoryTeam {
			if result.MemoryRecallObserved || result.MemoryRecallProviderCalls > 0 {
				collector.fail("recall_observation", result.CaseID, result.Arm, "no-memory Trial observed a recall provider")
			}
			continue
		}
		if !result.MemoryRecallObserved {
			collector.fail("recall_observation", result.CaseID, result.Arm, "memory Trial has no recall observation")
		} else if !result.MemoryRecallSuccess {
			collector.fail("recall_observation", result.CaseID, result.Arm, "memory recall observation was unsuccessful")
		}
	}
	collector.finishCheck("recall_observation", start)
}

func evaluateAttemptArtifacts(collector *validityCollector, directory, runID string, requireJudge bool, results []v2.TrialResult) {
	start := len(collector.failures)
	attempts, err := loadAttempts(filepath.Join(directory, "attempts.jsonl"))
	if err != nil {
		collector.fail("attempt_artifacts", "", "", "Attempt ledger is unavailable or malformed: "+err.Error())
		collector.finishCheck("attempt_artifacts", start)
		return
	}
	latest := make(map[string]v2.TrialAttempt, len(attempts))
	for _, attempt := range attempts {
		key := attempt.CaseID + "\x00" + attempt.Arm
		if current, exists := latest[key]; !exists || attempt.Number > current.Number {
			latest[key] = attempt
		}
	}
	for _, result := range results {
		attempt, exists := latest[result.CaseID+"\x00"+result.Arm]
		if !exists {
			collector.fail("attempt_artifacts", result.CaseID, result.Arm, "final Trial has no Attempt")
			continue
		}
		if attempt.RunID != runID {
			collector.fail("attempt_artifacts", result.CaseID, result.Arm, "Attempt run_id does not match Run")
		}
		if attempt.Status != "completed" {
			collector.fail("attempt_artifacts", result.CaseID, result.Arm, "final Attempt is not completed")
		}
		reference := attempt.ArtifactRefs["artifact_dir"]
		if !safeArtifactReference(reference) {
			collector.fail("attempt_artifacts", result.CaseID, result.Arm, "Attempt artifact reference is missing or unsafe")
			continue
		}
		required := []string{"consumer.jsonl"}
		if requireJudge {
			required = append(required, "judge.jsonl")
		}
		for _, name := range required {
			if !nonEmptyFile(filepath.Join(directory, reference, name)) {
				collector.fail("attempt_artifacts", result.CaseID, result.Arm, name+" is missing or empty")
			}
		}
	}
	collector.finishCheck("attempt_artifacts", start)
}

func evaluateResolvedConfig(collector *validityCollector, directory string, run v2.RunRecord) {
	start := len(collector.failures)
	var resolved resolvedConfig
	if err := readJSON(filepath.Join(directory, "config.resolved.json"), &resolved); err != nil {
		collector.fail("resolved_config_provenance", "", "", "resolved config is unavailable or malformed: "+err.Error())
		collector.finishCheck("resolved_config_provenance", start)
		return
	}
	hash, err := resolved.Config.HashWithRuntime(resolved.Runtime)
	if err != nil {
		collector.fail("resolved_config_provenance", "", "", "resolved config cannot be hashed: "+err.Error())
	} else if resolved.Config.Version != ConfigVersion || hash != run.ConfigHash {
		collector.fail("resolved_config_provenance", "", "", "resolved config does not match the durable Run")
	}
	collector.finishCheck("resolved_config_provenance", start)
}

func (collector *validityCollector) fail(check, caseID, arm, reason string) {
	collector.failures = append(collector.failures, ValidityFailure{Check: check, CaseID: caseID, Arm: arm, Reason: reason})
}

func (collector *validityCollector) finishCheck(name string, start int) {
	failures := len(collector.failures) - start
	collector.checks = append(collector.checks, ValidityCheck{Name: name, Passed: failures == 0, Failures: failures})
}

func readJSON(path string, target any) error {
	input, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(input, target); err != nil {
		return err
	}
	return nil
}

func loadAttempts(path string) ([]v2.TrialAttempt, error) {
	input, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	attempts := make([]v2.TrialAttempt, 0)
	scanner := bufio.NewScanner(bytes.NewReader(input))
	for scanner.Scan() {
		var attempt v2.TrialAttempt
		if err := json.Unmarshal(scanner.Bytes(), &attempt); err != nil {
			return nil, err
		}
		attempts = append(attempts, attempt)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return attempts, nil
}

func safeArtifactReference(reference string) bool {
	cleaned := filepath.Clean(reference)
	return strings.TrimSpace(reference) != "" && !filepath.IsAbs(reference) && cleaned != "." && cleaned != ".." && !strings.HasPrefix(cleaned, ".."+string(filepath.Separator))
}

func nonEmptyFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}
