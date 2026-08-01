package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/stretchr/testify/require"
)

func TestTodoNoteDirectory_ListOpenActionItems(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()

	store, err := postgres.Open(ctx, dsn)
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, store.Migrate(ctx))

	scopeID := uniqueScope("todoapp-notes")
	base := time.Now().UTC().Truncate(time.Second)

	seedTeamNote(t, store.Pool(), teamNoteSeed{
		scopeID: scopeID, noteID: "note-blocker-active", kind: "blocker",
		subject: "Active blocker", body: "blocking body", state: "active",
		updatedAt: base,
	})
	seedTeamNote(t, store.Pool(), teamNoteSeed{
		scopeID: scopeID, noteID: "note-handoff-active", kind: "handoff",
		subject: "Active handoff", body: "handoff body", state: "active",
		updatedAt: base.Add(10 * time.Second),
	})
	seedTeamNote(t, store.Pool(), teamNoteSeed{
		scopeID: scopeID, noteID: "note-blocker-resolved", kind: "blocker",
		subject: "Resolved blocker", body: "resolved body", state: "resolved",
		updatedAt: base.Add(20 * time.Second),
	})
	seedTeamNote(t, store.Pool(), teamNoteSeed{
		scopeID: scopeID, noteID: "note-status-active", kind: "status",
		subject: "Active status", body: "status body", state: "active",
		updatedAt: base.Add(30 * time.Second),
	})
	seedTeamNote(t, store.Pool(), teamNoteSeed{
		scopeID: scopeID, noteID: "note-blocker-expired", kind: "blocker",
		subject: "Expired blocker", body: "expired body", state: "active",
		updatedAt: base.Add(40 * time.Second),
		// Active by state, but past its TTL: must be excluded from results
		// the same way notes.go's canonical active-note predicate excludes
		// TTL-expired notes elsewhere.
		expiresAt: base.Add(-1 * time.Hour),
	})

	directory, err := postgres.NewTodoNoteDirectory(store.Pool(), scopeID)
	require.NoError(t, err)

	items, err := directory.ListOpenActionItems(ctx, 0)
	require.NoError(t, err)
	require.Len(t, items, 2)

	require.Equal(t, "note-handoff-active", items[0].NoteID)
	require.Equal(t, "handoff", items[0].Kind)
	require.Equal(t, "Active handoff", items[0].Subject)
	require.Equal(t, "handoff body", items[0].Body)
	require.WithinDuration(t, base.Add(10*time.Second), items[0].UpdatedAt, time.Second)

	require.Equal(t, "note-blocker-active", items[1].NoteID)
	require.Equal(t, "blocker", items[1].Kind)
	require.Equal(t, "Active blocker", items[1].Subject)
	require.Equal(t, "blocking body", items[1].Body)
	require.WithinDuration(t, base, items[1].UpdatedAt, time.Second)
}

func TestNewTodoNoteDirectory_ValidatesInputs(t *testing.T) {
	_, err := postgres.NewTodoNoteDirectory(nil, "scope")
	require.Error(t, err)

	dsn := testDSN(t)
	store, err := postgres.Open(context.Background(), dsn)
	require.NoError(t, err)
	defer store.Close()

	_, err = postgres.NewTodoNoteDirectory(store.Pool(), "  ")
	require.Error(t, err)
}

type teamNoteSeed struct {
	scopeID   string
	noteID    string
	kind      string
	subject   string
	body      string
	state     string
	updatedAt time.Time
	// expiresAt overrides soft_expires_at/hard_expires_at when non-zero;
	// otherwise both default to updatedAt+24h (well in the future).
	expiresAt time.Time
}

// seedTeamNote inserts a row directly into team_notes, filling every NOT
// NULL column, so directory reads can be tested without going through the
// note extraction pipeline.
func seedTeamNote(t *testing.T, pool *pgxpool.Pool, seed teamNoteSeed) {
	t.Helper()
	expiresAt := seed.expiresAt
	if expiresAt.IsZero() {
		expiresAt = seed.updatedAt.Add(24 * time.Hour)
	}
	_, err := pool.Exec(context.Background(), `
INSERT INTO team_notes (
    scope_id, note_id, note_key, kind, subject, body,
    task_ref, thread_ref, origin_user_id, origin_agent_id, origin_session_id,
    audience_agent_ids, state, current_revision,
    soft_expires_at, hard_expires_at, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    '', '', 'test-user', 'test-agent', 'test-session',
    '{}', $7, 1,
    $8, $8, $9, $9
)`,
		seed.scopeID, seed.noteID, seed.scopeID+":"+seed.noteID, seed.kind, seed.subject, seed.body,
		seed.state, expiresAt, seed.updatedAt,
	)
	require.NoError(t, err)
}
