package session

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Evidence Lake generalized contracts. Any external connector pushes streams of
// flat events through these types; source-specific structure lives only in
// registered metadata keys.

const (
	SourceAgentSession = "agent-session"
	SourceIMChannel    = "im-channel"

	KindText  = "text"
	KindAudio = "audio"
	KindImage = "image"
	KindVideo = "video"
	KindFile  = "file"

	VisibilityTeam = "team"
)

var (
	ErrInvalidStreamBatch = errors.New("invalid stream batch")
	ErrUnregisteredValue  = errors.New("unregistered contract value")
	ErrVisibilityRejected = errors.New("visibility not accepted")
	ErrMediaNotEnabled    = errors.New("media ingestion not enabled")
)

var registeredSources = map[string]struct{}{SourceAgentSession: {}, SourceIMChannel: {}}

var registeredKinds = map[string]struct{}{
	KindText: {}, KindAudio: {}, KindImage: {}, KindVideo: {}, KindFile: {},
}

var registeredEventTypes = map[string]struct{}{
	"message": {}, "reply": {}, "reaction": {}, "system": {}, "checkpoint": {}, "attachment": {},
}

var registeredAuthorKinds = map[string]struct{}{"user": {}, "agent": {}, "system": {}}

type Stream struct {
	Source   string `json:"source"`
	StreamID string `json:"stream_id"`
}

type Author struct {
	Kind     string `json:"kind"`
	NativeID string `json:"native_id"`
	UserID   string `json:"user_id,omitempty"`
}

type MediaRef struct {
	BlobID   string `json:"blob_id"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size"`
	Checksum string `json:"checksum"`
}

// StreamEvent is immutable source evidence pushed by an external connector.
// Sequence is assigned by ingest and must be zero on input.
type StreamEvent struct {
	ID         string            `json:"id"`
	Stream     Stream            `json:"stream"`
	Author     Author            `json:"author"`
	Sequence   int64             `json:"sequence"`
	Kind       string            `json:"kind"`
	Type       string            `json:"type"`
	Content    string            `json:"content"`
	Media      *MediaRef         `json:"media,omitempty"`
	ThreadRef  string            `json:"thread_ref,omitempty"`
	Visibility string            `json:"visibility"`
	OccurredAt time.Time         `json:"occurred_at"`
	CapturedAt time.Time         `json:"captured_at"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type StreamBatch struct {
	Events   []StreamEvent `json:"events"`
	Complete bool          `json:"complete"`
}

func ValidateStreamBatch(batch StreamBatch) error {
	if len(batch.Events) == 0 {
		return fmt.Errorf("empty batch: %w", ErrInvalidStreamBatch)
	}
	stream := batch.Events[0].Stream
	for _, event := range batch.Events {
		if strings.TrimSpace(event.ID) == "" || event.OccurredAt.IsZero() {
			return fmt.Errorf("event %q identity: %w", event.ID, ErrInvalidStreamBatch)
		}
		if event.Sequence != 0 {
			return fmt.Errorf("event %q carries a caller sequence: %w", event.ID, ErrInvalidStreamBatch)
		}
		if event.Stream != stream || strings.TrimSpace(stream.StreamID) == "" {
			return fmt.Errorf("event %q mixed or empty stream: %w", event.ID, ErrInvalidStreamBatch)
		}
		if _, ok := registeredSources[event.Stream.Source]; !ok {
			return fmt.Errorf("source %q: %w", event.Stream.Source, ErrUnregisteredValue)
		}
		if _, ok := registeredKinds[event.Kind]; !ok {
			return fmt.Errorf("kind %q: %w", event.Kind, ErrUnregisteredValue)
		}
		if _, ok := registeredEventTypes[event.Type]; !ok {
			return fmt.Errorf("type %q: %w", event.Type, ErrUnregisteredValue)
		}
		if _, ok := registeredAuthorKinds[event.Author.Kind]; !ok {
			return fmt.Errorf("author kind %q: %w", event.Author.Kind, ErrUnregisteredValue)
		}
		if strings.TrimSpace(event.Author.NativeID) == "" {
			return fmt.Errorf("event %q author native id: %w", event.ID, ErrInvalidStreamBatch)
		}
		if event.Visibility != VisibilityTeam {
			return fmt.Errorf("visibility %q: %w", event.Visibility, ErrVisibilityRejected)
		}
		if event.Kind != KindText {
			// Blob storage arrives in Plan 4; until then media kinds are rejected
			// deterministically instead of silently dropping the original bytes.
			return fmt.Errorf("kind %q: %w", event.Kind, ErrMediaNotEnabled)
		}
	}
	return nil
}

// StreamFromActor maps a legacy agent-session actor onto the generalized
// stream identity. The concatenation preserves the old per-(agent, session)
// stream uniqueness.
func StreamFromActor(actor Actor) Stream {
	return Stream{Source: SourceAgentSession, StreamID: actor.AgentID + ":" + actor.SessionID}
}

func AuthorFromActor(actor Actor) Author {
	return Author{Kind: "agent", NativeID: actor.AgentID, UserID: actor.UserID}
}
