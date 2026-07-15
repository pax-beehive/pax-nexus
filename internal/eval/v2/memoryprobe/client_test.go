package memoryprobe_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/v2/memoryprobe"
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
		s.Require().NoError(client.Ingest(context.Background(), provider, transcript))
	}

	calls := transport.snapshot()
	s.Require().Len(calls, 2)
	s.Equal("/v1/session-batches", calls[0].path)
	s.Equal("/memories", calls[1].path)
	for _, call := range calls {
		s.Contains(call.body, `  identical handoff\n`)
	}
}

func (s *clientSuite) TestIngestAcceptsMem0NoOpExtraction() {
	transport := &recordingTransport{memoryResponse: `{"results":[]}`}
	client, err := memoryprobe.New(memoryprobe.Config{
		TeamNoteURL: "http://team-note", TeamNoteAPIKey: "key", Mem0URL: "http://mem0",
		UserID: "user", AgentID: "producer", RunID: "run", HTTPClient: &http.Client{Transport: transport},
	})
	s.Require().NoError(err)

	s.Require().NoError(client.Ingest(context.Background(), memoryprobe.ProviderMem0, "handoff"))
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
	s.Contains(calls[5].body, `"agent_id":"preflight"`)
	s.Contains(calls[5].body, `"run_id":"run"`)
	s.NotContains(calls[5].body, `"filters"`)
	for _, index := range []int{2, 5, 7} {
		s.Contains(calls[index].body, "durable verification code remains active")
	}
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
		{name: "unknown provider", run: func() error { return client.Ingest(context.Background(), "unknown", "text") }},
		{name: "empty text", run: func() error { return client.Ingest(context.Background(), memoryprobe.ProviderMem0, " ") }},
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
	mu             sync.Mutex
	calls          []recordedCall
	searchCount    int
	memoryResponse string
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
		responseBody = `{"accepted":1,"duplicate":0,"cursor":1}`
	case "/v1/notes/recall":
		responseBody = `{"revision":"1","items":["Confirmed active for this run."],"tokens":1}`
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
