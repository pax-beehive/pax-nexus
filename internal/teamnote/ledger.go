package teamnote

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidCandidate = errors.New("invalid note candidate")
	ErrMissingEvidence  = errors.New("candidate evidence is missing")
	ErrNoteNotFound     = errors.New("team note not found")
	ErrInvalidRecall    = errors.New("invalid recall request")
)

type NoteKind string

const (
	KindStatus            NoteKind = "status"
	KindBlocker           NoteKind = "blocker"
	KindHandoff           NoteKind = "handoff"
	KindArtifactReference NoteKind = "artifact_reference"
)

type CandidateAction string

const (
	ActionCreate  CandidateAction = "create"
	ActionUpdate  CandidateAction = "update"
	ActionResolve CandidateAction = "resolve"
)

type NoteState string

const (
	StateActive   NoteState = "active"
	StateResolved NoteState = "resolved"
	StateExpired  NoteState = "expired"
)

type Candidate struct {
	ID               string          `json:"id"`
	Action           CandidateAction `json:"action"`
	Kind             NoteKind        `json:"kind"`
	Subject          string          `json:"subject"`
	Body             string          `json:"body"`
	TaskRef          string          `json:"task_ref,omitempty"`
	ThreadRef        string          `json:"thread_ref,omitempty"`
	Origin           Actor           `json:"origin"`
	AudienceAgentIDs []string        `json:"audience_agent_ids,omitempty"`
	RelatedSubjects  []string        `json:"related_subjects,omitempty"`
	EvidenceEventIDs []string        `json:"evidence_event_ids"`
	ValidAt          *time.Time      `json:"valid_at,omitempty"`
	InvalidAt        *time.Time      `json:"invalid_at,omitempty"`
	SourceOccurredAt time.Time       `json:"-"`
}

type Note struct {
	ID               string
	Key              string
	Kind             NoteKind
	Subject          string
	Body             string
	TaskRef          string
	ThreadRef        string
	Origin           Actor
	AudienceAgentIDs []string
	RelatedSubjects  []string
	EvidenceEventIDs []string
	State            NoteState
	Revision         int
	CreatedAt        time.Time
	UpdatedAt        time.Time
	SoftExpiresAt    time.Time
	HardExpiresAt    time.Time
	ValidAt          *time.Time
	InvalidAt        *time.Time
	SourceOccurredAt time.Time
}

type LeasePolicy struct {
	SoftTTL time.Duration
	HardTTL time.Duration
}

type TTLPolicy map[NoteKind]LeasePolicy

func DefaultTTLPolicy() TTLPolicy {
	return TTLPolicy{
		KindStatus:            {SoftTTL: 24 * time.Hour, HardTTL: 7 * 24 * time.Hour},
		KindBlocker:           {SoftTTL: 48 * time.Hour, HardTTL: 14 * 24 * time.Hour},
		KindHandoff:           {SoftTTL: 72 * time.Hour, HardTTL: 14 * 24 * time.Hour},
		KindArtifactReference: {SoftTTL: 7 * 24 * time.Hour, HardTTL: 30 * 24 * time.Hour},
	}
}

type Clock interface {
	Now() time.Time
}

type Ledger struct {
	mu         sync.Mutex
	policy     TTLPolicy
	clock      Clock
	notes      map[string]Note
	candidates map[string]string
	deliveries map[string]struct{}
}

func NewLedger(policy TTLPolicy, clock Clock) *Ledger {
	return &Ledger{
		policy: policy, clock: clock, notes: make(map[string]Note),
		candidates: make(map[string]string), deliveries: make(map[string]struct{}),
	}
}

