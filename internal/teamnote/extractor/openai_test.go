package extractor_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
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
	var calls []extractor.ProviderCall
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		s.Equal("Bearer secret", request.Header.Get("Authorization"))
		return response(http.StatusOK, `{
  "choices": [{"message": {"role": "assistant", "content": "{\"candidates\":[{\"id\":\"candidate-1\",\"action\":\"create\",\"kind\":\"blocker\",\"subject\":\"release\",\"body\":\"Tests are failing.\",\"task_ref\":\"release-42\",\"evidence_event_ids\":[\"event-1\"]}]}"}}],
  "usage": {"prompt_tokens": 25, "completion_tokens": 12}
}`), nil
	})}

	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", APIKey: "secret", Model: "small-model", PromptVersion: "v1", Client: client,
		ProviderCallObserver: func(call extractor.ProviderCall) { calls = append(calls, call) },
	})
	s.Require().NoError(err)
	result, err := adapter.Extract(context.Background(), extractorSlice())
	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Equal("extract-checksum-1", result.Candidates[0].ID)
	s.Equal("producer", result.Candidates[0].Origin.AgentID)
	s.Equal([]string{"event-1"}, result.Candidates[0].EvidenceEventIDs)
	s.Equal(25, result.Usage.InputTokens)
	s.Require().Len(calls, 1)
	s.Equal(extractor.ProviderCallPrimary, calls[0].Type)
	s.Equal(http.StatusOK, calls[0].HTTPStatus)
	s.Equal(25, calls[0].Usage.InputTokens)
	s.Empty(calls[0].Error)
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

func (s *openAISuite) TestModelCannotAssignAudienceIdentity() {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"choices":[{"message":{"content":"{\"candidates\":[{\"action\":\"create\",\"kind\":\"handoff\",\"subject\":\"review\",\"body\":\"User_7 reviews the pack.\",\"audience_agent_ids\":[\"User_7\"],\"evidence_event_ids\":[\"event-1\"]}]}"}}]}`), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{BaseURL: "http://extractor.test", Model: "model", Client: client})
	s.Require().NoError(err)
	result, err := adapter.Extract(context.Background(), extractorSlice())
	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Empty(result.Candidates[0].AudienceAgentIDs)
}

func (s *openAISuite) TestProposalCannotBecomeCanonicalOwnership() {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"choices":[{"message":{"content":"{\"candidates\":[{\"action\":\"create\",\"kind\":\"status\",\"subject\":\"compliance owner\",\"body\":\"Compliance is designated as owner.\",\"evidence_event_ids\":[\"event-1\"]}]}"}}]}`), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{BaseURL: "http://extractor.test", Model: "model", Client: client})
	s.Require().NoError(err)
	slice := extractorSlice()
	slice.Events[0].Content = "I propose Compliance as the owner for the review."

	result, err := adapter.Extract(context.Background(), slice)

	s.Require().NoError(err)
	s.Empty(result.Candidates)
	s.Require().Len(result.Rejections, 1)
	s.Contains(result.Rejections[0].Reason, "non-committal proposal")
}

func (s *openAISuite) TestExplicitApprovalCanBecomeCanonicalOwnership() {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"choices":[{"message":{"content":"{\"candidates\":[{\"action\":\"create\",\"kind\":\"status\",\"subject\":\"compliance owner\",\"body\":\"Compliance is designated as owner.\",\"evidence_event_ids\":[\"event-1\"]}]}"}}]}`), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{BaseURL: "http://extractor.test", Model: "model", Client: client})
	s.Require().NoError(err)
	slice := extractorSlice()
	slice.Events[0].Content = "I propose Compliance as owner; the team approved and assigned Compliance."

	result, err := adapter.Extract(context.Background(), slice)

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
}

func (s *openAISuite) TestV1AdmissionIgnoresAdjacentRequestMarker() {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"choices":[{"message":{"content":"{\"candidates\":[{\"action\":\"create\",\"kind\":\"status\",\"subject\":\"compliance owner\",\"body\":\"Compliance owns the log.\",\"evidence_event_ids\":[\"event-1\"]}]}"}}]}`), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{BaseURL: "http://extractor.test", Model: "model", Client: client})
	s.Require().NoError(err)
	slice := extractorSlice()
	slice.Events[0].Content = "Compliance owns the log and should publish it today."

	result, err := adapter.Extract(context.Background(), slice)

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
}

