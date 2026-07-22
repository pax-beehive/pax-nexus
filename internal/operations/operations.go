// Package operations defines the bounded administrative view of service
// activity and storage. It owns projections, not product content or policy.
package operations

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

var (
	ErrInvalidInput        = errors.New("invalid operations input")
	ErrRecallNotFound      = errors.New("recall diagnostic not found")
	ErrRecallExpired       = errors.New("recall diagnostic expired")
	ErrStorageNotAvailable = errors.New("storage snapshot not available")
)

type Kind string

const (
	KindObservationObserve Kind = "observation.observe"
	KindMemorySearch       Kind = "memory.search"
	KindMemoryGet          Kind = "memory.get"
	KindTeamNoteRecall     Kind = "team_note.recall"
	KindExtractionRun      Kind = "extraction.run"
	KindChannelSend        Kind = "channel.send"
	KindChannelAccept      Kind = "channel.accept"
	KindChannelArchive     Kind = "channel.archive"
	KindSystemRetention    Kind = "system.retention"
)

type Outcome string

const (
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeRejected  Outcome = "rejected"
	OutcomeFailed    Outcome = "failed"
	OutcomeTimedOut  Outcome = "timed_out"
	OutcomeCancelled Outcome = "cancelled"
)

type Actor struct {
	Kind         string
	UserID       string
	MembershipID string
	AgentID      string
	CredentialID string
}

type Event struct {
	OperationEventID int64
	AttemptID        string
	Kind             Kind
	Outcome          Outcome
	Actor            Actor
	SessionID        string
	StartedAt        time.Time
	CompletedAt      time.Time
	DurationMS       int64
	InputItems       int64
	AcceptedItems    int64
	DuplicateItems   int64
	ResultItems      int64
	DeliveredItems   int64
	EvidenceItems    int64
	HintItems        int64
	ReferenceItems   int64
	InputTokens      *int64
	OutputTokens     *int64
	DetailKind       string
	DetailID         string
	ErrorCode        string
}

func NewAttemptID() (string, error) {
	buffer := make([]byte, 18)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("create operation attempt ID: %w", err)
	}
	return "op_" + base64.RawURLEncoding.EncodeToString(buffer), nil
}

func (e Event) Validate() error {
	if strings.TrimSpace(e.AttemptID) == "" || !validKind(e.Kind) || !validOutcome(e.Outcome) ||
		!validActorKind(e.Actor.Kind) || e.StartedAt.IsZero() || e.CompletedAt.Before(e.StartedAt) ||
		e.DurationMS < 0 || anyNegative(e.InputItems, e.AcceptedItems, e.DuplicateItems, e.ResultItems,
		e.DeliveredItems, e.EvidenceItems, e.HintItems, e.ReferenceItems) ||
		(e.DetailKind == "") != (e.DetailID == "") || negativeOptional(e.InputTokens) || negativeOptional(e.OutputTokens) {
		return ErrInvalidInput
	}
	return nil
}

func validKind(kind Kind) bool {
	switch kind {
	case KindObservationObserve, KindMemorySearch, KindMemoryGet, KindTeamNoteRecall,
		KindExtractionRun, KindChannelSend, KindChannelAccept, KindChannelArchive, KindSystemRetention:
		return true
	default:
		return false
	}
}

func ParseKind(value string) (Kind, error) {
	kind := Kind(strings.TrimSpace(value))
	if kind == "" {
		return "", nil
	}
	if !validKind(kind) {
		return "", ErrInvalidInput
	}
	return kind, nil
}

func validOutcome(outcome Outcome) bool {
	switch outcome {
	case OutcomeSucceeded, OutcomeRejected, OutcomeFailed, OutcomeTimedOut, OutcomeCancelled:
		return true
	default:
		return false
	}
}

func ParseOutcome(value string) (Outcome, error) {
	outcome := Outcome(strings.TrimSpace(value))
	if outcome == "" {
		return "", nil
	}
	if !validOutcome(outcome) {
		return "", ErrInvalidInput
	}
	return outcome, nil
}

func validActorKind(kind string) bool {
	return kind == "agent" || kind == "human" || kind == "system"
}

func anyNegative(values ...int64) bool {
	for _, value := range values {
		if value < 0 {
			return true
		}
	}
	return false
}

func negativeOptional(value *int64) bool {
	return value != nil && *value < 0
}

type TimeFilter struct {
	From    time.Time
	To      time.Time
	AgentID string
}

type EventFilter struct {
	TimeFilter
	Kind    Kind
	Outcome Outcome
	Limit   int
	Cursor  string
}

type ObservationSummary struct {
	Requests        int64
	Succeeded       int64
	InputEvents     int64
	EventsWritten   int64
	DuplicateEvents int64
}

type ExtractionSummary struct {
	Runs                int64
	Completed           int64
	Quarantined         int64
	Failed              int64
	AdmittedRevisions   int64
	UnextractedEvents   int64
	OldestUnextractedAt *time.Time
}

