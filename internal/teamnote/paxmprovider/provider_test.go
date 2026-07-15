package paxmprovider_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/teamnote/paxmprovider"
	"github.com/stretchr/testify/suite"
)

type providerSuite struct {
	suite.Suite
	transport *providerTransport
	provider  *paxmprovider.Provider
	logs      bytes.Buffer
}

type providerTransport struct {
	requests     []*http.Request
	bodies       [][]byte
	responseBody string
}

func TestProviderSuite(t *testing.T) {
	suite.Run(t, new(providerSuite))
}

func (s *providerSuite) SetupTest() {
	s.transport = &providerTransport{}
	provider, err := paxmprovider.New(paxmprovider.Config{
		BaseURL: "http://team-memory:8080", APIKey: "scope-key", UserID: "owner",
		AgentID: "opencode-producer", TokenBudget: 450,
		Client: &http.Client{Transport: s.transport}, Logger: slog.New(slog.NewJSONHandler(&s.logs, nil)),
	})
	s.Require().NoError(err)
	s.provider = provider
}

func (s *providerSuite) TestPutMapsPaxmMetadataToSessionEvent() {
	request := `{"jsonrpc":"2.0","id":"1","method":"paxm.put","params":{` +
		`"id":"memory-1","text":"The migration is blocked by missing credentials.",` +
		`"created_at":"2026-07-14T12:00:00Z","metadata":{` +
		`"user_id":"metadata-owner","agent_id":"metadata-producer",` +
		`"session_id":"producer-session","task_ref":"task-1","thread_ref":"thread-1","sequence":"7"}}}`
	response := s.serve(request)
	s.Nil(response["error"])
	s.Require().Len(s.transport.requests, 1)
	s.Equal("Bearer scope-key", s.transport.requests[0].Header.Get("Authorization"))
	s.Equal("/v1/session-batches", s.transport.requests[0].URL.Path)

	var payload struct {
		Complete bool `json:"complete"`
		Events   []struct {
			ID       string `json:"id"`
			Sequence int64  `json:"sequence"`
			Actor    struct {
				UserID    string `json:"user_id"`
				AgentID   string `json:"agent_id"`
				SessionID string `json:"session_id"`
			} `json:"actor"`
		} `json:"events"`
	}
	s.Require().NoError(json.Unmarshal(s.transport.bodies[0], &payload))
	s.True(payload.Complete)
	s.Require().Len(payload.Events, 1)
	s.Equal("memory-1", payload.Events[0].ID)
	s.Equal(int64(7), payload.Events[0].Sequence)
	s.Equal("metadata-owner", payload.Events[0].Actor.UserID)
	s.Equal("metadata-producer", payload.Events[0].Actor.AgentID)
	s.Equal("producer-session", payload.Events[0].Actor.SessionID)
}

func (s *providerSuite) TestSearchMapsEnvelopeToHighConfidenceHits() {
	request := `{"jsonrpc":"2.0","id":"2","method":"paxm.search","params":{` +
		`"text":"What blocks deployment?","limit":2,"metadata":{` +
		`"agent_id":"metadata-consumer","session_id":"consumer-session","task_ref":"task-1"}}}`
	response := s.serve(request)
	s.Nil(response["error"])
	s.Require().Len(s.transport.requests, 1)
	s.Equal("/v1/notes/recall", s.transport.requests[0].URL.Path)

	var payload struct {
		Actor struct {
			AgentID   string `json:"agent_id"`
			SessionID string `json:"session_id"`
		} `json:"actor"`
		TokenBudget int `json:"token_budget"`
		MaxItems    int `json:"max_items"`
	}
	s.Require().NoError(json.Unmarshal(s.transport.bodies[0], &payload))
	s.Equal("metadata-consumer", payload.Actor.AgentID)
	s.Equal("consumer-session", payload.Actor.SessionID)
	s.Equal(200, payload.TokenBudget)
	s.Equal(2, payload.MaxItems)
	var temporalPayload struct {
		Query string `json:"query"`
	}
	s.Require().NoError(json.Unmarshal(s.transport.bodies[0], &temporalPayload))
	s.Equal("What blocks deployment?", temporalPayload.Query)

	result, ok := response["result"].(map[string]any)
	s.Require().True(ok)
	hits, ok := result["hits"].([]any)
	s.Require().True(ok)
	s.Require().Len(hits, 1)
	hit, ok := hits[0].(map[string]any)
	s.Require().True(ok)
	s.Equal("[blocker] Credentials are missing.", hit["text"])
	s.InDelta(float64(1), hit["score"], 0.0001)
}

