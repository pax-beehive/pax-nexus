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

	"github.com/pax-beehive/pax-nexus/internal/eval/stagecapture"
	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	v2 "github.com/pax-beehive/pax-nexus/internal/eval/v2"
	"github.com/pax-beehive/pax-nexus/internal/eval/v2/postgresstore"
	"github.com/pax-beehive/pax-nexus/internal/eval/v2/render"
	"github.com/pax-beehive/pax-nexus/internal/eval/v2/stageartifacts"
	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
)

func main() {
	logger := observability.NewLogger(os.Stderr)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], logger); err != nil {
		logger.ErrorContext(ctx, "eval v2 failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, logger *slog.Logger) error {
	flags := flag.NewFlagSet("team-memory-eval-v2", flag.ContinueOnError)
	configPath := flags.String("config", "evals/v2/config.yaml", "Eval v2 YAML configuration")
	runID := flags.String("run-id", "", "Override the configured run ID")
	manifestPath := flags.String("manifest", "", "Override the configured manifest path")
	outputDirectory := flags.String("output-dir", "", "Override the configured output directory")
	judgeInput := flags.String("judge-input", "", "Judge completed answers from an existing trials.jsonl without rerunning trials")
	resolvedConfigOutput := flags.String("resolved-config-output", "", "Write the resolved non-secret configuration before running")
	automationProvenance := flags.Bool("automation-provenance", false, "Record workstation automation provenance from the environment")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse eval v2 flags: %w", err)
	}
	config, err := v2.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	config = config.WithRunOverrides(*runID, *manifestPath, *outputDirectory)
	if *automationProvenance {
		config = config.WithRuntimeEnvironment(
			"CANDIDATE_GIT_SHA",
			"EVAL_FRAMEWORK_GIT_SHA",
			"EVAL_FRAMEWORK_VERSION",
			"EVAL_SELECTION_SEED",
			"EVAL_SELECTION_ALGORITHM",
			"EVAL_MANIFEST_SHA256",
			"EVAL_IMAGE_DIGESTS",
		)
	}
	if strings.TrimSpace(*resolvedConfigOutput) != "" {
		runtimeValues, runtimeErr := config.ResolveRuntime(os.Getenv)
		if runtimeErr != nil {
			return fmt.Errorf("resolve eval runtime provenance: %w", runtimeErr)
		}
		if exportErr := v2.ExportResolvedConfig(*resolvedConfigOutput, config, runtimeValues); exportErr != nil {
			return fmt.Errorf("export resolved eval config: %w", exportErr)
		}
	}
	cases, revision, err := v2.LoadCases(config.Run.Manifest)
	if err != nil {
		return err
	}
	dsn := strings.TrimSpace(os.Getenv(config.Store.DSNEnv))
	if strings.TrimSpace(*judgeInput) != "" {
		results, loadErr := v2.LoadTrialResultsJSONL(*judgeInput)
		if loadErr != nil {
			return fmt.Errorf("load judge input: %w", loadErr)
		}
		runRecord, judged, judgeErr := v2.JudgeExistingRun(ctx, v2.ProcessExecutor{}, logger, config, revision, v2.HydrateResults(results, cases))
		if judgeErr != nil {
			exportErr := exportResults(ctx, config, runRecord, cases, v2.RescoreResults(judged), dsn, false)
			return errors.Join(fmt.Errorf("judge existing eval results: %w", judgeErr), exportErr)
		}
		return exportResults(ctx, config, runRecord, cases, v2.RescoreResults(judged), dsn, false)
	}
	if dsn == "" {
		return fmt.Errorf("load eval v2 store: environment variable %s is required", config.Store.DSNEnv)
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
	runRecord, results, err := runner.Run(ctx, config, cases, revision)
	if err != nil {
		if len(results) == 0 {
			return err
		}
		exportErr := exportResults(ctx, config, runRecord, cases, v2.RescoreResults(results), dsn, true)
		return errors.Join(err, exportErr)
	}
	return exportResults(ctx, config, runRecord, cases, v2.RescoreResults(results), dsn, true)
}

func exportResults(
	ctx context.Context,
	config v2.Config,
	runRecord v2.RunRecord,
	cases []v2.Case,
	results []v2.TrialResult,
	dsn string,
	captureStage bool,
) error {
	var stageErr error
	if config.StageCapture != nil && captureStage {
		fixtures, err := stageeval.LoadFixtureSet(config.StageCapture.Fixtures)
		if err != nil {
			stageErr = err
		} else {
			observer, openErr := stagecapture.Open(ctx, dsn)
			if openErr != nil {
				stageErr = openErr
			} else {
				stageErr = stageartifacts.Export(ctx, observer, stageartifacts.Request{
					RunID: runRecord.ID, OutputDirectory: config.Run.OutputDir,
					Config: *config.StageCapture, Fixtures: fixtures, Cases: cases, Results: results,
				})
				observer.Close()
			}
		}
	}
	artifactErr := v2.ExportArtifacts(config.Run.OutputDir, runRecord, config.BaselineArm, config.OutputFormats(), results, func(writer io.Writer) error {
		return render.Report(runRecord, config.BaselineArm, results, writer)
	})
	return errors.Join(stageErr, artifactErr)
}
