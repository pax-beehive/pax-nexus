package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/pax-beehive/pax-nexus/internal/eval/harness"
	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
)

func main() {
	logger := observability.NewLogger(os.Stderr)
	if err := run(os.Args[1:], logger); err != nil {
		logger.Error("team-memory eval failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string, logger *slog.Logger) error {
	flags := flag.NewFlagSet("team-memory-eval", flag.ContinueOnError)
	arm := flags.String("arm", "", "experiment arm")
	expected := flags.String("expected", "", "exact expected answer")
	inputPath := flags.String("input", "", "OpenCode JSON stream")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse eval flags: %w", err)
	}
	if *arm == "" || *expected == "" || *inputPath == "" {
		return fmt.Errorf("arm, expected, and input are required")
	}
	input, err := os.Open(*inputPath)
	if err != nil {
		return fmt.Errorf("open eval input: %w", err)
	}
	output, err := harness.ParseOpenCodeJSON(input)
	closeErr := input.Close()
	if err != nil {
		return fmt.Errorf("parse OpenCode output: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close eval input: %w", closeErr)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(harness.ScoreExact(*arm, *expected, output)); err != nil {
		return fmt.Errorf("encode eval score: %w", err)
	}
	logger.Info("team-memory eval completed", "arm", *arm)
	return nil
}
