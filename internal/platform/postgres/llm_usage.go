package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
)

// LLMUsageStore persists per-call LLM token usage and serves windowed
// aggregates. It implements llm.UsageSink.
type LLMUsageStore struct {
	pool *pgxpool.Pool
}

func NewLLMUsageStore(pool *pgxpool.Pool) (*LLMUsageStore, error) {
	if pool == nil {
		return nil, errors.New("create LLM usage store: pool is required")
	}
	return &LLMUsageStore{pool: pool}, nil
}

func (s *LLMUsageStore) RecordLLMUsage(ctx context.Context, event llm.UsageEvent) error {
	if strings.TrimSpace(event.ScopeID) == "" || strings.TrimSpace(event.Component) == "" {
		return errors.New("record LLM usage: scope and component are required")
	}
	if _, err := s.pool.Exec(ctx, `
INSERT INTO llm_usage_events
    (scope_id, component, model, input_tokens, cache_hit_tokens, cache_miss_tokens, output_tokens)
VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		event.ScopeID, event.Component, event.Model,
		event.Usage.InputTokens, event.Usage.PromptCacheHitTokens,
		event.Usage.PromptCacheMissTokens, event.Usage.OutputTokens,
	); err != nil {
		return fmt.Errorf("record LLM usage: %w", err)
	}
	return nil
}

func (s *LLMUsageStore) UsageSummary(
	ctx context.Context,
	scopeID string,
	window time.Duration,
) ([]llm.LLMUsageRow, error) {
	if strings.TrimSpace(scopeID) == "" {
		return nil, errors.New("summarize LLM usage: scope is required")
	}
	// window.String() produces Go duration syntax ("168h0m0s"), which
	// Postgres does not parse as an interval. Compute the cutoff in Go so
	// clock authority stays in one place and the comparison is a plain
	// timestamptz comparison.
	cutoff := time.Now().Add(-window)
	rows, err := s.pool.Query(ctx, `
SELECT component, model, COUNT(*),
       COALESCE(SUM(input_tokens), 0), COALESCE(SUM(cache_hit_tokens), 0),
       COALESCE(SUM(cache_miss_tokens), 0), COALESCE(SUM(output_tokens), 0)
FROM llm_usage_events
WHERE scope_id = $1 AND occurred_at >= $2
GROUP BY component, model
ORDER BY component, model`,
		scopeID, cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("summarize LLM usage: %w", err)
	}
	defer rows.Close()
	var summary []llm.LLMUsageRow
	for rows.Next() {
		var row llm.LLMUsageRow
		if err := rows.Scan(
			&row.Component, &row.Model, &row.Calls, &row.InputTokens,
			&row.CacheHitTokens, &row.CacheMissTokens, &row.OutputTokens,
		); err != nil {
			return nil, fmt.Errorf("summarize LLM usage: scan: %w", err)
		}
		summary = append(summary, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("summarize LLM usage: %w", err)
	}
	return summary, nil
}
