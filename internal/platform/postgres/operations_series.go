package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/operations"
)

// Series returns one row per bucket across the whole window, including empty
// buckets, so callers can plot it without reconstructing gaps.
//
// Two sources, exactly as Summary splits them: Evidence and Recalls come from
// onprem_operation_events, Facts from note_revisions. Both are scope-isolated,
// each against its own table's scope_id column — the events CTE uses $6, the
// facts CTE uses $5. They are separate parameters on purpose even though both
// carry s.scopeID: the two CTEs read different tables and are not the same
// predicate merely written twice.
//
// filter.From is expected to be bucket-aligned; an unaligned From anchors the
// first grid point to the preceding bucket boundary, not to From itself.
func (s *OperationsStore) Series(
	ctx context.Context,
	filter operations.TimeFilter,
	bucket time.Duration,
) ([]operations.SeriesBucket, error) {
	if bucket <= 0 {
		return nil, fmt.Errorf("series bucket must be positive: %w", operations.ErrInvalidInput)
	}
	seconds := int64(bucket / time.Second)
	if seconds <= 0 {
		return nil, fmt.Errorf("series bucket must be at least one second: %w", operations.ErrInvalidInput)
	}

	rows, err := s.pool.Query(ctx, `
WITH bucket_starts AS (
    SELECT generate_series(
        to_timestamp(floor(extract(epoch FROM $1::timestamptz) / $3) * $3),
        $2::timestamptz - interval '1 microsecond',
        make_interval(secs => $3)
    ) AS bucket_at
),
events AS (
    SELECT
        to_timestamp(floor(extract(epoch FROM started_at) / $3) * $3) AS bucket_at,
        COALESCE(sum(accepted_items) FILTER (
            WHERE operation_kind = 'observation.observe'), 0) AS evidence,
        count(*) FILTER (
            WHERE operation_kind IN ('memory.search', 'memory.get', 'team_note.recall')
        ) AS recalls
    FROM onprem_operation_events
    WHERE started_at >= $1 AND started_at < $2
      AND ($4 = '' OR actor_agent_id = $4)
      AND scope_id = $6
    GROUP BY 1
),
facts AS (
    SELECT
        to_timestamp(floor(extract(epoch FROM revisions.created_at) / $3) * $3) AS bucket_at,
        count(*) AS facts
    FROM note_revisions revisions
    JOIN team_notes notes
      ON notes.scope_id = revisions.scope_id AND notes.note_id = revisions.note_id
    WHERE revisions.scope_id = $5
      AND revisions.created_at >= $1 AND revisions.created_at < $2
      AND ($4 = '' OR notes.origin_agent_id = $4)
    GROUP BY 1
)
SELECT
    bucket_starts.bucket_at,
    COALESCE(events.evidence, 0),
    COALESCE(facts.facts, 0),
    COALESCE(events.recalls, 0)
FROM bucket_starts
LEFT JOIN events ON events.bucket_at = bucket_starts.bucket_at
LEFT JOIN facts ON facts.bucket_at = bucket_starts.bucket_at
ORDER BY bucket_starts.bucket_at`,
		filter.From, filter.To, seconds, filter.AgentID, s.scopeID, s.scopeID)
	if err != nil {
		return nil, fmt.Errorf("query postgres operation series: %w", err)
	}
	defer rows.Close()

	var buckets []operations.SeriesBucket
	for rows.Next() {
		var item operations.SeriesBucket
		if err := rows.Scan(&item.BucketAt, &item.Evidence, &item.Facts, &item.Recalls); err != nil {
			return nil, fmt.Errorf("scan postgres operation series: %w", err)
		}
		buckets = append(buckets, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres operation series: %w", err)
	}
	return buckets, nil
}
