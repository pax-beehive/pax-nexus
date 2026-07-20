package extractor_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
	"github.com/stretchr/testify/suite"
)

type claimCardSuite struct {
	suite.Suite
}

func TestClaimCardSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(claimCardSuite))
}

func newClaimCardAdapter(s *claimCardSuite, client *http.Client) extractor.Extractor {
	return newClaimCardAdapterWithStrategy(s, client, extractor.CandidateStrategyClaimCardV1)
}

func newClaimCardAdapterWithStrategy(s *claimCardSuite, client *http.Client, strategy string) extractor.Extractor {
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: extractor.NewMemoryEpisodeStore(),
		ExtractionVersion: extractor.ExtractionVersionV2, V2Variant: strategy,
	})
	s.Require().NoError(err)
	return adapter
}

func (s *claimCardSuite) TestV2UsesExactValuePromptAndDistinctVersion() {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		s.Require().NoError(err)
		s.Contains(string(body), "Exact-value override")
		return response(http.StatusOK, v2BodyWithNoStateEvents("", "", `"event-1"`)), nil
	})}
	adapter := newClaimCardAdapterWithStrategy(s, client, extractor.CandidateStrategyClaimCardV2)

	result, err := adapter.Extract(teamnote.WithScope(context.Background(), "claim-card-v2-version"), v2Slice())

	s.Require().NoError(err)
	s.Equal(extractor.ExtractionVersionClaimCardV2, result.ExtractionVersion)
}

func (s *claimCardSuite) TestRendersCandidateFromPrimaryClaimInsteadOfModelBody() {
	claims := `{"claim_id":"c1","claim_type":"schedule","subject":"release target","predicate":"is scheduled for","value":"2026-07-18","speaker":"User_7","evidence_event_ids":["event-1"],"temporal_expression":"effective July 18","valid_at":"2026-07-18T00:00:00Z","temporal_resolution":"explicit"}`
	decisions := `{"decision":"update","identity_ref":"model/free-form-identity","claim_ids":["c1"],"reason_codes":["explicit_correction"],"candidate":{"kind":"blocker","subject":"wrong model subject","body":"freeform hallucination","related_subjects":["ignored"]}}`
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		s.Require().NoError(err)
		s.Contains(string(body), "Claim-card strategy override")
		return response(http.StatusOK, v2Body(claims, decisions)), nil
	})}
	adapter := newClaimCardAdapter(s, client)

	result, err := adapter.Extract(teamnote.WithScope(context.Background(), "claim-card-render"), v2Slice())

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	candidate := result.Candidates[0]
	s.Equal(teamnote.ActionUpdate, candidate.Action)
	s.Equal(teamnote.KindStatus, candidate.Kind)
	s.Equal("release target", candidate.Subject)
	s.True(strings.HasPrefix(candidate.IdentityRef, "claim-card/"))
	s.NotContains(candidate.IdentityRef, "model/free-form-identity")
	s.Contains(candidate.Body, "asserted_by=owner/producer/session-1")
	s.Contains(candidate.Body, "Predicate: is scheduled for")
	s.Contains(candidate.Body, "Value: 2026-07-18")
	s.NotContains(candidate.Body, "freeform hallucination")
	s.Empty(candidate.RelatedSubjects)
	s.Require().NotNil(candidate.ValidAt)
	s.Equal(time.Date(2026, time.July, 18, 0, 0, 0, 0, time.UTC), *candidate.ValidAt)
	s.Equal(extractor.ExtractionVersionClaimCardV1, result.ExtractionVersion)
}

func (s *claimCardSuite) TestMapsClaimTypeToConservativeNoteKind() {
	tests := []struct {
		name      string
		claimType string
		want      teamnote.NoteKind
	}{
		{name: "blocker", claimType: "blocker", want: teamnote.KindBlocker},
		{name: "handoff", claimType: "handoff", want: teamnote.KindHandoff},
		{name: "artifact", claimType: "artifact", want: teamnote.KindArtifactReference},
		{name: "status", claimType: "status", want: teamnote.KindStatus},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			claims := `{"claim_id":"c1","claim_type":"` + test.claimType + `","subject":"release","predicate":"is","value":"ready","speaker":"User_7","evidence_event_ids":["event-1"]}`
			decisions := `{"decision":"create","claim_ids":["c1"],"reason_codes":["explicit_new_fact"],"candidate":{"kind":"status","subject":"ignored","body":"ignored"}}`
			client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return response(http.StatusOK, v2Body(claims, decisions)), nil
			})}
			adapter := newClaimCardAdapter(s, client)

			result, err := adapter.Extract(teamnote.WithScope(context.Background(), "claim-card-kind-"+test.name), v2Slice())

			s.Require().NoError(err)
			s.Require().Len(result.Candidates, 1)
			s.Equal(test.want, result.Candidates[0].Kind)
		})
	}
}

func (s *claimCardSuite) TestRejectsDecisionWithoutExactlyOnePrimaryClaim() {
	decision := `{"decision":"create","evidence_event_ids":["event-1"],"reason_codes":["explicit_new_fact"],"candidate":{"kind":"status","subject":"release","body":"freeform body"}}`
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, v2Body("", decision)), nil
	})}
	adapter := newClaimCardAdapter(s, client)

	result, err := adapter.Extract(teamnote.WithScope(context.Background(), "claim-card-missing-primary"), v2Slice())

	s.Require().NoError(err)
	s.Empty(result.Candidates)
	s.Require().Len(result.Trace.DecisionRejections, 1)
	s.Contains(result.Trace.DecisionRejections[0].Reason, "exactly one primary claim")
}