func (s *openAISuite) TestRejectsInvalidResponses() {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "model error", status: http.StatusBadGateway, body: "upstream failed"},
		{name: "malformed envelope", status: http.StatusOK, body: "not-json"},
		{name: "malformed candidates", status: http.StatusOK, body: `{"choices":[{"message":{"content":"not-json"}}]}`},
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

func (s *openAISuite) TestDropsInadmissibleCandidatesWithRejections() {
	tooMany := make([]string, 11)
	for index := range tooMany {
		tooMany[index] = fmt.Sprintf(`{"action":"create","kind":"status","subject":"subject-%d","body":"body","evidence_event_ids":["event-1"]}`, index)
	}
	tests := []struct {
		name          string
		body          string
		wantKept      int
		wantRejection string
	}{
		{name: "missing required candidate fields", wantKept: 0, wantRejection: "missing subject",
			body: `{"choices":[{"message":{"content":"{\"candidates\":[{\"action\":\"create\",\"kind\":\"status\",\"description\":\"value\",\"citations\":[\"event-1\"]}]}"}}]}`},
		{name: "unknown evidence", wantKept: 0, wantRejection: "cites unknown event",
			body: `{"choices":[{"message":{"content":"{\"candidates\":[{\"action\":\"create\",\"kind\":\"status\",\"subject\":\"release\",\"body\":\"body\",\"evidence_event_ids\":[\"unknown\"]}]}"}}]}`},
		{name: "too many candidates", wantKept: 10, wantRejection: "overflow",
			body: `{"choices":[{"message":{"content":` + fmt.Sprintf("%q", `{"candidates":[`+strings.Join(tooMany, ",")+`]}`) + `}}]}`},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return response(http.StatusOK, test.body), nil
			})}
			adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{BaseURL: "http://extractor.test", Model: "model", Client: client})
			s.Require().NoError(err)
			result, err := adapter.Extract(context.Background(), extractorSlice())
			s.Require().NoError(err)
			s.Len(result.Candidates, test.wantKept)
			s.Require().NotEmpty(result.Rejections)
			s.Contains(result.Rejections[0].Reason, test.wantRejection)
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
	result, err := adapter.Extract(context.Background(), slice)
	s.Require().NoError(err)
	s.Empty(result.Candidates)
	s.Require().Len(result.Rejections, 1)
	s.Contains(result.Rejections[0].Reason, "cites only overlap events")
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

func (s *openAISuite) TestRollingContextCarriesKnowledgeAcrossSessions() {
	store := newMemoryEpisodeStore()
	var requests []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		s.Require().NoError(err)
		requests = append(requests, string(body))
		eventID := "event-1"
		action := "create"
		if strings.Contains(string(body), "event-2") {
			eventID = "event-2"
			action = "update"
		}
		content := fmt.Sprintf(`{"candidates":[{"action":%q,"kind":"status","subject":"release date","identity_ref":"decision/release-date","body":"Current release state.","evidence_event_ids":[%q]}]}`, action, eventID)
		return response(http.StatusOK, fmt.Sprintf(`{"choices":[{"message":{"content":%q}}],"usage":{"prompt_tokens":120,"completion_tokens":20,"prompt_cache_hit_tokens":80,"prompt_cache_miss_tokens":40}}`, content)), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: store,
		CompactStartTokens: 10_000, CompactTokens: 10_000,
	})
	s.Require().NoError(err)
	ctx := teamnote.WithScope(context.Background(), "scope-a")
	first := extractorSlice()
	_, err = adapter.Extract(ctx, first)
	s.Require().NoError(err)

	second := extractorSlice()
	second.Actor = teamnote.Actor{UserID: "owner", AgentID: "reviewer", SessionID: "session-2"}
	second.InputChecksum = "checksum-2"
	second.NewEventIDs = []string{"event-2"}
	second.Events[0].ID = "event-2"
	second.Events[0].Actor = second.Actor
	result, err := adapter.Extract(ctx, second)
	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Equal(teamnote.ActionUpdate, result.Candidates[0].Action)
	s.Equal("decision/release-date", result.Candidates[0].IdentityRef)
	s.Len(requests, 2)
	s.Contains(requests[1], "event-1")
	s.Contains(requests[1], "decision/release-date")
	s.Equal(80, result.Usage.PromptCacheHitTokens)

	episode, ok, err := store.LoadEpisode(ctx, extractor.EpisodeKey{ScopeID: "scope-a", TaskRef: "release-42"})
	s.Require().NoError(err)
	s.True(ok)
	s.Equal(2, episode.EventCount)
	s.Equal(int64(1), episode.Checkpoint.SourceCursors["owner/producer/session-1"])
	s.Equal(int64(1), episode.Checkpoint.SourceCursors["owner/reviewer/session-2"])
}

