package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		if _, writeErr := fmt.Fprintf(os.Stderr, "stage eval failed: %v\n", err); writeErr != nil {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("team-memory-stage-eval", flag.ContinueOnError)
	fixturePath := flags.String("fixtures", "", "Stage fixture set JSON")
	observationPath := flags.String("observations", "", "Extraction and recall observation JSONL")
	outputDirectory := flags.String("output-dir", "", "Output directory")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse stage eval flags: %w", err)
	}
	if *fixturePath == "" || *observationPath == "" || *outputDirectory == "" {
		return fmt.Errorf("parse stage eval flags: fixtures, observations, and output-dir are required")
	}
	fixtures, err := stageeval.LoadFixtureSet(*fixturePath)
	if err != nil {
		return err
	}
	observationData, err := os.ReadFile(*observationPath)
	if err != nil {
		return fmt.Errorf("open stage observations: %w", err)
	}
	results, summary, err := stageeval.Run(fixtures, bytes.NewReader(observationData))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*outputDirectory, 0o755); err != nil {
		return fmt.Errorf("create stage eval output directory: %w", err)
	}
	if err := writeFile(filepath.Join(*outputDirectory, "stage-results.jsonl"), func(writer io.Writer) error {
		return stageeval.WriteResultsJSONL(writer, results)
	}); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(*outputDirectory, "stage-summary.json"), func(writer io.Writer) error {
		return stageeval.WriteSummaryJSON(writer, summary)
	}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "stage eval: %d cases, extraction fact recall %.3f, conditional recall %.3f\n",
		summary.Cases, summary.ExtractionFactRecall, summary.RecallConditionalRecall)
	if err != nil {
		return fmt.Errorf("write stage eval summary: %w", err)
	}
	return nil
}

func writeFile(path string, write func(io.Writer) error) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create stage eval artifact %q: %w", path, err)
	}
	writeErr := write(file)
	closeErr := file.Close()
	if writeErr != nil {
		writeErr = fmt.Errorf("write stage eval artifact %q: %w", path, writeErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close stage eval artifact %q: %w", path, closeErr)
	}
	return errors.Join(writeErr, closeErr)
}
