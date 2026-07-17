package v2

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"
)

const ArtifactSchemaVersion = "pax-eval-v2.8"
const ArtifactSchemaVersionV3 = "pax-eval-v3.0"

type SummaryRow struct {
	DimensionType     string
	DimensionValue    string
	Arm               string
	Trials            int
	Completed         int
	Failed            int
	Exact             int
	SafeSuccess       int
	Judged            int
	Correct           int
	Accuracy          float64
	MeanTokenF1       float64
	TotalJudgeCost    float64
	MeanJudgeCost     float64
	CostScope         string
	TotalCost         float64
	MeanCompletedCost float64
	TotalInputTokens  int
	TotalOutputTokens int
	MeanDurationMS    float64
}

type PairwiseRow struct {
	Category                       string
	BaselineArm                    string
	CandidateArm                   string
	Pairs                          int
	Wins                           int
	Losses                         int
	Ties                           int
	AccuracyPairs                  int
	AccuracyWins                   int
	AccuracyLosses                 int
	AccuracyTies                   int
	CandidateAccuracy              float64
	AccuracyDelta                  float64
	MeanTokenF1                    float64
	MeanDeltaF1                    float64
	CostScope                      string
	BaselineMeanCost               float64
	CandidateMeanCost              float64
	MeanDeltaCost                  float64
	PairedCompletedIncrementalCost float64
	ExactLift                      int
	SafeSuccessLift                int
}

type CostSummary struct {
	Scope             string             `json:"scope"`
	TotalCost         float64            `json:"total_cost"`
	CompletedCost     float64            `json:"completed_cost"`
	FailedCost        float64            `json:"failed_cost"`
	TotalInputTokens  int                `json:"total_input_tokens"`
	TotalOutputTokens int                `json:"total_output_tokens"`
	ByArm             map[string]ArmCost `json:"by_arm"`
}

type ArmCost struct {
	Trials            int     `json:"trials"`
	TotalCost         float64 `json:"total_cost"`
	MeanAttemptCost   float64 `json:"mean_attempt_cost"`
	TotalInputTokens  int     `json:"total_input_tokens"`
	TotalOutputTokens int     `json:"total_output_tokens"`
}

type HTMLRenderer func(io.Writer) error

func LoadTrialResultsJSONL(path string) (results []TrialResult, returnedErr error) {
	input, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open eval trial JSONL: %w", err)
	}
	defer func() {
		if closeErr := input.Close(); closeErr != nil {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("close eval trial JSONL: %w", closeErr))
		}
	}()
	results = make([]TrialResult, 0)
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var result TrialResult
		if err := json.Unmarshal(scanner.Bytes(), &result); err != nil {
			return nil, fmt.Errorf("decode eval trial JSONL: %w", err)
		}
		results = append(results, result)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan eval trial JSONL: %w", err)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("load eval trial JSONL: results are required")
	}
	return results, nil
}

func ExportArtifacts(directory string, run RunRecord, baselineArm string, formats []string, results []TrialResult, renderHTML HTMLRenderer) error {
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
			if err := exportCSVArtifacts(directory, run.Config.Version, baselineArm, results); err != nil {
				return err
			}
			files["trials"] = "trials.csv"
			files["summary"] = "summary.csv"
			files["pairwise"] = "pairwise.csv"
		case "html":
			if renderHTML == nil {
				return fmt.Errorf("export eval artifacts: html renderer is required")
			}
			if err := writeHTMLReport(directory, renderHTML); err != nil {
				return err
			}
			files["report"] = "report.html"
		default:
			return fmt.Errorf("export eval artifacts: unsupported format %q", format)
		}
	}
	if err := ExportResolvedConfig(filepath.Join(directory, "config.resolved.json"), run.Config, run.Runtime); err != nil {
		return err
	}
	files["resolved_config"] = "config.resolved.json"
	if err := linkOptionalArtifacts(directory, run.Config, files); err != nil {
		return err
	}
	schemaVersion := ArtifactSchemaVersion
	if run.Config.Version == "v3" {
		schemaVersion = ArtifactSchemaVersionV3
	}
	manifest := map[string]any{
		"schema_version": schemaVersion,
		"run_id":         run.ID, "dataset": run.Dataset, "dataset_revision": run.DatasetRevision,
		"config_hash": run.ConfigHash, "generated_at": time.Now().UTC(),
		"runtime":                 run.Runtime,
		"answerer_seed":           run.Config.AnswererSeed,
		"mem0_reproduction_level": run.Config.Mem0ReproductionLevel,
		"files":                   files,
		"cost_summary":            CostTotals(results),
	}
	if run.Config.Version == "v3" {
		reproduction, err := buildMem0Reproduction(directory, run, results)
		if err != nil {
			return err
		}
		manifest["mem0_reproduction"] = reproduction
	}
	return writeJSON(filepath.Join(directory, "artifacts.json"), manifest)
}

