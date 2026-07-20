package extractor_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
	"github.com/stretchr/testify/suite"
)

type admissionSuite struct {
	suite.Suite
}

func TestAdmissionSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(admissionSuite))
}

func (s *admissionSuite) TestSourceClauseAdmissionValidatesExactAtomicEvidence() {
	tests := []struct {
		name      string
		content   string
		clause    string
		wantNotes int
		wantCause string
	}{
		{
			name: "exact committed clause", content: "Compliance owns the exceptions log. Please publish it today.",
			clause: "Compliance owns the exceptions log.", wantNotes: 1,
		},
		{
			name: "proposal clause", content: "I propose Compliance as owner. Reporting owns the current log.",
			clause: "I propose Compliance as owner.", wantCause: "non-committal",
		},
		{
			name: "quote absent from event", content: "Compliance owns the exceptions log.",
			clause: "Compliance owns the audit log.", wantCause: "exact text",
		},
		{
			name: "surrounding whitespace is not exact", content: "Compliance owns the exceptions log.",
			clause: "  Compliance owns the exceptions log.  ", wantCause: "surrounding whitespace",
		},
		{
			name: "question clause", content: "Should Compliance own the exceptions log?",
			clause: "Should Compliance own the exceptions log?", wantCause: "non-committal",
		},
		{
			name: "broad quote cannot launder proposal", content: "I propose Compliance owns the exceptions log. Reporting is the approved owner.",
			clause: "I propose Compliance owns the exceptions log. Reporting is the approved owner.", wantCause: "atomic clause",
		},
		{
			name: "factual predicate does not neutralize proposal", content: "I propose Compliance owns the exceptions log.",
			clause: "I propose Compliance owns the exceptions log.", wantCause: "non-committal",
		},
		{
			name: "adjacent approval does not neutralize proposal", content: "I propose Compliance owns the exceptions log, but Reporting is the approved owner.",
			clause: "I propose Compliance owns the exceptions log, but Reporting is the approved owner.", wantCause: "non-committal",
		},
		{
			name: "completed proposal is committed", content: "The proposal was approved.",
			clause: "The proposal was approved.", wantNotes: 1,
		},
		{
			name: "approved request is committed", content: "The request was approved.",
			clause: "The request was approved.", wantNotes: 1,
		},
		{
			name: "conditional state is committed", content: "If the July 26 milestone slips, Ops Lead owns the rollback evidence pack.",
			clause: "If the July 26 milestone slips, Ops Lead owns the rollback evidence pack.", wantNotes: 1,
		},
		{
			name: "observable incomplete state", content: "The control-mapping revision is still moving, so I can't lock the regulatory formatting yet.",
			clause: "The control-mapping revision is still moving, so I can't lock the regulatory formatting yet.", wantNotes: 1,
		},
		{
			name: "deadline-bound role capability", content: "I'm leaning toward one definition. Reporting can validate fit against the same standard before July 18.",
			clause: "Reporting can validate fit against the same standard before July 18.", wantNotes: 1,
		},
		{
			name: "capability request remains non-committal", content: "Can you validate fit against the same standard before July 18?",
			clause: "Can you validate fit against the same standard before July 18?", wantCause: "non-committal",
		},
		{
			name: "shortest compound clause", content: "Compliance owns the exceptions log, and Reporting owns the audit log.",
			clause: "Compliance owns the exceptions log", wantNotes: 1,
		},
		{
			name: "compound object remains atomic", content: "Compliance owns Finance and Risk controls.",
			clause: "Compliance owns Finance and Risk controls.", wantNotes: 1,
		},
		{
			name: "oxford comma list remains atomic", content: "Compliance owns Finance, Risk, and Audit controls.",
			clause: "Compliance owns Finance, Risk, and Audit controls.", wantNotes: 1,
		},
		{
			name: "exact value with decimal", content: "The alert threshold is 1.5%.",
			clause: "The alert threshold is 1.5%.", wantNotes: 1,
		},
		{
			name: "decimal cannot be truncated", content: "The alert threshold is 1.5%.",
			clause: "The alert threshold is 1", wantCause: "atomic clause",
		},
		{
			name: "abbreviation does not split clause", content: "Dr. Smith owns the exceptions log.",
			clause: "Dr. Smith owns the exceptions log.", wantNotes: 1,
		},
		{
			name: "url does not split clause", content: "The runbook is https://ops.example.com/runbook.",
			clause: "The runbook is https://ops.example.com/runbook.", wantNotes: 1,
		},
		{
			name: "url cannot be truncated", content: "The runbook is https://ops.example.com/runbook.",
			clause: "The runbook is https://ops", wantCause: "atomic clause",
		},
	}

	for _, test := range tests {
		s.Run(test.name, func() {
			slice := v2Slice()
			slice.Events[0].Content = test.content
			decision := `{"decision":"create","identity_ref":"owner/exceptions-log","evidence_event_ids":["event-1"],` +
				`"evidence_clauses":[{"event_id":"event-1","quote":` + quoteJSON(test.clause) + `}],` +
				`"reason_codes":["explicit_new_fact"],"candidate":{"kind":"status","subject":"exceptions log owner","body":"Compliance owns the exceptions log."}}`
			client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return response(http.StatusOK, v2Body("", decision)), nil
			})}
			adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
				BaseURL: "http://extractor.test", Model: "model", Client: client,
				ContextMode: extractor.ContextModeRolling, EpisodeStore: extractor.NewMemoryEpisodeStore(),
				ExtractionVersion: extractor.ExtractionVersionV2, V2Variant: extractor.V2VariantSourceClause,
			})
			s.Require().NoError(err)

			result, err := adapter.Extract(teamnote.WithScope(context.Background(), "source-clause-"+test.name), slice)

			s.Require().NoError(err)
			s.Len(result.Candidates, test.wantNotes)
			if test.wantNotes == 1 {
				s.Require().Len(result.TransitionAuthorities, 1)
				s.Equal(result.Candidates[0].ID, result.TransitionAuthorities[0].CandidateID)
				s.Require().Len(result.TransitionAuthorities[0].EvidenceClauses, 1)
				s.Equal(test.clause, result.TransitionAuthorities[0].EvidenceClauses[0].Quote)
			}
			if test.wantCause != "" {
				s.Require().Len(result.Trace.DecisionRejections, 1)
				s.Contains(result.Trace.DecisionRejections[0].Reason, test.wantCause)
			}
		})
	}
}

