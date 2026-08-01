package demo

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	llmwikidriver "github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/artifact/llmwiki"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/benchmark/qa"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/benchmark/quality"
	"github.com/pax-beehive/pax-nexus/internal/llmwiki/sessiondataset"
	"github.com/pax-beehive/pax-nexus/internal/llmwiki/workspace"
)

type SessionDatasetConfig struct {
	Dataset              string
	Partition            string
	CaseID               string
	RunIDPrefix          string
	IngestPath           string
	QueryPath            string
	GoldPath             string
	QuestionLimit        int
	QuestionOffset       int
	OutputDirectory      string
	CodeRevision         string
	Maintainer           *llmwikidriver.AgentBuilderConfig
	MaintainerResumePath string
	ReuseArtifact        *knowledgeeval.ArtifactRecord
	ReuseArtifactPath    string
	Reader               qa.Reader
	Judge                qa.AnswerJudge
}

type SessionDatasetBundle struct {
	SchemaVersion       string                      `json:"schema_version"`
	GeneratedAt         time.Time                   `json:"generated_at"`
	Dataset             string                      `json:"dataset"`
	Partition           string                      `json:"partition"`
	CaseID              string                      `json:"case_id"`
	Mode                string                      `json:"mode"`
	BuildStatus         string                      `json:"build_status"`
	Blocker             string                      `json:"blocker"`
	Ingest              sessiondataset.Result       `json:"ingest"`
	Questions           int                         `json:"questions"`
	QuestionOffset      int                         `json:"question_offset,omitempty"`
	CumulativeQuestions int                         `json:"cumulative_questions,omitempty"`
	Artifact            ArtifactSummary             `json:"artifact"`
	Query               knowledgeeval.QuerySnapshot `json:"query"`
	Arms                []SessionDatasetArm         `json:"arms"`
	Comparison          []knowledgeeval.MetricDelta `json:"comparison,omitempty"`
	Failures            []FailureSummary            `json:"failures"`
}

type SessionDatasetArm struct {
	ID             string                   `json:"id"`
	Role           string                   `json:"role"`
	BuildStatus    string                   `json:"build_status"`
	Blocker        string                   `json:"blocker,omitempty"`
	RunID          string                   `json:"run_id,omitempty"`
	Artifact       *ArtifactSummary         `json:"artifact,omitempty"`
	FailurePayload *knowledgeeval.OpaqueRef `json:"failure_payload,omitempty"`
}

type FailureSummary struct {
	ArmID     string `json:"arm_id"`
	Passed    int    `json:"passed"`
	Artifact  int    `json:"artifact"`
	Retrieval int    `json:"retrieval"`
	Reader    int    `json:"reader"`
}

type datasetRecord struct {
	CaseID   string            `json:"case_id"`
	Sessions []json.RawMessage `json:"sessions"`
}

type queryRecord struct {
	CaseID       string `json:"case_id"`
	Question     string `json:"question"`
	SourceCaseID string `json:"source_case_id"`
}

type goldRecord struct {
	CaseID           string   `json:"case_id"`
	Answer           any      `json:"answer"`
	Category         any      `json:"category"`
	QuestionType     string   `json:"question_type"`
	EvalFunction     string   `json:"eval_function"`
	Abstention       bool     `json:"abstention"`
	EvidenceDialogID []string `json:"evidence_dialog_ids"`
	EvidenceTurnIDs  []string `json:"evidence_turn_ids"`
}