func (s *providerSuite) TestSearchReturnsPerNoteOriginAttribution() {
	s.transport.responseBody = `{"revision":"note-1:2","items":["[handoff] Continue release."],"tokens":8,"details":[` +
		`{"note_id":"note-1","revision":2,"text":"[handoff] Continue release.",` +
		`"relevance":0.75,"certainty":"proposed",` +
		`"origin":{"user_id":"owner","agent_id":"producer","session_id":"producer-session"}}]}`
	response := s.serve(`{"jsonrpc":"2.0","id":"origin","method":"paxm.search","params":{` +
		`"text":"continue","limit":5,"metadata":{"session_id":"consumer-session"}}}`)
	s.Nil(response["error"])
	result, ok := response["result"].(map[string]any)
	s.Require().True(ok)
	hits, ok := result["hits"].([]any)
	s.Require().True(ok)
	s.Require().Len(hits, 1)
	hit, ok := hits[0].(map[string]any)
	s.Require().True(ok)
	s.Equal("note-1:2", hit["id"])
	origin, ok := hit["origin"].(map[string]any)
	s.Require().True(ok)
	s.Equal("owner", origin["user_id"])
	s.Equal("producer", origin["agent_id"])
	s.Equal("producer-session", origin["session_id"])
	s.InDelta(0.75, hit["relevance"], 0.0001)
	s.InDelta(0.75, hit["score"], 0.0001)
	metadata, ok := hit["metadata"].(map[string]any)
	s.Require().True(ok)
	s.Equal("proposed", metadata["certainty"])
}

func (s *providerSuite) TestPutAcceptsTopLevelPaxmOrigin() {
	request := `{"jsonrpc":"2.0","id":"origin-put","method":"paxm.put","params":{` +
		`"id":"origin-event","text":"handoff","created_at":"2026-07-14T12:00:00Z",` +
		`"origin":{"user_id":"origin-user","agent_id":"origin-agent","session_id":"origin-session"},"metadata":{}}}`
	response := s.serve(request)
	s.Nil(response["error"])
	var payload struct {
		Events []struct {
			Actor struct {
				UserID    string `json:"user_id"`
				AgentID   string `json:"agent_id"`
				SessionID string `json:"session_id"`
			} `json:"actor"`
		} `json:"events"`
	}
	s.Require().NoError(json.Unmarshal(s.transport.bodies[0], &payload))
	s.Equal("origin-user", payload.Events[0].Actor.UserID)
	s.Equal("origin-agent", payload.Events[0].Actor.AgentID)
	s.Equal("origin-session", payload.Events[0].Actor.SessionID)
}

func (s *providerSuite) TestProtocolAndValidationErrorsAreJSONRPCResponses() {
	tests := []struct {
		name    string
		request string
	}{
		{name: "unknown method", request: `{"jsonrpc":"2.0","id":"3","method":"paxm.delete","params":{}}`},
		{name: "put missing session", request: `{"jsonrpc":"2.0","id":"4","method":"paxm.put","params":{"text":"value","created_at":"2026-07-14T12:00:00Z"}}`},
		{name: "search missing session", request: `{"jsonrpc":"2.0","id":"5","method":"paxm.search","params":{"text":"value"}}`},
		{name: "invalid sequence", request: `{"jsonrpc":"2.0","id":"6","method":"paxm.put","params":{"text":"value","created_at":"2026-07-14T12:00:00Z","metadata":{"session_id":"s","sequence":"zero"}}}`},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			response := s.serve(test.request)
			s.NotNil(response["error"])
		})
	}
	s.Contains(s.logs.String(), `"msg":"paxm request failed"`)
	s.Contains(s.logs.String(), `"method":"paxm.delete"`)
	s.NotContains(s.logs.String(), `"text":"value"`)
}

