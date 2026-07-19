package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

type commandSuite struct{ suite.Suite }

func TestCommandSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(commandSuite))
}

func (s *commandSuite) TestRunValidatesInputsAndManifest() {
	s.Require().ErrorContains(run("", "", "", ""), "manifest, output, replay-output, and relation-fixtures are required")
	directory := s.T().TempDir()
	s.Require().ErrorContains(run(
		filepath.Join(directory, "missing.json"), filepath.Join(directory, "stage.json"),
		filepath.Join(directory, "replay.json"), filepath.Join(directory, "relations.json"),
	), "read recall eval v2 source manifest")
}
