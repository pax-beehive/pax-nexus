package todoapp_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/session"
	"github.com/pax-beehive/pax-nexus/internal/todoapp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	errSinkFailed = errors.New("sink error")
)

type fakeSink struct {
	batch session.StreamBatch
	ctx   context.Context
}

func (f *fakeSink) ObserveStream(ctx context.Context, batch session.StreamBatch) (session.IngestReceipt, error) {
	f.batch = batch
	f.ctx = ctx
	return session.IngestReceipt{}, nil
}

type fakeSinkWithError struct {
	err error
}

func (f *fakeSinkWithError) ObserveStream(ctx context.Context, batch session.StreamBatch) (session.IngestReceipt, error) {
	return session.IngestReceipt{}, f.err
}

func TestNewLakeReporter_InvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		sink    todoapp.EvidenceSink
		wantErr bool
	}{
		{
			name:    "nil sink",
			sink:    nil,
			wantErr: true,
		},
		{
			name:    "valid input",
			sink:    &fakeSink{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := todoapp.NewLakeReporter(tt.sink)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLakeReporter_Report_BlankScopeIsRejected(t *testing.T) {
	sink := &fakeSink{}
	reporter, err := todoapp.NewLakeReporter(sink)
	require.NoError(t, err)

	err = reporter.Report(context.Background(), "", todoapp.ReportEvent{Type: todoapp.EventTodoCompleted})
	require.Error(t, err)
}

func TestLakeReporter_Report_ThreadsScopeIntoSinkContext(t *testing.T) {
	sink := &fakeSink{}
	reporter, err := todoapp.NewLakeReporter(sink)
	require.NoError(t, err)

	err = reporter.Report(context.Background(), "other-scope", todoapp.ReportEvent{
		Type:       todoapp.EventTodoCompleted,
		UserID:     "user123",
		Summary:    "summary",
		OccurredAt: time.Now(),
	})
	require.NoError(t, err)

	scopeID, err := session.ScopeFromContext(sink.ctx)
	require.NoError(t, err)
	assert.Equal(t, "other-scope", scopeID)
}

func TestLakeReporter_Report(t *testing.T) {
	t.Run("successful report", func(t *testing.T) {
		sink := &fakeSink{}
		reporter, err := todoapp.NewLakeReporter(sink)
		require.NoError(t, err)

		ctx := context.Background()
		event := todoapp.ReportEvent{
			Type:         todoapp.EventTodoCompleted,
			UserID:       "user123",
			TodoID:       "todo456",
			SuggestionID: "suggestion789",
			NoteID:       "note012",
			Summary:      "Completed task summary",
			OccurredAt:   time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		}

		err = reporter.Report(ctx, "local-team", event)
		require.NoError(t, err)

		// Verify the sink was called
		assert.NotNil(t, sink.batch)
		assert.Len(t, sink.batch.Events, 1)

		// Verify scope inside sink context
		scopeID, err := session.ScopeFromContext(sink.ctx)
		require.NoError(t, err)
		assert.Equal(t, "local-team", scopeID)

		// Verify stream event properties
		streamEvent := sink.batch.Events[0]
		assert.Equal(t, session.SourceAppTodo, streamEvent.Stream.Source)
		assert.Equal(t, "app-todo", streamEvent.Stream.StreamID)
		assert.Equal(t, "Completed task summary", streamEvent.Content)
		assert.Equal(t, session.KindText, streamEvent.Kind)
		assert.Equal(t, "message", streamEvent.Type)
		assert.Equal(t, session.VisibilityTeam, streamEvent.Visibility)
		assert.Equal(t, "user", streamEvent.Author.Kind)
		assert.Equal(t, "user123", streamEvent.Author.NativeID)
		assert.Equal(t, "user123", streamEvent.Author.UserID)
		assert.Equal(t, time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC), streamEvent.OccurredAt)
		assert.Zero(t, streamEvent.Sequence)

		// Verify ID has correct prefix
		assert.Greater(t, len(streamEvent.ID), len("app-todo-"))

		// Verify metadata
		assert.Equal(t, "todo_completed", streamEvent.Metadata["event_type"])
		assert.Equal(t, "todo456", streamEvent.Metadata["todo_id"])
		assert.Equal(t, "suggestion789", streamEvent.Metadata["suggestion_id"])
		assert.Equal(t, "note012", streamEvent.Metadata["note_id"])

		// Verify batch passes validation
		err = session.ValidateStreamBatch(sink.batch)
		assert.NoError(t, err)
	})

	t.Run("metadata without empty keys", func(t *testing.T) {
		sink := &fakeSink{}
		reporter, err := todoapp.NewLakeReporter(sink)
		require.NoError(t, err)

		ctx := context.Background()
		event := todoapp.ReportEvent{
			Type:         todoapp.EventSuggestionAccepted,
			UserID:       "user123",
			TodoID:       "",
			SuggestionID: "sugg456",
			NoteID:       "",
			Summary:      "Suggestion accepted",
			OccurredAt:   time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		}

		err = reporter.Report(ctx, "local-team", event)
		require.NoError(t, err)

		streamEvent := sink.batch.Events[0]
		// event_type should always be present
		assert.Equal(t, "suggestion_accepted", streamEvent.Metadata["event_type"])
		assert.Equal(t, "sugg456", streamEvent.Metadata["suggestion_id"])
		// Empty fields should not be in metadata at all
		assert.NotContains(t, streamEvent.Metadata, "todo_id")
		assert.NotContains(t, streamEvent.Metadata, "note_id")
	})

	t.Run("sink error wrapped", func(t *testing.T) {
		sink := &fakeSinkWithError{err: errSinkFailed}
		reporter, err := todoapp.NewLakeReporter(sink)
		require.NoError(t, err)

		ctx := context.Background()
		event := todoapp.ReportEvent{
			Type:       todoapp.EventTodoCompleted,
			UserID:     "user123",
			TodoID:     "todo456",
			Summary:    "Test",
			OccurredAt: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		}

		err = reporter.Report(ctx, "local-team", event)
		require.Error(t, err)
		assert.ErrorIs(t, err, errSinkFailed)
	})
}

func TestLakeReporter_InjectableNewID(t *testing.T) {
	t.Run("custom id generator", func(t *testing.T) {
		sink := &fakeSink{}

		// Inject custom newID via functional option
		counter := 0
		reporter, err := todoapp.NewLakeReporter(
			sink,
			todoapp.WithLakeReporterNewID(func() string {
				counter++
				return "custom-id-" + strconv.Itoa(counter)
			}),
		)
		require.NoError(t, err)

		ctx := context.Background()
		event := todoapp.ReportEvent{
			Type:       todoapp.EventTodoCompleted,
			UserID:     "user123",
			TodoID:     "todo456",
			Summary:    "Test",
			OccurredAt: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		}

		err = reporter.Report(ctx, "local-team", event)
		require.NoError(t, err)

		streamEvent := sink.batch.Events[0]
		// Verify custom ID format
		assert.Contains(t, streamEvent.ID, "custom-id-")
	})
}
