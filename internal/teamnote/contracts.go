// Package teamnote owns canonical short-lived collaboration state.
//
// Runtime is the external seam used by transports and evals. Extractor and
// persistence seams belong to the implementation and are intentionally not
// exposed here until the first working adapters require them.
package teamnote

import (
	"context"

	"github.com/pax-beehive/pax-nexus/internal/session"
)

type Actor = session.Actor
type SessionEvent = session.SessionEvent
type SessionBatch = session.SessionBatch
type IngestReceipt = session.IngestReceipt

// RecallRequest describes one authenticated delivery opportunity.
type RecallRequest struct {
	Actor       Actor  `json:"actor"`
	TaskRef     string `json:"task_ref,omitempty"`
	ThreadRef   string `json:"thread_ref,omitempty"`
	TokenBudget int    `json:"token_budget"`
	Query       string `json:"query,omitempty"`
	MaxItems    int    `json:"max_items,omitempty"`
}

// NoteCertainty describes whether recalled text is an answerable fact or an
// open/proposed collaboration state.
type NoteCertainty string

const (
	CertaintyConfirmed  NoteCertainty = "confirmed"
	CertaintyProposed   NoteCertainty = "proposed"
	CertaintyUnresolved NoteCertainty = "unresolved"
)

// NoteEnvelope carries context text plus per-item identity and origin for
// provider attribution and evaluation.
type NoteEnvelope struct {
	Revision string                `json:"revision"`
	Items    []string              `json:"items"`
	Tokens   int                   `json:"tokens"`
	Details  []RecalledNote        `json:"details,omitempty"`
	Decision RecallDecisionSummary `json:"decision"`
	// ObservationID links successful durable recalls to an administrative
	// diagnostic without exposing the identifier in product JSON responses.
	ObservationID int64 `json:"-"`
}

// RecallReasonCode identifies a stable reason why the selected evidence is
// not sufficient for an early return from an outer recall router.
type RecallReasonCode string

const (
	RecallReasonNoEvidence   RecallReasonCode = "no_evidence"
	RecallReasonFactCoverage RecallReasonCode = "fact_coverage"
	RecallReasonConfidence   RecallReasonCode = "evidence_confidence"
	RecallReasonBudgetDrop   RecallReasonCode = "answer_bearing_budget_drop"
	RecallReasonHardGate     RecallReasonCode = "hard_gate"
)

// RecallDecisionSummary is the small auditable decision consumed by an outer
// recall router. Detailed scoring and rejection evidence remains in RecallTrace.
type RecallDecisionSummary struct {
	EvidenceSufficient bool               `json:"evidence_sufficient"`
	ReasonCodes        []RecallReasonCode `json:"reason_codes,omitempty"`
}

// RecalledNote carries per-item identity and origin without changing the
// context text consumed by existing callers.
type RecalledNote struct {
	NoteID        string        `json:"note_id"`
	SourceNoteIDs []string      `json:"source_note_ids,omitempty"`
	Revision      int           `json:"revision"`
	Text          string        `json:"text"`
	Origin        Actor         `json:"origin"`
	Relevance     float64       `json:"relevance"`
	Certainty     NoteCertainty `json:"certainty"`
}

// Runtime is the small caller interface shared by HTTP and eval adapters.
// Implementations own extraction, admission, canonical state, and delivery.
type Runtime interface {
	ObserveSession(context.Context, SessionBatch) (IngestReceipt, error)
	RecallNotes(context.Context, RecallRequest) (NoteEnvelope, error)
}

//go:generate mockgen -source=contracts.go -destination=mocks/runtime.go -package=mocks Runtime