func GenerateSessionDataset(
	ctx context.Context,
	config SessionDatasetConfig,
) (_ SessionDatasetBundle, returnedErr error) {
	if err := validateSessionDatasetConfig(config); err != nil {
		return SessionDatasetBundle{}, err
	}
	sessionCount, err := datasetSessionCount(config.IngestPath, config.CaseID)
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	if err := os.MkdirAll(config.OutputDirectory, 0o755); err != nil {
		return SessionDatasetBundle{}, fmt.Errorf("create dataset output directory: %w", err)
	}
	workspaceRoot, err := os.MkdirTemp("", "knowledge-eval-dataset-")
	if err != nil {
		return SessionDatasetBundle{}, fmt.Errorf("create dataset workspace: %w", err)
	}
	defer func() {
		returnedErr = errors.Join(returnedErr, cleanupDatasetWorkspace(workspaceRoot))
	}()
	ingest, err := sessiondataset.Build(ctx, sessiondataset.Config{
		IngestPath: config.IngestPath, Root: workspaceRoot,
		CaseID: config.CaseID, Start: 0, End: sessionCount,
	})
	if err != nil {
		return SessionDatasetBundle{}, fmt.Errorf("build session dataset Sources: %w", err)
	}
	cases, err := loadQACases(
		config.QueryPath,
		config.GoldPath,
		config.CaseID,
		config.QuestionOffset,
		config.QuestionLimit,
	)
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	clock := &fixedClock{current: time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC)}
	store, err := knowledgeeval.NewArtifactStore(filepath.Join(config.OutputDirectory, "artifacts"))
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	if config.Maintainer != nil && config.MaintainerResumePath != "" {
		resume, putErr := store.PutDirectory(
			ctx,
			"llmwiki-maintainer-failure",
			"pax.knowledge-eval.llmwiki-maintainer-failure.v1",
			config.MaintainerResumePath,
		)
		if putErr != nil {
			return SessionDatasetBundle{}, fmt.Errorf(
				"import failed maintainer workspace: %w",
				putErr,
			)
		}
		config.Maintainer.Resume = &resume
	}
	input, err := store.PutDirectory(ctx, "benchmark-build-input", "v1", workspaceRoot)
	if err != nil {
		return SessionDatasetBundle{}, fmt.Errorf("store dataset workspace: %w", err)
	}
	hidden, err := store.PutBytes(ctx, "benchmark-eval-input", "v1", []byte("{}"))
	if err != nil {
		return SessionDatasetBundle{}, fmt.Errorf("store dataset evaluation input: %w", err)
	}
	group := knowledgeeval.BenchmarkGroup{
		GroupID:      config.Dataset + "-" + config.CaseID,
		WorldID:      config.Dataset,
		CheckpointID: config.Partition + "-" + config.CaseID,
		BuildInput:   input, EvaluationInput: hidden,
	}
	if config.ReuseArtifact != nil {
		return evaluateReusedSessionArtifact(
			ctx,
			config,
			store,
			clock.Now,
			ingest,
			cases,
		)
	}
	return buildAndEvaluateSessionDataset(
		ctx,
		config,
		store,
		clock.Now,
		ingest,
		cases,
		group,
	)
}

func buildAndEvaluateSessionDataset(
	ctx context.Context,
	config SessionDatasetConfig,
	store *knowledgeeval.ArtifactStore,
	now func() time.Time,
	ingest sessiondataset.Result,
	cases []qa.Case,
	group knowledgeeval.BenchmarkGroup,
) (SessionDatasetBundle, error) {
	builder, err := llmwikidriver.NewDirectoryBuilder(store, now, "source-only-baseline")
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	artifact, err := builder.Build(ctx, knowledgeeval.BuildRequest{
		Group: group,
	})
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	driver, err := llmwikidriver.NewDriver(store, now)
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	runStore := knowledgeeval.NewMemoryRunStore(now)
	runner, err := knowledgeeval.NewRunner(runStore, now)
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	baselineArm, baselineDetail, err := evaluateDatasetArm(
		ctx,
		config,
		store,
		driver,
		runner,
		now,
		cases,
		"source-only",
		"baseline",
		artifact,
	)
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	arms := []SessionDatasetArm{baselineArm}
	details := map[string]knowledgeeval.RunDetail{"source-only": baselineDetail}
	buildStatus := "baseline_only"
	blocker := "Maintainer arm was not requested."
	mode := "source-only-baseline"
	var comparison []knowledgeeval.MetricDelta
	if config.Maintainer != nil {
		mode = "builder-comparison"
		maintainedArm, maintainedDetail, err := buildAndEvaluateMaintainerArm(
			ctx,
			config,
			store,
			now,
			group,
			artifact,
			driver,
			runner,
			cases,
		)
		if err != nil {
			return SessionDatasetBundle{}, err
		}
		arms = append(arms, maintainedArm)
		if maintainedDetail == nil {
			buildStatus = "partial"
			if maintainedArm.BuildStatus == "needs_more_rounds" {
				buildStatus = "needs_more_rounds"
			}
			blocker = maintainedArm.Blocker
		} else {
			buildStatus = "completed"
			blocker = ""
			details["maintained"] = *maintainedDetail
			comparison = knowledgeeval.CompareRuns(baselineDetail, *maintainedDetail)
		}
	}
	query, err := exportQuery(ctx, runStore, now)
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	bundle := SessionDatasetBundle{
		SchemaVersion: "pax.knowledge-eval.session-dataset-run.v1",
		GeneratedAt:   now(), Dataset: config.Dataset, Partition: config.Partition,
		CaseID: config.CaseID, Mode: mode,
		BuildStatus: buildStatus,
		Blocker:     blocker,
		Ingest:      ingest, Questions: len(cases),
		QuestionOffset:      config.QuestionOffset,
		CumulativeQuestions: config.QuestionOffset + len(cases),
		Artifact:            *baselineArm.Artifact,
		Query:               query, Arms: arms, Comparison: comparison,
		Failures: failureSummaries(details),
	}
	sanitizeDatasetBundleRefs(&bundle)
	if err := writeJSON(filepath.Join(config.OutputDirectory, "dataset-run.json"), bundle); err != nil {
		return SessionDatasetBundle{}, err
	}
	return bundle, nil
}

