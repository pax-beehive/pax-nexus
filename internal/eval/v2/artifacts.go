package v2

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"
)

const ArtifactSchemaVersion = "pax-eval-v2.1"

type SummaryRow struct {
	DimensionType  string
	DimensionValue string
	Arm            string
	Trials         int
	Completed      int
	Failed         int
	Exact          int
	SafeSuccess    int
	MeanTokenF1    float64
	MeanCost       float64
	MeanDurationMS float64
}

type PairwiseRow struct {
	Category        string
	BaselineArm     string
	CandidateArm    string
	Pairs           int
	Wins            int
	Losses          int
	Ties            int
	MeanTokenF1     float64
	MeanDeltaF1     float64
	ExactLift       int
	SafeSuccessLift int
}

func ExportArtifacts(directory string, run RunRecord, baselineArm string, formats []string, results []TrialResult) error {
	if len(results) == 0 {
		return fmt.Errorf("export eval artifacts: results are required")
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create eval artifact directory: %w", err)
	}
	slices.SortFunc(results, func(left, right TrialResult) int {
		if left.CaseID != right.CaseID {
			return cmpString(left.CaseID, right.CaseID)
		}
		return cmpString(left.Arm, right.Arm)
	})
	files := make(map[string]string)
	for _, format := range formats {
		switch format {
		case "jsonl":
			if err := writeJSONLines(filepath.Join(directory, "trials.jsonl"), results); err != nil {
				return err
			}
			files["raw_trials"] = "trials.jsonl"
		case "csv":
			if err := exportCSVArtifacts(directory, baselineArm, results); err != nil {
				return err
			}
			files["trials"] = "trials.csv"
			files["summary"] = "summary.csv"
			files["pairwise"] = "pairwise.csv"
		default:
			return fmt.Errorf("export eval artifacts: unsupported format %q", format)
		}
	}
	manifest := map[string]any{
		"schema_version": ArtifactSchemaVersion,
		"run_id":         run.ID, "dataset": run.Dataset, "dataset_revision": run.DatasetRevision,
		"config_hash": run.ConfigHash, "generated_at": time.Now().UTC(),
		"runtime": run.Runtime,
		"files":   files,
	}
	return writeJSON(filepath.Join(directory, "artifacts.json"), manifest)
}

func exportCSVArtifacts(directory, baselineArm string, results []TrialResult) error {
	if err := writeTrialsCSV(filepath.Join(directory, "trials.csv"), results); err != nil {
		return err
	}
	if err := writeSummaryCSV(filepath.Join(directory, "summary.csv"), Summarize(results)); err != nil {
		return err
	}
	if err := writePairwiseCSV(filepath.Join(directory, "pairwise.csv"), Pairwise(results, baselineArm)); err != nil {
		return err
	}
	return nil
}

func Summarize(results []TrialResult) []SummaryRow {
	type key struct{ dimensionType, dimensionValue, arm string }
	groups := make(map[key][]TrialResult)
	for _, result := range results {
		groups[key{"overall", "all", result.Arm}] = append(groups[key{"overall", "all", result.Arm}], result)
		groups[key{"category", result.Category, result.Arm}] = append(groups[key{"category", result.Category, result.Arm}], result)
	}
	keys := make([]key, 0, len(groups))
	for current := range groups {
		keys = append(keys, current)
	}
	slices.SortFunc(keys, func(left, right key) int {
		if left.dimensionType != right.dimensionType {
			return cmpString(left.dimensionType, right.dimensionType)
		}
		if left.dimensionValue != right.dimensionValue {
			return cmpString(left.dimensionValue, right.dimensionValue)
		}
		return cmpString(left.arm, right.arm)
	})
	rows := make([]SummaryRow, 0, len(keys))
	for _, current := range keys {
		rows = append(rows, summarizeGroup(current.dimensionType, current.dimensionValue, current.arm, groups[current]))
	}
	return rows
}

