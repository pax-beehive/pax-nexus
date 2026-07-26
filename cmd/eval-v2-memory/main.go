package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/v2/memoryprobe"
	"github.com/pax-beehive/pax-nexus/internal/session"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Getenv, os.Stdout, &http.Client{Timeout: 90 * time.Second}); err != nil {
		log.Printf("eval v2 memory helper failed: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, output io.Writer, httpClient *http.Client) error {
	flags := flag.NewFlagSet("eval-v2-memory", flag.ContinueOnError)
	action := flags.String("action", "", "preflight, preflight-mem0, or ingest")
	provider := flags.String("provider", "", "memory provider for ingest")
	textFile := flags.String("text-file", "", "shared producer transcript path")
	sessionBatchesFile := flags.String("session-batches-file", "", "native session batches path")
	requireWrite := flags.Bool("require-write", false, "reject a source-bearing ingest that produces no mutations")
	marker := flags.String("marker", "", "preflight marker")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse eval memory flags: %w", err)
	}
	recallAttempts, err := intEnv(getenv, "PAX_EVAL_RECALL_ATTEMPTS")
	if err != nil {
		return fmt.Errorf("configure eval memory probe: %w", err)
	}
	client, err := memoryprobe.New(memoryprobe.Config{
		TeamNoteURL:    envOrDefault(getenv, "TEAM_MEMORY_BASE_URL", "http://team-memory:8080"),
		TeamNoteAPIKey: getenv("TEAM_MEMORY_API_KEY"),
		Mem0URL:        envOrDefault(getenv, "MEM0_BASE_URL", "http://mem0:8000"),
		UserID:         getenv("PAXM_USER_ID"), AgentID: getenv("PAXM_AGENT_ID"), RunID: getenv("MEM0_RUN_ID"),
		HTTPClient:     httpClient,
		RecallAttempts: recallAttempts,
	})
	if err != nil {
		return err
	}
	switch *action {
	case "preflight":
		return client.Preflight(ctx, *marker)
	case "preflight-mem0":
		return client.PreflightMem0(ctx, *marker)
	case "ingest":
		if (*textFile == "") == (*sessionBatchesFile == "") {
			return fmt.Errorf("ingest eval memory: exactly one of text-file or session-batches-file is required")
		}
		var result memoryprobe.IngestResult
		if *sessionBatchesFile != "" {
			batches, err := readSessionBatches(*sessionBatchesFile)
			if err != nil {
				return fmt.Errorf("ingest native session batches: %w", err)
			}
			result, err = client.IngestBatches(ctx, *provider, batches)
			if err != nil {
				return fmt.Errorf("ingest native session batches: %w", err)
			}
		} else {
			text, err := os.ReadFile(*textFile)
			if err != nil {
				return fmt.Errorf("read shared producer transcript: %w", err)
			}
			result, err = client.Ingest(ctx, *provider, string(text))
			if err != nil {
				return err
			}
		}
		if *requireWrite && result.SourceEvents > 0 && result.NoOpKnown && result.NoOp {
			return fmt.Errorf("validate source-bearing ingest: provider %q produced no mutations for %d source events", result.Provider, result.SourceEvents)
		}
		if err := json.NewEncoder(output).Encode(result); err != nil {
			return fmt.Errorf("encode eval ingest result: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("eval memory action must be preflight, preflight-mem0, or ingest")
	}
}

func readSessionBatches(path string) ([]session.SessionBatch, error) {
	input, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read native session batches: %w", err)
	}
	var batches []session.SessionBatch
	if err := json.Unmarshal(input, &batches); err != nil {
		return nil, fmt.Errorf("decode native session batches: %w", err)
	}
	return batches, nil
}

func envOrDefault(getenv func(string) string, name, fallback string) string {
	if value := strings.TrimSpace(getenv(name)); value != "" {
		return value
	}
	return fallback
}

// intEnv parses name as an integer, following the same unset-means-zero,
// malformed-means-error contract as intEnv in
// cmd/paxm-team-memory-provider/main.go. Unset or empty returns 0 with no
// error (memoryprobe.New then applies its own default for a non-positive
// RecallAttempts). A value that fails to parse is a caller configuration
// mistake, not a signal to fall back silently, so it is reported by name and
// offending value rather than swallowed.
func intEnv(getenv func(string) string, name string) (int, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s=%q: %w", name, raw, err)
	}
	return value, nil
}