func evaluateReusedSessionArtifact(
	ctx context.Context,
	config SessionDatasetConfig,
	store *knowledgeeval.ArtifactStore,
	now func() time.Time,
	ingest sessiondataset.Result,
	cases []qa.Case,
) (SessionDatasetBundle, error) {
	if strings.TrimSpace(config.ReuseArtifactPath) == "" {
		return SessionDatasetBundle{}, fmt.Errorf(
			"%w: reused artifact path is required",
			knowledgeeval.ErrInvalidRecord,
		)
	}
	artifact := *config.ReuseArtifact
	payload, err := store.PutDirectory(
		ctx,
		artifact.Payload.Kind,
		artifact.Payload.SchemaVersion,
		config.ReuseArtifactPath,
	)
	if err != nil {
		return SessionDatasetBundle{}, fmt.Errorf("import reused artifact: %w", err)
	}
	if payload.SHA256 != artifact.Payload.SHA256 {
		return SessionDatasetBundle{}, fmt.Errorf(
			"%w: reused artifact digest changed",
			knowledgeeval.ErrInvalidRecord,
		)
	}
	artifact.Payload = payload
	if err := artifact.Validate(); err != nil {
		return SessionDatasetBundle{}, fmt.Errorf("validate reused artifact: %w", err)
	}
	driver, err := llmwikidriver.NewDriver(store, now)
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	subject, err := driver.Open(ctx, artifact)
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	views, err := renderDatasetViews(
		ctx,
		config.OutputDirectory,
		store,
		driver,
		artifact,
		"maintained",
	)
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	qaBenchmark, err := qa.NewAdapter(store, qa.Config{
		Cases: cases, Reader: config.Reader, Judge: config.Judge,
	})
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	runStore := knowledgeeval.NewMemoryRunStore(now)
	runner, err := knowledgeeval.NewRunner(runStore, now)
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	runIDParts := []string{"run"}
	if prefix := strings.TrimSpace(config.RunIDPrefix); prefix != "" {
		runIDParts = append(runIDParts, prefix)
	}
	runIDParts = append(runIDParts, config.Dataset, config.CaseID, "maintained")
	runID := strings.Join(runIDParts, "-")
	metadata := runMetadata(artifact)
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadata["reused_artifact"] = "true"
	metadata["question_offset"] = fmt.Sprint(config.QuestionOffset)
	metadata["question_count"] = fmt.Sprint(len(cases))
	detail, err := runner.Evaluate(ctx, knowledgeeval.Run{
		ID: runID, WorldID: artifact.WorldID, GroupID: artifact.GroupID,
		CheckpointID: artifact.CheckpointID, ArtifactID: artifact.ArtifactID,
		Metadata: metadata, CreatedAt: now(),
	}, subject, []knowledgeeval.BenchmarkAdapter{qaBenchmark})
	if err != nil {
		return SessionDatasetBundle{}, fmt.Errorf("evaluate reused artifact run %s: %w", runID, err)
	}
	query, err := exportQuery(ctx, runStore, now)
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	summary := ArtifactSummary{
		Product:  config.Dataset + " " + config.CaseID + " maintained",
		Artifact: artifact, Capabilities: subject.Capabilities(), Views: views,
	}
	arm := SessionDatasetArm{
		ID: "maintained", Role: "candidate", BuildStatus: "reused",
		RunID: runID, Artifact: &summary,
	}
	bundle := SessionDatasetBundle{
		SchemaVersion: "pax.knowledge-eval.session-dataset-run.v1",
		GeneratedAt:   now(), Dataset: config.Dataset, Partition: config.Partition,
		CaseID: config.CaseID, Mode: "artifact-evaluation", BuildStatus: "completed",
		Ingest: ingest, Questions: len(cases), QuestionOffset: config.QuestionOffset,
		CumulativeQuestions: config.QuestionOffset + len(cases),
		Query:               query, Arms: []SessionDatasetArm{arm},
		Failures: failureSummaries(map[string]knowledgeeval.RunDetail{"maintained": detail}),
	}
	sanitizeDatasetBundleRefs(&bundle)
	if err := writeJSON(filepath.Join(config.OutputDirectory, "dataset-run.json"), bundle); err != nil {
		return SessionDatasetBundle{}, err
	}
	return bundle, nil
}

