package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/pax-beehive/pax-nexus/internal/session"
)

// AppendStream persists one connector batch for a single generalized stream.
// Sequences are assigned here, in ingest order, under a stream row lock;
// duplicates (by event id) do not consume sequence numbers.
func (r *SessionRepository) AppendStream(ctx context.Context, scopeID string, batch session.StreamBatch) (receipt session.IngestReceipt, returnedErr error) {
	if err := session.ValidateStreamBatch(batch); err != nil {
		return session.IngestReceipt{}, fmt.Errorf("append stream: %w", err)
	}
	stream := batch.Events[0].Stream
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return session.IngestReceipt{}, fmt.Errorf("begin append stream: %w", err)
	}
	defer func() {
		rollbackErr := tx.Rollback(context.Background())
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("rollback append stream: %w", rollbackErr))
		}
	}()

	if _, err := tx.Exec(ctx, `
INSERT INTO session_streams (scope_id, source, stream_id, user_id, agent_id, session_id, visibility)
VALUES ($1, $2, $3, '', '', '', $4)
ON CONFLICT (scope_id, source, stream_id) DO NOTHING`,
		scopeID, stream.Source, stream.StreamID, session.VisibilityTeam); err != nil {
		return session.IngestReceipt{}, fmt.Errorf("ensure stream row: %w", err)
	}
	var lastSequence int64
	if err := tx.QueryRow(ctx, `
SELECT last_sequence FROM session_streams
WHERE scope_id = $1 AND source = $2 AND stream_id = $3
FOR UPDATE`, scopeID, stream.Source, stream.StreamID).Scan(&lastSequence); err != nil {
		return session.IngestReceipt{}, fmt.Errorf("lock stream row: %w", err)
	}

	for _, event := range batch.Events {
		metadata, err := json.Marshal(event.Metadata)
		if err != nil {
			return session.IngestReceipt{}, fmt.Errorf("marshal event %q metadata: %w", event.ID, err)
		}
		result, err := tx.Exec(ctx, `
INSERT INTO session_events (
    scope_id, event_id, source, stream_id, user_id, agent_id, session_id,
    author_kind, author_native_id, author_user_id, sequence, kind, event_type,
    content, task_ref, thread_ref, visibility, occurred_at, metadata
) VALUES ($1, $2, $3, $4, '', '', '', $5, $6, $7, $8, $9, $10, $11, '', $12, $13, $14, $15)
ON CONFLICT (scope_id, event_id) DO NOTHING`,
			scopeID, event.ID, stream.Source, stream.StreamID,
			event.Author.Kind, event.Author.NativeID, event.Author.UserID,
			lastSequence+1, event.Kind, event.Type, event.Content,
			event.ThreadRef, event.Visibility, event.OccurredAt, metadata)
		if err != nil {
			return session.IngestReceipt{}, fmt.Errorf("insert stream event %q: %w", event.ID, err)
		}
		if result.RowsAffected() == 0 {
			receipt.Duplicate++
			continue
		}
		lastSequence++
		receipt.Accepted++
	}
	receipt.Cursor = lastSequence

	if _, err := tx.Exec(ctx, `
UPDATE session_streams
SET last_sequence = $4, complete = complete OR $5, updated_at = NOW()
WHERE scope_id = $1 AND source = $2 AND stream_id = $3`,
		scopeID, stream.Source, stream.StreamID, lastSequence, batch.Complete); err != nil {
		return session.IngestReceipt{}, fmt.Errorf("advance stream head: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return session.IngestReceipt{}, fmt.Errorf("commit append stream: %w", err)
	}
	return receipt, nil
}

func (r *SessionRepository) StreamEvents(ctx context.Context, scopeID string, stream session.Stream, after int64, limit int) ([]session.StreamEvent, error) {
	if scopeID == "" || stream.Source == "" || stream.StreamID == "" || limit <= 0 {
		return nil, fmt.Errorf("list stream events: %w", ErrInvalidSessionBatch)
	}
	rows, err := r.pool.Query(ctx, `
SELECT event_id, source, stream_id, author_kind, author_native_id, author_user_id,
       sequence, kind, event_type, content, thread_ref, visibility,
       occurred_at, captured_at, metadata
FROM session_events
WHERE scope_id = $1 AND source = $2 AND stream_id = $3 AND sequence > $4
ORDER BY sequence
LIMIT $5`, scopeID, stream.Source, stream.StreamID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("query stream events: %w", err)
	}
	defer rows.Close()
	events, err := pgx.CollectRows(rows, scanStreamEvent)
	if err != nil {
		return nil, fmt.Errorf("scan stream events: %w", err)
	}
	return events, nil
}

func scanStreamEvent(row pgx.CollectableRow) (session.StreamEvent, error) {
	var event session.StreamEvent
	var metadata []byte
	err := row.Scan(
		&event.ID, &event.Stream.Source, &event.Stream.StreamID,
		&event.Author.Kind, &event.Author.NativeID, &event.Author.UserID,
		&event.Sequence, &event.Kind, &event.Type, &event.Content,
		&event.ThreadRef, &event.Visibility, &event.OccurredAt, &event.CapturedAt, &metadata,
	)
	if err != nil {
		return session.StreamEvent{}, fmt.Errorf("scan stream event columns: %w", err)
	}
	if err := json.Unmarshal(metadata, &event.Metadata); err != nil {
		return session.StreamEvent{}, fmt.Errorf("decode stream event %q metadata: %w", event.ID, err)
	}
	return event, nil
}
