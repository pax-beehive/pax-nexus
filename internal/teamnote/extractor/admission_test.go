package extractor_test

import (
	"context"
	"encoding/json"
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
			name: "exact value with decimal", content: "The alert threshold is 1.5%.",
			clause: "The alert threshold is 1.5%.", wantNotes: 1,
		},
		{
			name: "abbreviation does not split clause", content: "Dr. Smith owns the exceptions log.",
			clause: "Dr. Smith owns the exceptions log.", wantNotes: 1,
		},
		{
			name: "url does not split clause", content: "The runbook is https://ops.example.com/runbook.",
			clause: "The runbook is https://ops.example.com/runbook.", wantNotes: 1,
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
			if test.wantCause != "" {
				s.Require().Len(result.Trace.DecisionRejections, 1)
				s.Contains(result.Trace.DecisionRejections[0].Reason, test.wantCause)
			}
		})
	}
}

func (s *admissionSuite) TestTemporalAdmissionUsesNewEventObservationTime() {
	slice := v2Slice()
	slice.Events[0].OccurredAt = time.Date(2024, time.January, 2, 12, 0, 0, 0, time.UTC)
	overlap := slice.Events[0]
	overlap.ID = "event-overlap"
	overlap.Sequence = 0
	overlap.OccurredAt = time.Date(2030, time.January, 2, 12, 0, 0, 0, time.UTC)
	slice.Events = append([]teamnote.SessionEvent{overlap}, slice.Events...)
	slice.OverlapEventIDs = []string{overlap.ID}
	decision := `{"decision":"create","identity_ref":"window/review","evidence_event_ids":["event-1"],` +
		`"temporal_expression":"through 2025","invalid_at":"2025-01-01T00:00:00Z","temporal_resolution":"explicit",` +
		`"reason_codes":["explicit_new_fact"],"candidate":{"kind":"status","subject":"review window","body":"The review remains active through 2025."}}`
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, v2Body("", decision)), nil
	})}
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: extractor.NewMemoryEpisodeStore(),
		ExtractionVersion: extractor.ExtractionVersionV2,
	})
	s.Require().NoError(err)

	result, err := adapter.Extract(teamnote.WithScope(context.Background(), "temporal-observation-time"), slice)

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Require().NotNil(result.Candidates[0].InvalidAt)
	s.Equal(time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC), *result.Candidates[0].InvalidAt)
}

func quoteJSON(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