func (s *providerSuite) TestHealthCapabilitiesAndBatch() {
	tests := []struct {
		name    string
		request string
		calls   int
	}{
		{name: "health", request: `{"jsonrpc":"2.0","id":"7","method":"paxm.health","params":{}}`, calls: 1},
		{name: "capabilities", request: `{"jsonrpc":"2.0","id":"8","method":"paxm.capabilities","params":{}}`},
		{name: "empty batch", request: `{"jsonrpc":"2.0","id":"9","method":"paxm.putBatch","params":{"items":[]}}`},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			s.transport.requests = nil
			s.transport.bodies = nil
			response := s.serve(test.request)
			s.Nil(response["error"])
			s.Len(s.transport.requests, test.calls)
		})
	}
}

func (s *providerSuite) TestPutBatchSendsOneSessionBatchAndPreservesRefOrder() {
	request := `{"jsonrpc":"2.0","id":"batch-1","method":"paxm.putBatch","params":{"items":[` +
		`{"id":"event-2","text":"second","created_at":"2026-07-14T12:00:02Z","metadata":{"session_id":"session-a","sequence":"2"}},` +
		`{"id":"event-1","text":"first","created_at":"2026-07-14T12:00:01Z","metadata":{"session_id":"session-a","sequence":"1"}}]}}`
	response := s.serve(request)
	s.Nil(response["error"])
	s.Require().Len(s.transport.requests, 1)

	var payload struct {
		Complete bool `json:"complete"`
		Events   []struct {
			ID       string `json:"id"`
			Sequence int64  `json:"sequence"`
		} `json:"events"`
	}
	s.Require().NoError(json.Unmarshal(s.transport.bodies[0], &payload))
	s.True(payload.Complete)
	s.Require().Len(payload.Events, 2)
	s.Equal("event-2", payload.Events[0].ID)
	s.Equal("event-1", payload.Events[1].ID)

	result, ok := response["result"].(map[string]any)
	s.Require().True(ok)
	refs, ok := result["refs"].([]any)
	s.Require().True(ok)
	s.Require().Len(refs, 2)
	first, ok := refs[0].(map[string]any)
	s.Require().True(ok)
	second, ok := refs[1].(map[string]any)
	s.Require().True(ok)
	s.Equal("event-2", first["id"])
	s.Equal("event-1", second["id"])
}

func (s *providerSuite) TestPutBatchGroupsByResolvedActorAndSession() {
	request := `{"jsonrpc":"2.0","id":"batch-2","method":"paxm.putBatch","params":{"items":[` +
		`{"id":"a-1","text":"a1","created_at":"2026-07-14T12:00:01Z","metadata":{"session_id":"session-a","sequence":"1"}},` +
		`{"id":"b-1","text":"b1","created_at":"2026-07-14T12:00:02Z","metadata":{"session_id":"session-b","sequence":"1"}},` +
		`{"id":"a-2","text":"a2","created_at":"2026-07-14T12:00:03Z","metadata":{"session_id":"session-a","sequence":"2"}},` +
		`{"id":"c-1","text":"c1","created_at":"2026-07-14T12:00:04Z","metadata":{"session_id":"session-a","agent_id":"other-agent","sequence":"1"}}]}}`
	response := s.serve(request)
	s.Nil(response["error"])
	s.Require().Len(s.transport.requests, 3)

	wantIDs := [][]string{{"a-1", "a-2"}, {"b-1"}, {"c-1"}}
	for index, body := range s.transport.bodies {
		var payload struct {
			Events []struct {
				ID string `json:"id"`
			} `json:"events"`
		}
		s.Require().NoError(json.Unmarshal(body, &payload))
		ids := make([]string, len(payload.Events))
		for eventIndex := range payload.Events {
			ids[eventIndex] = payload.Events[eventIndex].ID
		}
		s.Equal(wantIDs[index], ids)
	}

	result, ok := response["result"].(map[string]any)
	s.Require().True(ok)
	refs, ok := result["refs"].([]any)
	s.Require().True(ok)
	wantRefs := []string{"a-1", "b-1", "a-2", "c-1"}
	for index, ref := range refs {
		mapped, mapOK := ref.(map[string]any)
		s.Require().True(mapOK)
		s.Equal(wantRefs[index], mapped["id"])
	}
}

