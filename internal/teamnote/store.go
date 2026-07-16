package teamnote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
)

type NoteStore interface {
	ApplyExtractionRun(context.Context, string, ExtractionRun) ([]Note, error)
	RecallNotes(context.Context, string, RecallRequest) (NoteEnvelope, error)
}

// ExtractionRun is one durable extraction decision over a bounded Session Slice.
type ExtractionRun struct {
	ID                string
	Actor             Actor
	FromSequence      int64
	ToSequence        int64
	InputChecksum     string
	CandidateChecksum string
	Model             string
	PromptVersion     string
	InputTokens       int
	OutputTokens      int
	Candidates        []Candidate
	Evidence          []SessionEvent
}

// NormalizeExtractionRun binds one idempotency key to the complete Candidate batch.
func NormalizeExtractionRun(run ExtractionRun) (ExtractionRun, error) {
	encoded, err := json.Marshal(run.Candidates)
	if err != nil {
		return ExtractionRun{}, fmt.Errorf("encode extraction run candidates: %w", err)
	}
	sum := sha256.Sum256(encoded)
	checksum := hex.EncodeToString(sum[:])
	if run.CandidateChecksum != "" && run.CandidateChecksum != checksum {
		return ExtractionRun{}, fmt.Errorf("candidate checksum for extraction run %q: %w", run.ID, ErrExtractionRunConflict)
	}
	run.CandidateChecksum = checksum
	return run, nil
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