func (s *openAISuite) TestRollingContextKeepsCurrentEvidenceWhenModelAlsoCitesHistory() {
	store := newMemoryEpisodeStore()
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		s.Require().NoError(err)
		calls.Add(1)
		evidence := `"event-1"`
		if strings.Contains(string(body), "event-2") {
			evidence = `"event-1","event-2"`
		}
		content := fmt.Sprintf(`{"candidates":[{"action":"create","kind":"status","subject":"release","body":"Release state.","evidence_event_ids":[%s]}]}`, evidence)
		return response(http.StatusOK, fmt.Sprintf(`{"choices":[{"message":{"content":%q}}]}`, content)), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: store,
	})
	s.Require().NoError(err)
	ctx := teamnote.WithScope(context.Background(), "scope-history-evidence")
	_, err = adapter.Extract(ctx, extractionEventSlice("event-1", "checksum-1", 1))
	s.Require().NoError(err)

	second := extractionEventSlice("event-2", "checksum-2", 2)
	result, err := adapter.Extract(ctx, second)
	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Equal([]string{"event-2"}, result.Candidates[0].EvidenceEventIDs)

	replayed, err := adapter.Extract(ctx, second)
	s.Require().NoError(err)
	s.Equal(result.Candidates, replayed.Candidates)
	s.Equal(int32(2), calls.Load())
}

func (s *openAISuite) TestRollingContextRejectsUnknownEvidenceAlongsideCurrentEvent() {
	store := newMemoryEpisodeStore()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		s.Require().NoError(err)
		evidence := `"event-1"`
		if strings.Contains(string(body), "event-2") {
			evidence = `"unknown","event-2"`
		}
		content := fmt.Sprintf(`{"candidates":[{"action":"create","kind":"status","subject":"release","body":"Release state.","evidence_event_ids":[%s]}]}`, evidence)
		return response(http.StatusOK, fmt.Sprintf(`{"choices":[{"message":{"content":%q}}]}`, content)), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: store,
	})
	s.Require().NoError(err)
	ctx := teamnote.WithScope(context.Background(), "scope-unknown-evidence")
	_, err = adapter.Extract(ctx, extractionEventSlice("event-1", "checksum-1", 1))
	s.Require().NoError(err)
	result, err := adapter.Extract(ctx, extractionEventSlice("event-2", "checksum-2", 2))
	s.Require().NoError(err)
	s.Empty(result.Candidates)
	s.Require().Len(result.Rejections, 1)
	s.Contains(result.Rejections[0].Reason, "cites unknown event")
}

