package postgres

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/session"
)

//go:embed migrations/001_init.sql migrations/002_temporal_notes.sql migrations/003_note_relations.sql migrations/004_extraction_latency.sql migrations/005_note_embeddings.sql migrations/006_note_identity.sql migrations/007_extraction_run_actor.sql migrations/008_extraction_run_candidates.sql migrations/009_extraction_run_result.sql migrations/010_note_identity_ref.sql migrations/011_recall_observations.sql
var migrations embed.FS

var ErrInvalidSessionBatch = errors.New("invalid session batch")

type Store struct {
	pool               *pgxpool.Pool
	extractionEnqueuer ExtractionEnqueuer
}

type ExtractionEnqueuer interface {
	EnqueueTx(context.Context, pgx.Tx, string, session.Actor, int64, bool) (string, error)
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("open postgres store: empty DSN")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

func (s *Store) ConfigureExtractionEnqueuer(enqueuer ExtractionEnqueuer) error {
	if enqueuer == nil {
		return fmt.Errorf("configure extraction enqueuer: enqueuer is required")
	}
	if s.extractionEnqueuer != nil {
		return fmt.Errorf("configure extraction enqueuer: already configured")
	}
	s.extractionEnqueuer = enqueuer
	return nil
}

func (s *Store) Migrate(ctx context.Context) error {
	for _, path := range []string{
		"migrations/001_init.sql",
		"migrations/002_temporal_notes.sql",
		"migrations/003_note_relations.sql",
		"migrations/004_extraction_latency.sql",
		"migrations/005_note_embeddings.sql",
		"migrations/006_note_identity.sql",
		"migrations/007_extraction_run_actor.sql",
		"migrations/008_extraction_run_candidates.sql",
		"migrations/009_extraction_run_result.sql",
		"migrations/010_note_identity_ref.sql",
		"migrations/011_recall_observations.sql",
	} {
		migration, err := migrations.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read postgres migration %q: %w", path, err)
		}
		if _, err := s.pool.Exec(ctx, string(migration)); err != nil {
			return fmt.Errorf("apply postgres migration %q: %w", path, err)
		}
	}
	return nil
}

func (s *Store) AppendSession(ctx context.Context, scopeID string, batch session.SessionBatch) (receipt session.IngestReceipt, returnedErr error) {
	if err := validateBatch(scopeID, batch); err != nil {
		return session.IngestReceipt{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return session.IngestReceipt{}, fmt.Errorf("begin append session: %w", err)
	}
	defer func() {
		rollbackErr := tx.Rollback(context.Background())
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("rollback append session: %w", rollbackErr))
		}
	}()

	receipt, err = appendEvents(ctx, tx, scopeID, batch)
	if err != nil {
		return session.IngestReceipt{}, err
	}
	if err := upsertStream(ctx, tx, scopeID, batch, receipt.Cursor); err != nil {
		return session.IngestReceipt{}, err
	}
	if s.extractionEnqueuer != nil {
		jobID, enqueueErr := s.extractionEnqueuer.EnqueueTx(ctx, tx, scopeID, batch.Events[0].Actor, receipt.Cursor, batch.Complete)
		if enqueueErr != nil {
			return session.IngestReceipt{}, fmt.Errorf("enqueue extraction: %w", enqueueErr)
		}
		receipt.RunID = jobID
	}
	if err := tx.Commit(ctx); err != nil {
		return session.IngestReceipt{}, fmt.Errorf("commit append session: %w", err)
	}
	return receipt, nil
}

func (s *Store) SessionLatestSequence(ctx context.Context, scopeID string, actor session.Actor) (int64, error) {
	if strings.TrimSpace(scopeID) == "" || strings.TrimSpace(actor.AgentID) == "" || strings.TrimSpace(actor.SessionID) == "" {
		return 0, fmt.Errorf("read session head: %w", ErrInvalidSessionBatch)
	}
	var sequence int64
	err := s.pool.QueryRow(ctx, `
SELECT last_sequence
FROM session_streams
WHERE scope_id = $1 AND agent_id = $2 AND session_id = $3`, scopeID, actor.AgentID, actor.SessionID).Scan(&sequence)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read session head: %w", err)
	}
	return sequence, nil
}

