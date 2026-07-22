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