func (s *openAISuite) TestRollingContextCompactsIntoStructuredCheckpoint() {
	store := newMemoryEpisodeStore()
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		s.Require().NoError(err)
		calls++
		if strings.Contains(string(body), "KNOWLEDGE CONTEXT CHECKPOINT COMPACTION") {
			checkpoint := `{"active_knowledge":[{"memory_id":"decision/release-date","kind":"status","subject":"release date","body":"Release is July 22.","evidence_event_ids":["event-1"]}],"resolved_knowledge":[],"open_questions":[],"evidence_index":{"decision/release-date":["event-1"]},"source_cursors":{}}`
			return response(http.StatusOK, fmt.Sprintf(`{"choices":[{"message":{"content":%q}}],"usage":{"prompt_tokens":200,"completion_tokens":30}}`, checkpoint)), nil
		}
		eventID := "event-1"
		if strings.Contains(string(body), "event-2") {
			eventID = "event-2"
			s.Contains(string(body), "checkpoint_loaded")
			s.Contains(string(body), "decision/release-date")
		}
		content := fmt.Sprintf(`{"candidates":[{"action":"create","kind":"status","subject":"release date","identity_ref":"decision/release-date","body":"Release state.","evidence_event_ids":[%q]}]}`, eventID)
		return response(http.StatusOK, fmt.Sprintf(`{"choices":[{"message":{"content":%q}}],"usage":{"prompt_tokens":100,"completion_tokens":10}}`, content)), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: store,
		CompactionEnabled:  true,
		CompactStartTokens: 1, CompactTokens: 1,
	})
	s.Require().NoError(err)
	ctx := teamnote.WithScope(context.Background(), "scope-a")
	_, err = adapter.Extract(ctx, extractorSlice())
	s.Require().NoError(err)
	second := extractorSlice()
	second.InputChecksum = "checksum-2"
	second.NewEventIDs = []string{"event-2"}
	second.Events[0].ID = "event-2"
	result, err := adapter.Extract(ctx, second)
	s.Require().NoError(err)
	s.Equal(3, calls)
	s.Equal(300, result.Usage.InputTokens)
	episode, ok, err := store.LoadEpisode(ctx, extractor.EpisodeKey{ScopeID: "scope-a", TaskRef: "release-42"})
	s.Require().NoError(err)
	s.True(ok)
	s.Equal(1, episode.CompactionCount)
	s.Require().Len(episode.Checkpoint.ActiveKnowledge, 1)
	s.Equal("decision/release-date", episode.Checkpoint.ActiveKnowledge[0].MemoryID)
}

func (s *openAISuite) TestRollingContextReplaysSavedRunWithoutCallingModel() {
	store := newMemoryEpisodeStore()
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		calls++
		content := `{"candidates":[{"action":"create","kind":"status","subject":"release","identity_ref":"decision/release","body":"Release is ready.","evidence_event_ids":["event-1"]}]}`
		return response(http.StatusOK, fmt.Sprintf(`{"choices":[{"message":{"content":%q}}]}`, content)), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: store,
	})
	s.Require().NoError(err)
	ctx := teamnote.WithScope(context.Background(), "scope-replay")
	slice := extractorSlice()
	first, err := adapter.Extract(ctx, slice)
	s.Require().NoError(err)
	second, err := adapter.Extract(ctx, slice)
	s.Require().NoError(err)
	s.Equal(1, calls)
	s.Equal(first.Candidates, second.Candidates)
	s.Zero(second.Usage.InputTokens)
}

func (s *openAISuite) TestRollingContextDoesNotCompactWhenDisabled() {
	store := newMemoryEpisodeStore()
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		s.Require().NoError(err)
		s.NotContains(string(body), "KNOWLEDGE CONTEXT CHECKPOINT COMPACTION")
		calls.Add(1)
		eventID := "event-1"
		if strings.Contains(string(body), "event-2") {
			eventID = "event-2"
		}
		content := fmt.Sprintf(`{"candidates":[{"action":"create","kind":"status","subject":"release","body":"Release state.","evidence_event_ids":[%q]}]}`, eventID)
		return response(http.StatusOK, fmt.Sprintf(`{"choices":[{"message":{"content":%q}}],"usage":{"prompt_tokens":100,"completion_tokens":10}}`, content)), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: store,
		CompactStartTokens: 1, CompactTokens: 1,
	})
	s.Require().NoError(err)
	ctx := teamnote.WithScope(context.Background(), "scope-no-compaction")
	_, err = adapter.Extract(ctx, extractionEventSlice("event-1", "checksum-1", 1))
	s.Require().NoError(err)
	_, err = adapter.Extract(ctx, extractionEventSlice("event-2", "checksum-2", 2))
	s.Require().NoError(err)
	s.Equal(int32(2), calls.Load())

	episode, ok, err := store.LoadEpisode(ctx, extractor.EpisodeKey{ScopeID: "scope-no-compaction", TaskRef: "release-42"})
	s.Require().NoError(err)
	s.True(ok)
	s.Zero(episode.CompactionCount)
	s.Greater(episode.EstimatedTokens, 1)
}

