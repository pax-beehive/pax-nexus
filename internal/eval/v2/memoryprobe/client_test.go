package memoryprobe_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/v2/memoryprobe"
	"github.com/pax-beehive/pax-nexus/internal/session"
	"github.com/stretchr/testify/suite"
)

type clientSuite struct{ suite.Suite }

func TestClientSuite(t *testing.T) { suite.Run(t, new(clientSuite)) }

func (s *clientSuite) TestIngestSendsTheSameTranscriptToBothProviders() {
	transport := &recordingTransport{}
	client, err := memoryprobe.New(memoryprobe.Config{
		TeamNoteURL: "http://team-note", TeamNoteAPIKey: "key", Mem0URL: "http://mem0",
		UserID: "user", AgentID: "producer", RunID: "run", HTTPClient: &http.Client{Transport: transport},
	})
	s.Require().NoError(err)

	transcript := "  identical handoff\n"
	for _, provider := range []string{memoryprobe.ProviderTeamNote, memoryprobe.ProviderMem0} {
		_, ingestErr := client.Ingest(context.Background(), provider, transcript)
		s.Require().NoError(ingestErr)
	}

	calls := transport.snapshot()
	s.Require().Len(calls, 2)
	s.Equal("/v1/session-batches", calls[0].path)
	s.Equal("/memories", calls[1].path)
	for _, call := range calls {
		s.Contains(call.body, `  identical handoff\n`)
	}
}

func (s *clientSuite) TestIngestBatchesPreservesTeamNoteActorsAndRendersTheSameEventsForMem0() {
	transport := &recordingTransport{}
	client, err := memoryprobe.New(memoryprobe.Config{
		TeamNoteURL: "http://team-note", TeamNoteAPIKey: "key", Mem0URL: "http://mem0",
		UserID: "asking-user", AgentID: "ingest-helper", RunID: "run", HTTPClient: &http.Client{Transport: transport},
	})
	s.Require().NoError(err)
	batches := []session.SessionBatch{
		{Complete: true, Events: []session.SessionEvent{{
			ID: "Msg_1", Actor: session.Actor{UserID: "User_3", AgentID: "groupmembench-User_3", SessionID: "session-3"},
			Sequence: 1, Type: "message", Content: "Ops owns rollback.", OccurredAt: time.Date(2025, time.July, 5, 7, 0, 0, 0, time.UTC),
			Metadata: map[string]string{"role": "PM", "channel": "Finance", "phase": "Plan", "topic": "Controls", "decision_point": "true", "noise": "false", "decision_change_metadata": `{"supersedes":"Msg_0"}`, "reply_to": "Msg_0"},
		}}},
		{Complete: true, Events: []session.SessionEvent{{
			ID: "Msg_2", Actor: session.Actor{UserID: "User_7", AgentID: "groupmembench-User_7", SessionID: "session-7"},
			Sequence: 1, Type: "message", Content: "Finance confirms cutoff.", OccurredAt: time.Date(2025, time.July, 5, 8, 0, 0, 0, time.UTC),
			Metadata: map[string]string{"role": "Analyst"},
		}}},
	}

	teamResult, err := client.IngestBatches(context.Background(), memoryprobe.ProviderTeamNote, batches)
	s.Require().NoError(err)
	s.Equal(2, teamResult.Accepted)
	s.Equal(2, teamResult.SourceEvents)
	s.Equal(2, teamResult.SourceActors)
	s.Equal(2, teamResult.SourceSessions)
	mem0Result, err := client.IngestBatches(context.Background(), memoryprobe.ProviderMem0, batches)
	s.Require().NoError(err)
	s.Equal(2, mem0Result.SourceEvents)

	calls := transport.snapshot()
	s.Require().Len(calls, 3)
	s.Equal("/v1/session-batches", calls[0].path)
	s.Contains(calls[0].body, `"user_id":"User_3"`)
	s.Contains(calls[0].body, `"agent_id":"groupmembench-User_3"`)
	s.Equal("/v1/session-batches", calls[1].path)
	s.Contains(calls[1].body, `"user_id":"User_7"`)
	s.Equal("/memories", calls[2].path)
	for _, expected := range []string{"Msg_1", "User_3", "PM", "Finance", "Plan", "Controls", "Decision point", "Noise", "supersedes", "Ops owns rollback.", "Msg_2", "User_7", "Finance confirms cutoff."} {
		s.Contains(calls[2].body, expected)
	}
}

