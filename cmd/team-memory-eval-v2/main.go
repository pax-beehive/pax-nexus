package main

import (
	"context"
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
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse eval v2 flags: %w", err)
	}
	config, err := v2.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	dsn := strings.TrimSpace(os.Getenv(config.Store.DSNEnv))
	if dsn == "" {
		return fmt.Errorf("load eval v2 store: environment variable %s is required", config.Store.DSNEnv)
	}
	cases, revision, err := v2.LoadCases(config.Run.Manifest)
	if err != nil {
		return err
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
		return err
	}
	results = v2.RescoreResults(results)
	return v2.ExportArtifacts(config.Run.OutputDir, runRecord, config.BaselineArm, config.OutputFormats(), results, func(writer io.Writer) error {
		return render.Report(runRecord, config.BaselineArm, results, writer)
	})
}
