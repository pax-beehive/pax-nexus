package extractor_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
	"github.com/stretchr/testify/suite"
)

type extractionV2Suite struct {
	suite.Suite
}

func TestExtractionV2Suite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(extractionV2Suite))
}

func v2Slice() sessionlake.Slice {
	return extractorSlice()
}

func v2Body(claims string, decisions string) string {
	return v2BodyWithNoStateEvents(claims, decisions, "")
}

func v2BodyWithNoStateEvents(claims string, decisions string, noStateEventIDs string) string {
	return v2BodyWithProducts(claims, decisions, noStateEventIDs, "")
}

func v2BodyWithProducts(claims string, decisions string, noStateEventIDs string, interactions string) string {
	content := fmt.Sprintf(`{"claims":[%s],"state_decisions":[%s],"no_state_event_ids":[%s],"interaction_observations":[%s]}`,
		claims, decisions, noStateEventIDs, interactions)
	return fmt.Sprintf(`{"choices":[{"message":{"content":%s}}],"usage":{"prompt_tokens":100,"completion_tokens":10}}`,
		fmt.Sprintf("%q", content))
}

func newV2Adapter(s *extractionV2Suite, client *http.Client) extractor.Extractor {
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: extractor.NewMemoryEpisodeStore(),
		ExtractionVersion: extractor.ExtractionVersionV2,
	})
	s.Require().NoError(err)
	return adapter
}

func (s *extractionV2Suite) TestConfigValidation() {
	tests := []struct {
		name   string
		config extractor.OpenAIConfig
	}{
		{name: "v2 requires rolling context", config: extractor.OpenAIConfig{
			BaseURL: "http://extractor.test", Model: "model",
			ContextMode: extractor.ContextModeSlice, ExtractionVersion: extractor.ExtractionVersionV2,
		}},
		{name: "unsupported extraction version", config: extractor.OpenAIConfig{
			BaseURL: "http://extractor.test", Model: "model",
			ContextMode: extractor.ContextModeRolling, EpisodeStore: extractor.NewMemoryEpisodeStore(),
			ExtractionVersion: "v3",
		}},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			_, err := extractor.NewOpenAI(test.config)
			s.Require().Error(err)
		})
	}
}

func (s *extractionV2Suite) TestMapsClaimsAndDecisionsToCandidates() {
	claims := `{"claim_id":"c1","claim_type":"status","subject":"release","predicate":"is blocked by","value":"failing tests","speaker":"producer","evidence_event_ids":["event-1"]}`
	decisions := `{"decision":"create","identity_ref":"","claim_ids":["c1"],"reason_codes":["explicit_new_fact"],"candidate":{"kind":"blocker","subject":"release","body":"Tests are failing."}}`
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, v2Body(claims, decisions)), nil
	})}
	adapter := newV2Adapter(s, client)

	result, err := adapter.Extract(teamnote.WithScope(context.Background(), "scope-v2"), v2Slice())
	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	candidate := result.Candidates[0]
	s.Equal(teamnote.ActionCreate, candidate.Action)
	s.Equal(teamnote.KindBlocker, candidate.Kind)
	s.Equal("release", candidate.Subject)
	s.Equal([]string{"event-1"}, candidate.EvidenceEventIDs)

	s.Require().NotNil(result.Trace)
	s.Require().Len(result.Trace.Claims, 1)
	s.Equal(extractor.ClaimTypeStatus, result.Trace.Claims[0].ClaimType)
	s.Require().Len(result.Trace.StateDecisions, 1)
	s.Empty(result.Trace.WouldVerify)
}

func (s *extractionV2Suite) TestMapsDirectDecisionWithoutDuplicatedClaimAndPreservesTemporalState() {
	decisions := `{"decision":"update","identity_ref":"decision/release-target","evidence_event_ids":["event-1"],"temporal_expression":"effective July 18","valid_at":"2026-07-18T00:00:00Z","temporal_resolution":"explicit","reason_codes":["explicit_correction"],"candidate":{"kind":"status","subject":"release target","body":"The release target moved to July 18.","related_subjects":["release readiness"]}}`
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, v2Body("", decisions)), nil
	})}
	adapter := newV2Adapter(s, client)

	result, err := adapter.Extract(teamnote.WithScope(context.Background(), "scope-v2-direct"), v2Slice())
	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	candidate := result.Candidates[0]
	s.Equal(teamnote.ActionUpdate, candidate.Action)
	s.Equal("decision/release-target", candidate.IdentityRef)
	s.Equal([]string{"event-1"}, candidate.EvidenceEventIDs)
	s.Equal([]string{"release readiness"}, candidate.RelatedSubjects)
	s.Require().NotNil(candidate.ValidAt)
	s.Equal(time.Date(2026, time.July, 18, 0, 0, 0, 0, time.UTC), *candidate.ValidAt)
	s.Nil(candidate.InvalidAt)
	s.Empty(result.Trace.Claims)
	s.Empty(result.Trace.OrphanClaimIDs)
	s.Empty(result.Trace.UnreviewedEventIDs)
	s.Equal(extractor.ExtractionVersionV2, result.ExtractionVersion)
}

