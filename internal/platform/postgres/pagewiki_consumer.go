package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/sessionconsumer"
	"github.com/pax-beehive/pax-nexus/internal/session"
)

// PageWikiConsumerStore serves every scope from one pool: each method either
// takes the scope explicitly or reads it off the row, so a single instance
// backs the process-wide session consumer.
type PageWikiConsumerStore struct {
	pool *pgxpool.Pool
}

func NewPageWikiConsumerStore(pool *pgxpool.Pool) (*PageWikiConsumerStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("create Page Wiki consumer store: pool is required")
	}
	return &PageWikiConsumerStore{pool: pool}, nil
}

func (s *PageWikiConsumerStore) AutoInjectEnabled(ctx context.Context, scopeID string) (bool, error) {
	var enabled bool
	err := s.pool.QueryRow(ctx, `
SELECT auto_inject FROM pagewiki_ingestion_settings WHERE scope_id = $1`, scopeID).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read Page Wiki ingestion setting: %w", err)
	}
	return enabled, nil
}

func (s *PageWikiConsumerStore) SetAutoInjectEnabled(ctx context.Context, scopeID string, enabled bool) error {
	if _, err := s.pool.Exec(ctx, `
INSERT INTO pagewiki_ingestion_settings (scope_id, auto_inject)
VALUES ($1, $2)
ON CONFLICT (scope_id) DO UPDATE SET auto_inject = EXCLUDED.auto_inject, updated_at = NOW()`,
		scopeID, enabled); err != nil {
		return fmt.Errorf("write Page Wiki ingestion setting: %w", err)
	}
	return nil
}

// PendingStreams returns the pending streams of every scope whose ingestion
// settings enable auto inject; the auto-inject decision stays per scope
// through the settings join, and each row carries its own scope_id so the
// consumer can resolve that scope's injector.
func (s *PageWikiConsumerStore) PendingStreams(ctx context.Context) ([]sessionconsumer.Stream, error) {
	return s.queryStreams(ctx, `
SELECT stream.scope_id, stream.user_id, stream.agent_id, stream.session_id, stream.last_sequence
FROM session_streams AS stream
JOIN pagewiki_ingestion_settings AS setting
  ON setting.scope_id = stream.scope_id AND setting.auto_inject = TRUE
LEFT JOIN session_processor_cursors AS cursor
  ON cursor.processor_name = $1
 AND cursor.processor_version = $2
 AND cursor.scope_id = stream.scope_id
 AND cursor.agent_id = stream.agent_id
 AND cursor.session_id = stream.session_id
WHERE stream.last_sequence > COALESCE(cursor.committed_sequence, 0)
  AND stream.source = 'agent-session'
  AND stream.agent_id <> ''
ORDER BY stream.updated_at
LIMIT 100`, sessionconsumer.ProcessorName, sessionconsumer.ProcessorVersion)
}

// Progress reports the ingestion backlog for the status page. Unlike
// PendingStreams it does not gate on auto_inject: the backlog is shown
// even while automatic injection is off.
func (s *PageWikiConsumerStore) Progress(
	ctx context.Context,
	scopeID string,
) (sessionconsumer.Progress, error) {
	var progress sessionconsumer.Progress
	err := s.pool.QueryRow(ctx, `
SELECT
  (SELECT COUNT(*)
   FROM session_streams AS stream
   LEFT JOIN session_processor_cursors AS cursor
     ON cursor.processor_name = $1
    AND cursor.processor_version = $2
    AND cursor.scope_id = stream.scope_id
    AND cursor.agent_id = stream.agent_id
    AND cursor.session_id = stream.session_id
   WHERE stream.last_sequence > COALESCE(cursor.committed_sequence, 0)
     AND stream.scope_id = $3
     AND stream.source = 'agent-session'
     AND stream.agent_id <> ''),
  (SELECT MAX(updated_at)
   FROM session_processor_cursors
   WHERE processor_name = $1 AND processor_version = $2 AND scope_id = $3)`,
		sessionconsumer.ProcessorName, sessionconsumer.ProcessorVersion, scopeID,
	).Scan(&progress.PendingSessions, &progress.LastProcessedAt)
	if err != nil {
		return sessionconsumer.Progress{}, fmt.Errorf("read Page Wiki ingestion progress: %w", err)
	}
	return progress, nil
}

