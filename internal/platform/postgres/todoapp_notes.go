package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/todoapp"
)

// TodoNoteDirectory implements todoapp.NoteDirectory on top of the shared
// team_notes table, enumerating open blocker/handoff notes as action items.
type TodoNoteDirectory struct {
	pool    *pgxpool.Pool
	scopeID string
}

// NewTodoNoteDirectory constructs a TodoNoteDirectory bound to a single scope.
func NewTodoNoteDirectory(pool *pgxpool.Pool, scopeID string) (*TodoNoteDirectory, error) {
	if pool == nil || strings.TrimSpace(scopeID) == "" {
		return nil, fmt.Errorf("create todoapp note directory: pool and scope are required")
	}
	return &TodoNoteDirectory{pool: pool, scopeID: scopeID}, nil
}

// ListOpenActionItems returns active blocker and handoff team notes for the
// bound scope, newest-updated first.
func (d *TodoNoteDirectory) ListOpenActionItems(ctx context.Context, limit int) ([]todoapp.ActionItem, error) {
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
LIMIT $3`, d.scopeID, now, limit)
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

var _ todoapp.NoteDirectory = (*TodoNoteDirectory)(nil)
