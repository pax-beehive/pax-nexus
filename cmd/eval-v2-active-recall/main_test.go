package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type activeRecallSuite struct{ suite.Suite }

func TestActiveRecallSuite(t *testing.T) { suite.Run(t, new(activeRecallSuite)) }

func (s *activeRecallSuite) TestRunAllowsTwoCallsAndRejectsThirdBeforePaxm() {
	directory := s.T().TempDir()
	capture := filepath.Join(directory, "requests")
	binary := filepath.Join(directory, "paxm")
	script := `#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$PAXM_REQUEST_CAPTURE"
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"recalled evidence"}]}}'
done
`
	s.Require().NoError(os.WriteFile(binary, []byte(script), 0o700))
	s.T().Setenv("PAXM_REQUEST_CAPTURE", capture)

	config := runConfig{
		PaxmBinary: binary,
		PaxmConfig: filepath.Join(directory, "paxm.yaml"),
		StateDir:   filepath.Join(directory, "state"),
		MaxCalls:   2,
	}
	for call := 1; call <= 2; call++ {
		var output bytes.Buffer
		err := run(context.Background(), config, "consumer-session", "focused query", &output)
		s.Require().NoError(err)
		s.Equal("recalled evidence", strings.TrimSpace(output.String()))
	}

	var output bytes.Buffer
	s.Require().NoError(run(context.Background(), config, "consumer-session", "third query", &output))
	s.Contains(output.String(), "Active recall limit reached: 2")

	requests, err := os.ReadFile(capture)
	s.Require().NoError(err)
	lines := strings.Split(strings.TrimSpace(string(requests)), "\n")
	s.Len(lines, 2)
	s.Contains(lines[0], `"query":"focused query"`)
	s.Contains(lines[0], `"session_id":"consumer-session"`)
	s.NotContains(string(requests), "third query")
}

func (s *activeRecallSuite) TestRunValidatesInputsAndHardMaximum() {
	base := runConfig{PaxmBinary: "paxm", PaxmConfig: "config", StateDir: s.T().TempDir(), MaxCalls: 2}
	tests := []struct {
		name      string
		config    runConfig
		sessionID string
		query     string
		wantError string
	}{
		{name: "empty session", config: base, query: "query", wantError: "session ID is required"},
		{name: "empty query", config: base, sessionID: "session", wantError: "query is required"},
		{name: "above hard maximum", config: runConfig{MaxCalls: 3}, sessionID: "session", query: "query", wantError: "between 1 and 2"},
		{name: "missing runtime config", config: runConfig{MaxCalls: 2}, sessionID: "session", query: "query", wantError: "binary, config, and active recall state"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			var output bytes.Buffer
			err := run(context.Background(), test.config, test.sessionID, test.query, &output)
			s.Require().Error(err)
			s.Contains(err.Error(), test.wantError)
		})
	}
}

func (s *activeRecallSuite) TestCallPaxmReportsProtocolAndProcessFailures() {
	tests := []struct {
		name      string
		script    string
		wantError string
	}{
		{name: "process failure", script: "#!/bin/sh\necho unavailable >&2\nexit 1\n", wantError: "unavailable"},
		{name: "empty response", script: "#!/bin/sh\nexit 0\n", wantError: "no response"},
		{name: "invalid JSON", script: "#!/bin/sh\necho not-json\n", wantError: "decode paxm"},
		{name: "RPC error", script: "#!/bin/sh\necho '{\"jsonrpc\":\"2.0\",\"id\":1,\"error\":{\"message\":\"provider failed\"}}'\n", wantError: "provider failed"},
		{name: "MCP tool error", script: "#!/bin/sh\necho '{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"isError\":true,\"content\":[{\"type\":\"text\",\"text\":\"recall timed out\"}]}}'\n", wantError: "recall timed out"},
		{name: "no text", script: "#!/bin/sh\necho '{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"content\":[]}}'\n", wantError: "no text content"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			directory := s.T().TempDir()
			binary := filepath.Join(directory, "paxm")
			s.Require().NoError(os.WriteFile(binary, []byte(test.script), 0o700))
			_, err := callPaxm(context.Background(), runConfig{PaxmBinary: binary, PaxmConfig: "config"}, "session", "query", 1)
			s.Require().Error(err)
			s.Contains(err.Error(), test.wantError)
		})
	}
}

func (s *activeRecallSuite) TestEnvOrDefault() {
	s.Equal("fallback", envOrDefault("ACTIVE_RECALL_MISSING_VALUE", "fallback"))
	s.T().Setenv("ACTIVE_RECALL_CONFIGURED_VALUE", " configured ")
	s.Equal("configured", envOrDefault("ACTIVE_RECALL_CONFIGURED_VALUE", "fallback"))
}