func (l *Ledger) Apply(ctx context.Context, candidate Candidate, evidence []SessionEvent) (Note, error) {
	if err := ctx.Err(); err != nil {
		return Note{}, fmt.Errorf("apply candidate context: %w", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if key, ok := l.candidates[candidate.ID]; ok {
		return l.notes[key], nil
	}
	key := CanonicalKey(candidate)
	note, exists := l.notes[key]
	var current *Note
	if exists {
		current = &note
	}
	note, err := AdmitCandidate(l.policy, l.clock.Now(), candidate, evidence, current)
	if err != nil {
		return Note{}, err
	}
	l.notes[key] = note
	l.candidates[candidate.ID] = key
	return cloneNote(note), nil
}

func (l *Ledger) Recall(ctx context.Context, request RecallRequest) (NoteEnvelope, error) {
	if err := ctx.Err(); err != nil {
		return NoteEnvelope{}, fmt.Errorf("recall notes context: %w", err)
	}
	if err := ValidateRecall(request); err != nil {
		return NoteEnvelope{}, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock.Now()
	eligible := l.eligibleNotes(now, request)
	items := make([]string, 0, len(eligible))
	details := make([]RecalledNote, 0, len(eligible))
	usedTokens := 0
	lastRevision := ""
	for _, note := range eligible {
		item := FormatForRecallWithRelated(note, relatedNotes(note, eligible))
		tokens := estimateTokens(item)
		if usedTokens+tokens > request.TokenBudget {
			continue
		}
		items = append(items, item)
		details = append(details, RecalledNote{NoteID: note.ID, Revision: note.Revision, Text: item, Origin: note.Origin})
		usedTokens += tokens
		lastRevision = fmt.Sprintf("%s:%d", note.ID, note.Revision)
		l.deliveries[deliveryKey(note, request.Actor)] = struct{}{}
	}
	return NoteEnvelope{Revision: lastRevision, Items: items, Tokens: usedTokens, Details: details}, nil
}

func AdmitCandidate(policy TTLPolicy, now time.Time, candidate Candidate, evidence []SessionEvent, current *Note) (Note, error) {
	candidate = WithEvidenceTime(candidate, evidence)
	candidate.RelatedSubjects = normalizeSubjects(candidate.Subject, candidate.RelatedSubjects)
	if err := validateCandidate(candidate, policy); err != nil {
		return Note{}, err
	}
	if err := validateEvidence(candidate, evidence); err != nil {
		return Note{}, err
	}
	if candidate.SourceOccurredAt.IsZero() {
		return Note{}, fmt.Errorf("candidate source occurred at: %w", ErrInvalidCandidate)
	}
	if candidate.Action == ActionResolve {
		if current == nil {
			return Note{}, fmt.Errorf("resolve %q: %w", CanonicalKey(candidate), ErrNoteNotFound)
		}
		resolved := cloneNote(*current)
		resolved.State = StateResolved
		resolved.Body = candidate.Body
		resolved.RelatedSubjects = append([]string(nil), candidate.RelatedSubjects...)
		resolved.EvidenceEventIDs = append([]string(nil), candidate.EvidenceEventIDs...)
		resolved.Revision++
		resolved.UpdatedAt = now
		resolved.InvalidAt = cloneTime(candidate.InvalidAt)
		if resolved.InvalidAt == nil {
			resolved.InvalidAt = timePointer(candidate.SourceOccurredAt)
		}
		resolved.SourceOccurredAt = candidate.SourceOccurredAt
		return resolved, nil
	}
	lease := policy[candidate.Kind]
	if current == nil {
		return Note{
			ID: candidate.ID, Key: CanonicalKey(candidate), Kind: candidate.Kind, Subject: candidate.Subject,
			Body: candidate.Body, TaskRef: candidate.TaskRef, ThreadRef: candidate.ThreadRef,
			Origin: candidate.Origin, AudienceAgentIDs: append([]string(nil), candidate.AudienceAgentIDs...),
			RelatedSubjects:  append([]string(nil), candidate.RelatedSubjects...),
			EvidenceEventIDs: append([]string(nil), candidate.EvidenceEventIDs...), State: stateForValidity(candidate.InvalidAt),
			Revision: 1, CreatedAt: now, UpdatedAt: now, SoftExpiresAt: now.Add(lease.SoftTTL),
			HardExpiresAt: now.Add(lease.HardTTL), ValidAt: cloneTime(candidate.ValidAt),
			InvalidAt: cloneTime(candidate.InvalidAt), SourceOccurredAt: candidate.SourceOccurredAt,
		}, nil
	}
	updated := cloneNote(*current)
	updated.Body = candidate.Body
	updated.Origin = candidate.Origin
	updated.AudienceAgentIDs = append([]string(nil), candidate.AudienceAgentIDs...)
	updated.RelatedSubjects = append([]string(nil), candidate.RelatedSubjects...)
	updated.EvidenceEventIDs = append([]string(nil), candidate.EvidenceEventIDs...)
	updated.State = stateForValidity(candidate.InvalidAt)
	updated.Revision++
	updated.UpdatedAt = now
	updated.SoftExpiresAt = minTime(updated.HardExpiresAt, now.Add(lease.SoftTTL))
	updated.ValidAt = cloneTime(candidate.ValidAt)
	updated.InvalidAt = cloneTime(candidate.InvalidAt)
	updated.SourceOccurredAt = candidate.SourceOccurredAt
	return updated, nil
}

func (l *Ledger) eligibleNotes(now time.Time, request RecallRequest) []Note {
	notes := make([]Note, 0, len(l.notes))
	for key, note := range l.notes {
		if note.State == StateActive && (!now.Before(note.SoftExpiresAt) || !now.Before(note.HardExpiresAt)) {
			note.State = StateExpired
			l.notes[key] = note
		}
		if l.eligibleForRecall(note, request) {
			notes = append(notes, note)
		}
	}
	sort.Slice(notes, func(i, j int) bool {
		leftScore, rightScore := QueryScore(notes[i], request.Query), QueryScore(notes[j], request.Query)
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		left, right := notePriority(notes[i].Kind), notePriority(notes[j].Kind)
		if left != right {
			return left < right
		}
		return notes[i].UpdatedAt.After(notes[j].UpdatedAt)
	})
	return notes
}

func (l *Ledger) eligibleForRecall(note Note, request RecallRequest) bool {
	if note.State != StateActive || (note.TaskRef != "" && note.TaskRef != request.TaskRef) {
		return false
	}
	if note.ThreadRef != "" && note.ThreadRef != request.ThreadRef {
		return false
	}
	if !includesAudience(note.AudienceAgentIDs, request.Actor.AgentID) {
		return false
	}
	_, delivered := l.deliveries[deliveryKey(note, request.Actor)]
	return !delivered
}

func validateCandidate(candidate Candidate, policy TTLPolicy) error {
	if strings.TrimSpace(candidate.ID) == "" || strings.TrimSpace(candidate.Subject) == "" {
		return fmt.Errorf("candidate identity: %w", ErrInvalidCandidate)
	}
	if strings.TrimSpace(candidate.Origin.UserID) == "" || strings.TrimSpace(candidate.Origin.AgentID) == "" || strings.TrimSpace(candidate.Origin.SessionID) == "" {
		return fmt.Errorf("candidate origin: %w", ErrInvalidCandidate)
	}
	if _, ok := policy[candidate.Kind]; !ok {
		return fmt.Errorf("candidate kind %q: %w", candidate.Kind, ErrInvalidCandidate)
	}
	if candidate.Action != ActionCreate && candidate.Action != ActionUpdate && candidate.Action != ActionResolve {
		return fmt.Errorf("candidate action %q: %w", candidate.Action, ErrInvalidCandidate)
	}
	if candidate.Action != ActionResolve && strings.TrimSpace(candidate.Body) == "" {
		return fmt.Errorf("candidate body: %w", ErrInvalidCandidate)
	}
	if candidate.ValidAt != nil && candidate.InvalidAt != nil && candidate.InvalidAt.Before(*candidate.ValidAt) {
		return fmt.Errorf("candidate validity window: %w", ErrInvalidCandidate)
	}
	return nil
}

func validateEvidence(candidate Candidate, events []SessionEvent) error {
	byID := make(map[string]SessionEvent, len(events))
	for _, event := range events {
		byID[event.ID] = event
	}
	if len(candidate.EvidenceEventIDs) == 0 {
		return fmt.Errorf("candidate %q: %w", candidate.ID, ErrMissingEvidence)
	}
	for _, id := range candidate.EvidenceEventIDs {
		event, ok := byID[id]
		if !ok || event.Actor.UserID != candidate.Origin.UserID || event.Actor.AgentID != candidate.Origin.AgentID {
			return fmt.Errorf("candidate %q evidence %q: %w", candidate.ID, id, ErrMissingEvidence)
		}
	}
	return nil
}

func WithEvidenceTime(candidate Candidate, events []SessionEvent) Candidate {
	if !candidate.SourceOccurredAt.IsZero() {
		return candidate
	}
	wanted := make(map[string]struct{}, len(candidate.EvidenceEventIDs))
	for _, id := range candidate.EvidenceEventIDs {
		wanted[id] = struct{}{}
	}
	var latest time.Time
	for _, event := range events {
		if _, ok := wanted[event.ID]; ok && event.OccurredAt.After(latest) {
			latest = event.OccurredAt.UTC()
		}
	}
	candidate.SourceOccurredAt = latest
	return candidate
}

func stateForValidity(invalidAt *time.Time) NoteState {
	if invalidAt != nil {
		return StateResolved
	}
	return StateActive
}

func ValidateRecall(request RecallRequest) error {
	if strings.TrimSpace(request.Actor.UserID) == "" || strings.TrimSpace(request.Actor.AgentID) == "" || strings.TrimSpace(request.Actor.SessionID) == "" || request.TokenBudget <= 0 {
		return fmt.Errorf("recall actor or budget: %w", ErrInvalidRecall)
	}
	return nil
}

func FormatForRecall(note Note) string {
	return FormatForRecallWithRelated(note, nil)
}

func FormatForRecallWithRelated(note Note, related []Note) string {
	text := fmt.Sprintf("[%s] %s", note.Kind, note.Body)
	if len(related) > 0 {
		parts := make([]string, 0, len(related))
		for _, linked := range related {
			parts = append(parts, linked.Subject+": "+linked.Body)
		}
		text += " [related: " + strings.Join(parts, "; ") + "]"
	}
	if note.ValidAt == nil && note.InvalidAt == nil {
		return text
	}
	validAt := "unknown"
	if note.ValidAt != nil {
		validAt = note.ValidAt.UTC().Format(time.RFC3339)
	}
	invalidAt := "present"
	if note.InvalidAt != nil {
		invalidAt = note.InvalidAt.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("%s [valid: %s to %s]", text, validAt, invalidAt)
}

func relatedNotes(note Note, candidates []Note) []Note {
	if len(note.RelatedSubjects) == 0 {
		return nil
	}
	wanted := make(map[string]struct{}, len(note.RelatedSubjects))
	for _, subject := range note.RelatedSubjects {
		wanted[strings.ToLower(subject)] = struct{}{}
	}
	result := make([]Note, 0, len(wanted))
	for _, candidate := range candidates {
		if _, ok := wanted[strings.ToLower(candidate.Subject)]; ok {
			result = append(result, candidate)
		}
	}
	return result
}

func normalizeSubjects(subject string, related []string) []string {
	seen := make(map[string]struct{}, len(related))
	result := make([]string, 0, len(related))
	for _, value := range related {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || strings.EqualFold(value, subject) {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func QueryScore(note Note, query string) int {
	queryTerms := searchableTerms(query)
	if len(queryTerms) == 0 {
		return 0
	}
	noteTerms := searchableTerms(note.Subject + " " + note.Body)
	score := 0
	for term := range queryTerms {
		if _, ok := noteTerms[term]; ok {
			score++
		}
	}
	return score
}

func SearchQuery(query string) string {
	terms := searchableTerms(query)
	ordered := make([]string, 0, len(terms))
	for term := range terms {
		ordered = append(ordered, term)
	}
	sort.Strings(ordered)
	return strings.Join(ordered, " ")
}

func searchableTerms(text string) map[string]struct{} {
	terms := make(map[string]struct{})
	for _, term := range strings.FieldsFunc(strings.ToLower(text), func(value rune) bool {
		return value < 'a' || value > 'z'
	}) {
		if len(term) < 3 || temporalStopWords[term] {
			continue
		}
		terms[term] = struct{}{}
	}
	return terms
}

var temporalStopWords = map[string]bool{
	"and": true, "are": true, "for": true, "from": true, "has": true, "the": true,
	"this": true, "was": true, "what": true, "when": true, "which": true, "with": true,
}

func CanonicalKey(candidate Candidate) string {
	return fmt.Sprintf("%d:%s%d:%s%d:%s%d:%s",
		len(candidate.TaskRef), candidate.TaskRef,
		len(candidate.ThreadRef), candidate.ThreadRef,
		len(candidate.Kind), candidate.Kind,
		len(candidate.Subject), candidate.Subject,
	)
}

func deliveryKey(note Note, actor Actor) string {
	return fmt.Sprintf("%s:%d:%s", note.ID, note.Revision, actor.SessionID)
}

func includesAudience(audience []string, agentID string) bool {
	if len(audience) == 0 {
		return true
	}
	for _, allowed := range audience {
		if allowed == agentID {
			return true
		}
	}
	return false
}

func notePriority(kind NoteKind) int {
	switch kind {
	case KindHandoff:
		return 0
	case KindBlocker:
		return 1
	case KindStatus:
		return 2
	case KindArtifactReference:
		return 3
	default:
		return 4
	}
}

func estimateTokens(text string) int {
	return max(1, (len(text)+3)/4)
}

func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}

func cloneNote(note Note) Note {
	note.AudienceAgentIDs = append([]string(nil), note.AudienceAgentIDs...)
	note.RelatedSubjects = append([]string(nil), note.RelatedSubjects...)
	note.EvidenceEventIDs = append([]string(nil), note.EvidenceEventIDs...)
	note.ValidAt = cloneTime(note.ValidAt)
	note.InvalidAt = cloneTime(note.InvalidAt)
	return note
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	value = value.UTC()
	return &value
}