type RecallSummary struct {
	Requests               int64
	Succeeded              int64
	WithEvidence           int64
	Empty                  int64
	MemoryHits             int64
	TeamNotesDelivered     int64
	MemorySearchRequests   int64
	MemoryGetRequests      int64
	TeamNoteRecallRequests int64
	EvidenceHits           int64
	HintHits               int64
	ReferenceHits          int64
}

type LatencySummary struct {
	SampleCount int64
	P50MS       *int64
	P95MS       *int64
}

type Summary struct {
	From         time.Time
	To           time.Time
	GeneratedAt  time.Time
	Observations ObservationSummary
	Extraction   ExtractionSummary
	Recalls      RecallSummary
	Latency      LatencySummary
	Errors       int64
}

type RecallDiagnostic struct {
	ObservationID         int64
	OccurredAt            time.Time
	AgentID               string
	SessionID             string
	DurationMS            int64
	TokenBudget           int64
	MaxItems              int64
	EvidenceSufficient    bool
	ReasonCodes           []string
	LanesExecuted         []string
	Candidates            int64
	FusionKept            int64
	PlannedNotes          int64
	PlannedTokens         int64
	DeliveredItems        int64
	DispositionCounts     map[string]int64
	RejectionCounts       map[string]int64
	BudgetDropCounts      map[string]int64
	HardGateFailureCounts map[string]int64
}

type StorageComponent struct {
	Component                 string           `json:"component"`
	Counts                    map[string]int64 `json:"counts"`
	LogicalBytes              int64            `json:"logical_bytes"`
	PhysicalBytes             int64            `json:"physical_bytes"`
	EstimatedReclaimableBytes *int64           `json:"estimated_reclaimable_bytes,omitempty"`
	OldestAt                  *time.Time       `json:"oldest_at,omitempty"`
	NewestAt                  *time.Time       `json:"newest_at,omitempty"`
}

type StorageSnapshot struct {
	SnapshotID            int64
	SchemaVersion         int32
	CapturedAt            time.Time
	Status                string
	WarningCodes          []string
	DatabasePhysicalBytes int64
	OtherPhysicalBytes    int64
	Components            []StorageComponent
}

type StorageFilter struct {
	From   time.Time
	To     time.Time
	Limit  int
	Cursor string
}

type Repository interface {
	Record(context.Context, Event) (Event, error)
	Summary(context.Context, TimeFilter, time.Time) (Summary, error)
	ListEvents(context.Context, EventFilter) ([]Event, error)
	GetRecallDiagnostic(context.Context, int64) (RecallDiagnostic, error)
	CaptureStorage(context.Context, time.Time) (StorageSnapshot, error)
	LatestStorage(context.Context) (StorageSnapshot, error)
	ListStorage(context.Context, StorageFilter) ([]StorageSnapshot, error)
	DeleteBefore(context.Context, time.Time, time.Time) (int64, int64, error)
}

type Recorder interface {
	Record(context.Context, Event) (Event, error)
}

type DropCountingRecorder struct {
	next    Recorder
	dropped atomic.Uint64
}

func NewDropCountingRecorder(next Recorder) (*DropCountingRecorder, error) {
	if next == nil {
		return nil, fmt.Errorf("create drop-counting operation recorder: recorder is required")
	}
	return &DropCountingRecorder{next: next}, nil
}

func (r *DropCountingRecorder) Record(ctx context.Context, event Event) (Event, error) {
	recorded, err := r.next.Record(ctx, event)
	if err != nil {
		r.dropped.Add(1)
		return Event{}, fmt.Errorf("record drop-counted operation event: %w", err)
	}
	return recorded, nil
}

func (r *DropCountingRecorder) Dropped() uint64 {
	return r.dropped.Load()
}

func DroppedObservations(recorder Recorder) uint64 {
	counter, ok := recorder.(interface{ Dropped() uint64 })
	if !ok {
		return 0
	}
	return counter.Dropped()
}

func NextEventCursor(events []Event, limit int) string {
	if limit <= 0 || len(events) <= limit {
		return ""
	}
	return encodeCursor(events[limit-1].StartedAt, events[limit-1].OperationEventID)
}

func NextStorageCursor(snapshots []StorageSnapshot, limit int) string {
	if limit <= 0 || len(snapshots) <= limit {
		return ""
	}
	return encodeCursor(snapshots[limit-1].CapturedAt, snapshots[limit-1].SnapshotID)
}

func DecodeCursor(value string) (time.Time, int64, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, 0, ErrInvalidInput
	}
	parts := strings.Split(string(decoded), "|")
	if len(parts) != 2 {
		return time.Time{}, 0, ErrInvalidInput
	}
	timestamp, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, 0, ErrInvalidInput
	}
	var id int64
	if _, err := fmt.Sscan(parts[1], &id); err != nil || id <= 0 {
		return time.Time{}, 0, ErrInvalidInput
	}
	return timestamp.UTC(), id, nil
}

func encodeCursor(timestamp time.Time, id int64) string {
	value := fmt.Sprintf("%s|%d", timestamp.UTC().Format(time.RFC3339Nano), id)
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}
