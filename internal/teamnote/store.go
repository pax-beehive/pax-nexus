package teamnote

import (
	"context"
	"sync"
)

type NoteStore interface {
	ApplyExtractionRun(context.Context, string, ExtractionRun) ([]Note, error)
	RecallNotes(context.Context, string, RecallRequest) (NoteEnvelope, error)
}

// ExtractionRun is one durable extraction decision over a bounded Session Slice.
type ExtractionRun struct {
	ID            string
	Actor         Actor
	FromSequence  int64
	ToSequence    int64
	InputChecksum string
	Model         string
	PromptVersion string
	InputTokens   int
	OutputTokens  int
	Candidates    []Candidate
	Evidence      []SessionEvent
}

type ScopedLedgerStore struct {
	mu      sync.Mutex
	policy  TTLPolicy
	clock   Clock
	ledgers map[string]*Ledger
}

func NewScopedLedgerStore(policy TTLPolicy, clock Clock) *ScopedLedgerStore {
	return &ScopedLedgerStore{policy: policy, clock: clock, ledgers: make(map[string]*Ledger)}
}

func (s *ScopedLedgerStore) ApplyCandidate(ctx context.Context, scopeID, _ string, candidate Candidate, evidence []SessionEvent) (Note, error) {
	return s.ledger(scopeID).Apply(ctx, candidate, evidence)
}

func (s *ScopedLedgerStore) ApplyExtractionRun(ctx context.Context, scopeID string, run ExtractionRun) ([]Note, error) {
	return s.ledger(scopeID).ApplyRun(ctx, run)
}

func (s *ScopedLedgerStore) RecallNotes(ctx context.Context, scopeID string, request RecallRequest) (NoteEnvelope, error) {
	return s.ledger(scopeID).Recall(ctx, request)
}

func (s *ScopedLedgerStore) ledger(scopeID string) *Ledger {
	s.mu.Lock()
	defer s.mu.Unlock()
	ledger, ok := s.ledgers[scopeID]
	if !ok {
		ledger = NewLedger(s.policy, s.clock)
		s.ledgers[scopeID] = ledger
	}
	return ledger
}
