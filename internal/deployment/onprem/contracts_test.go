package onprem_test

import (
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/stretchr/testify/require"
)

// internal/platform/postgres/migrations/031_operation_events_scope.sql
// backfills onprem_operation_events.scope_id with the literal 'local-team'
// rather than referencing this constant (SQL cannot import Go). If
// LocalScopeID ever changes, that migration silently diverges from it and
// every historical row backfilled by 031 becomes orphaned under a scope
// nothing resolves to anymore. This test is the tripwire: it must fail
// before that migration is touched.
func TestLocalScopeIDMatchesMigration031Backfill(t *testing.T) {
	require.Equal(t, "local-team", onprem.LocalScopeID)
}