func (s *extractionV2Suite) TestCoverageTraceReportsUnreviewedEventsAndOrphanClaims() {
	claims := `{"claim_id":"c1","claim_type":"status","subject":"release","predicate":"is","value":"ready","speaker":"producer","evidence_event_ids":["event-1"]}`
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, v2BodyWithNoStateEvents(claims, "", "")), nil
	})}
	adapter := newV2Adapter(s, client)
	slice := v2Slice()
	slice.NewEventIDs = append(slice.NewEventIDs, "event-2")
	second := slice.Events[0]
	second.ID = "event-2"
	second.Sequence++
	slice.Events = append(slice.Events, second)

	result, err := adapter.Extract(teamnote.WithScope(context.Background(), "scope-v2-coverage"), slice)
	s.Require().NoError(err)
	s.Require().NotNil(result.Trace)
	s.Equal([]string{"c1"}, result.Trace.OrphanClaimIDs)
	s.Equal([]string{"event-2"}, result.Trace.UnreviewedEventIDs)
	s.Empty(result.Trace.InvalidNoStateEventIDs)
}

func (s *extractionV2Suite) TestNoStateEventsCompleteCoverageWithoutCreatingCandidates() {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, v2BodyWithNoStateEvents("", "", `"event-1"`)), nil
	})}
	adapter := newV2Adapter(s, client)

	result, err := adapter.Extract(teamnote.WithScope(context.Background(), "scope-v2-no-state"), v2Slice())
	s.Require().NoError(err)
	s.Empty(result.Candidates)
	s.Equal([]string{"event-1"}, result.Trace.NoStateEventIDs)
	s.Empty(result.Trace.UnreviewedEventIDs)
	s.Empty(result.Trace.InvalidNoStateEventIDs)
}

func (s *extractionV2Suite) TestEvidenceCannotAlsoBeDeclaredNoState() {
	decisions := `{"decision":"create","evidence_event_ids":["event-1"],"candidate":{"kind":"status","subject":"release","body":"Release is ready."}}`
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, v2BodyWithNoStateEvents("", decisions, `"event-1"`)), nil
	})}
	adapter := newV2Adapter(s, client)

	result, err := adapter.Extract(teamnote.WithScope(context.Background(), "scope-v2-conflicting-coverage"), v2Slice())
	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Empty(result.Trace.NoStateEventIDs)
	s.Equal([]string{"event-1"}, result.Trace.InvalidNoStateEventIDs)
	s.Empty(result.Trace.UnreviewedEventIDs)
}

func (s *extractionV2Suite) TestInteractionObservationsRequireGroundedVocabulary() {
	tests := []struct {
		name             string
		interaction      string
		wantObservations int
		wantRejections   int
	}{
		{name: "valid", interaction: `{"actor":"producer","target":"reviewer","stance":"support","speech_act":"approve","evidence_event_id":"event-1"}`, wantObservations: 1},
		{name: "invalid stance", interaction: `{"actor":"producer","target":"reviewer","stance":"enthusiastic","speech_act":"approve","evidence_event_id":"event-1"}`, wantRejections: 1},
		{name: "unknown evidence", interaction: `{"actor":"producer","target":"reviewer","stance":"oppose","speech_act":"reject","evidence_event_id":"unknown"}`, wantRejections: 1},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return response(http.StatusOK, v2BodyWithProducts("", "", `"event-1"`, test.interaction)), nil
			})}
			adapter := newV2Adapter(s, client)
			result, err := adapter.Extract(
				teamnote.WithScope(context.Background(), "scope-v2-interactions-"+test.name), v2Slice(),
			)
			s.Require().NoError(err)
			s.Len(result.Trace.InteractionObservations, test.wantObservations)
			s.Len(result.Trace.InteractionRejections, test.wantRejections)
		})
	}
}