func (s *providerSuite) TestPutBatchValidatesEveryItemBeforeSending() {
	request := `{"jsonrpc":"2.0","id":"batch-3","method":"paxm.putBatch","params":{"items":[` +
		`{"id":"valid","text":"valid","created_at":"2026-07-14T12:00:01Z","metadata":{"session_id":"session-a"}},` +
		`{"id":"invalid","text":"","created_at":"2026-07-14T12:00:02Z","metadata":{"session_id":"session-a"}}]}}`
	response := s.serve(request)
	s.NotNil(response["error"])
	s.Empty(s.transport.requests)
}

func (s *providerSuite) serve(request string) map[string]any {
	var output bytes.Buffer
	err := s.provider.ServeOne(context.Background(), strings.NewReader(request), &output)
	s.Require().NoError(err)
	var response map[string]any
	s.Require().NoError(json.Unmarshal(output.Bytes(), &response))
	return response
}

func (t *providerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var body []byte
	if request.Body != nil {
		var err error
		body, err = io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
	}
	t.requests = append(t.requests, request)
	t.bodies = append(t.bodies, body)
	responseBody := t.responseBody
	if responseBody == "" && request.URL.Path == "/v1/notes/recall" {
		responseBody = `{"revision":"note-1:1","items":["[blocker] Credentials are missing."],"tokens":9}`
	}
	if responseBody == "" {
		responseBody = `{}`
	}
	return &http.Response{
		StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(responseBody)),
		Header: make(http.Header), Request: request,
	}, nil
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	tests := []paxmprovider.Config{
		{},
		{BaseURL: "://bad", APIKey: "key", UserID: "user", AgentID: "agent"},
		{BaseURL: "http://runtime", UserID: "user", AgentID: "agent"},
		{BaseURL: "http://runtime", APIKey: "key", UserID: "user", AgentID: "agent", RequestTimeout: -time.Second},
	}
	for _, config := range tests {
		_, err := paxmprovider.New(config)
		if err == nil {
			t.Fatalf("New(%+v) succeeded", config)
		}
	}
}

func TestPutUsesTimestampSequenceAndStableID(t *testing.T) {
	transport := &providerTransport{}
	provider, err := paxmprovider.New(paxmprovider.Config{
		BaseURL: "http://runtime", APIKey: "key", UserID: "user", AgentID: "agent",
		Client: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, time.July, 14, 12, 0, 0, 123, time.UTC)
	request := `{"jsonrpc":"2.0","id":"10","method":"paxm.put","params":{` +
		`"text":"value","created_at":"` + createdAt.Format(time.RFC3339Nano) + `",` +
		`"metadata":{"session_id":"session"}}}`
	var first bytes.Buffer
	if err := provider.ServeOne(context.Background(), strings.NewReader(request), &first); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Events []struct {
			ID       string `json:"id"`
			Sequence int64  `json:"sequence"`
		} `json:"events"`
	}
	if err := json.Unmarshal(transport.bodies[0], &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Events[0].ID == "" || payload.Events[0].Sequence != createdAt.UnixNano() {
		t.Fatalf("unexpected event: %+v", payload.Events[0])
	}
}
