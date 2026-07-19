package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/recallv2"
	"github.com/stretchr/testify/suite"
)

type commandSuite struct{ suite.Suite }

func TestCommandSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(commandSuite))
}

func (s *commandSuite) TestRunRejectsInvalidFlagsAndMissingConfig() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.Require().ErrorContains(run(context.Background(), []string{"-unknown"}, logger), "parse recall eval v2 flags")
	s.Require().ErrorContains(run(context.Background(), []string{"-config", filepath.Join(s.T().TempDir(), "missing.yaml")}, logger), "read recall eval v2 config")
}

func (s *commandSuite) TestWriteJSONAndReplayGate() {
	directory := s.T().TempDir()
	path := filepath.Join(directory, "artifact.json")
	s.Require().NoError(writeJSON(path, map[string]int{"value": 1}))
	encoded, err := os.ReadFile(path)
	s.Require().NoError(err)
	s.Contains(string(encoded), `"value": 1`)

	config := recallv2.Config{}
	config.Run.OutputDir = directory
	config.Recall.DeterministicReplay = filepath.Join("..", "..", "evals", "recall-v2", "groupmembench-finance-hard34-replay-v1.json")
	config.Recall.HintReplay = filepath.Join("..", "..", "evals", "recall-v2", "groupmembench-finance-hint12-replay-v1.json")
	config.Recall.MinReplayCases = 30
	s.Require().NoError(runReplayGate(config))
	_, err = os.Stat(filepath.Join(directory, "deterministic-replay", "hint-summary.json"))
	s.Require().NoError(err)
}
