package postgres_test

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/explorer"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/stretchr/testify/suite"
)

type explorerNoteMixSuite struct {
	suite.Suite
	store     *postgres.Store
	explorer  *postgres.ExplorerStore
	scope     string
	adminPool *pgxpool.Pool
	schema    string
}

func TestExplorerNoteMixSuite(t *testing.T) {
	suite.Run(t, new(explorerNoteMixSuite))
}

func (s *explorerNoteMixSuite) SetupSuite() {
	ctx := context.Background()
	dsn := testDSN(s.T())
	adminPool, err := pgxpool.New(ctx, dsn)
	s.Require().NoError(err)
	s.adminPool = adminPool
	s.schema = fmt.Sprintf("explorer_notemix_%d", time.Now().UnixNano())
	_, err = adminPool.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{s.schema}.Sanitize())
	s.Require().NoError(err)
	parsed, err := url.Parse(dsn)
	s.Require().NoError(err)
	query := parsed.Query()
	query.Set("search_path", s.schema+",public")
	parsed.RawQuery = query.Encode()
	store, err := postgres.Open(ctx, parsed.String())
	s.Require().NoError(err)
	s.Require().NoError(store.Migrate(ctx))
	s.store = store
	s.scope = "notemix-suite-scope"
	s.explorer = store.Explorer(s.scope)
}

func (s *explorerNoteMixSuite) TearDownSuite() {
	if s.store != nil {
		s.store.Close()
	}
	if s.adminPool != nil {
		_, err := s.adminPool.Exec(
			context.Background(),
			"DROP SCHEMA "+pgx.Identifier{s.schema}.Sanitize()+" CASCADE",
		)
		s.NoError(err)
		s.adminPool.Close()
	}
}

func (s *explorerNoteMixSuite) SetupTest() {
	_, err := s.store.Pool().Exec(context.Background(), `TRUNCATE team_notes CASCADE`)
	s.Require().NoError(err)
}

// Only live notes count: a resolved note and a hard-expired note must not
// appear, otherwise the Overview mix would grow monotonically and stop
// describing what the team currently remembers.
func (s *explorerNoteMixSuite) TestNoteMixCountsOnlyLiveNotesPerKind() {
	ctx := context.Background()
	at := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)

	s.insertNote(ctx, "n1", "decision", "active", at.Add(24*time.Hour))
	s.insertNote(ctx, "n2", "decision", "active", at.Add(24*time.Hour))
	s.insertNote(ctx, "n3", "blocker", "active", at.Add(24*time.Hour))
	s.insertNote(ctx, "n4", "blocker", "resolved", at.Add(24*time.Hour))
	s.insertNote(ctx, "n5", "handoff", "active", at.Add(-time.Hour)) // hard-expired

	mix, err := s.explorer.NoteMix(ctx, at)
	s.Require().NoError(err)
	s.Require().Len(mix, 2)
	s.Equal(explorer.NoteKindCount{Kind: "decision", Count: 2}, mix[0])
	s.Equal(explorer.NoteKindCount{Kind: "blocker", Count: 1}, mix[1])
}

// Another scope's notes must never appear — team_notes carries scope_id and
// this query must filter on it.
func (s *explorerNoteMixSuite) TestNoteMixIsScopeIsolated() {
	ctx := context.Background()
	at := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	s.insertNoteInScope(ctx, "other-scope", "x1", "decision", "active", at.Add(24*time.Hour))

	mix, err := s.explorer.NoteMix(ctx, at)
	s.Require().NoError(err)
	s.Empty(mix)
}

// Only ACTIVE notes whose hard expiry falls inside the lookahead window
// count: already-expired, resolved, beyond-window, and other-scope notes
// must all be excluded — otherwise the Overview tile inflates.
func (s *explorerNoteMixSuite) TestCountExpiringNotesCountsOnlyActiveInWindowSameScope() {
	ctx := context.Background()
	at := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)

	s.insertNote(ctx, "x1", "decision", "active", at.Add(2*time.Hour)) // counted
	s.insertNote(ctx, "x2", "blocker", "active", at.Add(23*time.Hour)) // counted
	s.insertNote(ctx, "x3", "handoff", "active", at.Add(-time.Hour))   // already hard-expired
	s.insertNote(ctx, "x4", "decision", "resolved", at.Add(2*time.Hour))
	s.insertNote(ctx, "x5", "decision", "active", at.Add(48*time.Hour)) // beyond window
	s.insertNoteInScope(ctx, "other-scope", "x6", "decision", "active", at.Add(2*time.Hour))

	count, err := s.explorer.CountExpiringNotes(ctx, at, 24*time.Hour)
	s.Require().NoError(err)
	s.Equal(int64(2), count)
}

func (s *explorerNoteMixSuite) insertNote(
	ctx context.Context,
	noteID string,
	kind string,
	state string,
	hardExpiresAt time.Time,
) {
	s.insertNoteInScope(ctx, s.scope, noteID, kind, state, hardExpiresAt)
}

func (s *explorerNoteMixSuite) insertNoteInScope(
	ctx context.Context,
	scopeID string,
	noteID string,
	kind string,
	state string,
	hardExpiresAt time.Time,
) {
	s.T().Helper()
	now := time.Now().UTC()
	_, err := s.store.Pool().Exec(ctx, `
INSERT INTO team_notes (
    scope_id, note_id, note_key, kind, subject, body,
    origin_user_id, origin_agent_id, origin_session_id,
    state, current_revision, soft_expires_at, hard_expires_at,
    created_at, updated_at
) VALUES (
    $1, $2, $2, $3, $4, $5,
    'user-1', 'agent-1', 'session-1',
    $6, 1, $7, $7,
    $8, $8
)`,
		scopeID, noteID, kind, "subject for "+noteID, "body for "+noteID,
		state, hardExpiresAt, now,
	)
	s.Require().NoError(err)
}
