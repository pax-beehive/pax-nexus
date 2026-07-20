package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/pax-beehive/pax-nexus/internal/eval/recallreplay"
	"github.com/pax-beehive/pax-nexus/internal/eval/recallv2"
	v2 "github.com/pax-beehive/pax-nexus/internal/eval/v2"
	"github.com/pax-beehive/pax-nexus/internal/eval/v2/postgresstore"
	"github.com/pax-beehive/pax-nexus/internal/eval/v2/render"
	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
)

func main() {
	logger := observability.NewLogger(os.Stderr)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], logger); err != nil {
		logger.ErrorContext(ctx, "recall eval v2 failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, logger *slog.Logger) error {
	flags := flag.NewFlagSet("team-memory-recall-eval-v2", flag.ContinueOnError)
	configPath := flags.String("config", "evals/recall-v2/config.local.yaml", "Recall Eval v2 YAML configuration")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse recall eval v2 flags: %w", err)
	}
	config, err := recallv2.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := runReplayGate(config); err != nil {
		return err
	}
	loadCases := recallv2.LoadCases
	if config.Recall.Diagnostic {
		loadCases = recallv2.LoadDiagnosticCases
	}
	cases, revision, cohort, err := loadCases(
		config.Run.Manifest, config.Recall.CaseAnnotations, config.AnswererSeed,
		config.Recall.MinAgentCases, config.Recall.MaxAgentCases,
	)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(config.Run.OutputDir, "cohort.json"), cohort); err != nil {
		return err
	}
	dsn := strings.TrimSpace(os.Getenv(config.Store.DSNEnv))
	if dsn == "" {
		return fmt.Errorf("load recall eval v2 store: environment variable %s is required", config.Store.DSNEnv)
	}
	store, err := postgresstore.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return err
	}
	runner, err := v2.NewRunner(store, v2.ProcessExecutor{}, logger)
	if err != nil {
		return err
	}
	runRecord, results, runErr := runner.Run(ctx, config.Config, cases, revision)
	if runErr != nil && len(results) == 0 {
		return runErr
	}
	results = v2.RescoreResults(results)
	report := recallv2.Audit(results, len(cases))
	auditErr := writeJSON(filepath.Join(config.Run.OutputDir, "recall-agent-report.json"), report)
	exportErr := v2.ExportArtifacts(config.Run.OutputDir, runRecord, config.BaselineArm, config.OutputFormats(), results, func(writer io.Writer) error {
		return render.Report(runRecord, config.BaselineArm, results, writer)
	})
	if report.UnscoredTrials > 0 {
		auditErr = errors.Join(auditErr, fmt.Errorf("audit recall eval v2: %d trials lack mandatory Agent recall evidence", report.UnscoredTrials))
	}
	if len(report.Regressions) > 0 {
		auditErr = errors.Join(auditErr, fmt.Errorf("audit recall eval v2: candidate regressed in slices %s", strings.Join(report.Regressions, ", ")))
	}
	return errors.Join(runErr, exportErr, auditErr)
}

func runReplayGate(config recallv2.Config) error {
	if err := recallv2.ValidateReplay(config.Recall.DeterministicReplay, config.Recall.MinReplayCases); err != nil {
		return err
	}
	fixtures, err := recallreplay.LoadFixtureSet(config.Recall.DeterministicReplay)
	if err != nil {
		return err
	}
	report, err := recallreplay.Run(fixtures, fixtures.Policy)
	if err != nil {
		return err
	}
	if err := recallv2.ValidateReplayReport(report); err != nil {
		return err
	}
	directory := filepath.Join(config.Run.OutputDir, "deterministic-replay")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create recall eval v2 replay output: %w", err)
	}
	if err := writeJSON(filepath.Join(directory, "summary.json"), report); err != nil {
		return err
	}
	hintFixtures, err := recallreplay.LoadFixtureSet(config.Recall.HintReplay)
	if err != nil {
		return err
	}
	hintReport, err := recallreplay.Run(hintFixtures, hintFixtures.Policy)
	if err != nil {
		return err
	}
	if err := recallv2.ValidateHintReplayReport(hintReport); err != nil {
		return err
	}
	return writeJSON(filepath.Join(directory, "hint-summary.json"), hintReport)
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create recall eval v2 artifact directory: %w", err)
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode recall eval v2 artifact: %w", err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("write recall eval v2 artifact %q: %w", path, err)
	}
	return nil
}
