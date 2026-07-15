package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

type commandSuite struct{ suite.Suite }

func TestCommandSuite(t *testing.T) { suite.Run(t, new(commandSuite)) }

func (s *commandSuite) TestRejectsInvalidInvocationBeforeNetworkUse() {
	environment := map[string]string{
		"TEAM_MEMORY_API_KEY": "key", "PAXM_USER_ID": "user", "PAXM_AGENT_ID": "agent", "MEM0_RUN_ID": "run",
	}
	getenv := func(name string) string { return environment[name] }
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing action"},
		{name: "missing ingest file", args: []string{"-action", "ingest", "-provider", "mem0"}},
		{name: "bad flags", args: []string{"-unknown"}},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			s.Error(run(context.Background(), test.args, getenv, io.Discard, nil))
		})
	}
}

func (s *commandSuite) TestIngestPrintsMem0NoOpReceipt() {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		s.Equal("/memories", request.URL.Path)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(`{"results":[]}`)), Header: make(http.Header)}, nil
	})}
	directory := s.T().TempDir()
	path := filepath.Join(directory, "handoff.txt")
	s.Require().NoError(os.WriteFile(path, []byte("handoff"), 0o600))
	environment := map[string]string{
		"TEAM_MEMORY_API_KEY": "key", "TEAM_MEMORY_BASE_URL": "http://team-note", "MEM0_BASE_URL": "http://mem0",
		"PAXM_USER_ID": "user", "PAXM_AGENT_ID": "agent", "MEM0_RUN_ID": "run",
	}
	var output bytes.Buffer
	err := run(context.Background(), []string{"-action", "ingest", "-provider", "mem0", "-text-file", path}, func(name string) string { return environment[name] }, &output, client)
	s.Require().NoError(err)
	s.JSONEq(`{"provider":"mem0","accepted":1,"duplicate":0,"created":0,"updated":0,"deleted":0,"noop":true}`, output.String())
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
