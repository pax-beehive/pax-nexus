package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pax-beehive/pax-nexus/internal/eval/recallreplay"
	"github.com/pax-beehive/pax-nexus/internal/eval/recallv2"
)

func main() {
	manifest := flag.String("manifest", "", "GroupMemBench case-context manifest")
	output := flag.String("output", "", "Stage fixture output path")
	replayOutput := flag.String("replay-output", "", "Synthetic hard replay output path")
	hintReplayOutput := flag.String("hint-replay-output", "", "Curated Hint Recall replay output path")
	relationFixtures := flag.String("relation-fixtures", "", "Reviewed relation-utility replay fixture to append")
	flag.Parse()
	if err := run(*manifest, *output, *replayOutput, *hintReplayOutput, *relationFixtures); err != nil {
		if _, writeErr := fmt.Fprintf(os.Stderr, "recall eval v2 fixture generation failed: %v\n", err); writeErr != nil {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func run(manifest, output, replayOutput, hintReplayOutput, relationFixtures string) error {
	if manifest == "" || output == "" || replayOutput == "" || hintReplayOutput == "" || relationFixtures == "" {
		return fmt.Errorf("manifest, output, replay-output, hint-replay-output, and relation-fixtures are required")
	}
	fixtures, err := recallv2.BuildAnswerAtomFixtures(manifest)
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(fixtures, "", "  ")
	if err != nil {
		return fmt.Errorf("encode recall eval v2 fixtures: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return fmt.Errorf("create recall eval v2 fixture directory: %w", err)
	}
	if err := os.WriteFile(output, append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("write recall eval v2 fixtures: %w", err)
	}
	replay, err := recallv2.BuildSyntheticHardReplay(fixtures)
	if err != nil {
		return err
	}
	relations, err := recallreplay.LoadFixtureSet(relationFixtures)
	if err != nil {
		return err
	}
	replay.Cases = append(replay.Cases, relations.Cases...)
	replay.Dataset += " plus reviewed relation marginal utility cases"
	if err := recallreplay.WriteFixtureSet(replayOutput, replay); err != nil {
		return err
	}
	hintReplay, err := recallv2.BuildHintRecallReplay(fixtures)
	if err != nil {
		return err
	}
	return recallreplay.WriteFixtureSet(hintReplayOutput, hintReplay)
}