func (s *Store) SessionEvents(ctx context.Context, scopeID string, actor session.Actor, after int64, limit int) ([]session.SessionEvent, error) {
	if strings.TrimSpace(scopeID) == "" || strings.TrimSpace(actor.AgentID) == "" || strings.TrimSpace(actor.SessionID) == "" || limit <= 0 {
		return nil, fmt.Errorf("list session events: %w", ErrInvalidSessionBatch)
	}
	rows, err := s.pool.Query(ctx, `
SELECT event_id, user_id, agent_id, session_id, sequence, event_type, content,
       task_ref, thread_ref, visibility, occurred_at, captured_at, extracted_at, metadata
FROM session_events
WHERE scope_id = $1 AND agent_id = $2 AND session_id = $3 AND sequence > $4
ORDER BY sequence
LIMIT $5`, scopeID, actor.AgentID, actor.SessionID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("query session events: %w", err)
	}
	defer rows.Close()

	events, err := pgx.CollectRows(rows, scanSessionEvent)
	if err != nil {
		return nil, fmt.Errorf("scan session events: %w", err)
	}
	return events, nil
}

func (s *Store) SessionEventsBefore(ctx context.Context, scopeID string, actor session.Actor, atOrBefore int64, limit int) ([]session.SessionEvent, error) {
	if strings.TrimSpace(scopeID) == "" || strings.TrimSpace(actor.AgentID) == "" || strings.TrimSpace(actor.SessionID) == "" || atOrBefore <= 0 || limit <= 0 {
		return nil, fmt.Errorf("list session overlap: %w", ErrInvalidSessionBatch)
	}
	rows, err := s.pool.Query(ctx, `
SELECT event_id, user_id, agent_id, session_id, sequence, event_type, content,
       task_ref, thread_ref, visibility, occurred_at, captured_at, extracted_at, metadata
FROM session_events
WHERE scope_id = $1 AND agent_id = $2 AND session_id = $3 AND sequence <= $4
ORDER BY sequence DESC
LIMIT $5`, scopeID, actor.AgentID, actor.SessionID, atOrBefore, limit)
	if err != nil {
		return nil, fmt.Errorf("query session overlap: %w", err)
	}
	defer rows.Close()
	events, err := pgx.CollectRows(rows, scanSessionEvent)
	if err != nil {
		return nil, fmt.Errorf("scan session overlap: %w", err)
	}
	for left, right := 0, len(events)-1; left < right; left, right = left+1, right-1 {
		events[left], events[right] = events[right], events[left]
	}
	return events, nil
}

func (s *Store) ExtractionCursor(ctx context.Context, scopeID string, actor session.Actor) (int64, error) {
	var cursor int64
	err := s.pool.QueryRow(ctx, `
SELECT extraction_cursor
FROM session_streams
WHERE scope_id = $1 AND agent_id = $2 AND session_id = $3`, scopeID, actor.AgentID, actor.SessionID).Scan(&cursor)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read extraction cursor: %w", err)
	}
	return cursor, nil
}

func (s *Store) AdvanceExtractionCursor(ctx context.Context, scopeID string, actor session.Actor, cursor int64) (returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin extraction cursor update: %w", err)
	}
	defer func() {
		if err := tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("rollback extraction cursor update: %w", err))
		}
	}()
	result, err := tx.Exec(ctx, `
UPDATE session_streams
SET extraction_cursor = GREATEST(extraction_cursor, $4), updated_at = NOW()
WHERE scope_id = $1 AND agent_id = $2 AND session_id = $3 AND last_sequence >= $4`,
		scopeID, actor.AgentID, actor.SessionID, cursor)
	if err != nil {
		return fmt.Errorf("advance extraction cursor: %w", err)
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("advance extraction cursor: %w", ErrInvalidSessionBatch)
	}
	if _, err := tx.Exec(ctx, `
UPDATE session_events
SET extracted_at = COALESCE(extracted_at, NOW())
WHERE scope_id = $1 AND agent_id = $2 AND session_id = $3 AND sequence <= $4`,
		scopeID, actor.AgentID, actor.SessionID, cursor); err != nil {
		return fmt.Errorf("mark session events extracted: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit extraction cursor update: %w", err)
	}
	return nil
}

