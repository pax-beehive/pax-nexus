// Package extractionshadow replays fixed Session Events from a persisted
// evaluation run through the real extraction pipeline with a selectable
// extractor protocol, so extraction versions can be compared before recall
// on an identical cohort.
package extractionshadow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

// ManifestCase maps one evaluation case to its persisted scope suffix.
type ManifestCase struct {
	ID       string
	Category string
	ScopeID  string
}

// LoadManifestCases reads the case list from an evaluation selection manifest.
func LoadManifestCases(path string) ([]ManifestCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("open extraction shadow manifest: %w", err)
	}
	var manifest struct {
		Cases []struct {
			ID       string `json:"id"`
			Category string `json:"category"`
			ScopeID  string `json:"scope_id"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode extraction shadow manifest: %w", err)
	}
	if len(manifest.Cases) == 0 {
		return nil, fmt.Errorf("decode extraction shadow manifest: at least one case is required")
	}
	cases := make([]ManifestCase, 0, len(manifest.Cases))
	for _, entry := range manifest.Cases {
		if entry.ID == "" || entry.ScopeID == "" {
			return nil, fmt.Errorf("decode extraction shadow manifest: case id and scope_id are required")
		}
		cases = append(cases, ManifestCase{ID: entry.ID, Category: entry.Category, ScopeID: entry.ScopeID})
	}
	return cases, nil
}

// StreamEvents is one producer stream's events in sequence order.
type StreamEvents struct {
	Actor  teamnote.Actor
	Events []teamnote.SessionEvent
}

// ExportStreams reads every Session Event persisted for one scope and groups
// them per producer stream in sequence order, reproducing the fixed input the
// live pipeline extracted.
func ExportStreams(ctx context.Context, pool *pgxpool.Pool, scopeID string) ([]StreamEvents, error) {
	rows, err := pool.Query(ctx, `
SELECT event_id, user_id, agent_id, session_id, sequence, event_type, content,
       task_ref, thread_ref, visibility, occurred_at, captured_at, metadata
FROM session_events WHERE scope_id = $1
ORDER BY user_id, agent_id, session_id, sequence`, scopeID)
	if err != nil {
		return nil, fmt.Errorf("export session events for %q: %w", scopeID, err)
	}
	defer rows.Close()
	streams := make(map[string]*StreamEvents)
	order := make([]string, 0)
	for rows.Next() {
		var event teamnote.SessionEvent
		var metadata map[string]string
		if err := rows.Scan(
			&event.ID, &event.Actor.UserID, &event.Actor.AgentID, &event.Actor.SessionID,
			&event.Sequence, &event.Type, &event.Content, &event.TaskRef, &event.ThreadRef,
			&event.Visibility, &event.OccurredAt, &event.CapturedAt, &metadata,
		); err != nil {
			return nil, fmt.Errorf("scan session event for %q: %w", scopeID, err)
		}
		event.Metadata = metadata
		key := event.Actor.UserID + "/" + event.Actor.AgentID + "/" + event.Actor.SessionID
		stream, ok := streams[key]
		if !ok {
			stream = &StreamEvents{Actor: event.Actor}
			streams[key] = stream
			order = append(order, key)
		}
		stream.Events = append(stream.Events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read session events for %q: %w", scopeID, err)
	}
	if len(streams) == 0 {
		return nil, fmt.Errorf("export session events for %q: no events persisted", scopeID)
	}
	sort.Strings(order)
	result := make([]StreamEvents, 0, len(order))
	for _, key := range order {
		result = append(result, *streams[key])
	}
	return result, nil
}