func (s *openAISuite) TestRollingSummaryRunsAsynchronouslyAndKeepsRecentTail() {
	store := newMemoryEpisodeStore()
	providerCalls := make(chan extractor.ProviderCall, 10)
	summaryStarted := make(chan struct{})
	releaseSummary := make(chan struct{})
	summaryReturned := make(chan struct{})
	var summaryCalls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		s.Require().NoError(err)
		if strings.Contains(string(body), "Update the rolling continuity summary") {
			if summaryCalls.Add(1) == 1 {
				close(summaryStarted)
				<-releaseSummary
				close(summaryReturned)
			}
			content := `{"summary":"Release remains planned; event-1 established the initial state."}`
			return response(http.StatusOK, fmt.Sprintf(`{"choices":[{"message":{"content":%q}}],"usage":{"prompt_tokens":40,"completion_tokens":10}}`, content)), nil
		}
		eventID := "event-1"
		for _, candidate := range []string{"event-2", "event-3"} {
			if strings.Contains(string(body), candidate) {
				eventID = candidate
			}
		}
		if eventID == "event-3" {
			s.Contains(string(body), "Release remains planned")
			s.Contains(string(body), "event-2")
		}
		content := fmt.Sprintf(`{"candidates":[{"action":"create","kind":"status","subject":"release","body":"Release state.","evidence_event_ids":[%q]}]}`, eventID)
		return response(http.StatusOK, fmt.Sprintf(`{"choices":[{"message":{"content":%q}}],"usage":{"prompt_tokens":100,"completion_tokens":10}}`, content)), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: store,
		SummaryEnabled: true, SummaryTriggerTokens: 1, SummaryTailTokens: 1,
		ProviderCallObserver: func(call extractor.ProviderCall) { providerCalls <- call },
	})
	s.Require().NoError(err)
	ctx := teamnote.WithScope(context.Background(), "scope-summary")
	_, err = adapter.Extract(ctx, extractionEventSlice("event-1", "checksum-1", 1))
	s.Require().NoError(err)
	select {
	case <-summaryStarted:
	case <-time.After(time.Second):
		s.FailNow("periodic summary did not start")
	}

	done := make(chan error, 1)
	go func() {
		_, extractErr := adapter.Extract(ctx, extractionEventSlice("event-2", "checksum-2", 2))
		done <- extractErr
	}()
	select {
	case extractErr := <-done:
		s.Require().NoError(extractErr)
	case <-time.After(time.Second):
		s.FailNow("extraction waited for periodic summary")
	}
	close(releaseSummary)
	<-summaryReturned
	time.Sleep(20 * time.Millisecond)

	_, err = adapter.Extract(ctx, extractionEventSlice("event-3", "checksum-3", 3))
	s.Require().NoError(err)
	episode, ok, err := store.LoadEpisode(ctx, extractor.EpisodeKey{ScopeID: "scope-summary", TaskRef: "release-42"})
	s.Require().NoError(err)
	s.True(ok)
	s.Equal(1, episode.Checkpoint.SummaryCount)
	s.Equal(1, episode.Checkpoint.SummaryAttempts)
	s.Zero(episode.Checkpoint.SummaryFailures)
	s.NotEmpty(episode.Checkpoint.Summary)
	callTypes := make(map[extractor.ProviderCallType]int)
	for {
		select {
		case call := <-providerCalls:
			callTypes[call.Type]++
		default:
			s.GreaterOrEqual(callTypes[extractor.ProviderCallPrimary], 3)
			s.GreaterOrEqual(callTypes[extractor.ProviderCallSummary], 1)
			return
		}
	}
}