func Pairwise(results []TrialResult, baselineArm string) []PairwiseRow {
	byCase := make(map[string]map[string]TrialResult)
	categories := make(map[string]struct{})
	for _, result := range results {
		if result.Status != "completed" {
			continue
		}
		byCase[result.CaseID] = ensureArmMap(byCase[result.CaseID])
		byCase[result.CaseID][result.Arm] = result
		categories[result.Category] = struct{}{}
	}
	arms := uniqueArms(results, baselineArm)
	dimensions := []string{"all"}
	for category := range categories {
		dimensions = append(dimensions, category)
	}
	slices.Sort(dimensions[1:])
	rows := make([]PairwiseRow, 0, len(dimensions)*len(arms))
	for _, category := range dimensions {
		for _, arm := range arms {
			rows = append(rows, compareArm(byCase, category, baselineArm, arm))
		}
	}
	return rows
}

func summarizeGroup(dimensionType, dimensionValue, arm string, results []TrialResult) SummaryRow {
	row := SummaryRow{DimensionType: dimensionType, DimensionValue: dimensionValue, Arm: arm, Trials: len(results)}
	for _, result := range results {
		if result.Status != "completed" {
			row.Failed++
			continue
		}
		row.Completed++
		row.Exact += boolInt(result.Exact)
		row.SafeSuccess += boolInt(result.SafeSuccess)
		row.MeanTokenF1 += result.TokenF1
		row.MeanCost += result.Cost
		row.MeanDurationMS += float64(result.TotalDurationMS)
	}
	if row.Completed > 0 {
		row.MeanTokenF1 /= float64(row.Completed)
		row.MeanCost /= float64(row.Completed)
		row.MeanDurationMS /= float64(row.Completed)
	}
	return row
}

func compareArm(byCase map[string]map[string]TrialResult, category, baselineArm, candidateArm string) PairwiseRow {
	row := PairwiseRow{Category: category, BaselineArm: baselineArm, CandidateArm: candidateArm}
	for _, arms := range byCase {
		baseline, baselineOK := arms[baselineArm]
		candidate, candidateOK := arms[candidateArm]
		if !baselineOK || !candidateOK || (category != "all" && candidate.Category != category) {
			continue
		}
		delta := candidate.TokenF1 - baseline.TokenF1
		row.Pairs++
		row.MeanTokenF1 += candidate.TokenF1
		row.MeanDeltaF1 += delta
		row.ExactLift += boolInt(candidate.Exact) - boolInt(baseline.Exact)
		row.SafeSuccessLift += boolInt(candidate.SafeSuccess) - boolInt(baseline.SafeSuccess)
		switch {
		case delta > 0:
			row.Wins++
		case delta < 0:
			row.Losses++
		default:
			row.Ties++
		}
	}
	if row.Pairs > 0 {
		row.MeanTokenF1 /= float64(row.Pairs)
		row.MeanDeltaF1 /= float64(row.Pairs)
	}
	return row
}

func uniqueArms(results []TrialResult, baseline string) []string {
	set := make(map[string]struct{})
	for _, result := range results {
		if result.Arm != baseline {
			set[result.Arm] = struct{}{}
		}
	}
	arms := make([]string, 0, len(set))
	for arm := range set {
		arms = append(arms, arm)
	}
	slices.Sort(arms)
	return arms
}

func ensureArmMap(value map[string]TrialResult) map[string]TrialResult {
	if value == nil {
		return make(map[string]TrialResult)
	}
	return value
}

func writeJSONLines(path string, results []TrialResult) error {
	output, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create eval JSONL: %w", err)
	}
	buffer := bufio.NewWriter(output)
	encoder := json.NewEncoder(buffer)
	for _, result := range results {
		if err := encoder.Encode(result); err != nil {
			if closeErr := output.Close(); closeErr != nil {
				return errors.Join(fmt.Errorf("encode eval JSONL: %w", err), fmt.Errorf("close eval JSONL: %w", closeErr))
			}
			return fmt.Errorf("encode eval JSONL: %w", err)
		}
	}
	if err := buffer.Flush(); err != nil {
		if closeErr := output.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("flush eval JSONL: %w", err), fmt.Errorf("close eval JSONL: %w", closeErr))
		}
		return fmt.Errorf("flush eval JSONL: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close eval JSONL: %w", err)
	}
	return nil
}

