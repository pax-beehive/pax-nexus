// Command team-memory-migrate applies the database schema and exits. It is
// the entrypoint a deployment pipeline runs as a one-shot job before rolling
// out new application instances, so schema changes land once, before any
// instance serves traffic. Single-binary on-prem installs do not need it:
// cmd/team-memory-onprem still migrates on boot.
//
// It requires only TEAM_MEMORY_DATABASE_URL.
package main

import (
	"context"
	"os"

	"github.com/pax-beehive/pax-nexus/internal/app"
	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
)

func main() {
	logger := observability.NewLogger(os.Stdout)
	if err := app.Migrate(context.Background(), logger); err != nil {
		logger.Error("team-memory migration failed", "error", err)
		os.Exit(1)
	}
}
