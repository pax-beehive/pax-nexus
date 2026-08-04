package main

import (
	"context"
	"os"

	"github.com/pax-beehive/pax-nexus/internal/app"
	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
)

func main() {
	logger := observability.NewLogger(os.Stdout)
	if err := app.RunSaaS(context.Background(), logger); err != nil {
		logger.Error("team-memory saas failed", "error", err)
		os.Exit(1)
	}
}
