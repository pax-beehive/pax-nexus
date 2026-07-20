package main

import (
	"context"
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

	v2 "github.com/pax-beehive/pax-nexus/internal/eval/v2"
	"github.com/pax-beehive/pax-nexus/internal/eval/v2/postgresstore"
	"github.com/pax-beehive/pax-nexus/internal/eval/v2/render"
	v3 "github.com/pax-beehive/pax-nexus/internal/eval/v3"
	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
)

func main() {
	logger := observability.NewLogger(os.Stderr)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], logger); err != nil {
		logger.ErrorContext(ctx, "eval v3 failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, logger *slog.Logger) error {
	flags := flag.NewFlagSet("team-memory-eval-v3", flag.ContinueOnError)
	configPath := flags.String("config", "evals/v3/config.yaml", "Eval v3 YAML configuration")
	runID := flags.String("run-id", "", "Override the configured run ID")
	manifestPath := flags.String("manifest", "", "Override the configured manifest path")
	outputDirectory := flags.String("output-dir", "", "Override the configured output directory")
	judgeInput := flags.String("judge-input", "", "Judge completed answers from an existing trials.jsonl")
	resolvedConfigOutput := flags.String("resolved-config-output", "", "Write the resolved non-secret configuration")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse eval v3 flags: %w", err)
	}
	config, err := v3.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	config = config.WithRunOverrides(*runID, *manifestPath, *outputDirectory)
	if err := v3.Validate(config); err != nil {
		return err
	}
	if strings.TrimSpace(*resolvedConfigOutput) != "" {
		runtimeValues, runtimeErr := config.ResolveRuntime(os.Getenv)
		if runtimeErr != nil {
			return fmt.Errorf("resolve eval v3 runtime provenance: %w", runtimeErr)
		}
		if exportErr := v2.ExportResolvedConfig(*resolvedConfigOutput, config, runtimeValues); exportErr != nil {
			return fmt.Errorf("export resolved eval v3 config: %w", exportErr)
		}
	}
	cases, revision, err := v3.LoadCases(config.Run.Manifest, config.AnswererSeed)
	if err != nil {
		return err
	}
	plannedRun, err := plannedRunRecord(config, revision)
	if err != nil {
		return err
	}
	dsn := strings.TrimSpace(os.Getenv(config.Store.DSNEnv))
	if strings.TrimSpace(*judgeInput) != "" {
		results, loadErr := v2.LoadTrialResultsJSONL(*judgeInput)
		if loadErr != nil {
			return fmt.Errorf("load eval v3 judge input: %w", loadErr)
		}
		hydrated := v2.HydrateResults(results, cases)
		runRecord, judged, judgeErr := v2.JudgeExistingRun(ctx, v2.ProcessExecutor{}, logger, config, revision, hydrated)
		if runRecord.ID == "" {
			runRecord = plannedRun
		}
		evidenceErr := v3.RecordRejudgeEvidence(config.Run.OutputDir, runRecord.ID, hydrated, judged)
		exportErr := exportResults(config, runRecord, cases, v2.RescoreResults(judged))
		return errors.Join(judgeErr, evidenceErr, exportErr)
	}
	if dsn == "" {
		storeErr := fmt.Errorf("load eval v3 store: environment variable %s is required", config.Store.DSNEnv)
		return errors.Join(storeErr, exportResults(config, plannedRun, cases, nil))
	}
	store, err := postgresstore.Open(ctx, dsn)
	if err != nil {
		return errors.Join(err, exportResults(config, plannedRun, cases, nil))
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return errors.Join(err, exportResults(config, plannedRun, cases, nil))
	}
	runner, err := v2.NewRunner(store, v2.ProcessExecutor{}, logger)
	if err != nil {
		return errors.Join(err, exportResults(config, plannedRun, cases, nil))
	}
	runRecord, results, runErr := runner.Run(ctx, config, cases, revision)
	if runRecord.ID == "" {
		runRecord = plannedRun
	}
	exportErr := exportResults(config, runRecord, cases, v2.RescoreResults(results))
	return errors.Join(runErr, exportErr)
}

func plannedRunRecord(config v2.Config, revision string) (v2.RunRecord, error) {
	runtimeValues, err := config.ResolveRuntime(os.Getenv)
	if err != nil {
		return v2.RunRecord{}, fmt.Errorf("resolve planned eval v3 runtime provenance: %w", err)
	}
	hash, err := config.HashWithRuntime(runtimeValues)
	if err != nil {
		return v2.RunRecord{}, fmt.Errorf("hash planned eval v3 configuration: %w", err)
	}
	return v2.RunRecord{
		ID: config.Run.ID, Dataset: config.Run.Dataset, DatasetRevision: revision,
		ConfigHash: hash, Config: config, Runtime: runtimeValues,
	}, nil
}

func exportResults(config v2.Config, runRecord v2.RunRecord, cases []v2.Case, results []v2.TrialResult) error {
	if err := os.MkdirAll(config.Run.OutputDir, 0o755); err != nil {
		return fmt.Errorf("create eval v3 output directory: %w", err)
	}
	resolvedErr := v2.ExportResolvedConfig(filepath.Join(config.Run.OutputDir, "config.resolved.json"), config, runRecord.Runtime)
	report, validityErr := v3.EvaluateValidity(config.Run.OutputDir, runRecord, cases, results)
	var validityExportErr error
	var gateErr error
	if validityErr == nil {
		validityExportErr = v3.ExportValidity(config.Run.OutputDir, report)
		gateErr = v3.RequireValid(report)
	}
	formats := config.OutputFormats()
	var cleanupErr error
	if gateErr != nil {
		formats = []string{"jsonl"}
		cleanupErr = removeComparisonArtifacts(config.Run.OutputDir)
	}
	artifactErr := v2.ExportArtifacts(config.Run.OutputDir, runRecord, config.BaselineArm, formats, results, func(writer io.Writer) error {
		return render.Report(runRecord, config.BaselineArm, results, writer)
	})
	return errors.Join(resolvedErr, validityErr, validityExportErr, cleanupErr, artifactErr, gateErr)
}

func removeComparisonArtifacts(directory string) error {
	var returnedErr error
	for _, name := range []string{"trials.csv", "summary.csv", "pairwise.csv", "report.html"} {
		if err := os.Remove(filepath.Join(directory, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("remove invalid eval v3 comparison artifact %s: %w", name, err))
		}
	}
	return returnedErr
}
