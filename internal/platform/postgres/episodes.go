package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
)

// EpisodeStore persists extraction episode checkpoints using optimistic concurrency.
type EpisodeStore struct {
	pool *pgxpool.Pool
}

func (s *EpisodeStore) LoadEpisode(ctx context.Context, key extractor.EpisodeKey) (extractor.Episode, bool, error) {
	if strings.TrimSpace(key.ScopeID) == "" {
		return extractor.Episode{}, false, fmt.Errorf("load extraction episode: scope is required")
	}
	var episode extractor.Episode
	var checkpoint, messages, runs []byte
	episode.Key = key
	err := s.pool.QueryRow(ctx, `
SELECT version, checkpoint, messages, runs, estimated_tokens, event_count,
       compaction_count, protocol_version, model, prompt_version
FROM extraction_episodes
WHERE scope_id = $1 AND task_ref = $2 AND thread_ref = $3`,
		key.ScopeID, key.TaskRef, key.ThreadRef).Scan(
		&episode.Version, &checkpoint, &messages, &runs, &episode.EstimatedTokens,
		&episode.EventCount, &episode.CompactionCount, &episode.ProtocolVersion,
		&episode.Model, &episode.PromptVersion,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return extractor.Episode{Key: key}, false, nil
	}
	if err != nil {
		return extractor.Episode{}, false, fmt.Errorf("load extraction episode: %w", err)
	}
	if err := json.Unmarshal(checkpoint, &episode.Checkpoint); err != nil {
		return extractor.Episode{}, false, fmt.Errorf("decode extraction episode checkpoint: %w", err)
	}
	if err := json.Unmarshal(messages, &episode.Messages); err != nil {
		return extractor.Episode{}, false, fmt.Errorf("decode extraction episode messages: %w", err)
	}
	if err := json.Unmarshal(runs, &episode.Runs); err != nil {
		return extractor.Episode{}, false, fmt.Errorf("decode extraction episode runs: %w", err)
	}
	return episode, true, nil
}

func (s *EpisodeStore) SaveEpisode(ctx context.Context, episode extractor.Episode, expectedVersion int64) error {
	checkpoint, err := json.Marshal(episode.Checkpoint)
	if err != nil {
		return fmt.Errorf("encode extraction episode checkpoint: %w", err)
	}
	messages, err := json.Marshal(episode.Messages)
	if err != nil {
		return fmt.Errorf("encode extraction episode messages: %w", err)
	}
	runs, err := json.Marshal(episode.Runs)
	if err != nil {
		return fmt.Errorf("encode extraction episode runs: %w", err)
	}
	if expectedVersion == 0 {
		result, insertErr := s.pool.Exec(ctx, `
INSERT INTO extraction_episodes (
    scope_id, task_ref, thread_ref, version, checkpoint, messages, runs,
    estimated_tokens, event_count, compaction_count, protocol_version, model, prompt_version
) VALUES ($1, $2, $3, 1, $4::jsonb, $5::jsonb, $6::jsonb, $7, $8, $9, $10, $11, $12)
ON CONFLICT DO NOTHING`, episode.Key.ScopeID, episode.Key.TaskRef, episode.Key.ThreadRef,
			string(checkpoint), string(messages), string(runs), episode.EstimatedTokens, episode.EventCount,
			episode.CompactionCount, episode.ProtocolVersion, episode.Model, episode.PromptVersion)
		if insertErr != nil {
			return fmt.Errorf("insert extraction episode: %w", insertErr)
		}
		if result.RowsAffected() != 1 {
			return extractor.ErrEpisodeConflict
		}
		return nil
	}
	result, err := s.pool.Exec(ctx, `
UPDATE extraction_episodes
SET version = version + 1, checkpoint = $5::jsonb, messages = $6::jsonb, runs = $7::jsonb,
	    estimated_tokens = $8, event_count = $9, compaction_count = $10,
	    protocol_version = $11, model = $12, prompt_version = $13, updated_at = NOW()
WHERE scope_id = $1 AND task_ref = $2 AND thread_ref = $3 AND version = $4`,
		episode.Key.ScopeID, episode.Key.TaskRef, episode.Key.ThreadRef, expectedVersion,
		string(checkpoint), string(messages), string(runs), episode.EstimatedTokens, episode.EventCount,
		episode.CompactionCount, episode.ProtocolVersion, episode.Model, episode.PromptVersion)
	if err != nil {
		return fmt.Errorf("update extraction episode: %w", err)
	}
	if result.RowsAffected() != 1 {
		return extractor.ErrEpisodeConflict
	}
	return nil
}
