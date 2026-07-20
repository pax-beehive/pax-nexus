package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

type commandSuite struct{ suite.Suite }

func TestCommandSuite(t *testing.T) { suite.Run(t, new(commandSuite)) }

func (s *commandSuite) TestRunRendersEligibleBM25Evidence() {
	directory := s.T().TempDir()
	input := filepath.Join(directory, "session-batches.json")
	s.Require().NoError(os.WriteFile(input, []byte(`[
		{"complete":true,"events":[
			{"id":"event-1","content":"Ops owns the rollback evidence.","visibility":"team_note_eligible","occurred_at":"2026-07-19T10:00:00Z","actor":{"user_id":"User_1","session_id":"session-1"}},
			{"id":"event-2","content":"Unrelated private detail.","visibility":"private","occurred_at":"2026-07-19T10:01:00Z","actor":{"user_id":"User_2","session_id":"session-1"}}
		]}
	]`), 0o600))

	var output bytes.Buffer
	err := run([]string{
		"-session-batches-file", input,
		"-query", "Who owns rollback evidence?",
		"-candidate-limit", "4",
		"-token-budget", "100",
		"-chunk-events", "1",
	}, &output)

	s.Require().NoError(err)
	s.Contains(output.String(), `"selected_event_ids":["event-1"]`)
	s.Contains(output.String(), "Ops owns the rollback evidence.")
	s.NotContains(output.String(), "Unrelated private detail.")
}

func (s *commandSuite) TestRunRejectsInvalidTemporalCutoff() {
	input := filepath.Join(s.T().TempDir(), "session-batches.json")
	s.Require().NoError(os.WriteFile(input, []byte("[]"), 0o600))
	var output bytes.Buffer
	err := run([]string{
		"-session-batches-file", input,
		"-temporal-cutoff", "not-a-time",
	}, &output)

	s.ErrorContains(err, "parse BM25 temporal cutoff")
}

func (s *commandSuite) TestRunRequiresSourceFile() {
	var output bytes.Buffer
	err := run([]string{"-query", "Who owns rollback evidence?"}, &output)

	s.ErrorContains(err, "session-batches-file is required")
}

func (s *commandSuite) TestRunRejectsInvalidFlag() {
	var output bytes.Buffer
	err := run([]string{"-not-a-real-flag"}, &output)

	s.ErrorContains(err, "parse BM25 flags")
}
