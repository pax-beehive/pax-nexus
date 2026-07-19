package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/recallreplay"
	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/pax-beehive/pax-nexus/internal/platform/textembedding"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		if _, writeErr := fmt.Fprintf(os.Stderr, "recall replay failed: %v\n", err); writeErr != nil {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("team-memory-recall-replay", flag.ContinueOnError)
	exportMode := flags.Bool("export", false, "Export a replay fixture from a persisted store instead of replaying")
	replayFixturePath := flags.String("fixtures", "", "Recall replay fixture JSON for replay mode")
	stageFixturePath := flags.String("stage-fixtures", "", "Stage fixture set JSON for export mode")
	dsn := flags.String("dsn", "", "PostgreSQL DSN for export mode")
	runID := flags.String("run-id", "", "Eval run identifier used to derive case scopes for export mode")
	scopeID := flags.String("scope-id", "", "Shared Team Note scope for every exported case")
	arm := flags.String("arm", "team_note", "Eval arm for export mode (team_note or team_note_hybrid)")
	embeddingBaseURL := flags.String("embedding-base-url", "", "OpenAI-compatible embedding endpoint for export mode")
	embeddingModel := flags.String("embedding-model", "Qwen/Qwen3-Embedding-0.6B", "Embedding model name for export mode")
	semanticThreshold := flags.Float64("semantic-threshold", 0.65, "Semantic lane threshold")
	candidateLimit := flags.Int("candidate-limit", 16, "Fusion candidate lane limit")
	dedup := flags.Bool("dedup", false, "Suppress near-duplicate facts during selection")
	degradeRelated := flags.Bool("degrade-related", false, "Fall back to the note without its related block when over budget")
	outputPath := flags.String("output", "", "Replay fixture output path for export mode")
	outputDirectory := flags.String("output-dir", "", "Output directory for replay mode")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse recall replay flags: %w", err)
	}
	if *exportMode {
		return runExport(*stageFixturePath, *dsn, *runID, *scopeID, *arm, *embeddingBaseURL, *embeddingModel,
			*semanticThreshold, *candidateLimit, *outputPath, stdout)
	}
	return runReplay(*replayFixturePath, *semanticThreshold, *candidateLimit, *dedup, *degradeRelated, *outputDirectory, stdout)
}

func runReplay(fixturePath string, threshold float64, limit int, dedup, degradeRelated bool, outputDirectory string, stdout io.Writer) error {
	if fixturePath == "" || outputDirectory == "" {
		return fmt.Errorf("parse recall replay flags: fixtures and output-dir are required")
	}
	set, err := recallreplay.LoadFixtureSet(fixturePath)
	if err != nil {
		return err
	}
	report, err := recallreplay.Run(set, recallreplay.Policy{
		SemanticThreshold: threshold, CandidateLimit: limit,
		SuppressDuplicates: dedup, DegradeRelated: degradeRelated,
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return fmt.Errorf("create recall replay output directory: %w", err)
	}
	if err := writeFile(filepath.Join(outputDirectory, "replay-results.jsonl"), func(writer io.Writer) error {
		return recallreplay.WriteResultsJSONL(writer, report)
	}); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(outputDirectory, "recall-loss-ledger.jsonl"), func(writer io.Writer) error {
		return recallreplay.WriteLossLedgerJSONL(writer, report)
	}); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(outputDirectory, "replay-summary.json"), func(writer io.Writer) error {
		return recallreplay.WriteSummaryJSON(writer, report)
	}); err != nil {
		return err
	}
	summary := report.Summary
	_, err = fmt.Fprintf(stdout,
		"recall eval v1: %d cases, gold recall %.3f, conditional recall %.3f, candidate recall@limit %.3f, relation recall %.3f, missed available %d, planner p95 %dns, stage totals %+v\n",
		summary.Cases, summary.RecallGoldRecall, summary.RecallConditionalRecall,
		report.RecallEval.CandidateRecallAtLimit, report.RecallEval.RelationExpandedRecall,
		summary.RecallMissedAvailableAtoms, report.RecallEval.PlannerP95DurationNS, report.StageTotals)
	if err != nil {
		return fmt.Errorf("write recall replay summary: %w", err)
	}
	return nil
}

func runExport(
	stageFixturePath, dsn, runID, scopeID, arm, embeddingBaseURL, embeddingModel string,
	threshold float64, limit int, outputPath string, stdout io.Writer,
) error {
	if stageFixturePath == "" || dsn == "" || runID == "" || outputPath == "" {
		return fmt.Errorf("parse recall replay export flags: stage-fixtures, dsn, run-id, and output are required")
	}
	scopeSuffix, err := armScopeSuffix(arm)
	if err != nil {
		return err
	}
	fixtures, err := stageeval.LoadFixtureSet(stageFixturePath)
	if err != nil {
		return err
	}
	ctx := context.Background()
	store, err := postgres.Open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open recall replay export store: %w", err)
	}
	defer store.Close()
	embedder, err := openEmbedder(embeddingBaseURL, embeddingModel)
	if err != nil {
		return err
	}
	exporter, err := recallreplay.NewExporter(store, embedder, embeddingModel, recallreplay.Policy{
		SemanticThreshold: threshold, CandidateLimit: limit,
	})
	if err != nil {
		return err
	}
	resolveScope, scopePrefix := exportScopeResolver(runID, scopeID, scopeSuffix)
	provenance := recallreplay.Provenance{
		RunID: runID, Arm: arm, ScopePrefix: scopePrefix, EmbeddingModel: embeddingModel,
	}
	set, err := exporter.Export(ctx, fixtures, provenance, resolveScope)
	if err != nil {
		return err
	}
	if err := recallreplay.WriteFixtureSet(outputPath, set); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "recall replay export: %d cases written to %s\n", len(set.Cases), outputPath)
	if err != nil {
		return fmt.Errorf("write recall replay export summary: %w", err)
	}
	return nil
}

func exportScopeResolver(runID, sharedScope, scopeSuffix string) (func(string) string, string) {
	if strings.TrimSpace(sharedScope) != "" {
		return func(string) string { return sharedScope }, sharedScope
	}
	prefix := runID + "-groupmembench-"
	return func(caseID string) string {
		return fmt.Sprintf("%s%s%s", prefix, caseID, scopeSuffix)
	}, prefix
}

func armScopeSuffix(arm string) (string, error) {
	switch arm {
	case "team_note":
		return "", nil
	case "team_note_hybrid":
		return "-team-note-hybrid", nil
	default:
		return "", fmt.Errorf("parse recall replay export flags: unsupported arm %q", arm)
	}
}

func openEmbedder(baseURL, model string) (textembedding.Embedder, error) {
	if baseURL == "" {
		return nil, nil
	}
	embedder, err := textembedding.NewOpenAI(textembedding.OpenAIConfig{
		BaseURL: baseURL, Model: model, Dimensions: postgres.EmbeddingDimensions,
		Client: &http.Client{Timeout: 60 * time.Second},
	})
	if err != nil {
		return nil, fmt.Errorf("create recall replay embedder: %w", err)
	}
	return embedder, nil
}

func writeFile(path string, write func(io.Writer) error) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create recall replay artifact %q: %w", path, err)
	}
	writeErr := write(file)
	closeErr := file.Close()
	if writeErr != nil {
		writeErr = fmt.Errorf("write recall replay artifact %q: %w", path, writeErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close recall replay artifact %q: %w", path, closeErr)
	}
	return errors.Join(writeErr, closeErr)
}