func (s *openAISuite) TestWaitForBackgroundPersistsSummaryWithoutAnotherSlice() {
	store := newMemoryEpisodeStore()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		s.Require().NoError(err)
		if strings.Contains(string(body), "Update the rolling continuity summary") {
			content := `{"summary":"Release remains planned."}`
			return response(http.StatusOK, fmt.Sprintf(`{"choices":[{"message":{"content":%q}}],"usage":{"prompt_tokens":40,"completion_tokens":10}}`, content)), nil
		}
		content := `{"candidates":[{"action":"create","kind":"status","subject":"release","body":"Release state.","evidence_event_ids":["event-1"]}]}`
		return response(http.StatusOK, fmt.Sprintf(`{"choices":[{"message":{"content":%q}}]}`, content)), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: store,
		SummaryEnabled: true, SummaryTriggerTokens: 1, SummaryTailTokens: 1,
	})
	s.Require().NoError(err)
	ctx := teamnote.WithScope(context.Background(), "scope-summary-resume")
	_, err = adapter.Extract(ctx, extractionEventSlice("event-1", "checksum-1", 1))
	s.Require().NoError(err)
	s.Require().NoError(adapter.WaitForBackground(ctx))

	episode, ok, err := store.LoadEpisode(ctx, extractor.EpisodeKey{ScopeID: "scope-summary-resume", TaskRef: "release-42"})
	s.Require().NoError(err)
	s.True(ok)
	s.Equal(1, episode.Checkpoint.SummaryCount)
	s.Equal("Release remains planned.", episode.Checkpoint.Summary)
}

func (s *openAISuite) TestRollingSummaryFailureDoesNotBlockExtraction() {
	store := newMemoryEpisodeStore()
	summaryReturned := make(chan struct{})
	var summaryCalls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		s.Require().NoError(err)
		if strings.Contains(string(body), "Update the rolling continuity summary") {
			if summaryCalls.Add(1) == 1 {
				close(summaryReturned)
			}
			return response(http.StatusOK, `{"choices":[{"message":{"content":"{\"wrong\":true}"}}]}`), nil
		}
		eventID := "event-1"
		if strings.Contains(string(body), "event-2") {
			eventID = "event-2"
		}
		content := fmt.Sprintf(`{"candidates":[{"action":"create","kind":"status","subject":"release","body":"Release state.","evidence_event_ids":[%q]}]}`, eventID)
		return response(http.StatusOK, fmt.Sprintf(`{"choices":[{"message":{"content":%q}}],"usage":{"prompt_tokens":100,"completion_tokens":10}}`, content)), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: store,
		SummaryEnabled: true, SummaryTriggerTokens: 1, SummaryTailTokens: 1,
	})
	s.Require().NoError(err)
	ctx := teamnote.WithScope(context.Background(), "scope-summary-failure")
	_, err = adapter.Extract(ctx, extractionEventSlice("event-1", "checksum-1", 1))
	s.Require().NoError(err)
	<-summaryReturned
	time.Sleep(20 * time.Millisecond)
	_, err = adapter.Extract(ctx, extractionEventSlice("event-2", "checksum-2", 2))
	s.Require().NoError(err)

	episode, ok, err := store.LoadEpisode(ctx, extractor.EpisodeKey{ScopeID: "scope-summary-failure", TaskRef: "release-42"})
	s.Require().NoError(err)
	s.True(ok)
	s.Equal(1, episode.Checkpoint.SummaryAttempts)
	s.Equal(1, episode.Checkpoint.SummaryFailures)
	s.NotEmpty(episode.Checkpoint.SummaryLastError)
}

