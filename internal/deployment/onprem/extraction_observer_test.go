package onprem_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/operations"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	teamruntime "github.com/pax-beehive/pax-nexus/internal/teamnote/runtime"
	"github.com/stretchr/testify/require"
)

// extractionRecorderFake captures the last recorded event so tests can
// assert on it without a database.
type extractionRecorderFake struct {
	recorded *operations.Event
}

func (r *extractionRecorderFake) Record(_ context.Context, event operations.Event) (operations.Event, error) {
	r.recorded = &event
	return event, nil
}

func TestNewExtractionObserverAttributesScopeFromResolver(t *testing.T) {
	recorder := &extractionRecorderFake{}
	wantScope := "team-acme"
	observer := onprem.NewExtractionObserver(recorder, func(context.Context) string { return wantScope }, slog.New(slog.DiscardHandler))

	startedAt := time.Now().UTC().Add(-time.Second)
	observer(context.Background(), teamruntime.ExtractionObservation{
		Actor:     teamnote.Actor{UserID: "owner", AgentID: "agent-1", SessionID: "session-1"},
		RunID:     "run-1",
		StartedAt: startedAt, CompletedAt: startedAt.Add(time.Second),
		Status: teamruntime.ExtractionCompleted, InputEvents: 3, Candidates: 2,
	})

	require.NotNil(t, recorder.recorded)
	require.Equal(t, wantScope, recorder.recorded.ScopeID)
}
