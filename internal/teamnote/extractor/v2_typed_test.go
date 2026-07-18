package extractor_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
	"github.com/stretchr/testify/suite"
)

type extractionV2TypedSuite struct {
	suite.Suite
}

func TestExtractionV2TypedSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(extractionV2TypedSuite))
}

func typedV2Body(decisions string) string {
	content := fmt.Sprintf(`{"claims":[],"state_decisions":[%s],"no_state_event_ids":[],"interaction_observations":[]}`,
		decisions)
	return fmt.Sprintf(`{"choices":[{"message":{"content":%s}}],"usage":{"prompt_tokens":100,"completion_tokens":10}}`,
		fmt.Sprintf("%q", content))
}

func (s *extractionV2TypedSuite) TestRendersCandidateBodyFromTypedAtomicFact() {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		s.Require().NoError(err)
		var payload struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		s.Require().NoError(json.Unmarshal(body, &payload))
		s.Require().NotEmpty(payload.Messages)
		s.Contains(payload.Messages[0].Content, `"statement":"one exact complete atomic fact"`)
		s.NotContains(payload.Messages[0].Content, `"body":"one atomic factual note"`)
		decision := `{"decision":"create","evidence_event_ids":["event-1"],"reason_codes":["explicit_new_fact"],"fact":{"kind":"status","subject":"evidence deadline","statement":"Finance Ops must post evidence by Friday EOD","modality":"committed"}}`
		return response(http.StatusOK, typedV2Body(decision)), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: extractor.NewMemoryEpisodeStore(),
		ExtractionVersion: extractor.ExtractionVersionV2, V2Variant: extractor.V2VariantTypedCurrent,
	})
	s.Require().NoError(err)
	slice := v2Slice()
	slice.Events[0].Content = "Finance Ops must post evidence by Friday EOD."

	result, err := adapter.Extract(teamnote.WithScope(context.Background(), "scope-v2-typed"), slice)

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Equal("Finance Ops must post evidence by Friday EOD.", result.Candidates[0].Body)
	s.Equal("evidence deadline", result.Candidates[0].Subject)
	s.Equal(teamnote.KindStatus, result.Candidates[0].Kind)
}

func (s *extractionV2TypedSuite) TestPrefersAtomicSourceStatementOverFieldConcatenation() {
	decision := `{"decision":"create","evidence_event_ids":["event-1"],"reason_codes":["explicit_new_fact"],"fact":{"kind":"status","subject":"evidence deadline","statement":"Finance Ops must post evidence by Friday EOD","actor":"Finance Ops","predicate":"is responsible for","value":"evidence","modality":"committed"}}`
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, typedV2Body(decision)), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: extractor.NewMemoryEpisodeStore(),
		ExtractionVersion: extractor.ExtractionVersionV2, V2Variant: extractor.V2VariantTypedCurrent,
	})
	s.Require().NoError(err)
	slice := v2Slice()
	slice.Events[0].Content = "Finance Ops must post evidence by Friday EOD."

	result, err := adapter.Extract(teamnote.WithScope(context.Background(), "scope-v2-typed-statement"), slice)

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Equal("Finance Ops must post evidence by Friday EOD.", result.Candidates[0].Body)
}

func (s *extractionV2TypedSuite) TestTypedAdmissionMatrix() {
	tests := []struct {
		name          string
		kind          string
		modality      string
		wantCandidate bool
		wantRejection string
	}{
		{name: "asserted", kind: "status", modality: "asserted", wantCandidate: true},
		{name: "committed", kind: "status", modality: "committed", wantCandidate: true},
		{name: "proposed", kind: "status", modality: "proposed", wantRejection: `modality "proposed"`},
		{name: "uncertain", kind: "status", modality: "uncertain", wantRejection: `modality "uncertain"`},
		{name: "unknown modality", kind: "status", modality: "rumored", wantRejection: "modality vocabulary"},
		{name: "unknown kind", kind: "decision", modality: "asserted", wantRejection: `kind "decision"`},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			decision := fmt.Sprintf(`{"decision":"create","evidence_event_ids":["event-1"],"reason_codes":["explicit_new_fact"],"fact":{"kind":%q,"subject":"evidence deadline","statement":"Finance Ops must post evidence by Friday EOD","modality":%q}}`,
				test.kind, test.modality)
			client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return response(http.StatusOK, typedV2Body(decision)), nil
			})}
			adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
				BaseURL: "http://extractor.test", Model: "model", Client: client,
				ContextMode: extractor.ContextModeRolling, EpisodeStore: extractor.NewMemoryEpisodeStore(),
				ExtractionVersion: extractor.ExtractionVersionV2, V2Variant: extractor.V2VariantTypedCurrent,
			})
			s.Require().NoError(err)
			slice := v2Slice()
			slice.Events[0].Content = "Finance Ops must post evidence by Friday EOD."

			result, err := adapter.Extract(teamnote.WithScope(context.Background(), "scope-v2-typed-"+test.name), slice)

			s.Require().NoError(err)
			if test.wantCandidate {
				s.Require().Len(result.Candidates, 1)
				s.Empty(result.Trace.DecisionRejections)
				return
			}
			s.Empty(result.Candidates)
			s.Require().Len(result.Trace.DecisionRejections, 1)
			s.Contains(result.Trace.DecisionRejections[0].Reason, test.wantRejection)
		})
	}
}

