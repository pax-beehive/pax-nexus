package demo

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	llmwikidriver "github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/artifact/llmwiki"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/benchmark/qa"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/benchmark/quality"
	"github.com/pax-beehive/pax-nexus/internal/llmwiki/sessiondataset"
)

type SessionDatasetConfig struct {
	Dataset         string
	Partition       string
	CaseID          string
	IngestPath      string
	QueryPath       string
	GoldPath        string
	QuestionLimit   int
	OutputDirectory string
}

type SessionDatasetBundle struct {
	SchemaVersion string                      `json:"schema_version"`
	GeneratedAt   time.Time                   `json:"generated_at"`
	Dataset       string                      `json:"dataset"`
	Partition     string                      `json:"partition"`
	CaseID        string                      `json:"case_id"`
	Mode          string                      `json:"mode"`
	BuildStatus   string                      `json:"build_status"`
	Blocker       string                      `json:"blocker"`
	Ingest        sessiondataset.Result       `json:"ingest"`
	Questions     int                         `json:"questions"`
	Artifact      ArtifactSummary             `json:"artifact"`
	Query         knowledgeeval.QuerySnapshot `json:"query"`
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
	CaseID string `json:"case_id"`
	Answer any    `json:"answer"`
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
	cases, err := loadQACases(config.QueryPath, config.GoldPath, config.CaseID, config.QuestionLimit)
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	clock := &fixedClock{current: time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC)}
	store, err := knowledgeeval.NewArtifactStore(filepath.Join(config.OutputDirectory, "artifacts"))
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	input, err := store.PutDirectory(ctx, "benchmark-build-input", "v1", workspaceRoot)
	if err != nil {
		return SessionDatasetBundle{}, fmt.Errorf("store dataset workspace: %w", err)
	}
	hidden, err := store.PutBytes(ctx, "benchmark-eval-input", "v1", []byte("{}"))
	if err != nil {
		return SessionDatasetBundle{}, fmt.Errorf("store dataset evaluation input: %w", err)
	}
	builder, err := llmwikidriver.NewDirectoryBuilder(store, clock.Now, "source-only-baseline")
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	artifact, err := builder.Build(ctx, knowledgeeval.BuildRequest{
		Group: knowledgeeval.BenchmarkGroup{
			GroupID: config.Dataset + "-" + config.CaseID,
			WorldID: config.Dataset, CheckpointID: "source-only",
			BuildInput: input, EvaluationInput: hidden,
		},
	})
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	driver, err := llmwikidriver.NewDriver(store, clock.Now)
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	subject, err := driver.Open(ctx, artifact)
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	views, err := renderDatasetViews(ctx, config.OutputDirectory, store, driver, artifact)
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	benchmarks, err := sessionDatasetBenchmarks(store, cases)
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	runStore := knowledgeeval.NewMemoryRunStore(clock.Now)
	runner, err := knowledgeeval.NewRunner(runStore, clock.Now)
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	if _, err := evaluate(
		ctx, runner, clock.Now, "run-"+config.Dataset+"-"+config.CaseID+"-source-only",
		artifact, subject, benchmarks,
	); err != nil {
		return SessionDatasetBundle{}, err
	}
	query, err := exportQuery(ctx, runStore, clock.Now)
	if err != nil {
		return SessionDatasetBundle{}, err
	}
	bundle := SessionDatasetBundle{
		SchemaVersion: "pax.knowledge-eval.session-dataset-run.v1",
		GeneratedAt:   clock.Now(), Dataset: config.Dataset, Partition: config.Partition,
		CaseID: config.CaseID, Mode: "source-only-baseline",
		BuildStatus: "blocked",
		Blocker:     "Wiki maintainer was not run because no model API credential is available.",
		Ingest:      ingest, Questions: len(cases),
		Artifact: ArtifactSummary{
			Product:  config.Dataset + " " + config.CaseID,
			Artifact: artifact, Capabilities: subject.Capabilities(), Views: views,
		},
		Query: query,
	}
	sanitizeDatasetBundleRefs(&bundle)
	if err := writeJSON(filepath.Join(config.OutputDirectory, "dataset-run.json"), bundle); err != nil {
		return SessionDatasetBundle{}, err
	}
	return bundle, nil
}

func sessionDatasetBenchmarks(
	store *knowledgeeval.ArtifactStore,
	cases []qa.Case,
) ([]knowledgeeval.BenchmarkAdapter, error) {
	qualityBenchmark, err := quality.NewAdapter(store, quality.Config{MinimumScore: 0.8})
	if err != nil {
		return nil, err
	}
	qaBenchmark, err := qa.NewAdapter(store, qa.Config{Cases: cases})
	if err != nil {
		return nil, err
	}
	return []knowledgeeval.BenchmarkAdapter{qualityBenchmark, qaBenchmark}, nil
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

func loadQACases(queryPath, goldPath, sourceCaseID string, limit int) ([]qa.Case, error) {
	gold := make(map[string]string)
	if err := scanJSONL(goldPath, func(encoded []byte) error {
		var record goldRecord
		if err := json.Unmarshal(encoded, &record); err != nil {
			return err
		}
		gold[record.CaseID] = fmt.Sprint(record.Answer)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("read dataset gold: %w", err)
	}
	var cases []qa.Case
	if err := scanJSONL(queryPath, func(encoded []byte) error {
		if len(cases) == limit {
			return nil
		}
		var record queryRecord
		if err := json.Unmarshal(encoded, &record); err != nil {
			return err
		}
		if record.SourceCaseID != sourceCaseID {
			return nil
		}
		answer, exists := gold[record.CaseID]
		if !exists {
			return fmt.Errorf("query %s has no gold answer", record.CaseID)
		}
		cases = append(cases, qa.Case{
			ID: record.CaseID, Question: record.Question, Expected: answer,
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
		name := "dataset-" + kind + ".html"
		if err := copyView(ctx, store, view, filepath.Join(outputDirectory, "views", name)); err != nil {
			return nil, err
		}
		views[kind] = "views/" + name
	}
	return views, nil
}

func sanitizeDatasetBundleRefs(bundle *SessionDatasetBundle) {
	sanitizeRef(&bundle.Artifact.Artifact.Payload)
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
