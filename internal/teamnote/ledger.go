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
	ErrInvalidCandidate      = errors.New("invalid note candidate")
	ErrMissingEvidence       = errors.New("candidate evidence is missing")
	ErrNoteNotFound          = errors.New("team note not found")
	ErrInvalidRecall         = errors.New("invalid recall request")
	ErrExtractionRunConflict = errors.New("extraction run conflicts with durable result")
	// ErrExtractionRunQuarantined marks a run whose candidates failed
	// deterministic admission. The run is recorded as quarantined instead of
	// admitted, so callers may skip the slice instead of retrying forever.
	ErrExtractionRunQuarantined = errors.New("extraction run quarantined")
)

type NoteKind string

const (
	KindStatus            NoteKind = "status"
	KindBlocker           NoteKind = "blocker"
	KindHandoff           NoteKind = "handoff"
	KindArtifactReference NoteKind = "artifact_reference"
	// KindSourceSpan stores immutable source text with extraction metadata. It
	// is not a normalized collaboration-state assertion.
	KindSourceSpan NoteKind = "source_span"
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
	IdentityRef      string          `json:"identity_ref,omitempty"`
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
		KindSourceSpan:        {SoftTTL: 7 * 24 * time.Hour, HardTTL: 30 * 24 * time.Hour},
	}
}

type Clock interface {
	Now() time.Time
}

type Ledger struct {
	mu             sync.Mutex
	policy         TTLPolicy
	clock          Clock
	notes          map[string]Note
	candidates     map[string]string
	deliveries     map[string]struct{}
	hintDeliveries map[string]struct{}
	recallPolicy   RecallPolicy
	runs           map[string]storedExtractionRun
}

type storedExtractionRun struct {
	Input       ExtractionRun
	Notes       []Note
	Quarantined bool
	Reason      string
}

func NewLedger(policy TTLPolicy, clock Clock) *Ledger {
	return NewLedgerWithRecallPolicy(policy, clock, RecallPolicy{
		SuppressDuplicates: true,
		DegradeRelated:     true,
	})
}

// NewLedgerWithRecallPolicy configures the same planner candidate used by the
// PostgreSQL adapter while preserving the default production-off behavior.
func NewLedgerWithRecallPolicy(policy TTLPolicy, clock Clock, recallPolicy RecallPolicy) *Ledger {
	return &Ledger{
		policy: policy, clock: clock, notes: make(map[string]Note),
		candidates: make(map[string]string), deliveries: make(map[string]struct{}), hintDeliveries: make(map[string]struct{}),
		runs: make(map[string]storedExtractionRun), recallPolicy: recallPolicy,
	}
}

func (l *Ledger) Apply(ctx context.Context, candidate Candidate, evidence []SessionEvent) (Note, error) {
	notes, err := l.ApplyRun(ctx, ExtractionRun{ID: candidate.ID, Candidates: []Candidate{candidate}, Evidence: evidence})
	if err != nil {
		return Note{}, err
	}
	return notes[0], nil
}

// ApplyRun atomically admits every Candidate in one ExtractionRun.
func (l *Ledger) ApplyRun(ctx context.Context, run ExtractionRun) ([]Note, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("apply extraction run context: %w", err)
	}
	var err error
	run, err = PrepareExtractionRun(run)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	input := extractionRunInput(run)
	if stored, ok := l.runs[run.ID]; ok && run.ID != "" {
		status := ExtractionRunCompleted
		if stored.Quarantined {
			status = ExtractionRunQuarantined
		}
		if err := ValidateExtractionRunReplay(stored.Input, run, status, stored.Reason); err != nil {
			return nil, err
		}
		// The durable result wins over any recomputation for the same input,
		// so a replay after a lost saved response cannot double-apply state.
		return cloneNoteSlice(stored.Notes), nil
	}

	notes := cloneNotes(l.notes)
	candidates := make(map[string]string, len(l.candidates)+len(run.Candidates))
	for id, key := range l.candidates {
		candidates[id] = key
	}
	result := make([]Note, 0, len(run.Candidates))
	for _, candidate := range run.Candidates {
		if key, ok := candidates[candidate.ID]; ok {
			result = append(result, cloneNote(notes[key]))
			continue
		}
		key := CanonicalKey(candidate)
		note, exists := notes[key]
		var current *Note
		if exists {
			current = &note
		}
		admitted, err := AdmitCandidate(l.policy, l.clock.Now(), candidate, run.Evidence, current)
		if err != nil {
			if !ShouldQuarantineExtractionRun(err) {
				return nil, err
			}
			// Deterministic admission failures cannot succeed on retry, so the
			// run is quarantined and replayed attempts observe the same outcome.
			quarantine := storedExtractionRun{Input: input, Quarantined: true, Reason: err.Error()}
			if run.ID != "" {
				l.runs[run.ID] = quarantine
			}
			return nil, fmt.Errorf("quarantine extraction run %q: %w", run.ID, errors.Join(ErrExtractionRunQuarantined, err))
		}
		notes[key] = admitted
		candidates[candidate.ID] = key
		result = append(result, cloneNote(admitted))
	}
	l.notes = notes
	l.candidates = candidates
	if run.ID != "" {
		l.runs[run.ID] = storedExtractionRun{Input: input, Notes: cloneNoteSlice(result)}
	}
	return result, nil
}