func (s *extractionV2TypedSuite) TestRejectsIncompleteTypedFactInTrace() {
	decision := `{"decision":"create","evidence_event_ids":["event-1"],"reason_codes":["explicit_new_fact"],"fact":{"kind":"status","subject":"","statement":"Finance Ops must post evidence by Friday EOD","modality":"committed"}}`
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, typedV2Body(decision)), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: extractor.NewMemoryEpisodeStore(),
		ExtractionVersion: extractor.ExtractionVersionV2, V2Variant: extractor.V2VariantTypedCurrent,
	})
	s.Require().NoError(err)

	result, err := adapter.Extract(teamnote.WithScope(context.Background(), "scope-v2-typed-incomplete"), v2Slice())

	s.Require().NoError(err)
	s.Empty(result.Candidates)
	s.Empty(result.Rejections)
	s.Require().Len(result.Trace.DecisionRejections, 1)
	s.Contains(result.Trace.DecisionRejections[0].Reason, "missing subject")
}

func (s *extractionV2TypedSuite) TestDoesNotAppendASCIIPunctuationAfterCJKTerminator() {
	decision := `{"decision":"create","evidence_event_ids":["event-1"],"reason_codes":["explicit_new_fact"],"fact":{"kind":"status","subject":"证据期限","statement":"财务团队 必须提交 证据。","modality":"committed"}}`
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, typedV2Body(decision)), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: extractor.NewMemoryEpisodeStore(),
		ExtractionVersion: extractor.ExtractionVersionV2, V2Variant: extractor.V2VariantTypedCurrent,
	})
	s.Require().NoError(err)
	slice := v2Slice()
	slice.Events[0].Content = "财务团队必须提交证据。"

	result, err := adapter.Extract(teamnote.WithScope(context.Background(), "scope-v2-typed-cjk"), slice)

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Equal("财务团队 必须提交 证据。", result.Candidates[0].Body)
}

func (s *extractionV2TypedSuite) TestReplaysTypedRunWithoutAnotherProviderCall() {
	store := extractor.NewMemoryEpisodeStore()
	calls := 0
	decision := `{"decision":"create","evidence_event_ids":["event-1"],"reason_codes":["explicit_new_fact"],"fact":{"kind":"status","subject":"evidence deadline","statement":"Finance Ops must post evidence by Friday EOD","modality":"committed"}}`
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		calls++
		return response(http.StatusOK, typedV2Body(decision)), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", PromptVersion: "prompt", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: store,
		ExtractionVersion: extractor.ExtractionVersionV2, V2Variant: extractor.V2VariantTypedCurrent,
	})
	s.Require().NoError(err)
	slice := v2Slice()
	slice.Events[0].Content = "Finance Ops must post evidence by Friday EOD."
	ctx := teamnote.WithScope(context.Background(), "scope-v2-typed-replay")

	first, err := adapter.Extract(ctx, slice)
	s.Require().NoError(err)
	second, err := adapter.Extract(ctx, slice)

	s.Require().NoError(err)
	s.Equal(1, calls)
	s.Require().Len(first.Candidates, 1)
	s.Equal(first.Candidates[0].Body, second.Candidates[0].Body)
	s.Equal(first.Candidates[0].ID, second.Candidates[0].ID)
}

func (s *extractionV2TypedSuite) TestTypedProtocolDoesNotReplayCurrentV2Response() {
	store := extractor.NewMemoryEpisodeStore()
	currentCalls := 0
	currentClient := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		currentCalls++
		decision := `{"decision":"create","evidence_event_ids":["event-1"],"reason_codes":["explicit_new_fact"],"candidate":{"kind":"status","subject":"release","body":"Tests are failing."}}`
		return response(http.StatusOK, v2Body("", decision)), nil
	})}
	current, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", PromptVersion: "prompt", Client: currentClient,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: store,
		ExtractionVersion: extractor.ExtractionVersionV2, V2Variant: extractor.V2VariantCurrent,
	})
	s.Require().NoError(err)
	typedCalls := 0
	typedClient := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		typedCalls++
		decision := `{"decision":"create","evidence_event_ids":["event-1"],"reason_codes":["explicit_new_fact"],"fact":{"kind":"blocker","subject":"release","statement":"Tests are failing","modality":"asserted"}}`
		return response(http.StatusOK, typedV2Body(decision)), nil
	})}
	typed, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", PromptVersion: "prompt", Client: typedClient,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: store,
		ExtractionVersion: extractor.ExtractionVersionV2, V2Variant: extractor.V2VariantTypedCurrent,
	})
	s.Require().NoError(err)
	ctx := teamnote.WithScope(context.Background(), "scope-v2-typed-protocol-change")

	_, err = current.Extract(ctx, v2Slice())
	s.Require().NoError(err)
	result, err := typed.Extract(ctx, v2Slice())

	s.Require().NoError(err)
	s.Equal(1, currentCalls)
	s.Equal(1, typedCalls)
	s.Require().Len(result.Candidates, 1)
	s.Equal("Tests are failing.", result.Candidates[0].Body)
}
