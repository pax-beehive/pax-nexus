package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/explorer"
)

// NoteMix counts live Team Notes per kind for the store's scope.
//
// "Live" reuses the same effective-state expression measureTeamMemory uses
// for the storage snapshot: a note is expired when its state says so or its
// hard expiry has passed, resolved when its state says so or invalid_at has
// passed, otherwise active. Keeping one definition matters — two different
// notions of "live" on the same page would not add up.
func (s *ExplorerStore) NoteMix(
	ctx context.Context,
	at time.Time,
) ([]explorer.NoteKindCount, error) {
	rows, err := s.pool.Query(ctx, `
WITH effective_notes AS (
    SELECT kind, CASE
        WHEN state = 'expired' OR hard_expires_at <= $1 THEN 'expired'
        WHEN state = 'resolved' OR (invalid_at IS NOT NULL AND invalid_at <= $1) THEN 'resolved'
        ELSE 'active'
    END AS effective_state
    FROM team_notes
    WHERE scope_id = $2
)
SELECT kind, count(*)
FROM effective_notes
WHERE effective_state = 'active'
GROUP BY kind
ORDER BY count(*) DESC, kind ASC`, at, s.scopeID)
	if err != nil {
		return nil, fmt.Errorf("query postgres note mix: %w", err)
	}
	defer rows.Close()

	var mix []explorer.NoteKindCount
	for rows.Next() {
		var item explorer.NoteKindCount
		if err := rows.Scan(&item.Kind, &item.Count); err != nil {
			return nil, fmt.Errorf("scan postgres note mix: %w", err)
		}
		mix = append(mix, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres note mix: %w", err)
	}
	return mix, nil
}