func (s *clientSuite) TestFullDomainMem0IngestBatchesMessagesByNativeSession() {
	transport := &recordingTransport{}
	client, err := memoryprobe.New(memoryprobe.Config{
		TeamNoteURL: "http://team-note", TeamNoteAPIKey: "key", Mem0URL: "http://mem0",
		UserID: "shared", AgentID: "shared", RunID: "domain", HTTPClient: &http.Client{Transport: transport},
	})
	s.Require().NoError(err)
	batches := []session.SessionBatch{{Complete: true, Events: []session.SessionEvent{
		{ID: "Msg_1", Actor: session.Actor{UserID: "User_1", AgentID: "groupmembench-User_1", SessionID: "s1"}, Sequence: 1, Content: "first", OccurredAt: time.Unix(1, 0).UTC(), Metadata: map[string]string{"role": "Lead"}},
		{ID: "Msg_1b", Actor: session.Actor{UserID: "User_1", AgentID: "groupmembench-User_1", SessionID: "s1"}, Sequence: 2, Content: "follow-up", OccurredAt: time.Unix(3, 0).UTC(), Metadata: map[string]string{"role": "Lead"}},
	}}, {Complete: true, Events: []session.SessionEvent{
		{ID: "Msg_2", Actor: session.Actor{UserID: "User_2", AgentID: "groupmembench-User_2", SessionID: "s2"}, Sequence: 1, Content: "second", OccurredAt: time.Unix(2, 0).UTC(), Metadata: map[string]string{"role": "Reviewer"}},
	}}}

	result, err := client.IngestBatches(context.Background(), memoryprobe.ProviderMem0Messages, batches)

	s.Require().NoError(err)
	s.Equal(3, result.Accepted)
	calls := transport.snapshot()
	s.Require().Len(calls, 2)
	for index, expected := range []string{"Msg_1", "Msg_2"} {
		s.Equal("/memories", calls[index].path)
		s.Contains(calls[index].body, expected)
		s.Contains(calls[index].body, `"source_agent_id":"groupmembench-User_`)
	}
	s.Contains(calls[0].body, "Msg_1b")
	s.Contains(calls[0].body, "follow-up")
	s.Contains(calls[0].body, `"source_session_id":"s1"`)
}

func (s *clientSuite) TestIngestBatchesValidationMatrix() {
	client, err := memoryprobe.New(memoryprobe.Config{
		TeamNoteURL: "http://team-note", TeamNoteAPIKey: "key", Mem0URL: "http://mem0",
		UserID: "asking-user", AgentID: "agent", RunID: "run", HTTPClient: &http.Client{Transport: &recordingTransport{}},
	})
	s.Require().NoError(err)
	valid := func() []session.SessionBatch {
		actor := session.Actor{UserID: "User_3", AgentID: "groupmembench-User_3", SessionID: "session-3"}
		return []session.SessionBatch{{Complete: true, Events: []session.SessionEvent{{
			ID: "Msg_1", Actor: actor, Sequence: 1, Type: "message", Content: "decision",
			OccurredAt: time.Date(2025, time.July, 5, 7, 0, 0, 0, time.UTC),
		}}}}
	}
	tests := []struct {
		name     string
		provider string
		mutate   func([]session.SessionBatch) []session.SessionBatch
	}{
		{name: "empty batches", provider: memoryprobe.ProviderTeamNote, mutate: func([]session.SessionBatch) []session.SessionBatch { return nil }},
		{name: "incomplete batch", provider: memoryprobe.ProviderTeamNote, mutate: func(batches []session.SessionBatch) []session.SessionBatch {
			batches[0].Complete = false
			return batches
		}},
		{name: "empty events", provider: memoryprobe.ProviderTeamNote, mutate: func(batches []session.SessionBatch) []session.SessionBatch { batches[0].Events = nil; return batches }},
		{name: "missing required field", provider: memoryprobe.ProviderTeamNote, mutate: func(batches []session.SessionBatch) []session.SessionBatch {
			batches[0].Events[0].ID = ""
			return batches
		}},
		{name: "mixed actor sessions", provider: memoryprobe.ProviderTeamNote, mutate: func(batches []session.SessionBatch) []session.SessionBatch {
			second := batches[0].Events[0]
			second.ID, second.Sequence, second.Actor.SessionID = "Msg_2", 2, "session-other"
			batches[0].Events = append(batches[0].Events, second)
			return batches
		}},
		{name: "non increasing sequence", provider: memoryprobe.ProviderTeamNote, mutate: func(batches []session.SessionBatch) []session.SessionBatch {
			second := batches[0].Events[0]
			second.ID = "Msg_2"
			batches[0].Events = append(batches[0].Events, second)
			return batches
		}},
		{name: "unknown provider", provider: "unknown", mutate: func(batches []session.SessionBatch) []session.SessionBatch { return batches }},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			_, ingestErr := client.IngestBatches(context.Background(), test.provider, test.mutate(valid()))
			s.Require().Error(ingestErr)
		})
	}
}

