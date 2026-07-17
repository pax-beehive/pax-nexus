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
	dsn := strings.TrimSpace(os.Getenv(config.Store.DSNEnv))
	if strings.TrimSpace(*judgeInput) != "" {
		results, loadErr := v2.LoadTrialResultsJSONL(*judgeInput)
		if loadErr != nil {
			return fmt.Errorf("load eval v3 judge input: %w", loadErr)
		}
		runRecord, judged, judgeErr := v2.JudgeExistingRun(ctx, v2.ProcessExecutor{}, logger, config, revision, v2.HydrateResults(results, cases))
		exportErr := exportResults(config, runRecord, v2.RescoreResults(judged))
		return errors.Join(judgeErr, exportErr)
	}
	if dsn == "" {
		return fmt.Errorf("load eval v3 store: environment variable %s is required", config.Store.DSNEnv)
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
	runRecord, results, runErr := runner.Run(ctx, config, cases, revision)
	if runErr != nil && len(results) == 0 {
		return runErr
	}
	exportErr := exportResults(config, runRecord, v2.RescoreResults(results))
	return errors.Join(runErr, exportErr)
}

func exportResults(config v2.Config, runRecord v2.RunRecord, results []v2.TrialResult) error {
	return v2.ExportArtifacts(config.Run.OutputDir, runRecord, config.BaselineArm, config.OutputFormats(), results, func(writer io.Writer) error {
		return render.Report(runRecord, config.BaselineArm, results, writer)
	})
}