func buildAndEvaluateMaintainerArm(
	ctx context.Context,
	config SessionDatasetConfig,
	store *knowledgeeval.ArtifactStore,
	now func() time.Time,
	group knowledgeeval.BenchmarkGroup,
	baseArtifact knowledgeeval.ArtifactRecord,
	driver *llmwikidriver.Driver,
	runner *knowledgeeval.Runner,
	cases []qa.Case,
) (SessionDatasetArm, *knowledgeeval.RunDetail, error) {
	maintainerBuilder, err := llmwikidriver.NewAgentBuilder(
		store,
		now,
		config.CodeRevision,
		*config.Maintainer,
	)
	if err != nil {
		return SessionDatasetArm{}, nil, err
	}
	maintainedArtifact, buildErr := maintainerBuilder.Build(
		ctx,
		knowledgeeval.BuildRequest{Group: group, BaseArtifact: &baseArtifact},
	)
	if buildErr != nil {
		buildStatus := "failed"
		var roundLimitErr *workspace.RoundLimitError
		if errors.As(buildErr, &roundLimitErr) {
			buildStatus = "needs_more_rounds"
		}
		failedArm := SessionDatasetArm{
			ID: "maintained", Role: "candidate", BuildStatus: buildStatus,
			Blocker: buildErr.Error(),
		}
		var agentErr *llmwikidriver.AgentBuildError
		if errors.As(buildErr, &agentErr) && agentErr.FailurePayload.URI != "" {
			failedArm.FailurePayload = &agentErr.FailurePayload
		}
		return failedArm, nil, nil
	}
	maintainedArm, maintainedDetail, err := evaluateDatasetArm(
		ctx,
		config,
		store,
		driver,
		runner,
		now,
		cases,
		"maintained",
		"candidate",
		maintainedArtifact,
	)
	if err != nil {
		return SessionDatasetArm{}, nil, err
	}
	return maintainedArm, &maintainedDetail, nil
}

func sessionDatasetBenchmarks(
	store *knowledgeeval.ArtifactStore,
	cases []qa.Case,
	reader qa.Reader,
	judge qa.AnswerJudge,
) ([]knowledgeeval.BenchmarkAdapter, error) {
	qualityBenchmark, err := quality.NewAdapter(store, quality.Config{MinimumScore: 0.8})
	if err != nil {
		return nil, err
	}
	qaBenchmark, err := qa.NewAdapter(store, qa.Config{
		Cases: cases, Reader: reader, Judge: judge,
	})
	if err != nil {
		return nil, err
	}
	return []knowledgeeval.BenchmarkAdapter{qualityBenchmark, qaBenchmark}, nil
}

func evaluateDatasetArm(
	ctx context.Context,
	config SessionDatasetConfig,
	store *knowledgeeval.ArtifactStore,
	driver *llmwikidriver.Driver,
	runner *knowledgeeval.Runner,
	now func() time.Time,
	cases []qa.Case,
	armID string,
	role string,
	artifact knowledgeeval.ArtifactRecord,
) (SessionDatasetArm, knowledgeeval.RunDetail, error) {
	subject, err := driver.Open(ctx, artifact)
	if err != nil {
		return SessionDatasetArm{}, knowledgeeval.RunDetail{}, err
	}
	views, err := renderDatasetViews(
		ctx,
		config.OutputDirectory,
		store,
		driver,
		artifact,
		armID,
	)
	if err != nil {
		return SessionDatasetArm{}, knowledgeeval.RunDetail{}, err
	}
	benchmarks, err := sessionDatasetBenchmarks(store, cases, config.Reader, config.Judge)
	if err != nil {
		return SessionDatasetArm{}, knowledgeeval.RunDetail{}, err
	}
	runIDParts := []string{"run"}
	if prefix := strings.TrimSpace(config.RunIDPrefix); prefix != "" {
		runIDParts = append(runIDParts, prefix)
	}
	runIDParts = append(runIDParts, config.Dataset, config.CaseID, armID)
	runID := strings.Join(runIDParts, "-")
	detail, err := evaluate(
		ctx,
		runner,
		now,
		runID,
		artifact,
		subject,
		benchmarks,
	)
	if err != nil {
		return SessionDatasetArm{}, knowledgeeval.RunDetail{}, err
	}
	summary := ArtifactSummary{
		Product:  config.Dataset + " " + config.CaseID + " " + armID,
		Artifact: artifact, Capabilities: subject.Capabilities(), Views: views,
	}
	return SessionDatasetArm{
		ID: armID, Role: role, BuildStatus: "completed",
		RunID: runID, Artifact: &summary,
	}, detail, nil
}

