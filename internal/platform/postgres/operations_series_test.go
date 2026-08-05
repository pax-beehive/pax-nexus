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
// the last bucket — the half-open interval must match Summary's.
func (s *operationsSeriesSuite) TestSeriesExcludesTheUpperBound() {
	ctx := context.Background()
	base := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	s.recordObservation(ctx, base.Add(time.Hour), 5)

	buckets, err := s.operations.Series(
		ctx,
		operations.TimeFilter{From: base, To: base.Add(time.Hour)},
		10*time.Minute,
	)
	s.Require().NoError(err)
	s.Require().Len(buckets, 6)
	for _, bucket := range buckets {
		s.Equal(int64(0), bucket.Evidence)
	}
}

func (s *operationsSeriesSuite) recordObservation(
	ctx context.Context, at time.Time, accepted int64,
) {
	s.T().Helper()
	attempt, err := operations.NewAttemptID()
	s.Require().NoError(err)
	_, err = s.operations.Record(ctx, operations.Event{
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

func (s *operationsSeriesSuite) recordRecall(ctx context.Context, at time.Time) {
	s.T().Helper()
	attempt, err := operations.NewAttemptID()
	s.Require().NoError(err)
	_, err = s.operations.Record(ctx, operations.Event{
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
