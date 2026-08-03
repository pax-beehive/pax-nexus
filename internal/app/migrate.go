package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractionqueue"
)

// Migrate brings the database schema up to date and returns. It exists so a
// deployment pipeline can run migrations as a standalone job before rolling
// out new instances, instead of relying on migrate-on-boot: a managed
// runtime starts several instances at once, and schema changes should land
// before any of them serve traffic.
//
// It reads only TEAM_MEMORY_DATABASE_URL rather than the full application
// configuration on purpose. A migration job has no HTTP transport and no
// identity provider, so forcing it to carry the authentication, OIDC, and
// extractor variables would be a deployment footgun with no upside.
//
// Run still migrates on boot (see Run), which keeps single-binary on-prem
// installs working unchanged. Both paths call migrateStores, so the job and
// the boot path can never drift.
func Migrate(ctx context.Context, logger *slog.Logger) error {
	databaseURL := strings.TrimSpace(os.Getenv("TEAM_MEMORY_DATABASE_URL"))
	if databaseURL == "" {
		return fmt.Errorf("TEAM_MEMORY_DATABASE_URL is required")
	}
	store, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("initialize storage: %w", err)
	}
	defer store.Close()
	if err := migrateStores(ctx, store); err != nil {
		return err
	}
	logger.InfoContext(ctx, "database schema migrated")
	return nil
}

// migrateStores applies the team-memory schema and the extraction queue's
// River schema, in that order, under one schema lock.
//
// The lock is what makes concurrent migration safe. Store.Migrate serializes
// itself, but River's migrations take no lock, so without the outer lock two
// processes migrating at once deadlock: one holds this package's tables
// while the other holds River's. That happens in practice whenever a
// migration job overlaps an instance still migrating on boot.
func migrateStores(ctx context.Context, store *postgres.Store) error {
	return store.WithSchemaLock(ctx, func(ctx context.Context) error {
		if err := store.Migrate(ctx); err != nil {
			return fmt.Errorf("initialize storage schema: %w", err)
		}
		if err := extractionqueue.Migrate(ctx, store.Pool()); err != nil {
			return fmt.Errorf("initialize extraction queue schema: %w", err)
		}
		return nil
	})
}