func (s *admissionSuite) TestTemporalAdmissionUsesNewEventObservationTime() {
	slice := v2Slice()
	slice.Events[0].Content = "The review remains active through 2025."
	slice.Events[0].OccurredAt = time.Date(2024, time.January, 2, 12, 0, 0, 0, time.UTC)
	overlap := slice.Events[0]
	overlap.ID = "event-overlap"
	overlap.Sequence = 0
	overlap.OccurredAt = time.Date(2030, time.January, 2, 12, 0, 0, 0, time.UTC)
	slice.Events = append([]teamnote.SessionEvent{overlap}, slice.Events...)
	slice.OverlapEventIDs = []string{overlap.ID}
	decision := `{"decision":"create","identity_ref":"window/review","evidence_event_ids":["event-1"],` +
		`"evidence_clauses":[{"event_id":"event-1","quote":"The review remains active through 2025."}],` +
		`"temporal_expression":"through 2025","invalid_at":"2025-01-01T00:00:00Z","temporal_resolution":"explicit",` +
		`"reason_codes":["explicit_new_fact"],"candidate":{"kind":"status","subject":"review window","body":"The review remains active through 2025."}}`
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, v2Body("", decision)), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: extractor.NewMemoryEpisodeStore(),
		ExtractionVersion: extractor.ExtractionVersionV2, V2Variant: extractor.V2VariantSourceClause,
	})
	s.Require().NoError(err)

	result, err := adapter.Extract(teamnote.WithScope(context.Background(), "temporal-observation-time"), slice)

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Require().NotNil(result.Candidates[0].InvalidAt)
	s.Equal(time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC), *result.Candidates[0].InvalidAt)
}

func (s *admissionSuite) TestImplicitStatePromptReviewsDurableStateBeforeNoState() {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		s.Require().NoError(err)
		prompt := string(body)
		s.Contains(prompt, "Observable incomplete state is durable")
		s.Contains(prompt, "Declarative role capability tied to a concrete action and deadline")
		s.Contains(prompt, "Preserve can, cannot, and not-yet modality")
		s.Contains(prompt, "can you")
		return response(http.StatusOK, v2BodyWithNoStateEvents("", "", `"event-1"`)), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: extractor.NewMemoryEpisodeStore(),
		ExtractionVersion: extractor.ExtractionVersionV2, V2Variant: extractor.V2VariantImplicitState,
	})
	s.Require().NoError(err)

	_, err = adapter.Extract(teamnote.WithScope(context.Background(), "implicit-state-review-prompt"), v2Slice())

	s.Require().NoError(err)
}

func (s *admissionSuite) TestSourceClausePromptExcludesImplicitStateExperiment() {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		s.Require().NoError(err)
		s.NotContains(string(body), "Implicit durable-state review before no_state")
		return response(http.StatusOK, v2BodyWithNoStateEvents("", "", `"event-1"`)), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: extractor.NewMemoryEpisodeStore(),
		ExtractionVersion: extractor.ExtractionVersionV2, V2Variant: extractor.V2VariantSourceClause,
	})
	s.Require().NoError(err)

	_, err = adapter.Extract(teamnote.WithScope(context.Background(), "source-clause-prompt-isolation"), v2Slice())

	s.Require().NoError(err)
}

func quoteJSON(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