func buildMem0Reproduction(directory string, run RunRecord, results []TrialResult) (map[string]any, error) {
	observed := make(map[string]float64)
	for _, row := range Summarize(results) {
		if row.Arm != "groupmembench_mem0" {
			continue
		}
		if row.DimensionType == "overall" {
			observed["aggregate"] = row.Accuracy
		}
		if row.DimensionType == "category" {
			observed[row.DimensionValue] = row.Accuracy
		}
	}
	input, err := os.ReadFile(filepath.Join(directory, "memory", "mem0-ingest.json"))
	if err != nil {
		return nil, fmt.Errorf("read eval v3 Mem0 ingest receipt: %w", err)
	}
	var parsed struct {
		Accepted     int `json:"accepted"`
		Created      int `json:"created"`
		Updated      int `json:"updated"`
		Deleted      int `json:"deleted"`
		SourceEvents int `json:"source_events"`
	}
	if err := json.Unmarshal(input, &parsed); err != nil {
		return nil, fmt.Errorf("decode eval v3 Mem0 ingest receipt: %w", err)
	}
	receipt := map[string]int{
		"accepted": parsed.Accepted, "created": parsed.Created, "updated": parsed.Updated,
		"deleted": parsed.Deleted, "source_events": parsed.SourceEvents,
	}
	return map[string]any{
		"level": run.Config.Mem0ReproductionLevel, "implementation": "self_hosted_mem0",
		"groupmembench_revision": run.DatasetRevision, "mem0_image": run.Runtime["MEM0_IMAGE"],
		"ingestion_unit": "original_message", "author_metadata_preserved": true,
		"namespace": map[string]string{
			"user_id": run.Runtime["MEM0_EVAL_USER_ID"], "agent_id": run.Runtime["MEM0_EVAL_AGENT_ID"], "run_scope": "one shared domain run_id",
		},
		"retrieval": map[string]any{
			"max_results": 5, "score_semantics": run.Runtime["MEM0_SCORE_SEMANTICS"], "scope_payload": run.Runtime["MEM0_SEARCH_SCOPE_PAYLOAD"],
		},
		"models": map[string]string{
			"ingestion": run.Runtime["MEM0_DEFAULT_LLM_MODEL"], "embedding": run.Runtime["MEM0_DEFAULT_EMBEDDER_MODEL"],
			"answer": run.Runtime["OPENCODE_MODEL"], "judge": run.Runtime["EVAL_V2_JUDGE_MODEL"],
		},
		"ingest_receipt": receipt, "observed_accuracy": observed,
		"published_accuracy_targets": map[string]float64{"aggregate": 0.2573, "knowledge_update": 0.0467},
		"deviations":                 []string{"official Mem0 runner and per-question artifacts are unavailable", "self-hosted models and paxm retrieval profile are used"},
	}, nil
}

func linkOptionalArtifacts(directory string, config Config, files map[string]string) error {
	if config.StageCapture != nil {
		if _, err := os.Stat(filepath.Join(directory, "stage", "artifacts.json")); err == nil {
			files["stage"] = filepath.Join("stage", "artifacts.json")
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect stage artifact manifest: %w", err)
		}
	}
	if config.Version == "v3" {
		return linkEvalV3MemoryReceipts(directory, files)
	}
	return nil
}

func linkEvalV3MemoryReceipts(directory string, files map[string]string) error {
	receipts := map[string]string{
		"team_note_ingest":      "team-note-ingest.json",
		"mem0_ingest":           "mem0-ingest.json",
		"private_sqlite_ingest": "private-sqlite-ingest.json",
	}
	for key, name := range receipts {
		relativePath := filepath.Join("memory", name)
		if _, err := os.Stat(filepath.Join(directory, relativePath)); err == nil {
			files[key] = relativePath
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect eval v3 memory receipt %q: %w", name, err)
		}
	}
	return nil
}

func ExportResolvedConfig(path string, config Config, runtime map[string]string) error {
	resolved := struct {
		Config  Config            `json:"config"`
		Runtime map[string]string `json:"runtime,omitempty"`
	}{Config: config, Runtime: runtime}
	if err := writeJSON(path, resolved); err != nil {
		return fmt.Errorf("write resolved eval config: %w", err)
	}
	return nil
}

