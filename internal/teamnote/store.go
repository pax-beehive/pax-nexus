package teamnote

import (
	"context"
	"sync"
)

type NoteStore interface {
	ApplyCandidate(context.Context, string, string, Candidate, []SessionEvent) (Note, error)
	RecallNotes(context.Context, string, RecallRequest) (NoteEnvelope, error)
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
