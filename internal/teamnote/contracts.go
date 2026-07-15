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
}

// NoteEnvelope carries context text plus per-item identity and origin for
// provider attribution and evaluation.
type NoteEnvelope struct {
	Revision string         `json:"revision"`
	Items    []string       `json:"items"`
	Tokens   int            `json:"tokens"`
	Details  []RecalledNote `json:"details,omitempty"`
}

// RecalledNote carries per-item identity and origin without changing the
// context text consumed by existing callers.
type RecalledNote struct {
	NoteID   string `json:"note_id"`
	Revision int    `json:"revision"`
	Text     string `json:"text"`
	Origin   Actor  `json:"origin"`
}

// Runtime is the small caller interface shared by HTTP and eval adapters.
// Implementations own extraction, admission, canonical state, and delivery.
type Runtime interface {
	ObserveSession(context.Context, SessionBatch) (IngestReceipt, error)
	RecallNotes(context.Context, RecallRequest) (NoteEnvelope, error)
}

//go:generate mockgen -source=contracts.go -destination=mocks/runtime.go -package=mocks Runtime
