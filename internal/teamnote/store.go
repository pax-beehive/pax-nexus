package teamnote

import (
	"context"
	"sort"
	"sync"
)

type NoteStore interface {
	ApplyExtractionRun(context.Context, string, ExtractionRun) ([]Note, error)
	RecallNotes(context.Context, string, RecallRequest) (NoteEnvelope, error)
}

// ExtractionRun is one durable extraction decision over a bounded Session Slice.
type ExtractionRun struct {
	ID                    string
	Actor                 Actor
	FromSequence          int64
	ToSequence            int64
	InputChecksum         string
	CandidateChecksum     string
	Model                 string
	PromptVersion         string
	InputTokens           int
	OutputTokens          int
	Candidates            []Candidate
	TransitionAuthorities []TransitionAuthority
	Evidence              []SessionEvent
	// Rejections records candidates dropped before admission. Rejected
	// candidates are not part of the admitted batch and do not affect the
	// candidate checksum.
	Rejections []CandidateRejection
}

// TransitionAuthority records the exact validated source clauses that may
// authorize one candidate to replace facts in an existing note.
type TransitionAuthority struct {
	CandidateID     string
	PriorStateRef   string
	EvidenceClauses []TransitionEvidenceClause
	ReasonCodes     []string
}

type TransitionEvidenceClause struct {
	EventID string
	Quote   string
}

// CandidateRejection records one candidate dropped before admission with the
// deterministic reason, so extraction evaluation can attribute lost facts.
type CandidateRejection struct {
	Candidate Candidate
	Reason    string
}

type ScopedLedgerStore struct {
	mu           sync.Mutex
	policy       TTLPolicy
	clock        Clock
	recallPolicy RecallPolicy
	ledgers      map[string]*Ledger
}

func NewScopedLedgerStore(policy TTLPolicy, clock Clock) *ScopedLedgerStore {
	return NewScopedLedgerStoreWithRecallPolicy(policy, clock, DefaultRecallPolicy())
}

// NewScopedLedgerStoreWithRecallPolicy configures every scope with the same
// recall planning policy used by durable NoteStore adapters.
func NewScopedLedgerStoreWithRecallPolicy(policy TTLPolicy, clock Clock, recallPolicy RecallPolicy) *ScopedLedgerStore {
	return &ScopedLedgerStore{
		policy: policy, clock: clock, recallPolicy: recallPolicy, ledgers: make(map[string]*Ledger),
	}
}

func (s *ScopedLedgerStore) ApplyCandidate(ctx context.Context, scopeID, _ string, candidate Candidate, evidence []SessionEvent) (Note, error) {
	return s.ledger(scopeID).Apply(ctx, candidate, evidence)
}

func (s *ScopedLedgerStore) ApplyExtractionRun(ctx context.Context, scopeID string, run ExtractionRun) ([]Note, error) {
	return s.ledger(scopeID).ApplyRun(ctx, run)
}

// SnapshotNotes returns the active notes in one scope, ordered by ID. It
// supports evaluation snapshots that mirror the persisted active-note view.
func (s *ScopedLedgerStore) SnapshotNotes(scopeID string) []Note {
	return s.ledger(scopeID).snapshotNotes()
}

func (l *Ledger) snapshotNotes() []Note {
	l.mu.Lock()
	defer l.mu.Unlock()
	notes := make([]Note, 0, len(l.notes))
	for _, note := range l.notes {
		if note.State == StateActive && note.InvalidAt == nil {
			notes = append(notes, cloneNote(note))
		}
	}
	sort.Slice(notes, func(left, right int) bool { return notes[left].ID < notes[right].ID })
	return notes
}

func (s *ScopedLedgerStore) RecallNotes(ctx context.Context, scopeID string, request RecallRequest) (NoteEnvelope, error) {
	return s.ledger(scopeID).Recall(ctx, request)
}

func (s *ScopedLedgerStore) ledger(scopeID string) *Ledger {
	s.mu.Lock()
	defer s.mu.Unlock()
	ledger, ok := s.ledgers[scopeID]
	if !ok {
		ledger = NewLedgerWithRecallPolicy(s.policy, s.clock, s.recallPolicy)
		s.ledgers[scopeID] = ledger
	}
	return ledger
}
