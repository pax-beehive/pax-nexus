package extractor_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
	"github.com/stretchr/testify/suite"
)

type openAISuite struct {
	suite.Suite
}

func TestOpenAISuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(openAISuite))
}

func (s *openAISuite) TestExtractsGroundedCandidates() {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		s.Equal("Bearer secret", request.Header.Get("Authorization"))
		return response(http.StatusOK, `{
  "choices": [{"message": {"role": "assistant", "content": "{\"candidates\":[{\"id\":\"candidate-1\",\"action\":\"create\",\"kind\":\"blocker\",\"subject\":\"release\",\"body\":\"Tests are failing.\",\"task_ref\":\"release-42\",\"evidence_event_ids\":[\"event-1\"]}]}"}}],
  "usage": {"prompt_tokens": 25, "completion_tokens": 12}
}`), nil
	})}

	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", APIKey: "secret", Model: "small-model", PromptVersion: "v1", Client: client,
	})
	s.Require().NoError(err)
	result, err := adapter.Extract(context.Background(), extractorSlice())
	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Equal("extract-checksum-1", result.Candidates[0].ID)
	s.Equal("producer", result.Candidates[0].Origin.AgentID)
	s.Equal([]string{"event-1"}, result.Candidates[0].EvidenceEventIDs)
	s.Equal(25, result.Usage.InputTokens)
}

func (s *openAISuite) TestExtractsTemporalMetadataAndSourceTime() {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"choices":[{"message":{"content":"{\"candidates\":[{\"action\":\"update\",\"kind\":\"status\",\"subject\":\"release readiness\",\"body\":\"The release deadline is July 18.\",\"valid_at\":\"2026-07-18T00:00:00Z\",\"related_subjects\":[\"release validation\"],\"evidence_event_ids\":[\"event-1\"]}]}"}}]}`), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{BaseURL: "http://extractor.test", Model: "model", Client: client})
	s.Require().NoError(err)
	slice := extractorSlice()
	slice.Events[0].OccurredAt = time.Date(2026, time.July, 15, 9, 30, 0, 0, time.UTC)

	result, err := adapter.Extract(context.Background(), slice)
	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Require().NotNil(result.Candidates[0].ValidAt)
	s.Equal(time.Date(2026, time.July, 18, 0, 0, 0, 0, time.UTC), *result.Candidates[0].ValidAt)
	s.Equal([]string{"release validation"}, result.Candidates[0].RelatedSubjects)
	s.Equal(slice.Events[0].OccurredAt, result.Candidates[0].SourceOccurredAt)
}

func (s *openAISuite) TestRejectsInvalidResponses() {
	tooMany := make([]string, 11)
	for index := range tooMany {
		tooMany[index] = fmt.Sprintf(`{"action":"create","kind":"status","subject":"subject-%d","body":"body","evidence_event_ids":["event-1"]}`, index)
	}
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "model error", status: http.StatusBadGateway, body: "upstream failed"},
		{name: "malformed envelope", status: http.StatusOK, body: "not-json"},
		{name: "malformed candidates", status: http.StatusOK, body: `{"choices":[{"message":{"content":"not-json"}}]}`},
		{name: "missing required candidate fields", status: http.StatusOK, body: `{"choices":[{"message":{"content":"{\"candidates\":[{\"action\":\"create\",\"kind\":\"status\",\"description\":\"value\",\"citations\":[\"event-1\"]}]}"}}]}`},
		{name: "unknown evidence", status: http.StatusOK, body: `{"choices":[{"message":{"content":"{\"candidates\":[{\"action\":\"create\",\"kind\":\"status\",\"subject\":\"release\",\"body\":\"body\",\"evidence_event_ids\":[\"unknown\"]}]}"}}]}`},
		{name: "too many candidates", status: http.StatusOK, body: `{"choices":[{"message":{"content":` + fmt.Sprintf("%q", `{"candidates":[`+strings.Join(tooMany, ",")+`]}`) + `}}]}`},
	}

	for _, test := range tests {
		s.Run(test.name, func() {
			client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return response(test.status, test.body), nil
			})}
			adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{BaseURL: "http://extractor.test", Model: "model", Client: client})
			s.Require().NoError(err)
			_, err = adapter.Extract(context.Background(), extractorSlice())
			s.Require().Error(err)
		})
	}
}

func (s *openAISuite) TestRejectsCandidateGroundedOnlyInOverlap() {
	slice := extractorSlice()
	slice.Events = append([]teamnote.SessionEvent{{ID: "event-0", Actor: slice.Actor, Sequence: 0, Type: "assistant", Content: "old"}}, slice.Events...)
	slice.OverlapEventIDs = []string{"event-0"}
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"choices":[{"message":{"content":"{\"candidates\":[{\"action\":\"create\",\"kind\":\"status\",\"subject\":\"release\",\"body\":\"old\",\"evidence_event_ids\":[\"event-0\"]}]}"}}]}`), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{BaseURL: "http://extractor.test", Model: "model", Client: client})
	s.Require().NoError(err)
	_, err = adapter.Extract(context.Background(), slice)
	s.Require().ErrorIs(err, extractor.ErrInvalidModelResponse)
}

func (s *openAISuite) TestFixtureNoopAndCodeFence() {
	slice := extractorSlice()
	want := extractor.Result{Usage: extractor.Usage{InputTokens: 4}}
	fixture := extractor.Fixture{ByChecksum: map[string]extractor.Result{slice.InputChecksum: want}}
	got, err := fixture.Extract(context.Background(), slice)
	s.Require().NoError(err)
	s.Equal(want, got)

	_, err = fixture.Extract(context.Background(), sessionlake.Slice{InputChecksum: "missing"})
	s.Require().Error(err)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = fixture.Extract(canceled, slice)
	s.Require().ErrorIs(err, context.Canceled)

	got, err = (extractor.Noop{}).Extract(context.Background(), slice)
	s.Require().NoError(err)
	s.Empty(got.Candidates)
	_, err = (extractor.Noop{}).Extract(canceled, slice)
	s.Require().ErrorIs(err, context.Canceled)

	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"choices":[{"message":{"content":"`+"```json\\n"+`{\"candidates\":[]}`+"\\n```"+`"}}]}`), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{BaseURL: "http://extractor.test", Model: "model", Client: client})
	s.Require().NoError(err)
	got, err = adapter.Extract(context.Background(), slice)
	s.Require().NoError(err)
	s.Empty(got.Candidates)
}

func (s *openAISuite) TestRejectsInvalidConfiguration() {
	tests := []extractor.OpenAIConfig{
		{},
		{BaseURL: "://bad", Model: "model"},
		{BaseURL: "http://extractor.test"},
	}
	for _, config := range tests {
		_, err := extractor.NewOpenAI(config)
		s.Error(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func extractorSlice() sessionlake.Slice {
	actor := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "session-1"}
	return sessionlake.Slice{
		Actor: actor, FromSequence: 1, ToSequence: 1, InputChecksum: "checksum",
		NewEventIDs: []string{"event-1"},
		Events: []teamnote.SessionEvent{{
			ID: "event-1", Actor: actor, Sequence: 1, Type: "assistant",
			Content: "Tests are failing.", TaskRef: "release-42", OccurredAt: time.Now().UTC(),
		}},
	}
}
