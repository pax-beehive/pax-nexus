package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/llmwiki/effecteval"
	"github.com/pax-beehive/pax-nexus/internal/llmwiki/workspace"
	"github.com/stretchr/testify/suite"
)

type commandSuite struct {
	suite.Suite
	ctx    context.Context
	root   string
	export string
}

func TestCommandSuite(t *testing.T) {
	suite.Run(t, new(commandSuite))
}

func (s *commandSuite) SetupTest() {
	s.ctx = context.Background()
	s.root = s.T().TempDir()
	s.T().Cleanup(func() {
		err := os.Chmod(filepath.Join(s.root, "sources"), 0o755)
		if err != nil && !os.IsNotExist(err) {
			s.NoError(err)
		}
	})
	exported := workspace.SessionExport{
		SchemaVersion: workspace.PaxmSessionSchema,
		SessionID:     "cli-session",
		Turns: []workspace.SessionTurn{
			{ID: "turn-1", User: "First fact.", Assistant: "First answer."},
			{ID: "turn-2", User: "Second fact.", Assistant: "Second answer."},
		},
	}
	encoded, err := json.Marshal(exported)
	s.Require().NoError(err)
	s.export = filepath.Join(s.T().TempDir(), "export.json")
	s.Require().NoError(os.WriteFile(s.export, encoded, 0o600))
}

func (s *commandSuite) TestBuildValidateAndRunFailureAudit() {
	output, err := s.execute(
		"build",
		"--workspace", s.root,
		"--session", "cli-session",
		"--start", "0",
		"--end", "1",
		"--export", s.export,
	)
	s.Require().NoError(err)
	s.Contains(output, `"turn_count": 1`)

	output, err = s.execute("validate", "--workspace", s.root)
	s.Require().NoError(err)
	s.Contains(output, `"valid": true`)

	s.T().Setenv("DEEPSEEK_API_KEY", "")
	_, err = s.execute(
		"run",
		"--workspace", s.root,
		"--run-id", "missing-key",
	)
	s.Require().ErrorContains(err, "API key")
	s.FileExists(filepath.Join(s.root, ".pax/runs/missing-key.json"))

	s.Require().NoError(os.WriteFile(
		filepath.Join(s.root, "wiki/index.md"),
		[]byte("# Wiki\n\n[missing](pages/missing.md)\n"),
		0o644,
	))
	output, err = s.execute("validate", "--workspace", s.root)
	s.Require().ErrorContains(err, "validation failed")
	s.Contains(output, `"valid": false`)
}

func (s *commandSuite) TestSnapshotPublishDiffAndRollbackCommands() {
	_, err := s.execute(
		"build", "--workspace", s.root, "--session", "cli-session",
		"--start", "0", "--end", "1", "--export", s.export,
	)
	s.Require().NoError(err)
	store := filepath.Join(s.T().TempDir(), "store.git")
	base, err := s.execute(
		"init-store", "--workspace", s.root, "--store", store,
	)
	s.Require().NoError(err)
	base = strings.TrimSpace(base)

	checkout := filepath.Join(s.T().TempDir(), "checkout")
	s.Require().NoError(os.MkdirAll(filepath.Dir(checkout), 0o755))
	_, err = s.execute("checkout", "--store", store, "--workspace", checkout)
	s.Require().NoError(err)
	s.T().Cleanup(func() {
		chmodErr := os.Chmod(filepath.Join(checkout, "sources"), 0o755)
		if chmodErr != nil && !os.IsNotExist(chmodErr) {
			s.NoError(chmodErr)
		}
	})
	s.Require().NoError(os.WriteFile(
		filepath.Join(checkout, "wiki/index.md"),
		[]byte("# Wiki\n\nCLI snapshot.\n"),
		0o644,
	))
	revision, err := s.execute(
		"commit", "--workspace", checkout, "--message", "CLI snapshot",
	)
	s.Require().NoError(err)
	revision = strings.TrimSpace(revision)

	diff, err := s.execute(
		"diff", "--repo", checkout, "--base", base, "--revision", revision,
	)
	s.Require().NoError(err)
	s.Contains(diff, "CLI snapshot.")
	_, err = s.execute(
		"publish", "--store", store, "--workspace", checkout,
		"--base", base, "--revision", revision,
	)
	s.Require().NoError(err)
	_, err = s.execute(
		"rollback", "--store", store, "--head", revision, "--target", base,
	)
	s.Require().NoError(err)
}

func (s *commandSuite) TestEvalCommandsAndArgumentErrors() {
	fixture := filepath.Join(s.T().TempDir(), "eval.json")
	value := effecteval.Fixture{
		SchemaVersion: effecteval.SchemaVersion,
		ID:            "cli-eval",
		Cases: []effecteval.Case{{
			ID: "case",
			Sessions: []effecteval.Session{{
				ID: "eval-session",
				Events: []effecteval.Event{{
					ID: "event", Role: "user", Content: "Answer is blue.",
				}},
			}},
			Evaluator: effecteval.Evaluator{
				AnswerPatterns: []string{"(?i)blue"},
			},
		}},
	}
	encoded, err := json.Marshal(value)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(fixture, encoded, 0o600))

	output, err := s.execute(
		"eval-prepare", "--fixture", fixture, "--workspace", s.root,
	)
	s.Require().NoError(err)
	s.Contains(output, `"dataset_id": "cli-eval"`)
	output, err = s.execute(
		"eval-score", "--fixture", fixture, "--workspace", s.root,
	)
	s.Require().NoError(err)
	s.Contains(output, `"raw_source_hits": 1`)

	_, err = s.execute("unknown")
	s.Require().ErrorContains(err, "unknown command")
	_, err = s.execute("build", "--workspace", s.root)
	s.Require().ErrorContains(err, "required")
	_, err = s.execute("validate", "--unknown-flag")
	s.Require().Error(err)
	_, err = s.execute("serve", "--workspace", s.root, "--addr", "127.0.0.1:-1")
	s.Require().Error(err)
	err = execute(
		s.ctx,
		[]string{"serve", "--workspace", s.root},
		failingWriter{},
	)
	s.Require().ErrorContains(err, "write viewer address")
}

func (s *commandSuite) execute(arguments ...string) (string, error) {
	s.T().Helper()
	var output bytes.Buffer
	err := execute(s.ctx, arguments, &output)
	return output.String(), err
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, os.ErrClosed
}