func writeHTMLReport(directory string, render HTMLRenderer) (returnedErr error) {
	file, err := os.CreateTemp(directory, ".report-*.html")
	if err != nil {
		return fmt.Errorf("create temporary eval report: %w", err)
	}
	temporaryPath := file.Name()
	defer func() {
		if err := os.Remove(temporaryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("remove temporary eval report: %w", err))
		}
	}()
	if err := render(file); err != nil {
		if closeErr := file.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("render eval report: %w", err), fmt.Errorf("close temporary eval report: %w", closeErr))
		}
		return fmt.Errorf("render eval report: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary eval report: %w", err)
	}
	if err := os.Rename(temporaryPath, filepath.Join(directory, "report.html")); err != nil {
		return fmt.Errorf("publish eval report: %w", err)
	}
	return nil
}

func CostTotals(results []TrialResult) CostSummary {
	summary := CostSummary{Scope: "opencode_reported", ByArm: make(map[string]ArmCost)}
	for _, result := range results {
		summary.TotalCost += result.Cost
		summary.TotalInputTokens += result.InputTokens
		summary.TotalOutputTokens += result.OutputTokens
		if result.Status == "completed" {
			summary.CompletedCost += result.Cost
		} else {
			summary.FailedCost += result.Cost
		}
		arm := summary.ByArm[result.Arm]
		arm.Trials++
		arm.TotalCost += result.Cost
		arm.TotalInputTokens += result.InputTokens
		arm.TotalOutputTokens += result.OutputTokens
		summary.ByArm[result.Arm] = arm
	}
	for name, arm := range summary.ByArm {
		if arm.Trials > 0 {
			arm.MeanAttemptCost = arm.TotalCost / float64(arm.Trials)
		}
		summary.ByArm[name] = arm
	}
	return summary
}