func (s *clientSuite) TestIngestAcceptsMem0NoOpExtraction() {
	transport := &recordingTransport{memoryResponse: `{"results":[]}`}
	client, err := memoryprobe.New(memoryprobe.Config{
		TeamNoteURL: "http://team-note", TeamNoteAPIKey: "key", Mem0URL: "http://mem0",
		UserID: "user", AgentID: "producer", RunID: "run", HTTPClient: &http.Client{Transport: transport},
	})
	s.Require().NoError(err)

	result, err := client.Ingest(context.Background(), memoryprobe.ProviderMem0, "handoff")
	s.Require().NoError(err)
	s.Equal(memoryprobe.ProviderMem0, result.Provider)
	s.Equal(1, result.Accepted)
	s.Zero(result.Created)
	s.Zero(result.Updated)
	s.Zero(result.Deleted)
	s.True(result.NoOpKnown)
	s.True(result.NoOp)
}

func (s *clientSuite) TestTeamNoteReceiptDoesNotClaimAsyncExtractionOutcome() {
	transport := &recordingTransport{}
	client, err := memoryprobe.New(memoryprobe.Config{
		TeamNoteURL: "http://team-note", TeamNoteAPIKey: "key", Mem0URL: "http://mem0",
		UserID: "user", AgentID: "producer", RunID: "run", HTTPClient: &http.Client{Transport: transport},
	})
	s.Require().NoError(err)

	result, err := client.Ingest(context.Background(), memoryprobe.ProviderTeamNote, "handoff")
	s.Require().NoError(err)
	s.Equal(1, result.Accepted)
	s.False(result.NoOpKnown)
	s.False(result.NoOp)
}

func (s *clientSuite) TestIngestCountsMem0WriteActions() {
	transport := &recordingTransport{memoryResponse: `{"results":[{"id":"one","event":"ADD"},{"id":"two","event":"UPDATE"},{"id":"three","event":"DELETE"},{"event":"NONE"}]}`}
	client, err := memoryprobe.New(memoryprobe.Config{
		TeamNoteURL: "http://team-note", TeamNoteAPIKey: "key", Mem0URL: "http://mem0",
		UserID: "user", AgentID: "producer", RunID: "run", HTTPClient: &http.Client{Transport: transport},
	})
	s.Require().NoError(err)

	result, err := client.Ingest(context.Background(), memoryprobe.ProviderMem0, "handoff")
	s.Require().NoError(err)
	s.Equal(1, result.Accepted)
	s.Equal(1, result.Created)
	s.Equal(1, result.Updated)
	s.Equal(1, result.Deleted)
	s.True(result.NoOpKnown)
	s.False(result.NoOp)
}

func (s *clientSuite) TestIngestValidatesMem0ResponseEnvelope() {
	tests := []struct {
		name     string
		response string
	}{
		{name: "malformed", response: `{`},
		{name: "missing results", response: `{}`},
		{name: "null results", response: `{"results":null}`},
		{name: "wrong results type", response: `{"results":{}}`},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			transport := &recordingTransport{memoryResponse: test.response}
			client, err := memoryprobe.New(memoryprobe.Config{
				TeamNoteURL: "http://team-note", TeamNoteAPIKey: "key", Mem0URL: "http://mem0",
				UserID: "user", AgentID: "producer", RunID: "run", HTTPClient: &http.Client{Transport: transport},
			})
			s.Require().NoError(err)

			_, ingestErr := client.Ingest(context.Background(), memoryprobe.ProviderMem0, "handoff")
			s.Require().Error(ingestErr)
		})
	}
}

func (s *clientSuite) TestPreflightExercisesAddRecallAndSupportedCleanup() {
	transport := &recordingTransport{}
	client, err := memoryprobe.New(memoryprobe.Config{
		TeamNoteURL: "http://team-note", TeamNoteAPIKey: "key", Mem0URL: "http://mem0",
		UserID: "user", AgentID: "preflight", RunID: "run", HTTPClient: &http.Client{Transport: transport},
		PollInterval: time.Millisecond,
	})
	s.Require().NoError(err)

	s.Require().NoError(client.Preflight(context.Background(), "probe-marker"))
	calls := transport.snapshot()
	s.Equal([]string{
		"GET /healthz", "POST /v1/session-batches", "POST /v1/notes/recall",
		"GET /openapi.json", "POST /memories", "POST /search", "DELETE /memories/mem-1", "POST /search",
	}, callLabels(calls))
	s.Equal("Bearer key", calls[1].authorization)
	for _, index := range []int{1, 4} {
		s.Contains(calls[index].body, "The evaluation owner confirmed the durable verification code probe-marker remains active")
	}
	s.Contains(calls[5].body, `"user_id":"user"`)
	s.Contains(calls[5].body, `"agent_id":"preflight-eval-`)
	s.Contains(calls[5].body, `"run_id":"run-eval-`)
	s.NotContains(calls[5].body, `"filters"`)
	for _, index := range []int{2, 5, 7} {
		s.Contains(calls[index].body, "durable verification code remains active")
	}
}