func failureSummaries(details map[string]knowledgeeval.RunDetail) []FailureSummary {
	armIDs := make([]string, 0, len(details))
	for armID := range details {
		armIDs = append(armIDs, armID)
	}
	slices.Sort(armIDs)
	result := make([]FailureSummary, 0, len(armIDs))
	for _, armID := range armIDs {
		summary := FailureSummary{ArmID: armID}
		for _, trial := range details[armID].Trials {
			if trial.BenchmarkID != qa.BenchmarkID || trial.Result == nil {
				continue
			}
			for _, testCase := range trial.Result.CaseResults {
				switch testCase.FailureStage {
				case "":
					summary.Passed++
				case "artifact":
					summary.Artifact++
				case "retrieval":
					summary.Retrieval++
				case "reader":
					summary.Reader++
				}
			}
		}
		result = append(result, summary)
	}
	return result
}

func cleanupDatasetWorkspace(root string) error {
	writableErr := makeDirectoriesWritable(root)
	removeErr := os.RemoveAll(root)
	if writableErr != nil || removeErr != nil {
		return fmt.Errorf("clean dataset workspace: %w", errors.Join(writableErr, removeErr))
	}
	return nil
}

func validateSessionDatasetConfig(config SessionDatasetConfig) error {
	for name, value := range map[string]string{
		"dataset": config.Dataset, "partition": config.Partition, "case ID": config.CaseID,
		"ingest path": config.IngestPath, "query path": config.QueryPath,
		"gold path": config.GoldPath, "output directory": config.OutputDirectory,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: %s is required", knowledgeeval.ErrInvalidRecord, name)
		}
	}
	if config.QuestionLimit <= 0 {
		return fmt.Errorf("%w: question limit must be positive", knowledgeeval.ErrInvalidRecord)
	}
	if config.QuestionOffset < 0 {
		return fmt.Errorf("%w: question offset cannot be negative", knowledgeeval.ErrInvalidRecord)
	}
	if config.ReuseArtifact != nil && strings.TrimSpace(config.ReuseArtifactPath) == "" {
		return fmt.Errorf("%w: reused artifact path is required", knowledgeeval.ErrInvalidRecord)
	}
	return nil
}

func datasetSessionCount(path, caseID string) (int, error) {
	var selected datasetRecord
	if err := scanJSONL(path, func(encoded []byte) error {
		var record datasetRecord
		if err := json.Unmarshal(encoded, &record); err != nil {
			return err
		}
		if record.CaseID == caseID {
			selected = record
		}
		return nil
	}); err != nil {
		return 0, fmt.Errorf("read dataset ingest: %w", err)
	}
	if len(selected.Sessions) == 0 {
		return 0, fmt.Errorf("%w: dataset case %s", knowledgeeval.ErrNotFound, caseID)
	}
	return len(selected.Sessions), nil
}

func loadQACases(
	queryPath,
	goldPath,
	sourceCaseID string,
	offset,
	limit int,
) ([]qa.Case, error) {
	gold := make(map[string]goldRecord)
	if err := scanJSONL(goldPath, func(encoded []byte) error {
		var record goldRecord
		if err := json.Unmarshal(encoded, &record); err != nil {
			return err
		}
		record.EvidenceDialogID = slices.Clone(record.EvidenceDialogID)
		record.EvidenceTurnIDs = slices.Clone(record.EvidenceTurnIDs)
		gold[record.CaseID] = record
		return nil
	}); err != nil {
		return nil, fmt.Errorf("read dataset gold: %w", err)
	}
	var cases []qa.Case
	matched := 0
	if err := scanJSONL(queryPath, func(encoded []byte) error {
		if len(cases) == limit {
			return nil
		}
		var record queryRecord
		if err := json.Unmarshal(encoded, &record); err != nil {
			return err
		}
		recordSourceCaseID := record.SourceCaseID
		if recordSourceCaseID == "" {
			recordSourceCaseID = record.CaseID
		}
		if recordSourceCaseID != sourceCaseID {
			return nil
		}
		if matched < offset {
			matched++
			return nil
		}
		matched++
		answer, exists := gold[record.CaseID]
		if !exists {
			return fmt.Errorf("query %s has no gold answer", record.CaseID)
		}
		cases = append(cases, qa.Case{
			ID: record.CaseID, Question: record.Question,
			Expected:        formatGoldAnswer(answer.Answer),
			AnswerKind:      answerKindForGold(answer),
			DatasetCategory: formatDatasetCategory(answer.Category),
			SupportRefs:     supportRefsForGold(answer),
		})
		return nil
	}); err != nil {
		return nil, fmt.Errorf("read dataset queries: %w", err)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("%w: no questions for %s", knowledgeeval.ErrNotFound, sourceCaseID)
	}
	return cases, nil
}

