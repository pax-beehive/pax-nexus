// Package explorer defines the Owner-only read model for Team Memory content
// and diagnostic provenance. It owns projections, not product write policy.
package explorer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidInput = errors.New("invalid explorer input")
	ErrNotFound     = errors.New("explorer resource not found")
)

type TeamNoteFilter struct {
	Query     string
	Kind      string
	State     string
	AgentID   string
	TaskRef   string
	ThreadRef string
	Limit     int
	Cursor    string
}

type TeamNoteSummary struct {
	NoteID           string
	Kind             string
	Subject          string
	State            string
	TaskRef          string
	ThreadRef        string
	OriginAgentID    string
	AudienceAgentIDs []string
	Revision         int
	CreatedAt        time.Time
	UpdatedAt        time.Time
	SoftExpiresAt    time.Time
	HardExpiresAt    time.Time
}

type TeamNote struct {
	TeamNoteSummary
	Body             string
	OriginUserID     string
	OriginSessionID  string
	RelatedSubjects  []string
	ValidAt          *time.Time
	InvalidAt        *time.Time
	SourceOccurredAt *time.Time
}

type SourceEvent struct {
	EventID     string
	UserID      string
	AgentID     string
	SessionID   string
	Sequence    int64
	Type        string
	Content     string
	TaskRef     string
	ThreadRef   string
	Visibility  string
	OccurredAt  time.Time
	CapturedAt  time.Time
	ExtractedAt *time.Time
}

type ExtractionRun struct {
	RunID         string
	UserID        string
	AgentID       string
	SessionID     string
	FromSequence  int64
	ToSequence    int64
	Model         string
	PromptVersion string
	Status        string
	InputTokens   int64
	OutputTokens  int64
	ErrorCode     string
	CreatedAt     time.Time
	CompletedAt   *time.Time
}

type Candidate struct {
	CandidateID      string
	Action           string
	Kind             string
	Subject          string
	Body             string
	TaskRef          string
	ThreadRef        string
	OriginAgentID    string
	EvidenceEventIDs []string
	AdmissionStatus  string
	RejectionReason  string
	CreatedAt        time.Time
	ResultingNoteID  string
}

type Revision struct {
	Revision        int
	CandidateID     string
	Candidate       Candidate
	Operation       string
	Body            string
	RelatedSubjects []string
	ValidAt         *time.Time
	InvalidAt       *time.Time
	CreatedAt       time.Time
	ExpiredAt       *time.Time
	Extraction      ExtractionRun
	Evidence        []SourceEvent
	Deliveries      []Delivery
}

type Delivery struct {
	RecipientUserID    string
	RecipientAgentID   string
	RecipientSessionID string
	DeliveredAt        time.Time
	ContextTokens      int
}

type RecallUse struct {
	ObservationID      int64
	RecipientAgentID   string
	RecipientSessionID string
	OccurredAt         time.Time
	Disposition        string
	Delivered          bool
	RejectionReasons   []string
	BudgetDropReasons  []string
	HardGateFailures   []string
}

type TeamNoteDetail struct {
	Note               TeamNote
	RelatedNotes       []TeamNoteSummary
	Revisions          []Revision
	RecallObservations []RecallUse
}

type ExtractionDiagnostic struct {
	Run            ExtractionRun
	Candidates     []Candidate
	SourceEvents   []SourceEvent
	ResultingNotes []TeamNoteSummary
}

type KnowledgeCapsule struct {
	CapsuleID       string
	SourceSessionID string
	SourceAgent     string
	Keyword         string
	Title           string
	Summary         string
	Content         string
	Status          string
	Truncated       bool
	RouteMatchType  string
}

type ChannelDiagnostic struct {
	EnvelopeID    string
	FromAgentID   string
	ToAgentID     string
	Status        string
	Message       string
	PayloadStatus string
	CreatedAt     time.Time
	AcceptedAt    *time.Time
	ArchivedAt    *time.Time
	Capsule       KnowledgeCapsule
}

type Repository interface {
	ListTeamNotes(context.Context, TeamNoteFilter) ([]TeamNoteSummary, error)
	GetTeamNote(context.Context, string) (TeamNoteDetail, error)
	GetExtractionDiagnostic(context.Context, string) (ExtractionDiagnostic, error)
	GetChannelDiagnostic(context.Context, string) (ChannelDiagnostic, error)
}

type teamNoteCursor struct {
	UpdatedAt string `json:"updated_at"`
	NoteID    string `json:"note_id"`
}

func NextTeamNoteCursor(notes []TeamNoteSummary, limit int) string {
	if limit <= 0 || len(notes) <= limit {
		return ""
	}
	cursor := teamNoteCursor{
		UpdatedAt: notes[limit-1].UpdatedAt.UTC().Format(time.RFC3339Nano),
		NoteID:    notes[limit-1].NoteID,
	}
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func DecodeTeamNoteCursor(value string) (time.Time, string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, "", nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, "", ErrInvalidInput
	}
	var cursor teamNoteCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return time.Time{}, "", ErrInvalidInput
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, cursor.UpdatedAt)
	if err != nil || strings.TrimSpace(cursor.NoteID) == "" {
		return time.Time{}, "", ErrInvalidInput
	}
	return updatedAt.UTC(), strings.TrimSpace(cursor.NoteID), nil
}
