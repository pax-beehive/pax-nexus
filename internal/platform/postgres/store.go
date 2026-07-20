package postgres

import (
	"context"
	"embed"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/001_init.sql migrations/002_temporal_notes.sql migrations/003_note_relations.sql migrations/004_extraction_latency.sql migrations/005_note_embeddings.sql migrations/006_note_identity.sql migrations/007_extraction_run_actor.sql migrations/008_extraction_run_candidates.sql migrations/009_extraction_run_result.sql migrations/010_note_identity_ref.sql migrations/011_recall_observations.sql migrations/012_extraction_episodes.sql migrations/013_recall_trace.sql migrations/014_recall_hint_deliveries.sql
var migrations embed.FS

type Store struct {
	pool     *pgxpool.Pool
	sessions *SessionRepository
	episodes *EpisodeStore
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("open postgres store: empty DSN")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return newStore(pool), nil
}

func newStore(pool *pgxpool.Pool) *Store {
	return &Store{
		pool:     pool,
		sessions: &SessionRepository{pool: pool},
		episodes: &EpisodeStore{pool: pool},
	}
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

func (s *Store) Sessions() *SessionRepository {
	return s.sessions
}

func (s *Store) Episodes() *EpisodeStore {
	return s.episodes
}

func (s *Store) Migrate(ctx context.Context) error {
	for _, path := range []string{
		"migrations/001_init.sql",
		"migrations/002_temporal_notes.sql",
		"migrations/003_note_relations.sql",
		"migrations/004_extraction_latency.sql",
		"migrations/005_note_embeddings.sql",
		"migrations/006_note_identity.sql",
		"migrations/007_extraction_run_actor.sql",
		"migrations/008_extraction_run_candidates.sql",
		"migrations/009_extraction_run_result.sql",
		"migrations/010_note_identity_ref.sql",
		"migrations/011_recall_observations.sql",
		"migrations/012_extraction_episodes.sql",
		"migrations/013_recall_trace.sql",
		"migrations/014_recall_hint_deliveries.sql",
	} {
		migration, err := migrations.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read postgres migration %q: %w", path, err)
		}
		if _, err := s.pool.Exec(ctx, string(migration)); err != nil {
			return fmt.Errorf("apply postgres migration %q: %w", path, err)
		}
	}
	return nil
}
