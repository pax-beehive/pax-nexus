package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/v2/memoryprobe"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Getenv); err != nil {
		log.Printf("eval v2 memory helper failed: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string) error {
	flags := flag.NewFlagSet("eval-v2-memory", flag.ContinueOnError)
	action := flags.String("action", "", "preflight or ingest")
	provider := flags.String("provider", "", "memory provider for ingest")
	textFile := flags.String("text-file", "", "shared producer transcript path")
	marker := flags.String("marker", "", "preflight marker")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse eval memory flags: %w", err)
	}
	client, err := memoryprobe.New(memoryprobe.Config{
		TeamNoteURL:    envOrDefault(getenv, "TEAM_MEMORY_BASE_URL", "http://team-memory:8080"),
		TeamNoteAPIKey: getenv("TEAM_MEMORY_API_KEY"),
		Mem0URL:        envOrDefault(getenv, "MEM0_BASE_URL", "http://mem0:8000"),
		UserID:         getenv("PAXM_USER_ID"), AgentID: getenv("PAXM_AGENT_ID"), RunID: getenv("MEM0_RUN_ID"),
		HTTPClient: &http.Client{Timeout: 90 * time.Second},
	})
	if err != nil {
		return err
	}
	switch *action {
	case "preflight":
		return client.Preflight(ctx, *marker)
	case "ingest":
		if strings.TrimSpace(*textFile) == "" {
			return fmt.Errorf("ingest shared producer transcript: text-file is required")
		}
		text, err := os.ReadFile(*textFile)
		if err != nil {
			return fmt.Errorf("read shared producer transcript: %w", err)
		}
		return client.Ingest(ctx, *provider, string(text))
	default:
		return fmt.Errorf("eval memory action must be preflight or ingest")
	}
}

func envOrDefault(getenv func(string) string, name, fallback string) string {
	if value := strings.TrimSpace(getenv(name)); value != "" {
		return value
	}
	return fallback
}
