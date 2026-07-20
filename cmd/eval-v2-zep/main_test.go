package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

type commandSuite struct{ suite.Suite }

func TestCommandSuite(t *testing.T) { suite.Run(t, new(commandSuite)) }

func (s *commandSuite) TestIngestsEligibleEventsIntoIsolatedUserGraph() {
	requests := 0
	var graphRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		switch request.URL.Path {
		case "/users":
			writer.WriteHeader(http.StatusCreated)
		case "/graph":
			if err := json.NewDecoder(request.Body).Decode(&graphRequest); err != nil {
				s.T().Errorf("decode graph request: %v", err)
			}
			writer.WriteHeader(http.StatusAccepted)
			if _, err := writer.Write([]byte(`{"uuid":"episode-1"}`)); err != nil {
				s.T().Errorf("write graph response: %v", err)
			}
		default:
			s.T().Errorf("unexpected path %s", request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	input := filepath.Join(s.T().TempDir(), "batches.json")
	s.Require().NoError(os.WriteFile(input, []byte(`[{"complete":true,"events":[{"id":"event-2","content":"Legal approved the hold.","visibility":"team_note_eligible","occurred_at":"2025-07-19T13:35:14Z","actor":{"user_id":"User_2","agent_id":"legal-agent","session_id":"session"}},{"id":"event-1","content":"Ops owns rollback evidence.","visibility":"team_note_eligible","occurred_at":"2025-07-19T13:34:14Z","actor":{"user_id":"User_1","agent_id":"ops-agent","session_id":"session"}},{"id":"private-event","content":"private scratchpad","visibility":"private","occurred_at":"2025-07-19T13:36:14Z","actor":{"user_id":"User_1","agent_id":"ops-agent","session_id":"session"}}]},{"complete":true,"events":[{"id":"event-3","content":"Compliance owns the final review.","visibility":"team_note_eligible","occurred_at":"2025-07-19T13:37:14Z","actor":{"user_id":"User_3","agent_id":"compliance-agent","session_id":"session"}}]}]`), 0o600))
	s.T().Setenv("ZEP_API_KEY", "test-key")
	var stdout bytes.Buffer
	err := run([]string{"-action", "ingest", "-user-id", "zep-eval-case-1", "-session-batches-file", input, "-base-url", server.URL}, &stdout, server.Client())
	s.Require().NoError(err)
	s.Contains(stdout.String(), `"accepted":1`)
	s.Contains(stdout.String(), `"source_events":4`)
	s.Equal(2, requests)
	s.Equal("zep-eval-case-1", graphRequest["user_id"])
	s.Equal("2025-07-19T13:37:14Z", graphRequest["created_at"])
	s.Contains(graphRequest["data"], "event=event-1")
	s.Contains(graphRequest["data"], "event=event-2")
	s.Contains(graphRequest["data"], "event=event-3")
	s.NotContains(graphRequest["data"], "private scratchpad")
}

func (s *commandSuite) TestSearchReturnsNativeContextAndProcessingState() {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		s.Equal("/graph/search", request.URL.Path)
		if _, err := writer.Write([]byte(`{"context":"<EPISODES>Ops owns rollback evidence.</EPISODES>","episodes":[{"uuid":"episode-1","processed":true}]}`)); err != nil {
			s.T().Errorf("write search response: %v", err)
		}
	}))
	defer server.Close()
	s.T().Setenv("ZEP_API_KEY", "test-key")
	var stdout bytes.Buffer
	err := run([]string{"-action", "search", "-user-id", "zep-eval-case-1", "-query", "Who owns rollback?", "-base-url", server.URL}, &stdout, server.Client())
	s.Require().NoError(err)
	s.Contains(stdout.String(), `"processed":1`)
	s.Contains(stdout.String(), "Ops owns rollback evidence")
}

func (s *commandSuite) TestReadyCountsProcessedEpisodes() {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		s.Equal("/graph/episodes/user/zep-eval-case-1", request.URL.Path)
		if _, err := writer.Write([]byte(`{"episodes":[{"uuid":"episode-1","processed":true},{"uuid":"episode-2","processed":false}]}`)); err != nil {
			s.T().Errorf("write readiness response: %v", err)
		}
	}))
	defer server.Close()
	s.T().Setenv("ZEP_API_KEY", "test-key")
	var stdout bytes.Buffer
	err := run([]string{"-action", "ready", "-user-id", "zep-eval-case-1", "-base-url", server.URL}, &stdout, server.Client())
	s.Require().NoError(err)
	s.Contains(stdout.String(), `"episodes":2`)
	s.Contains(stdout.String(), `"processed":1`)
}

func (s *commandSuite) TestRequiresCredentialAndAction() {
	s.T().Setenv("ZEP_API_KEY", "")
	err := run([]string{"-action", "search", "-user-id", "zep-eval-case-1", "-query", "Who owns rollback?"}, io.Discard, http.DefaultClient)
	s.ErrorContains(err, "ZEP_API_KEY is required")
}