func exportCSVArtifacts(directory, version, baselineArm string, results []TrialResult) error {
	if err := writeTrialsCSV(filepath.Join(directory, "trials.csv"), results); err != nil {
		return err
	}
	if err := writeSummaryCSV(filepath.Join(directory, "summary.csv"), Summarize(results)); err != nil {
		return err
	}
	comparisons := PairwiseComparisons(results, version, baselineArm)
	if err := writePairwiseCSV(filepath.Join(directory, "pairwise.csv"), comparisons); err != nil {
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
		if result.StrictCrossAgent {
			groups[key{"trial_class", "strict_cross_agent", result.Arm}] = append(groups[key{"trial_class", "strict_cross_agent", result.Arm}], result)
		}
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
	return pairwiseForCandidates(results, baselineArm, uniqueArms(results, baselineArm))
}

// PairwiseComparisons adds protocol-specific paired comparisons to the common
// baseline rows used by CSV and HTML reports.
func PairwiseComparisons(results []TrialResult, version, baselineArm string) []PairwiseRow {
	comparisons := Pairwise(results, baselineArm)
	if version == "v3" {
		comparisons = append(comparisons, pairwiseForCandidates(results, "groupmembench_mem0", []string{"private_sqlite_plus_team_note"})...)
	}
	return comparisons
}

func pairwiseForCandidates(results []TrialResult, baselineArm string, arms []string) []PairwiseRow {
	byCase := make(map[string]map[string]TrialResult)
	categories := make(map[string]struct{})
	for _, result := range results {
		byCase[result.CaseID] = ensureArmMap(byCase[result.CaseID])
		byCase[result.CaseID][result.Arm] = result
		categories[result.Category] = struct{}{}
	}
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
	row := SummaryRow{DimensionType: dimensionType, DimensionValue: dimensionValue, Arm: arm, Trials: len(results), CostScope: "opencode_reported"}
	for _, result := range results {
		row.TotalCost += result.Cost
		row.TotalInputTokens += result.InputTokens
		row.TotalOutputTokens += result.OutputTokens
		if result.Status != "completed" {
			row.Failed++
			continue
		}
		row.Completed++
		row.Exact += boolInt(result.Exact)
		row.SafeSuccess += boolInt(result.SafeSuccess)
		if result.Judged {
			row.Judged++
			row.Correct += boolInt(result.Correct)
			row.TotalJudgeCost += result.JudgeCost
		}
		row.MeanTokenF1 += result.TokenF1
		row.MeanCompletedCost += result.Cost
		row.MeanDurationMS += float64(result.TotalDurationMS)
	}
	if row.Completed > 0 {
		row.MeanTokenF1 /= float64(row.Completed)
		row.MeanCompletedCost /= float64(row.Completed)
		row.MeanDurationMS /= float64(row.Completed)
	}
	if row.Judged > 0 {
		row.Accuracy = float64(row.Correct) / float64(row.Judged)
		row.MeanJudgeCost = row.TotalJudgeCost / float64(row.Judged)
	}
	return row
}

func compareArm(byCase map[string]map[string]TrialResult, category, baselineArm, candidateArm string) PairwiseRow {
	row := PairwiseRow{Category: category, BaselineArm: baselineArm, CandidateArm: candidateArm, CostScope: "opencode_reported"}
	for _, arms := range byCase {
		baseline, baselineOK := arms[baselineArm]
		candidate, candidateOK := arms[candidateArm]
		if !baselineOK || !candidateOK || (category != "all" && candidate.Category != category) {
			continue
		}
		if accuracyResultReady(baseline) && accuracyResultReady(candidate) {
			baselineCorrect := boolInt(baseline.Status == "completed" && baseline.Correct)
			candidateCorrect := boolInt(candidate.Status == "completed" && candidate.Correct)
			row.AccuracyPairs++
			row.CandidateAccuracy += float64(candidateCorrect)
			row.AccuracyDelta += float64(candidateCorrect - baselineCorrect)
			switch {
			case candidateCorrect > baselineCorrect:
				row.AccuracyWins++
			case candidateCorrect < baselineCorrect:
				row.AccuracyLosses++
			default:
				row.AccuracyTies++
			}
		}
		if baseline.Status != "completed" || candidate.Status != "completed" {
			continue
		}
		delta := candidate.TokenF1 - baseline.TokenF1
		costDelta := candidate.Cost - baseline.Cost
		row.Pairs++
		row.MeanTokenF1 += candidate.TokenF1
		row.MeanDeltaF1 += delta
		row.BaselineMeanCost += baseline.Cost
		row.CandidateMeanCost += candidate.Cost
		row.MeanDeltaCost += costDelta
		row.PairedCompletedIncrementalCost += costDelta
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
		row.BaselineMeanCost /= float64(row.Pairs)
		row.CandidateMeanCost /= float64(row.Pairs)
		row.MeanDeltaCost /= float64(row.Pairs)
	}
	if row.AccuracyPairs > 0 {
		row.CandidateAccuracy /= float64(row.AccuracyPairs)
		row.AccuracyDelta /= float64(row.AccuracyPairs)
	}
	return row
}

func accuracyResultReady(result TrialResult) bool {
	return result.Status == "completed" && result.Judged
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
	header := []string{"run_id", "dataset", "dataset_revision", "case_id", "category", "arm", "asking_user_id", "answering_agent_id", "answerer_seed", "strict_cross_agent", "answerer_source_overlap", "status", "memory_ingest_provider", "memory_ingest_accepted", "memory_ingest_duplicate", "memory_ingest_created", "memory_ingest_updated", "memory_ingest_deleted", "memory_ingest_noop_known", "memory_ingest_noop", "memory_source_events", "memory_source_actors", "memory_source_sessions", "exact", "safe_success", "token_f1", "judged", "correct", "judge_answer", "judge_error", "judge_session_id", "judge_input_tokens", "judge_output_tokens", "judge_cost", "judge_duration_ms", "input_tokens", "output_tokens", "cost", "cost_scope", "producer_input_tokens", "producer_output_tokens", "producer_cost", "consumer_input_tokens", "consumer_output_tokens", "consumer_cost", "producer_duration_ms", "readiness_duration_ms", "consumer_duration_ms", "total_duration_ms", "session_id", "question", "expected", "answer", "error", "started_at", "completed_at"}
	rows := make([][]string, 0, len(results))
	for _, result := range results {
		rows = append(rows, []string{
			result.RunID, result.Dataset, result.DatasetRevision, result.CaseID, result.Category, result.Arm, result.AskingUserID,
			result.AnsweringAgentID, result.AnswererSeed, strconv.FormatBool(result.StrictCrossAgent), result.AnswererSourceOverlap, result.Status,
			result.MemoryIngestProvider, strconv.Itoa(result.MemoryIngestAccepted), strconv.Itoa(result.MemoryIngestDuplicate),
			strconv.Itoa(result.MemoryIngestCreated), strconv.Itoa(result.MemoryIngestUpdated), strconv.Itoa(result.MemoryIngestDeleted),
			strconv.FormatBool(result.MemoryIngestNoOpKnown), strconv.FormatBool(result.MemoryIngestNoOp),
			strconv.Itoa(result.MemorySourceEvents), strconv.Itoa(result.MemorySourceActors), strconv.Itoa(result.MemorySourceSessions),
			strconv.FormatBool(result.Exact), strconv.FormatBool(result.SafeSuccess), floatString(result.TokenF1),
			strconv.FormatBool(result.Judged), strconv.FormatBool(result.Correct), result.JudgeAnswer, result.JudgeError, result.JudgeSessionID,
			strconv.Itoa(result.JudgeInputTokens), strconv.Itoa(result.JudgeOutputTokens), floatString(result.JudgeCost), strconv.FormatInt(result.JudgeDurationMS, 10), strconv.Itoa(result.InputTokens),
			strconv.Itoa(result.OutputTokens), floatString(result.Cost), result.CostScope,
			strconv.Itoa(result.ProducerInputTokens), strconv.Itoa(result.ProducerOutputTokens), floatString(result.ProducerCost),
			strconv.Itoa(result.ConsumerInputTokens), strconv.Itoa(result.ConsumerOutputTokens), floatString(result.ConsumerCost),
			strconv.FormatInt(result.ProducerDurationMS, 10), strconv.FormatInt(result.ReadinessDurationMS, 10),
			strconv.FormatInt(result.ConsumerDurationMS, 10), strconv.FormatInt(result.TotalDurationMS, 10), result.SessionID, result.Question, result.Expected, result.Answer, result.Error,
			result.StartedAt.Format(time.RFC3339Nano), result.CompletedAt.Format(time.RFC3339Nano),
		})
	}
	return writeCSV(path, header, rows)
}

func writeSummaryCSV(path string, summaries []SummaryRow) error {
	header := []string{"dimension_type", "dimension_value", "arm", "trials", "completed", "failed", "judged", "correct", "accuracy", "exact", "safe_success", "mean_token_f1", "cost_scope", "total_cost", "mean_completed_cost", "total_judge_cost", "mean_judge_cost", "total_input_tokens", "total_output_tokens", "mean_duration_ms"}
	rows := make([][]string, 0, len(summaries))
	for _, row := range summaries {
		rows = append(rows, []string{row.DimensionType, row.DimensionValue, row.Arm, strconv.Itoa(row.Trials), strconv.Itoa(row.Completed), strconv.Itoa(row.Failed), strconv.Itoa(row.Judged), strconv.Itoa(row.Correct), floatString(row.Accuracy), strconv.Itoa(row.Exact), strconv.Itoa(row.SafeSuccess), floatString(row.MeanTokenF1), row.CostScope, floatString(row.TotalCost), floatString(row.MeanCompletedCost), floatString(row.TotalJudgeCost), floatString(row.MeanJudgeCost), strconv.Itoa(row.TotalInputTokens), strconv.Itoa(row.TotalOutputTokens), floatString(row.MeanDurationMS)})
	}
	return writeCSV(path, header, rows)
}

func writePairwiseCSV(path string, comparisons []PairwiseRow) error {
	header := []string{"category", "baseline_arm", "candidate_arm", "accuracy_pairs", "accuracy_wins", "accuracy_losses", "accuracy_ties", "candidate_accuracy", "accuracy_delta", "lexical_pairs", "lexical_wins", "lexical_losses", "lexical_ties", "mean_token_f1", "mean_delta_f1", "cost_scope", "baseline_mean_cost", "candidate_mean_cost", "mean_delta_cost", "paired_completed_incremental_cost", "exact_lift", "safe_success_lift"}
	rows := make([][]string, 0, len(comparisons))
	for _, row := range comparisons {
		rows = append(rows, []string{row.Category, row.BaselineArm, row.CandidateArm, strconv.Itoa(row.AccuracyPairs), strconv.Itoa(row.AccuracyWins), strconv.Itoa(row.AccuracyLosses), strconv.Itoa(row.AccuracyTies), floatString(row.CandidateAccuracy), floatString(row.AccuracyDelta), strconv.Itoa(row.Pairs), strconv.Itoa(row.Wins), strconv.Itoa(row.Losses), strconv.Itoa(row.Ties), floatString(row.MeanTokenF1), floatString(row.MeanDeltaF1), row.CostScope, floatString(row.BaselineMeanCost), floatString(row.CandidateMeanCost), floatString(row.MeanDeltaCost), floatString(row.PairedCompletedIncrementalCost), strconv.Itoa(row.ExactLift), strconv.Itoa(row.SafeSuccessLift)})
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
