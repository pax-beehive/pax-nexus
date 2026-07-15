package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/paxmprovider"
)

func main() {
	logger := observability.NewLogger(os.Stderr)
	if err := run(context.Background(), logger); err != nil {
		logger.Error("paxm Team Memory provider failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	budget, err := intEnv("TEAM_MEMORY_TOKEN_BUDGET")
	if err != nil {
		return fmt.Errorf("load provider token budget: %w", err)
	}
	requestTimeout, err := durationEnv("TEAM_MEMORY_REQUEST_TIMEOUT")
	if err != nil {
		return fmt.Errorf("load provider request timeout: %w", err)
	}
	if requestTimeout == 0 {
		requestTimeout = 60 * time.Second
	}
	provider, err := paxmprovider.New(paxmprovider.Config{
		BaseURL: os.Getenv("TEAM_MEMORY_BASE_URL"), APIKey: os.Getenv("TEAM_MEMORY_API_KEY"),
		UserID: os.Getenv("PAXM_USER_ID"), AgentID: os.Getenv("PAXM_AGENT_ID"), TokenBudget: budget,
		RequestTimeout: requestTimeout, Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("initialize paxm provider: %w", err)
	}
	logger.InfoContext(ctx, "paxm Team Memory provider started")
	if err := provider.ServeOne(ctx, os.Stdin, os.Stdout); err != nil {
		return fmt.Errorf("serve paxm request: %w", err)
	}
	return nil
}

func durationEnv(name string) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return 0, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("parse %s: duration must be positive", name)
	}
	return parsed, nil
}

func intEnv(name string) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}
