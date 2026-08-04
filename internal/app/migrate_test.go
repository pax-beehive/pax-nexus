package app

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMigrateRequiresDatabaseURL pins the migration job's only configuration
// contract. A job that silently no-ops without a database would let a broken
// deploy pipeline report success.
func TestMigrateRequiresDatabaseURL(t *testing.T) {
	t.Setenv("TEAM_MEMORY_DATABASE_URL", "   ")

	err := Migrate(context.Background(), slog.New(slog.DiscardHandler))

	require.Error(t, err)
	require.ErrorContains(t, err, "TEAM_MEMORY_DATABASE_URL")
}

// TestMigrateIsRepeatable is the deploy-pipeline contract: the job runs on
// every release against an already-migrated database, so a second run must
// succeed rather than fail on existing objects. It also covers the shared
// path Run uses on boot, since both go through migrateStores.
func TestMigrateIsRepeatable(t *testing.T) {
	dsn := os.Getenv("TEAM_MEMORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEAM_MEMORY_TEST_POSTGRES_DSN is not set")
	}
	t.Setenv("TEAM_MEMORY_DATABASE_URL", dsn)
	logger := slog.New(slog.DiscardHandler)

	require.NoError(t, Migrate(context.Background(), logger))
	require.NoError(t, Migrate(context.Background(), logger),
		"the deploy pipeline reruns the job on every release, so migration must be repeatable")
}

// TestConcurrentMigrationsDoNotDeadlock covers the overlap a rollout
// actually produces: the migration job runs while instances still migrating
// on boot are starting. Store.Migrate serializes itself, but River's
// migrations hold no lock of their own, so without the shared schema lock
// the two migration sources deadlock (SQLSTATE 40P01) with one holding this
// package's tables and the other holding River's.
func TestConcurrentMigrationsDoNotDeadlock(t *testing.T) {
	dsn := os.Getenv("TEAM_MEMORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEAM_MEMORY_TEST_POSTGRES_DSN is not set")
	}
	t.Setenv("TEAM_MEMORY_DATABASE_URL", dsn)
	logger := slog.New(slog.DiscardHandler)

	const migrators = 4
	errs := make(chan error, migrators)
	var start sync.WaitGroup
	start.Add(1)
	for range migrators {
		go func() {
			start.Wait()
			errs <- Migrate(context.Background(), logger)
		}()
	}
	start.Done()

	for range migrators {
		require.NoError(t, <-errs, "concurrent migrators must serialize, not deadlock")
	}
}