func (s *PageWikiConsumerStore) StreamsBySessionID(
	ctx context.Context,
	scopeID string,
	sessionID string,
) ([]sessionconsumer.Stream, error) {
	return s.queryStreams(ctx, `
SELECT scope_id, user_id, agent_id, session_id, last_sequence
FROM session_streams
WHERE scope_id = $1 AND session_id = $2 AND agent_id <> ''
ORDER BY agent_id`, scopeID, sessionID)
}

func (s *PageWikiConsumerStore) queryStreams(
	ctx context.Context,
	query string,
	args ...any,
) ([]sessionconsumer.Stream, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query Page Wiki session streams: %w", err)
	}
	defer rows.Close()
	streams := make([]sessionconsumer.Stream, 0)
	for rows.Next() {
		var stream sessionconsumer.Stream
		if err := rows.Scan(
			&stream.ScopeID,
			&stream.Actor.UserID,
			&stream.Actor.AgentID,
			&stream.Actor.SessionID,
			&stream.Head,
		); err != nil {
			return nil, fmt.Errorf("scan Page Wiki session stream: %w", err)
		}
		streams = append(streams, stream)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Page Wiki session streams: %w", err)
	}
	return streams, nil
}

func (s *PageWikiConsumerStore) SessionEvents(
	ctx context.Context,
	stream sessionconsumer.Stream,
) ([]session.SessionEvent, error) {
	rows, err := s.pool.Query(ctx, `
SELECT event_id, user_id, agent_id, session_id, sequence, event_type, content,
       task_ref, thread_ref, visibility, occurred_at, captured_at, extracted_at, metadata
FROM session_events
WHERE scope_id = $1 AND agent_id = $2 AND session_id = $3 AND sequence <= $4 AND agent_id <> ''
ORDER BY sequence`, stream.ScopeID, stream.Actor.AgentID, stream.Actor.SessionID, stream.Head)
	if err != nil {
		return nil, fmt.Errorf("query Page Wiki session events: %w", err)
	}
	defer rows.Close()
	events := make([]session.SessionEvent, 0)
	for rows.Next() {
		var event session.SessionEvent
		var metadata []byte
		if err := rows.Scan(
			&event.ID, &event.Actor.UserID, &event.Actor.AgentID, &event.Actor.SessionID,
			&event.Sequence, &event.Type, &event.Content, &event.TaskRef, &event.ThreadRef,
			&event.Visibility, &event.OccurredAt, &event.CapturedAt, &event.ExtractedAt, &metadata,
		); err != nil {
			return nil, fmt.Errorf("scan Page Wiki session event: %w", err)
		}
		if len(metadata) > 0 && strings.TrimSpace(string(metadata)) != "{}" {
			event.Metadata = map[string]string{"raw": string(metadata)}
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Page Wiki session events: %w", err)
	}
	return events, nil
}

func (s *PageWikiConsumerStore) AdvanceCursor(
	ctx context.Context,
	stream sessionconsumer.Stream,
) error {
	if _, err := s.pool.Exec(ctx, `
INSERT INTO session_processor_cursors (
    processor_name, processor_version, scope_id, agent_id, session_id, committed_sequence
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (processor_name, processor_version, scope_id, agent_id, session_id)
DO UPDATE SET committed_sequence = GREATEST(
    session_processor_cursors.committed_sequence,
    EXCLUDED.committed_sequence
), updated_at = NOW()`,
		sessionconsumer.ProcessorName, sessionconsumer.ProcessorVersion, stream.ScopeID,
		stream.Actor.AgentID, stream.Actor.SessionID, stream.Head,
	); err != nil {
		return fmt.Errorf("advance Page Wiki consumer cursor: %w", err)
	}
	return nil
}

var _ sessionconsumer.Store = (*PageWikiConsumerStore)(nil)