func (s *openAISuite) TestRollingContextCompactsAsynchronouslyAndWaitsAtHardLimit() {
	store := newMemoryEpisodeStore()
	compactionStarted := make(chan struct{})
	releaseCompaction := make(chan struct{})
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		s.Require().NoError(err)
		calls.Add(1)
		if strings.Contains(string(body), "KNOWLEDGE CONTEXT CHECKPOINT COMPACTION") {
			close(compactionStarted)
			<-releaseCompaction
			checkpoint := `{"active_knowledge":[{"memory_id":"decision/release","kind":"status","subject":"release","body":"Release is planned.","evidence_event_ids":["event-1"]}],"resolved_knowledge":[],"open_questions":[],"evidence_index":{"decision/release":["event-1"]},"source_cursors":{}}`
			return response(http.StatusOK, fmt.Sprintf(`{"choices":[{"message":{"content":%q}}],"usage":{"prompt_tokens":300,"completion_tokens":30}}`, checkpoint)), nil
		}
		eventID, promptTokens := "event-1", 30
		if strings.Contains(string(body), "event-2") {
			eventID, promptTokens = "event-2", 490
		}
		if strings.Contains(string(body), "event-3") {
			eventID, promptTokens = "event-3", 20
			s.Contains(string(body), "checkpoint_loaded")
			s.Contains(string(body), "event-2")
		}
		content := fmt.Sprintf(`{"candidates":[{"action":"create","kind":"status","subject":"release","identity_ref":"decision/release","body":"Release state.","evidence_event_ids":[%q]}]}`, eventID)
		return response(http.StatusOK, fmt.Sprintf(`{"choices":[{"message":{"content":%q}}],"usage":{"prompt_tokens":%d,"completion_tokens":10}}`, content, promptTokens)), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: store,
		CompactionEnabled:  true,
		CompactStartTokens: 40, CompactTokens: 500,
	})
	s.Require().NoError(err)
	ctx := teamnote.WithScope(context.Background(), "scope-async")
	_, err = adapter.Extract(ctx, extractionEventSlice("event-1", "checksum-1", 1))
	s.Require().NoError(err)
	select {
	case <-compactionStarted:
	case <-time.After(time.Second):
		s.FailNow("asynchronous compaction did not start")
	}

	_, err = adapter.Extract(ctx, extractionEventSlice("event-2", "checksum-2", 2))
	s.Require().NoError(err, "soft-limit extraction should not wait for compaction")

	type extractionOutcome struct {
		result extractor.Result
		err    error
	}
	done := make(chan extractionOutcome, 1)
	go func() {
		result, extractErr := adapter.Extract(ctx, extractionEventSlice("event-3", "checksum-3", 3))
		done <- extractionOutcome{result: result, err: extractErr}
	}()
	select {
	case <-done:
		s.FailNow("hard-limit extraction did not wait for compaction")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseCompaction)
	select {
	case outcome := <-done:
		s.Require().NoError(outcome.err)
		s.Require().Len(outcome.result.Candidates, 1)
		s.Equal("event-3", outcome.result.Candidates[0].EvidenceEventIDs[0])
	case <-time.After(time.Second):
		s.FailNow("hard-limit extraction did not resume")
	}
	s.Equal(int32(4), calls.Load())

	episode, ok, err := store.LoadEpisode(ctx, extractor.EpisodeKey{ScopeID: "scope-async", TaskRef: "release-42"})
	s.Require().NoError(err)
	s.True(ok)
	s.Equal(1, episode.CompactionCount)
	s.Require().Len(episode.Messages, 4)
	s.Contains(episode.Messages[0].Content, "event-2")
	s.Contains(episode.Messages[2].Content, "event-3")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

type memoryEpisodeStore struct {
	episodes map[extractor.EpisodeKey]extractor.Episode
}

func newMemoryEpisodeStore() *memoryEpisodeStore {
	return &memoryEpisodeStore{episodes: make(map[extractor.EpisodeKey]extractor.Episode)}
}

func (s *memoryEpisodeStore) LoadEpisode(_ context.Context, key extractor.EpisodeKey) (extractor.Episode, bool, error) {
	episode, ok := s.episodes[key]
	if !ok {
		return extractor.Episode{Key: key}, false, nil
	}
	return episode, ok, nil
}

func (s *memoryEpisodeStore) SaveEpisode(_ context.Context, episode extractor.Episode, expectedVersion int64) error {
	current, ok := s.episodes[episode.Key]
	if (!ok && expectedVersion != 0) || (ok && current.Version != expectedVersion) {
		return extractor.ErrEpisodeConflict
	}
	episode.Version = expectedVersion + 1
	encoded, err := json.Marshal(episode)
	if err != nil {
		return err
	}
	var copied extractor.Episode
	if err := json.Unmarshal(encoded, &copied); err != nil {
		return err
	}
	s.episodes[episode.Key] = copied
	return nil
}

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

func extractionEventSlice(eventID, checksum string, sequence int64) sessionlake.Slice {
	slice := extractorSlice()
	slice.InputChecksum = checksum
	slice.FromSequence = sequence
	slice.ToSequence = sequence
	slice.NewEventIDs = []string{eventID}
	slice.Events[0].ID = eventID
	slice.Events[0].Sequence = sequence
	return slice
}