// ValidateCandidate reports whether one candidate satisfies the deterministic
// admission policy without consulting store state. Callers use it to drop
// candidates that can never be admitted before applying an extraction run.
func ValidateCandidate(candidate Candidate, policy TTLPolicy) error {
	return validateCandidate(candidate, policy)
}

func cloneNoteSlice(notes []Note) []Note {
	cloned := make([]Note, len(notes))
	for index, note := range notes {
		cloned[index] = cloneNote(note)
	}
	return cloned
}

func cloneNotes(notes map[string]Note) map[string]Note {
	cloned := make(map[string]Note, len(notes))
	for key, note := range notes {
		cloned[key] = cloneNote(note)
	}
	return cloned
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
	candidates := make([]RecallCandidate, 0, len(eligible))
	for _, note := range eligible {
		candidates = append(candidates, RecallCandidate{Note: note, LexicalScore: float64(QueryScore(note, request.Query))})
	}
	envelope := NoteEnvelope{}
	recallPolicy := l.recallPolicy
	if recallPolicy.CandidateLimit == 0 {
		recallPolicy.CandidateLimit = len(candidates)
	}
	recallPolicy.ObservationTime = now
	planned, _ := PlanRecall(candidates, request, recallPolicy)
	for _, item := range planned {
		if !item.ClaimNoteDelivery {
			if _, delivered := l.hintDeliveries[item.HintFingerprint]; delivered {
				continue
			}
			AppendPlannedHint(&envelope, item)
			l.hintDeliveries[item.HintFingerprint] = struct{}{}
			continue
		}
		AppendPlannedRecall(&envelope, item)
		l.deliveries[deliveryKey(item.Note, request.Actor)] = struct{}{}
	}
	return envelope, nil
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
	return notes
}

// QueryRequestsOwnContext identifies first-person questions where source user
// provenance can resolve otherwise equally relevant team facts.
func QueryRequestsOwnContext(query string) bool {
	for _, term := range strings.FieldsFunc(strings.ToLower(query), func(value rune) bool {
		return value < 'a' || value > 'z'
	}) {
		switch term {
		case "i", "me", "my", "mine", "we", "our", "ours":
			return true
		}
	}
	return false
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
		if !ok || event.Actor != candidate.Origin || !evidenceVisible(event.Visibility) ||
			event.TaskRef != candidate.TaskRef || event.ThreadRef != candidate.ThreadRef {
			return fmt.Errorf("candidate %q evidence %q: %w", candidate.ID, id, ErrMissingEvidence)
		}
	}
	return nil
}

func evidenceVisible(visibility string) bool {
	switch strings.TrimSpace(strings.ToLower(visibility)) {
	case "", "team_note_eligible", "team_visible":
		return true
	default:
		return false
	}
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
	if strings.TrimSpace(request.Actor.UserID) == "" || strings.TrimSpace(request.Actor.AgentID) == "" || strings.TrimSpace(request.Actor.SessionID) == "" || request.TokenBudget <= 0 || request.MaxItems < 0 {
		return fmt.Errorf("recall actor or budget: %w", ErrInvalidRecall)
	}
	return nil
}

func FormatForRecall(note Note) string {
	return FormatForRecallWithRelated(note, nil)
}