func scanJSONL(path string, visit func([]byte) error) (returnedErr error) {
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		if err := input.Close(); err != nil {
			returnedErr = errors.Join(returnedErr, err)
		}
	}()
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		if err := visit(scanner.Bytes()); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func renderDatasetViews(
	ctx context.Context,
	outputDirectory string,
	store *knowledgeeval.ArtifactStore,
	driver *llmwikidriver.Driver,
	artifact knowledgeeval.ArtifactRecord,
	prefix string,
) (map[string]string, error) {
	if err := os.MkdirAll(filepath.Join(outputDirectory, "views"), 0o755); err != nil {
		return nil, fmt.Errorf("create dataset views directory: %w", err)
	}
	views := make(map[string]string)
	for _, kind := range []string{"native", "canonical", "raw"} {
		view, err := driver.RenderView(ctx, knowledgeeval.ArtifactViewRequest{
			Artifact: artifact, Kind: kind,
		})
		if err != nil {
			return nil, err
		}
		name := prefix + "-" + kind + ".html"
		if err := copyView(ctx, store, view, filepath.Join(outputDirectory, "views", name)); err != nil {
			return nil, err
		}
		views[kind] = "views/" + name
	}
	return views, nil
}

func sanitizeDatasetBundleRefs(bundle *SessionDatasetBundle) {
	sanitizeRef(&bundle.Artifact.Artifact.Payload)
	for index := range bundle.Arms {
		if bundle.Arms[index].Artifact != nil {
			sanitizeRef(&bundle.Arms[index].Artifact.Artifact.Payload)
		}
		if bundle.Arms[index].FailurePayload != nil {
			sanitizeRef(bundle.Arms[index].FailurePayload)
		}
	}
	for runIndex := range bundle.Query.Runs {
		for trialIndex := range bundle.Query.Runs[runIndex].Trials {
			result := bundle.Query.Runs[runIndex].Trials[trialIndex].Result
			if result == nil {
				continue
			}
			sanitizeRef(&result.Observations)
			sanitizeRef(&result.RawReport)
		}
	}
}

func formatGoldAnswer(value any) string {
	switch typed := value.(type) {
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprint(value)
	}
}

func formatDatasetCategory(value any) string {
	if value == nil {
		return ""
	}
	if numeric, ok := value.(float64); ok && numeric == float64(int64(numeric)) {
		return fmt.Sprint(int64(numeric))
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func answerKindForGold(gold goldRecord) qa.AnswerKind {
	classifier := strings.ToLower(strings.Join([]string{
		gold.QuestionType,
		gold.EvalFunction,
	}, " "))
	if gold.Abstention || strings.Contains(classifier, "unanswerable") ||
		strings.Contains(classifier, "abstention") {
		return qa.AnswerUnanswerable
	}
	if _, isList := gold.Answer.([]any); isList {
		return qa.AnswerList
	}
	if strings.Contains(classifier, "temporal") || strings.Contains(classifier, "date") ||
		strings.Contains(classifier, "time") {
		return qa.AnswerTemporal
	}
	if strings.Contains(classifier, "numeric") || strings.Contains(classifier, "number") ||
		strings.Contains(classifier, "count") || strings.Contains(classifier, "arithmetic") {
		return qa.AnswerNumeric
	}
	return qa.AnswerFact
}

func supportRefsForGold(gold goldRecord) []string {
	if len(gold.EvidenceDialogID) > 0 {
		return slices.Clone(gold.EvidenceDialogID)
	}
	return slices.Clone(gold.EvidenceTurnIDs)
}
