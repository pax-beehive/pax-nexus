package todoapp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/session"
)

// EvidenceSink is the interface for writing events to the evidence lake.
type EvidenceSink interface {
	ObserveStream(ctx context.Context, batch session.StreamBatch) (session.IngestReceipt, error)
}

// LakeReporter is a Reporter that sends events to the evidence lake.
type LakeReporter struct {
	sink    EvidenceSink
	scopeID string
	newID   func() string
}

// lakeReporterOption is a functional option for LakeReporter.
type lakeReporterOption func(*LakeReporter)

// WithLakeReporterNewID returns an option that sets the ID generator for LakeReporter.
func WithLakeReporterNewID(fn func() string) lakeReporterOption {
	return func(r *LakeReporter) {
		r.newID = fn
	}
}

// NewLakeReporter creates a new LakeReporter with the given sink and scope ID.
// It returns an error if sink is nil or scope ID is blank.
// Optional functional options can be passed to customize the reporter.
func NewLakeReporter(sink EvidenceSink, scopeID string, opts ...lakeReporterOption) (*LakeReporter, error) {
	if sink == nil {
		return nil, fmt.Errorf("new lake reporter: %w", ErrInvalidInput)
	}
	if scopeID == "" {
		return nil, fmt.Errorf("new lake reporter: %w", ErrInvalidInput)
	}
	r := &LakeReporter{
		sink:    sink,
		scopeID: scopeID,
		newID:   generateRandomID,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
}

// Report reports a ReportEvent to the evidence lake.
func (r *LakeReporter) Report(ctx context.Context, event ReportEvent) error {
	scoped := session.WithScope(ctx, r.scopeID)

	// Build metadata, dropping empty-string keys
	metadata := make(map[string]string)
	if event.Type != "" {
		metadata["event_type"] = string(event.Type)
	}
	if event.TodoID != "" {
		metadata["todo_id"] = event.TodoID
	}
	if event.SuggestionID != "" {
		metadata["suggestion_id"] = event.SuggestionID
	}
	if event.NoteID != "" {
		metadata["note_id"] = event.NoteID
	}

	streamEvent := session.StreamEvent{
		ID: "app-todo-" + r.newID(),
		Stream: session.Stream{
			Source:   session.SourceAppTodo,
			StreamID: "app-todo",
		},
		Author: session.Author{
			Kind:     "user",
			NativeID: event.UserID,
			UserID:   event.UserID,
		},
		Kind:       session.KindText,
		Type:       "message",
		Content:    event.Summary,
		Visibility: session.VisibilityTeam,
		OccurredAt: event.OccurredAt,
		Metadata:   metadata,
	}

	_, err := r.sink.ObserveStream(scoped, session.StreamBatch{Events: []session.StreamEvent{streamEvent}})
	if err != nil {
		return fmt.Errorf("report todo event %s: %w", event.Type, err)
	}

	return nil
}

// generateRandomID generates a random 16-byte hex string.
func generateRandomID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("id-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