func (s *clientSuite) TestPreflightIgnoresStaleTeamNoteRecall() {
	transport := &recordingTransport{staleRecallCount: 1}
	client, err := memoryprobe.New(memoryprobe.Config{
		TeamNoteURL: "http://team-note", TeamNoteAPIKey: "key", Mem0URL: "http://mem0",
		UserID: "user", AgentID: "preflight", RunID: "run", HTTPClient: &http.Client{Transport: transport},
		PollInterval: time.Millisecond,
	})
	s.Require().NoError(err)

	s.Require().NoError(client.Preflight(context.Background(), "probe-marker"))
	labels := callLabels(transport.snapshot())
	s.Equal(2, countLabel(labels, "POST /v1/notes/recall"))
}

func (s *clientSuite) TestPreflightRejectsDuplicateTeamNoteProbe() {
	transport := &recordingTransport{teamReceipt: `{"accepted":0,"duplicate":1,"cursor":1}`}
	client, err := memoryprobe.New(memoryprobe.Config{
		TeamNoteURL: "http://team-note", TeamNoteAPIKey: "key", Mem0URL: "http://mem0",
		UserID: "user", AgentID: "preflight", RunID: "run", HTTPClient: &http.Client{Transport: transport},
		PollInterval: time.Millisecond,
	})
	s.Require().NoError(err)

	s.Require().Error(client.Preflight(context.Background(), "probe-marker"))
}

func (s *clientSuite) TestValidationAndInputErrors() {
	client, err := memoryprobe.New(memoryprobe.Config{
		TeamNoteURL: "http://team-note", TeamNoteAPIKey: "key", Mem0URL: "http://mem0",
		UserID: "user", AgentID: "agent", RunID: "run", HTTPClient: &http.Client{Transport: &recordingTransport{}},
	})
	s.Require().NoError(err)
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "invalid config", run: func() error { _, err := memoryprobe.New(memoryprobe.Config{}); return err }},
		{name: "unknown provider", run: func() error { _, err := client.Ingest(context.Background(), "unknown", "text"); return err }},
		{name: "empty text", run: func() error { _, err := client.Ingest(context.Background(), memoryprobe.ProviderMem0, " "); return err }},
	}
	for _, test := range tests {
		s.Run(test.name, func() { s.Require().Error(test.run()) })
	}
}

type recordedCall struct {
	method        string
	path          string
	body          string
	authorization string
}

type recordingTransport struct {
	mu                sync.Mutex
	calls             []recordedCall
	searchCount       int
	recallCount       int
	memoryResponse    string
	teamReceipt       string
	observedSessionID string
	staleRecallCount  int
}

func (t *recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var body []byte
	if request.Body != nil {
		var err error
		body, err = io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = append(t.calls, recordedCall{
		method: request.Method, path: request.URL.Path, body: string(body), authorization: request.Header.Get("Authorization"),
	})
	responseBody := `{}`
	switch request.URL.Path {
	case "/healthz":
		responseBody = `{"status":"ok"}`
	case "/v1/session-batches":
		var payload struct {
			Events []struct {
				Actor struct {
					SessionID string `json:"session_id"`
				} `json:"actor"`
			} `json:"events"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, err
		}
		if len(payload.Events) > 0 {
			t.observedSessionID = payload.Events[0].Actor.SessionID
		}
		responseBody = t.teamReceipt
		if responseBody == "" {
			responseBody = `{"accepted":1,"duplicate":0,"cursor":1}`
		}
	case "/v1/notes/recall":
		t.recallCount++
		originSessionID := t.observedSessionID
		if t.recallCount <= t.staleRecallCount {
			originSessionID = "stale-run"
		}
		responseBody = fmt.Sprintf(`{"revision":"1","items":["Confirmed active for this run."],"tokens":1,"details":[{"origin":{"session_id":%q}}]}`, originSessionID)
	case "/memories":
		responseBody = t.memoryResponse
		if responseBody == "" {
			responseBody = `{"results":[{"id":"mem-1","event":"ADD"}]}`
		}
	case "/search":
		t.searchCount++
		if t.searchCount == 1 {
			responseBody = `{"results":[{"id":"mem-1","memory":"Retain the verification state."}]}`
		} else {
			responseBody = `{"results":[]}`
		}
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(responseBody)),
		Header:     make(http.Header),
		Request:    request,
	}, nil
}

func (t *recordingTransport) snapshot() []recordedCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]recordedCall(nil), t.calls...)
}

func callLabels(calls []recordedCall) []string {
	labels := make([]string, 0, len(calls))
	for _, call := range calls {
		labels = append(labels, call.method+" "+call.path)
	}
	return labels
}

func countLabel(labels []string, target string) int {
	count := 0
	for _, label := range labels {
		if label == target {
			count++
		}
	}
	return count
}