func (s *extractionV2Suite) TestTraceOnlyDecisionsAndWouldVerifyTriggers() {
	claims := strings.Join([]string{
		`{"claim_id":"c1","claim_type":"status","subject":"release","predicate":"is","value":"on track","speaker":"agent-a","evidence_event_ids":["event-1"]}`,
		`{"claim_id":"c2","claim_type":"correction","subject":"release","predicate":"is","value":"slipped","speaker":"agent-b","evidence_event_ids":["event-1"]}`,
		`{"claim_id":"c3","claim_type":"schedule","subject":"release date","predicate":"moves to","value":"next friday","speaker":"agent-a","evidence_event_ids":["event-1"],"temporal_expression":"next friday","temporal_resolution":"unresolved"}`,
	}, ",")
	decisions := strings.Join([]string{
		`{"decision":"keep_conflict_open","identity_ref":"release-state","claim_ids":["c1","c2"],"reason_codes":["conflicting_report"]}`,
		`{"decision":"update","claim_ids":["c3"],"reason_codes":["same_obligation_new_deadline"],"candidate":{"kind":"status","subject":"release date","body":"Release moves to next Friday."}}`,
	}, ",")
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, v2Body(claims, decisions)), nil
	})}
	adapter := newV2Adapter(s, client)

	result, err := adapter.Extract(teamnote.WithScope(context.Background(), "scope-v2"), v2Slice())
	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Equal(teamnote.ActionUpdate, result.Candidates[0].Action)
	s.Require().NotNil(result.Trace)
	s.Require().Len(result.Trace.StateDecisions, 2)
	s.ElementsMatch([]string{"conflicting_reports", "uncertain_prior_identity", "unresolved_temporal_anchor"}, result.Trace.WouldVerify)
}

func (s *extractionV2Suite) TestRejectsInvalidTemporalMetadata() {
	tests := []struct {
		name       string
		temporal   string
		wantReason string
	}{
		{
			name:       "timestamp without source expression",
			temporal:   `"valid_at":"2026-07-18T00:00:00Z","temporal_resolution":"explicit",`,
			wantReason: "without a source expression",
		},
		{
			name:       "unresolved expression with timestamp",
			temporal:   `"temporal_expression":"next Friday","valid_at":"2026-07-18T00:00:00Z","temporal_resolution":"unresolved",`,
			wantReason: "unresolved time",
		},
		{
			name:       "invalid window",
			temporal:   `"temporal_expression":"effective window","valid_at":"2026-07-19T00:00:00Z","invalid_at":"2026-07-18T00:00:00Z","temporal_resolution":"explicit",`,
			wantReason: "invalid temporal window",
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			decision := `{"decision":"create","identity_ref":"decision/release",` + test.temporal + `"evidence_event_ids":["event-1"],"reason_codes":["explicit_new_fact"],"candidate":{"kind":"status","subject":"release","body":"Release is ready."}}`
			client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return response(http.StatusOK, v2Body("", decision)), nil
			})}
			adapter := newV2Adapter(s, client)

			result, err := adapter.Extract(teamnote.WithScope(context.Background(), "scope-v2-time-"+strings.ReplaceAll(test.name, " ", "-")), v2Slice())
			s.Require().NoError(err)
			s.Empty(result.Candidates)
			s.Require().Len(result.Trace.DecisionRejections, 1)
			s.Contains(result.Trace.DecisionRejections[0].Reason, test.wantReason)
		})
	}
}

func (s *extractionV2Suite) TestDropsInvalidClaimsAndDecisionsWithTraceRejections() {
	claims := strings.Join([]string{
		`{"claim_id":"c1","claim_type":"status","subject":"release","predicate":"is","value":"ready","speaker":"producer","evidence_event_ids":["event-1"]}`,
		`{"claim_id":"c1","claim_type":"status","subject":"dupe","predicate":"is","value":"dupe","speaker":"producer","evidence_event_ids":["event-1"]}`,
		`{"claim_id":"c2","claim_type":"mood","subject":"release","predicate":"feels","value":"good","speaker":"producer","evidence_event_ids":["event-1"]}`,
		`{"claim_id":"c3","claim_type":"status","subject":"ghost","predicate":"is","value":"gone","speaker":"producer","evidence_event_ids":["unknown-event"]}`,
	}, ",")
	decisions := strings.Join([]string{
		`{"decision":"create","claim_ids":["c1"],"reason_codes":["explicit_new_fact"],"candidate":{"kind":"status","subject":"release","body":"Release is ready."}}`,
		`{"decision":"create","claim_ids":["c3"],"reason_codes":["explicit_new_fact"],"candidate":{"kind":"status","subject":"ghost","body":"Ghost."}}`,
		`{"decision":"create","claim_ids":["c1"],"reason_codes":["invented_reason"],"candidate":{"kind":"status","subject":"bad reason","body":"Bad."}}`,
		`{"decision":"resolve","claim_ids":["c1"],"reason_codes":["explicit_resolution"]}`,
	}, ",")
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, v2Body(claims, decisions)), nil
	})}
	adapter := newV2Adapter(s, client)

	result, err := adapter.Extract(teamnote.WithScope(context.Background(), "scope-v2"), v2Slice())
	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Equal("release", result.Candidates[0].Subject)
	s.Require().NotNil(result.Trace)
	s.Require().Len(result.Trace.Claims, 1)
	s.Require().Len(result.Trace.ClaimRejections, 3)
	s.Contains(result.Trace.ClaimRejections[0].Reason, "duplicate claim_id")
	s.Contains(result.Trace.ClaimRejections[1].Reason, "claim vocabulary")
	s.Contains(result.Trace.ClaimRejections[2].Reason, "unknown event")
	s.Require().Len(result.Trace.StateDecisions, 1)
	s.Require().Len(result.Trace.DecisionRejections, 3)
	s.Contains(result.Trace.DecisionRejections[0].Reason, "unknown or rejected claim")
	s.Contains(result.Trace.DecisionRejections[1].Reason, "reason vocabulary")
	s.Contains(result.Trace.DecisionRejections[2].Reason, "require a candidate")
}

