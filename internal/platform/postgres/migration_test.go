package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/suite"
)

type migrationSuite struct {
	suite.Suite
	dsn  string
	pool *pgxpool.Pool
}

func TestMigrationSuite(t *testing.T) {
	suite.Run(t, new(migrationSuite))
}

func (s *migrationSuite) SetupSuite() {
	s.dsn = os.Getenv("TEAM_MEMORY_TEST_POSTGRES_DSN")
	if s.dsn == "" {
		s.T().Skip("TEAM_MEMORY_TEST_POSTGRES_DSN is not set")
	}

	pool, err := pgxpool.New(context.Background(), s.dsn)
	s.Require().NoError(err)
	s.Require().NoError(pool.Ping(context.Background()))
	s.pool = pool
}

func (s *migrationSuite) TearDownSuite() {
	if s.pool != nil {
		s.pool.Close()
	}
}

func (s *migrationSuite) TestConcurrentMigrationsAreSerialized() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	schema := fmt.Sprintf("migration_concurrency_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	_, err := s.pool.Exec(ctx, "CREATE SCHEMA "+quotedSchema)
	s.Require().NoError(err)
	s.T().Cleanup(func() {
		_, cleanupErr := s.pool.Exec(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
		s.NoError(cleanupErr)
	})

	stores := make([]*Store, 0, 2)
	for range 2 {
		config, configErr := pgxpool.ParseConfig(s.dsn)
		s.Require().NoError(configErr)
		config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
		pool, poolErr := pgxpool.NewWithConfig(ctx, config)
		s.Require().NoError(poolErr)
		s.T().Cleanup(pool.Close)
		stores = append(stores, newStore(pool))
	}

	start := make(chan struct{})
	results := make(chan error, len(stores))
	for _, store := range stores {
		go func(current *Store) {
			<-start
			results <- current.Migrate(ctx)
		}(store)
	}
	close(start)

	for range stores {
		s.NoError(<-results)
	}
}

func (s *migrationSuite) TestEvidenceStreamSchema() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	schema := fmt.Sprintf("evidence_schema_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	_, err := s.pool.Exec(ctx, "CREATE SCHEMA "+quotedSchema)
	s.Require().NoError(err)
	s.T().Cleanup(func() {
		_, cleanupErr := s.pool.Exec(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
		s.NoError(cleanupErr)
	})

	config, err := pgxpool.ParseConfig(s.dsn)
	s.Require().NoError(err)
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	s.Require().NoError(err)
	s.T().Cleanup(pool.Close)
	s.Require().NoError(newStore(pool).Migrate(ctx))

	for _, column := range []string{"source", "stream_id", "kind", "author_kind", "author_native_id", "author_user_id", "media"} {
		var found bool
		err := pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = $1 AND table_name = 'session_events' AND column_name = $2)`,
			schema, column).Scan(&found)
		s.Require().NoError(err)
		s.True(found, "session_events.%s missing", column)
	}

	var streamPK string
	err = pool.QueryRow(ctx, `
SELECT string_agg(a.attname, ',' ORDER BY array_position(i.indkey, a.attnum))
FROM pg_index i
JOIN pg_class c ON c.oid = i.indrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = ANY (i.indkey)
WHERE n.nspname = $1 AND c.relname = 'session_streams' AND i.indisprimary`, schema).Scan(&streamPK)
	s.Require().NoError(err)
	s.Equal("scope_id,source,stream_id", streamPK)

	var uniqueExists bool
	err = pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = $1 AND tablename = 'session_events'
      AND indexname = 'session_events_stream_sequence_key')`, schema).Scan(&uniqueExists)
	s.Require().NoError(err)
	s.True(uniqueExists)
}
