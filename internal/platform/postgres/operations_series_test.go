package postgres_test

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/operations"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/stretchr/testify/suite"
)

type operationsSeriesSuite struct {
	suite.Suite
	store      *postgres.Store
	operations *postgres.OperationsStore
	scope      string
	adminPool  *pgxpool.Pool
	schema     string
}

func TestOperationsSeriesSuite(t *testing.T) {
	suite.Run(t, new(operationsSeriesSuite))
}

func (s *operationsSeriesSuite) SetupSuite() {
	ctx := context.Background()
	dsn := testDSN(s.T())
	adminPool, err := pgxpool.New(ctx, dsn)
	s.Require().NoError(err)
	s.adminPool = adminPool
	s.schema = fmt.Sprintf("opseries_%d", time.Now().UnixNano())
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
	s.scope = "series-suite-scope"
	s.operations = store.Operations(s.scope)
}

func (s *operationsSeriesSuite) TearDownSuite() {
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

// The series must be gap-free: a window with data only in the first and last
// bucket still returns one row per bucket, so the frontend can plot it without
// reconstructing missing time slots.
func (s *operationsSeriesSuite) TestSeriesReturnsEveryBucketIncludingEmptyOnes() {
	ctx := context.Background()
	base := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)

	s.recordObservation(ctx, base.Add(1*time.Minute), 7)
	s.recordRecall(ctx, base.Add(1*time.Minute))
	s.recordObservation(ctx, base.Add(52*time.Minute), 3)

	filter := operations.TimeFilter{From: base, To: base.Add(time.Hour)}
	buckets, err := s.operations.Series(ctx, filter, 10*time.Minute)
	s.Require().NoError(err)
	s.Require().Len(buckets, 6)

	for i, bucket := range buckets {
		s.Equal(base.Add(time.Duration(i)*10*time.Minute), bucket.BucketAt.UTC())
	}
	s.Equal(int64(7), buckets[0].Evidence)
	s.Equal(int64(1), buckets[0].Recalls)
	s.Equal(int64(0), buckets[1].Evidence)
	s.Equal(int64(0), buckets[1].Recalls)
	s.Equal(int64(3), buckets[5].Evidence)
}

// A row exactly on the window's upper bound belongs to the next window, not to
// the last bucket — the half-open interval must match Summary's. The window is
// deliberately NOT bucket-aligned (To = base+55m with a 10m bucket) so the
// boundary instant floors onto an EXISTING grid point (base+50m): with a
// broken `<= $2` the excluded row's items would leak into that bucket's
// total. A second, in-window row in the same bucket (1 second before the
// bound) proves the bucket itself still works, distinguishing "boundary
// correctly excluded" from "bucket is broken and reads zero no matter what".
func (s *operationsSeriesSuite) TestSeriesExcludesTheUpperBound() {
	ctx := context.Background()
	// A distinct base from the other event-recording test in this suite:
	// events are shared state across the suite's tests (no per-test cleanup),
	// so overlapping windows on the same agent would cross-contaminate counts.
	base := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	upperBound := base.Add(55 * time.Minute)

	s.recordObservation(ctx, upperBound, 9)                     // == To: must be excluded
	s.recordObservation(ctx, upperBound.Add(-1*time.Second), 5) // 1s before To: same bucket, must count

	buckets, err := s.operations.Series(
		ctx,
		operations.TimeFilter{From: base, To: upperBound},
		10*time.Minute,
	)
	s.Require().NoError(err)
	s.Require().Len(buckets, 6)
	s.Equal(base.Add(50*time.Minute), buckets[5].BucketAt.UTC())
	s.Equal(int64(5), buckets[5].Evidence)
	for i := 0; i < 5; i++ {
		s.Equal(int64(0), buckets[i].Evidence, "bucket %d", i)
	}
}

// The facts half of the query is the ONLY scope-isolated half — it has the
// real JOIN, the real scope_id filter, and its own grouping. A second note
// revision in a different scope at the same instant proves the isolation:
// if the scope filter or the join condition were wrong, the other scope's
// revision would leak into this store's bucket.
func (s *operationsSeriesSuite) TestSeriesCountsFactsOnlyForItsOwnScope() {
	ctx := context.Background()
	base := time.Date(2026, 8, 5, 14, 0, 0, 0, time.UTC)
	at := base.Add(5 * time.Minute)

	s.insertNoteRevision(s.scope, "run-series-own", "candidate-series-own", "note-series-own", at)
	s.insertNoteRevision("series-suite-other-scope", "run-series-other", "candidate-series-other", "note-series-other", at)

	filter := operations.TimeFilter{From: base, To: base.Add(time.Hour)}
	buckets, err := s.operations.Series(ctx, filter, 10*time.Minute)
	s.Require().NoError(err)
	s.Require().Len(buckets, 6)

	s.Equal(int64(1), buckets[0].Facts)
	for i := 1; i < len(buckets); i++ {
		s.Equal(int64(0), buckets[i].Facts, "bucket %d", i)
	}
}