func FormatForRecallWithRelated(note Note, related []Note) string {
	text := fmt.Sprintf("[%s certainty=%s] %s", note.Kind, CertaintyForKind(note.Kind), note.Body)
	if len(related) > 0 {
		parts := make([]string, 0, len(related))
		for _, linked := range related {
			parts = append(parts, fmt.Sprintf("[certainty=%s] %s: %s", CertaintyForKind(linked.Kind), linked.Subject, linked.Body))
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

// CertaintyForKind maps the current note taxonomy onto conservative answerability.
func CertaintyForKind(kind NoteKind) NoteCertainty {
	switch kind {
	case KindBlocker:
		return CertaintyUnresolved
	case KindHandoff, KindSourceSpan:
		return CertaintyProposed
	default:
		return CertaintyConfirmed
	}
}

func relatedNotes(note Note, candidates []Note) []Note {
	wanted := make(map[string]struct{}, len(note.RelatedSubjects))
	for _, subject := range note.RelatedSubjects {
		wanted[strings.ToLower(subject)] = struct{}{}
	}
	result := make([]Note, 0, len(wanted)+1)
	for _, candidate := range candidates {
		if candidate.ID == note.ID {
			continue
		}
		_, forward := wanted[strings.ToLower(candidate.Subject)]
		reverse := false
		for _, related := range candidate.RelatedSubjects {
			if strings.EqualFold(related, note.Subject) {
				reverse = true
				break
			}
		}
		if forward || reverse {
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

// QueryRelevance reports query-term coverage in the range [0, 1].
func QueryRelevance(note Note, query string) float64 {
	queryTerms := searchableTerms(query)
	if len(queryTerms) == 0 {
		return 1
	}
	return float64(QueryScore(note, query)) / float64(len(queryTerms))
}

// QueryRelevant rejects weak lexical neighbors and notes that cannot supply an
// explicitly requested scalar slot.
func QueryRelevant(note Note, query string) bool {
	if strings.TrimSpace(query) == "" {
		return true
	}
	queryTerms := searchableTerms(query)
	if scalarSlotRequested(query) && len(queryTerms) < 2 {
		return false
	}
	minimumOverlap := min(2, len(queryTerms))
	if QueryScore(note, query) < minimumOverlap {
		return false
	}
	return slotCompatible(note.Subject+" "+note.Body, query)
}

// QuerySemanticallyRelevant preserves deterministic precision checks after a
// semantic retriever has supplied a strong candidate.
func QuerySemanticallyRelevant(note Note, query string) bool {
	if strings.TrimSpace(query) == "" {
		return true
	}
	if strings.Contains(strings.ToLower(query), "exact") {
		return false
	}
	queryTerms := searchableTerms(query)
	if scalarSlotRequested(query) && len(queryTerms) < 2 {
		return false
	}
	return slotCompatible(note.Subject+" "+note.Body, query)
}

// QueryRelated allows a weaker lexical match only for an explicit one-hop
// relation while preserving scalar-slot compatibility.
func QueryRelated(note Note, query string) bool {
	if strings.TrimSpace(query) == "" {
		return true
	}
	if strings.Contains(strings.ToLower(query), "exact") {
		return QueryRelevant(note, query)
	}
	return QueryScore(note, query) > 0 && slotCompatible(note.Subject+" "+note.Body, query)
}

func relevantRelatedNotes(notes []Note, query string, limit int, limited bool) []Note {
	result := make([]Note, 0, len(notes))
	for _, note := range notes {
		if !QueryRelated(note, query) {
			continue
		}
		result = append(result, note)
	}
	if queryRequestsCurrentState(query) {
		sort.SliceStable(result, func(left, right int) bool {
			if !result[left].SourceOccurredAt.Equal(result[right].SourceOccurredAt) {
				return result[left].SourceOccurredAt.After(result[right].SourceOccurredAt)
			}
			if QueryScore(result[left], query) != QueryScore(result[right], query) {
				return QueryScore(result[left], query) > QueryScore(result[right], query)
			}
			return result[left].ID < result[right].ID
		})
	}
	if limited && len(result) > max(0, limit) {
		result = result[:max(0, limit)]
	}
	return result
}

func queryRequestsCurrentState(query string) bool {
	for term := range searchableTerms(query) {
		switch term {
		case "current", "currently", "latest", "now", "present":
			return true
		}
	}
	return false
}

func slotCompatible(noteText, query string) bool {
	noteText = strings.ToLower(noteText)
	query = strings.ToLower(query)
	if containsAny(query, "when", " date", "deadline", " timestamp", " time") {
		return containsDigit(noteText) || containsAny(noteText,
			"january", "february", "march", "april", "may", "june", "july", "august",
			"september", "october", "november", "december", "today", "tomorrow", "eod",
			"before", "after", " by ", "monday", "tuesday", "wednesday", "thursday", "friday")
	}
	if containsAny(query, "version", " count", " number", " value", "how many") {
		return containsDigit(noteText)
	}
	if containsAny(query, "who", " owner", " owns", "designated", "responsible") {
		return containsAny(noteText, " owns ", " owner", "assigned", "responsible", "designated", "@", "user_")
	}
	return true
}

func scalarSlotRequested(query string) bool {
	query = strings.ToLower(query)
	return containsAny(query, "when", " date", "deadline", " timestamp", " time", "version",
		" count", " number", " value", "how many", "who", " owner", " owns", "designated", "responsible")
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func containsDigit(value string) bool {
	for _, character := range value {
		if character >= '0' && character <= '9' {
			return true
		}
	}
	return false
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
	"answer": true, "available": true, "conversation": true, "count": true, "date": true,
	"exact": true, "information": true, "name": true, "number": true, "owner": true,
	"owners": true, "owns": true, "should": true, "target": true, "time": true,
	"timestamp": true, "value": true, "version": true, "who": true,
}

func CanonicalKey(candidate Candidate) string {
	identityRef := strings.TrimSpace(candidate.IdentityRef)
	identity := identityRef
	if identity == "" {
		identity = candidate.Subject
		if candidate.Kind == KindBlocker || candidate.Kind == KindArtifactReference {
			identity = strings.Join(strings.Fields(strings.ToLower(candidate.Subject)), " ")
		}
	}
	key := fmt.Sprintf("%d:%s%d:%s%d:%s%d:%s",
		len(candidate.TaskRef), candidate.TaskRef,
		len(candidate.ThreadRef), candidate.ThreadRef,
		len(candidate.Kind), candidate.Kind,
		len(identity), identity,
	)
	if identityRef == "" && (candidate.Kind == KindStatus || candidate.Kind == KindHandoff) {
		key += fmt.Sprintf("%d:%s", len(candidate.Origin.AgentID), candidate.Origin.AgentID)
	}
	return key
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