func appendEvents(ctx context.Context, tx pgx.Tx, scopeID string, batch session.SessionBatch) (session.IngestReceipt, error) {
	receipt := session.IngestReceipt{}
	for _, event := range batch.Events {
		metadata, err := json.Marshal(event.Metadata)
		if err != nil {
			return session.IngestReceipt{}, fmt.Errorf("marshal event %q metadata: %w", event.ID, err)
		}
		result, err := tx.Exec(ctx, `
INSERT INTO session_events (
    scope_id, event_id, user_id, agent_id, session_id, sequence, event_type,
    content, task_ref, thread_ref, visibility, occurred_at, metadata
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (scope_id, event_id) DO NOTHING`,
			scopeID, event.ID, event.Actor.UserID, event.Actor.AgentID, event.Actor.SessionID,
			event.Sequence, event.Type, event.Content, event.TaskRef, event.ThreadRef,
			event.Visibility, event.OccurredAt, metadata)
		if err != nil {
			return session.IngestReceipt{}, fmt.Errorf("insert session event %q: %w", event.ID, err)
		}
		if result.RowsAffected() == 0 {
			receipt.Duplicate++
		} else {
			receipt.Accepted++
		}
		if event.Sequence > receipt.Cursor {
			receipt.Cursor = event.Sequence
		}
	}
	return receipt, nil
}

func upsertStream(ctx context.Context, tx pgx.Tx, scopeID string, batch session.SessionBatch, cursor int64) error {
	actor := batch.Events[0].Actor
	_, err := tx.Exec(ctx, `
INSERT INTO session_streams (
    scope_id, user_id, agent_id, session_id, last_sequence, complete
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (scope_id, agent_id, session_id) DO UPDATE SET
    last_sequence = GREATEST(session_streams.last_sequence, EXCLUDED.last_sequence),
    complete = session_streams.complete OR EXCLUDED.complete,
    updated_at = NOW()`, scopeID, actor.UserID, actor.AgentID, actor.SessionID, cursor, batch.Complete)
	if err != nil {
		return fmt.Errorf("upsert session stream: %w", err)
	}
	return nil
}

func validateBatch(scopeID string, batch session.SessionBatch) error {
	if strings.TrimSpace(scopeID) == "" || len(batch.Events) == 0 {
		return fmt.Errorf("scope or events: %w", ErrInvalidSessionBatch)
	}
	first := batch.Events[0].Actor
	for _, event := range batch.Events {
		if strings.TrimSpace(event.ID) == "" || event.Sequence <= 0 || event.OccurredAt.IsZero() {
			return fmt.Errorf("event identity: %w", ErrInvalidSessionBatch)
		}
		if event.Actor != first || strings.TrimSpace(event.Actor.UserID) == "" || strings.TrimSpace(event.Actor.AgentID) == "" || strings.TrimSpace(event.Actor.SessionID) == "" {
			return fmt.Errorf("mixed or empty actor: %w", ErrInvalidSessionBatch)
		}
	}
	return nil
}

func scanSessionEvent(row pgx.CollectableRow) (session.SessionEvent, error) {
	var event session.SessionEvent
	var metadata []byte
	err := row.Scan(
		&event.ID, &event.Actor.UserID, &event.Actor.AgentID, &event.Actor.SessionID,
		&event.Sequence, &event.Type, &event.Content, &event.TaskRef, &event.ThreadRef,
		&event.Visibility, &event.OccurredAt, &event.CapturedAt, &event.ExtractedAt, &metadata,
	)
	if err != nil {
		return session.SessionEvent{}, fmt.Errorf("scan event columns: %w", err)
	}
	if err := json.Unmarshal(metadata, &event.Metadata); err != nil {
		return session.SessionEvent{}, fmt.Errorf("decode event %q metadata: %w", event.ID, err)
	}
	return event, nil
}
