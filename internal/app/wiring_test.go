package app

import (
	"context"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/stretchr/testify/require"
)

// extractionEventScope is the resolver injected into onprem.NewExtractionObserver
// (see wiring.go) so the observer can attribute a recorded extraction Operation
// Event without internal/deployment/onprem importing teamnote directly. A
// context that carries no scope — e.g. the extraction context was never
// wrapped with one — must fall back to the single-tenant scope rather than
// recording an unattributed event.
func TestExtractionEventScopeFallsBackWhenContextCarriesNoScope(t *testing.T) {
	got := extractionEventScope(context.Background())

	require.Equal(t, onprem.LocalScopeID, got)
}
