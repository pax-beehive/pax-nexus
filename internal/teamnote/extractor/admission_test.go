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
		name       string
		content    string
		clause     string
		wantClause string
		subject    string
		body       string
		wantNotes  int
		wantCause  string
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
			name: "exact commitment overrides surrounding suggestion", content: "I suggest one final review. I’ll log UX as owner for the final journey lock.",
			clause:  "I’ll log UX as owner for the final journey lock.",
			subject: "final journey lock", body: "UX is owner for the final journey lock.", wantNotes: 1,
		},
		{
			name: "unrelated commitment cannot authorize ownership", content: "I suggest one final review. I’ll update the final baseline.",
			clause:  "I’ll update the final baseline.",
			subject: "final baseline owner", body: "Legal owns the final baseline.", wantCause: "unsupported clause, owner, deadline, or answer-changing modifier",
		},
		{
			name: "shared temporal qualifier cannot authorize ownership", content: "I suggest one final review. I’ll update the final baseline now.",
			clause:  "I’ll update the final baseline now.",
			subject: "final baseline", body: "Legal owns the final baseline now.", wantCause: "unsupported clause, owner, deadline, or answer-changing modifier",
		},
		{
			name: "agreed clause overrides surrounding suggestion", content: "I suggest one final review. Agreed — final baseline.",
			clause:  "Agreed — final baseline.",
			subject: "baseline", body: "Final baseline agreed.", wantNotes: 1,
		},
		{
			name: "change-to-decision label establishes commitment", content: "I suggest one final review. **Change to decision: Legal takes ownership of rollback evidence.**",
			clause:  "**Change to decision: Legal takes ownership of rollback evidence.**",
			subject: "rollback evidence", body: "Legal takes ownership of rollback evidence.", wantNotes: 1,
		},
		{
			name: "markdown formatting preserves exact source span", content: "I suggest revising the baseline. **Update the baseline now** to include the mandatory consent timestamp and 7-year audit-trail fields.",
			clause:     "Update the baseline now to include the mandatory consent timestamp and 7-year audit-trail fields.",
			wantClause: "**Update the baseline now** to include the mandatory consent timestamp and 7-year audit-trail fields.",
			subject:    "baseline consent and audit fields", body: "Update the baseline with the mandatory consent timestamp and 7-year audit-trail fields.", wantNotes: 1,
		},
		{
			name: "ambiguous markdown span is not repaired", content: "Update **the** baseline now. Later: Update **the** baseline now.",
			clause: "Update the baseline now.", wantCause: "exact text",
		},
		{
			name: "overlapping markdown span is not repaired", content: "**a****a****a**",
			clause: "aa", wantCause: "exact text",
		},
		{
			name: "underscore formatting preserves exact source span", content: "Update _the_ baseline now.",
			clause:     "Update the baseline now.",
			wantClause: "Update _the_ baseline now.",
			subject:    "baseline", body: "Update the baseline now.", wantNotes: 1,
		},
		{
			name: "markdown decision label is an atomic boundary", content: "I suggest tightening this up. **Change to decision:** pause scenario creation and have Legal and Ops freeze the audit-trail rules.",
			clause:  "pause scenario creation and have Legal and Ops freeze the audit-trail rules.",
			subject: "scenario creation and audit rules", body: "Pause scenario creation and have Legal and Ops freeze the audit-trail rules.", wantNotes: 1,
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
			clause:  "Reporting can validate fit against the same standard before July 18.",
			subject: "reporting validation", body: "Reporting can validate fit against the same standard before July 18.", wantNotes: 1,
		},
		{
			name: "preference to lock remains non-committal", content: "I’d lock the single provider model now.",
			clause:  "I’d lock the single provider model now.",
			subject: "provider model", body: "Lock the single provider model now.", wantCause: "non-committal",
		},
		{
			name:      "candidate cannot add adjacent clause details",
			content:   "Compliance owns the exception log. After Legal signs, we can close this as Detected.",
			clause:    "we can close this as Detected",
			subject:   "risk status",
			body:      "Risk can close as Detected after Compliance owns the exception log and Legal signs.",
			wantCause: "unsupported clause, owner, deadline, or answer-changing modifier",
		},
		{
			name:      "candidate cannot invent owner and deadline",
			content:   "Risk is blocked.",
			clause:    "Risk is blocked.",
			subject:   "risk status",
			body:      "Risk is blocked; Alice owns it until July 18.",
			wantCause: "unsupported clause, owner, deadline, or answer-changing modifier",
		},
		{
			name:      "candidate cannot invent owner in same clause",
			content:   "Risk is blocked.",
			clause:    "Risk is blocked.",
			subject:   "risk status",
			body:      "Risk is blocked and Alice owns it.",
			wantCause: "unsupported clause, owner, deadline, or answer-changing modifier",
		},
		{
			name:      "candidate cannot invent deadline in same clause",
			content:   "Risk is blocked.",
			clause:    "Risk is blocked.",
			subject:   "risk status",
			body:      "Risk is blocked until July 18.",
			wantCause: "unsupported clause, owner, deadline, or answer-changing modifier",
		},
		{
			name:      "candidate cannot invent ISO deadline",
			content:   "Risk is blocked.",
			clause:    "Risk is blocked.",
			subject:   "risk status",
			body:      "Risk is blocked by 2026-08-01.",
			wantCause: "unsupported clause, owner, deadline, or answer-changing modifier",
		},
		{
			name:      "candidate cannot invent slash deadline",
			content:   "Risk is blocked.",
			clause:    "Risk is blocked.",
			subject:   "risk status",
			body:      "Risk is blocked on 08/01/2026.",
			wantCause: "unsupported clause, owner, deadline, or answer-changing modifier",
		},
		{
			name:      "candidate preserves supported owner and deadline",
			content:   "Alice owns the blocked risk until July 18.",
			clause:    "Alice owns the blocked risk until July 18.",
			subject:   "risk status",
			body:      "Alice owns the blocked risk until July 18.",
			wantNotes: 1,
		},
		{
			name:      "candidate cannot replace supported owner and deadline values",
			content:   "Alice owns the blocked risk until July 18.",
			clause:    "Alice owns the blocked risk until July 18.",
			subject:   "risk status",
			body:      "Bob owns the blocked risk until August 1.",
			wantCause: "unsupported clause, owner, deadline, or answer-changing modifier",
		},
		{
			name:      "short candidate body cannot replace supported owner",
			content:   "Alice owns risk.",
			clause:    "Alice owns risk.",
			subject:   "Alice risk",
			body:      "Bob owns it.",
			wantCause: "unsupported clause, owner, deadline, or answer-changing modifier",
		},
		{
			name:      "candidate cannot replace designated lead",
			content:   "Alice is designated lead for risk.",
			clause:    "Alice is designated lead for risk.",
			subject:   "risk lead",
			body:      "Bob is designated lead for risk.",
			wantCause: "unsupported clause, owner, deadline, or answer-changing modifier",
		},
		{
			name:      "candidate cannot replace owned-by value",
			content:   "Risk is owned by Alice.",
			clause:    "Risk is owned by Alice.",
			subject:   "risk owner",
			body:      "Risk is owned by Bob.",
			wantCause: "unsupported clause, owner, deadline, or answer-changing modifier",
		},
		{
			name:      "candidate may preserve owner using owned-by syntax",
			content:   "Alice owns risk.",
			clause:    "Alice owns risk.",
			subject:   "risk owner",
			body:      "Risk is owned by Alice.",
			wantNotes: 1,
		},
		{
			name: "capability request remains non-committal", content: "Can you validate fit against the same standard before July 18?",
			clause: "Can you validate fit against the same standard before July 18?", wantCause: "non-committal",
		},
		{
			name: "desired owner remains non-committal", content: "I’d want Legal to own rollback evidence. Can we lock that in now?",
			clause:  "I’d want Legal to own rollback evidence.",
			subject: "rollback evidence owner", body: "Legal owns rollback evidence.", wantCause: "non-committal",
		},
		{
			name: "punctuation-free can-we remains non-committal", content: "Can we lock Legal as rollback evidence owner",
			clause:  "Can we lock Legal as rollback evidence owner",
			subject: "rollback evidence owner", body: "Legal is rollback evidence owner.", wantCause: "non-committal",
		},
		{
			name: "shortest compound clause", content: "Compliance owns the exceptions log, and Reporting owns the audit log.",
			clause: "Compliance owns the exceptions log", wantNotes: 1,
		},
		{
			name: "compound object remains atomic", content: "Compliance owns Finance and Risk controls.",
			clause:  "Compliance owns Finance and Risk controls.",
			subject: "Finance and Risk controls", body: "Compliance owns Finance and Risk controls.", wantNotes: 1,
		},
		{
			name: "oxford comma list remains atomic", content: "Compliance owns Finance, Risk, and Audit controls.",
			clause:  "Compliance owns Finance, Risk, and Audit controls.",
			subject: "Finance Risk and Audit controls", body: "Compliance owns Finance, Risk, and Audit controls.", wantNotes: 1,
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
			clause:  "Dr. Smith owns the exceptions log.",
			subject: "exceptions log owner", body: "Dr. Smith owns the exceptions log.", wantNotes: 1,
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
			subject := test.subject
			if subject == "" {
				subject = "exceptions log owner"
			}
			body := test.body
			if body == "" {
				body = "Compliance owns the exceptions log."
			}
			decision := `{"decision":"create","identity_ref":"owner/exceptions-log","evidence_event_ids":["event-1"],` +
				`"evidence_clauses":[{"event_id":"event-1","quote":` + quoteJSON(test.clause) + `}],` +
				`"reason_codes":["explicit_new_fact"],"candidate":{"kind":"status","subject":` + quoteJSON(subject) +
				`,"body":` + quoteJSON(body) + `}}`
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
			s.Len(result.Candidates, test.wantNotes, "decision trace: %+v", result.Trace)
			if test.wantNotes == 1 {
				s.Require().Len(result.TransitionAuthorities, 1)
				s.Equal(result.Candidates[0].ID, result.TransitionAuthorities[0].CandidateID)
				s.Require().Len(result.TransitionAuthorities[0].EvidenceClauses, 1)
				wantClause := test.wantClause
				if wantClause == "" {
					wantClause = test.clause
				}
				s.Equal(wantClause, result.TransitionAuthorities[0].EvidenceClauses[0].Quote)
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

func quoteJSON(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
