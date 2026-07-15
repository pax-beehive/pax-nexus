package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/session"
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
	s.JSONEq(`{"provider":"mem0","accepted":1,"duplicate":0,"created":0,"updated":0,"deleted":0,"noop_known":true,"noop":true}`, output.String())
}

func (s *commandSuite) TestIngestReadsNativeSessionBatches() {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		s.Equal("/v1/session-batches", request.URL.Path)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(`{"accepted":1,"duplicate":0}`)), Header: make(http.Header)}, nil
	})}
	directory := s.T().TempDir()
	path := filepath.Join(directory, "session-batches.json")
	input, err := json.Marshal([]session.SessionBatch{{Complete: true, Events: []session.SessionEvent{{
		ID: "Msg_1", Actor: session.Actor{UserID: "User_3", AgentID: "groupmembench-User_3", SessionID: "session-3"},
		Sequence: 1, Type: "message", Content: "decision", OccurredAt: time.Date(2025, time.July, 5, 7, 0, 0, 0, time.UTC),
	}}}})
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(path, input, 0o600))
	environment := map[string]string{
		"TEAM_MEMORY_API_KEY": "key", "TEAM_MEMORY_BASE_URL": "http://team-note", "MEM0_BASE_URL": "http://mem0",
		"PAXM_USER_ID": "asking-user", "PAXM_AGENT_ID": "helper", "MEM0_RUN_ID": "run",
	}
	var output bytes.Buffer
	err = run(context.Background(), []string{"-action", "ingest", "-provider", "team_note", "-session-batches-file", path}, func(name string) string { return environment[name] }, &output, client)
	s.Require().NoError(err)
	s.JSONEq(`{"provider":"team_note","accepted":1,"duplicate":0,"created":0,"updated":0,"deleted":0,"noop_known":false,"noop":false,"source_events":1,"source_actors":1,"source_sessions":1}`, output.String())
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