func writeTrialsCSV(path string, results []TrialResult) error {
	header := []string{"run_id", "dataset", "dataset_revision", "case_id", "category", "arm", "asking_user_id", "status", "exact", "safe_success", "token_f1", "input_tokens", "output_tokens", "cost", "producer_duration_ms", "readiness_duration_ms", "consumer_duration_ms", "total_duration_ms", "session_id", "expected", "answer", "error", "started_at", "completed_at"}
	rows := make([][]string, 0, len(results))
	for _, result := range results {
		rows = append(rows, []string{
			result.RunID, result.Dataset, result.DatasetRevision, result.CaseID, result.Category, result.Arm, result.AskingUserID, result.Status,
			strconv.FormatBool(result.Exact), strconv.FormatBool(result.SafeSuccess), floatString(result.TokenF1), strconv.Itoa(result.InputTokens),
			strconv.Itoa(result.OutputTokens), floatString(result.Cost), strconv.FormatInt(result.ProducerDurationMS, 10), strconv.FormatInt(result.ReadinessDurationMS, 10),
			strconv.FormatInt(result.ConsumerDurationMS, 10), strconv.FormatInt(result.TotalDurationMS, 10), result.SessionID, result.Expected, result.Answer, result.Error,
			result.StartedAt.Format(time.RFC3339Nano), result.CompletedAt.Format(time.RFC3339Nano),
		})
	}
	return writeCSV(path, header, rows)
}

func writeSummaryCSV(path string, summaries []SummaryRow) error {
	header := []string{"dimension_type", "dimension_value", "arm", "trials", "completed", "failed", "exact", "safe_success", "mean_token_f1", "mean_cost", "mean_duration_ms"}
	rows := make([][]string, 0, len(summaries))
	for _, row := range summaries {
		rows = append(rows, []string{row.DimensionType, row.DimensionValue, row.Arm, strconv.Itoa(row.Trials), strconv.Itoa(row.Completed), strconv.Itoa(row.Failed), strconv.Itoa(row.Exact), strconv.Itoa(row.SafeSuccess), floatString(row.MeanTokenF1), floatString(row.MeanCost), floatString(row.MeanDurationMS)})
	}
	return writeCSV(path, header, rows)
}

func writePairwiseCSV(path string, comparisons []PairwiseRow) error {
	header := []string{"category", "baseline_arm", "candidate_arm", "pairs", "wins", "losses", "ties", "mean_token_f1", "mean_delta_f1", "exact_lift", "safe_success_lift"}
	rows := make([][]string, 0, len(comparisons))
	for _, row := range comparisons {
		rows = append(rows, []string{row.Category, row.BaselineArm, row.CandidateArm, strconv.Itoa(row.Pairs), strconv.Itoa(row.Wins), strconv.Itoa(row.Losses), strconv.Itoa(row.Ties), floatString(row.MeanTokenF1), floatString(row.MeanDeltaF1), strconv.Itoa(row.ExactLift), strconv.Itoa(row.SafeSuccessLift)})
	}
	return writeCSV(path, header, rows)
}

func writeCSV(path string, header []string, rows [][]string) error {
	output, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create eval CSV: %w", err)
	}
	writer := csv.NewWriter(output)
	if err := writer.Write(header); err != nil {
		if closeErr := output.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("write eval CSV header: %w", err), fmt.Errorf("close eval CSV: %w", closeErr))
		}
		return fmt.Errorf("write eval CSV header: %w", err)
	}
	if err := writer.WriteAll(rows); err != nil {
		if closeErr := output.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("write eval CSV: %w", err), fmt.Errorf("close eval CSV: %w", closeErr))
		}
		return fmt.Errorf("write eval CSV: %w", err)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		if closeErr := output.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("flush eval CSV: %w", err), fmt.Errorf("close eval CSV: %w", closeErr))
		}
		return fmt.Errorf("flush eval CSV: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close eval CSV: %w", err)
	}
	return nil
}

func writeJSON(path string, value any) error {
	output, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create eval artifact manifest: %w", err)
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		if closeErr := output.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("encode eval artifact manifest: %w", err), fmt.Errorf("close eval artifact manifest: %w", closeErr))
		}
		return fmt.Errorf("encode eval artifact manifest: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close eval artifact manifest: %w", err)
	}
	return nil
}

func floatString(value float64) string { return strconv.FormatFloat(value, 'f', 6, 64) }

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func cmpString(left, right string) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}