func (s *extractionV2Suite) TestRollingReplayReturnsSavedRunWithoutCallingModel() {
	claims := `{"claim_id":"c1","claim_type":"status","subject":"release","predicate":"is","value":"ready","speaker":"producer","evidence_event_ids":["event-1"]}`
	decisions := `{"decision":"create","identity_ref":"decision/release","claim_ids":["c1"],"reason_codes":["explicit_new_fact"],"candidate":{"kind":"status","subject":"release","body":"Release is ready."}}`
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		calls++
		return response(http.StatusOK, v2Body(claims, decisions)), nil
	})}
	adapter := newV2Adapter(s, client)
	ctx := teamnote.WithScope(context.Background(), "scope-v2-replay")

	slice := v2Slice()
	first, err := adapter.Extract(ctx, slice)
	s.Require().NoError(err)
	second, err := adapter.Extract(ctx, slice)
	s.Require().NoError(err)
	s.Equal(1, calls)
	s.Equal(first.Candidates, second.Candidates)
	s.Require().NotNil(second.Trace)
	s.Equal(first.Trace.Claims, second.Trace.Claims)
	s.Zero(second.Usage.InputTokens)
}

func (s *extractionV2Suite) TestProtocolChangeStartsFreshEpisodeInsteadOfReplayingV1() {
	store := extractor.NewMemoryEpisodeStore()
	v1Calls := 0
	v1Client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		v1Calls++
		return response(http.StatusOK, `{"choices":[{"message":{"content":"{\"candidates\":[]}"}}]}`), nil
	})}
	v1, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", PromptVersion: "prompt",
		Client: v1Client, ContextMode: extractor.ContextModeRolling, EpisodeStore: store,
		ExtractionVersion: extractor.ExtractionVersionV1,
	})
	s.Require().NoError(err)

	v2Calls := 0
	v2Client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		v2Calls++
		decision := `{"decision":"create","identity_ref":"decision/release","evidence_event_ids":["event-1"],"reason_codes":["explicit_new_fact"],"candidate":{"kind":"status","subject":"release","body":"Release is ready."}}`
		return response(http.StatusOK, v2Body("", decision)), nil
	})}
	v2, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", PromptVersion: "prompt",
		Client: v2Client, ContextMode: extractor.ContextModeRolling, EpisodeStore: store,
		ExtractionVersion: extractor.ExtractionVersionV2,
	})
	s.Require().NoError(err)
	ctx := teamnote.WithScope(context.Background(), "scope-protocol-change")

	_, err = v1.Extract(ctx, v2Slice())
	s.Require().NoError(err)
	result, err := v2.Extract(ctx, v2Slice())
	s.Require().NoError(err)
	s.Equal(1, v1Calls)
	s.Equal(1, v2Calls)
	s.Require().Len(result.Candidates, 1)
	s.Equal(extractor.ExtractionVersionV2, result.ExtractionVersion)
}

func (s *extractionV2Suite) TestRejectsMalformedV2Responses() {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed envelope", body: "not-json"},
		{name: "malformed content", body: `{"choices":[{"message":{"content":"not-json"}}]}`},
		{name: "missing products", body: `{"choices":[{"message":{"content":"{\"candidates\":[]}"}}]}`},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return response(http.StatusOK, test.body), nil
			})}
			adapter := newV2Adapter(s, client)
			_, err := adapter.Extract(teamnote.WithScope(context.Background(), "scope-v2-malformed"), v2Slice())
			s.Require().ErrorIs(err, extractor.ErrInvalidModelResponse)
		})
	}
}