// The events half of the query (Evidence/Recalls) must be scope-isolated the
// same way facts already is. A foreign-scope observation must not leak into
// this store's Evidence count. Window chosen as 16:00-17:00 UTC on the same
// date, non-overlapping with the other three tests in this suite (10:00-11:00,
// 12:00-12:55, 14:00-15:00) since events here have no per-test cleanup.
func (s *operationsSeriesSuite) TestSeriesCountsEventsOnlyForItsOwnScope() {
	ctx := context.Background()
	base := time.Date(2026, 8, 5, 16, 0, 0, 0, time.UTC)
	at := base.Add(5 * time.Minute)

	s.recordObservationForScope(ctx, s.scope, at, 4)
	s.recordObservationForScope(ctx, "series-suite-foreign-events-scope", at, 11)

	filter := operations.TimeFilter{From: base, To: base.Add(time.Hour)}
	buckets, err := s.operations.Series(ctx, filter, 10*time.Minute)
	s.Require().NoError(err)
	s.Require().Len(buckets, 6)

	s.Equal(int64(4), buckets[0].Evidence)
	for i := 1; i < len(buckets); i++ {
		s.Equal(int64(0), buckets[i].Evidence, "bucket %d", i)
	}
}

func (s *operationsSeriesSuite) recordObservation(
	ctx context.Context, at time.Time, accepted int64,
) {
	s.T().Helper()
	s.recordObservationForScope(ctx, s.scope, at, accepted)
}

func (s *operationsSeriesSuite) recordObservationForScope(
	ctx context.Context, scope string, at time.Time, accepted int64,
) {
	s.T().Helper()
	attempt, err := operations.NewAttemptID()
	s.Require().NoError(err)
	_, err = s.operations.Record(ctx, operations.Event{
		ScopeID:       scope,
		AttemptID:     attempt,
		Kind:          operations.KindObservationObserve,
		Outcome:       operations.OutcomeSucceeded,
		Actor:         operations.Actor{Kind: "agent", AgentID: "series-agent"},
		StartedAt:     at,
		CompletedAt:   at.Add(20 * time.Millisecond),
		DurationMS:    20,
		InputItems:    accepted,
		AcceptedItems: accepted,
	})
	s.Require().NoError(err)
}

// insertNoteRevision writes the minimal chain note_revisions requires:
// extraction_runs -> note_candidates -> team_notes, then the revision itself,
// so a test can exercise the facts CTE's join and scope_id filter directly.
func (s *operationsSeriesSuite) insertNoteRevision(
	scope, runID, candidateID, noteID string, at time.Time,
) {
	s.T().Helper()
	ctx := context.Background()
	_, err := s.store.Pool().Exec(ctx, `
INSERT INTO extraction_runs (
    scope_id, run_id, agent_id, session_id, from_sequence, to_sequence,
    input_checksum, status, completed_at
) VALUES ($1, $2, 'series-agent', 'series-session', 1, 1, $3, 'completed', $4)`,
		scope, runID, "checksum-"+runID, at)
	s.Require().NoError(err)
	_, err = s.store.Pool().Exec(ctx, `
INSERT INTO note_candidates (
    scope_id, candidate_id, run_id, action, kind, subject, body,
    origin_user_id, origin_agent_id, origin_session_id, evidence_event_ids,
    admission_status
) VALUES ($1, $2, $3, 'create', 'fact', 'series subject', 'series body',
          'series-user', 'series-agent', 'series-session', ARRAY[]::TEXT[], 'admitted')`,
		scope, candidateID, runID)
	s.Require().NoError(err)
	_, err = s.store.Pool().Exec(ctx, `
INSERT INTO team_notes (
    scope_id, note_id, note_key, kind, subject, body,
    origin_user_id, origin_agent_id, origin_session_id,
    state, current_revision, soft_expires_at, hard_expires_at, created_at, updated_at
) VALUES ($1, $2, $3, 'fact', 'series subject', 'series body',
          'series-user', 'series-agent', 'series-session',
          'active', 1, $4, $5, $6, $6)`,
		scope, noteID, "key-"+noteID, at.Add(24*time.Hour), at.Add(30*24*time.Hour), at)
	s.Require().NoError(err)
	_, err = s.store.Pool().Exec(ctx, `
INSERT INTO note_revisions (
    scope_id, note_id, revision, candidate_id, operation, body, created_at
) VALUES ($1, $2, 1, $3, 'create', 'series body', $4)`,
		scope, noteID, candidateID, at)
	s.Require().NoError(err)
}

func (s *operationsSeriesSuite) recordRecall(ctx context.Context, at time.Time) {
	s.T().Helper()
	attempt, err := operations.NewAttemptID()
	s.Require().NoError(err)
	_, err = s.operations.Record(ctx, operations.Event{
		ScopeID:     s.scope,
		AttemptID:   attempt,
		Kind:        operations.KindMemorySearch,
		Outcome:     operations.OutcomeSucceeded,
		Actor:       operations.Actor{Kind: "agent", AgentID: "series-agent"},
		StartedAt:   at,
		CompletedAt: at.Add(30 * time.Millisecond),
		DurationMS:  30,
		ResultItems: 2,
	})
	s.Require().NoError(err)
}
