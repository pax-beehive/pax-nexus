package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/todoapp"
)

// TodoNoteDirectory implements todoapp.NoteDirectory on top of the shared
// team_notes table, enumerating open blocker/handoff notes as action items.
type TodoNoteDirectory struct {
	pool *pgxpool.Pool
}

// NewTodoNoteDirectory constructs a TodoNoteDirectory. Callers pass scopeID
// per call.
func NewTodoNoteDirectory(pool *pgxpool.Pool) (*TodoNoteDirectory, error) {
	if pool == nil {
		return nil, fmt.Errorf("create todoapp note directory: pool is required")
	}
	return &TodoNoteDirectory{pool: pool}, nil
}

// ListOpenActionItems returns active blocker and handoff team notes for
// scopeID, newest-updated first.
func (d *TodoNoteDirectory) ListOpenActionItems(ctx context.Context, scopeID string, limit int) ([]todoapp.ActionItem, error) {
	if limit <= 0 {
		limit = 50
	}
	now := time.Now().UTC()
	rows, err := d.pool.Query(ctx, `
SELECT note_id, kind, subject, body, updated_at
FROM team_notes
WHERE scope_id = $1 AND kind IN ('blocker', 'handoff') AND state = 'active'
  AND soft_expires_at > $2 AND hard_expires_at > $2
ORDER BY updated_at DESC, note_id DESC
LIMIT $3`, scopeID, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list todoapp open action items: %w", err)
	}
	defer rows.Close()
	items := make([]todoapp.ActionItem, 0)
	for rows.Next() {
		var item todoapp.ActionItem
		if err := rows.Scan(&item.NoteID, &item.Kind, &item.Subject, &item.Body, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan todoapp open action item: %w", err)
		}
		item.UpdatedAt = item.UpdatedAt.UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate todoapp open action items: %w", err)
	}
	return items, nil
}

// ListScopes enumerates the scopes that currently have team notes — the
// population the suggestion-refresh sweep serves. Scope discovery is
// data-driven until the control plane provides a team registry (Phase 3).
func (d *TodoNoteDirectory) ListScopes(ctx context.Context) ([]string, error) {
	rows, err := d.pool.Query(ctx, `SELECT DISTINCT scope_id FROM team_notes`)
	if err != nil {
		return nil, fmt.Errorf("list todo scopes: %w", err)
	}
	defer rows.Close()
	scopeIDs := make([]string, 0)
	for rows.Next() {
		var scopeID string
		if err := rows.Scan(&scopeID); err != nil {
			return nil, fmt.Errorf("list todo scopes: %w", err)
		}
		scopeIDs = append(scopeIDs, scopeID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list todo scopes: %w", err)
	}
	return scopeIDs, nil
}

var _ todoapp.NoteDirectory = (*TodoNoteDirectory)(nil)
var _ todoapp.ScopeLister = (*TodoNoteDirectory)(nil)
