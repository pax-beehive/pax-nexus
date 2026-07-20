// Command eval-v2-bm25 renders deterministic raw-session BM25 context for Eval v2.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/bm25"
	"github.com/pax-beehive/pax-nexus/internal/session"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "eval v2 BM25 failed: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("eval-v2-bm25", flag.ContinueOnError)
	input := flags.String("session-batches-file", "", "native session batches JSON path")
	query := flags.String("query", "", "consumer question")
	candidateLimit := flags.Int("candidate-limit", 8, "maximum BM25 candidates before budget packing")
	tokenBudget := flags.Int("token-budget", 500, "maximum context token budget")
	chunkEvents := flags.Int("chunk-events", 4, "source events per BM25 document")
	cutoff := flags.String("temporal-cutoff", "", "optional RFC3339 cutoff for source events")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse BM25 flags: %w", err)
	}
	if strings.TrimSpace(*input) == "" {
		return fmt.Errorf("read BM25 source: session-batches-file is required")
	}
	batches, err := readBatches(*input)
	if err != nil {
		return err
	}
	temporalCutoff, err := parseCutoff(*cutoff)
	if err != nil {
		return err
	}
	result, err := bm25.RecallRaw(batches, bm25.RawQuery{
		Text: *query, CandidateLimit: *candidateLimit, TokenBudget: *tokenBudget,
		ChunkEvents: *chunkEvents, TemporalCutoff: temporalCutoff,
	})
	if err != nil {
		return err
	}
	if err := json.NewEncoder(output).Encode(result); err != nil {
		return fmt.Errorf("encode BM25 result: %w", err)
	}
	return nil
}

func readBatches(path string) ([]session.SessionBatch, error) {
	input, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read BM25 source %q: %w", path, err)
	}
	var batches []session.SessionBatch
	if err := json.Unmarshal(input, &batches); err != nil {
		return nil, fmt.Errorf("decode BM25 source %q: %w", path, err)
	}
	return batches, nil
}

func parseCutoff(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	cutoff, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse BM25 temporal cutoff: %w", err)
	}
	return cutoff, nil
}
