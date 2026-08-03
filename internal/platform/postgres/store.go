package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/001_init.sql migrations/002_temporal_notes.sql migrations/003_note_relations.sql migrations/004_extraction_latency.sql migrations/005_note_embeddings.sql migrations/006_note_identity.sql migrations/007_extraction_run_actor.sql migrations/008_extraction_run_candidates.sql migrations/009_extraction_run_result.sql migrations/010_note_identity_ref.sql migrations/011_recall_observations.sql migrations/012_extraction_episodes.sql migrations/013_recall_trace.sql migrations/014_recall_hint_deliveries.sql migrations/015_onprem_credentials.sql migrations/016_onprem_channel_envelopes.sql migrations/017_onprem_identity_registry.sql migrations/018_onprem_operations.sql migrations/019_onprem_device_provisioning.sql migrations/020_pagewiki_session_consumer.sql migrations/021_evidence_streams.sql migrations/022_pagewiki_topic_trees.sql migrations/023_todoapp.sql migrations/024_pagewiki_generation_settings.sql migrations/025_llm_usage_events.sql migrations/026_pagewiki_curation.sql migrations/027_pagewiki_type_registry.sql migrations/028_session_audit.sql
var migrations embed.FS

const migrationAdvisoryLockName = "pax-nexus.platform-postgres.migrate"

// schemaAdvisoryLockName guards every schema change a deployment applies,
// not just this package's. Store.Migrate serializes itself with
// migrationAdvisoryLockName, but the extraction queue's River migrations
// take no lock of their own, so two processes migrating at once can deadlock
// with one holding this package's tables while the other holds River's.
// Callers that apply both wrap them in WithSchemaLock.
//
// The key differs from migrationAdvisoryLockName on purpose: the outer lock
// is session-scoped and held on its own connection, so sharing a key with
// the inner transaction-scoped lock would make Store.Migrate block on a lock
// its own caller already holds.
const schemaAdvisoryLockName = "pax-nexus.platform-postgres.schema"

type Store struct {
	pool           *pgxpool.Pool
	operationsPool *pgxpool.Pool
	sessions       *SessionRepository
	episodes       *EpisodeStore
	credentials    *CredentialStore
	channel        *ChannelStore
	identity       *IdentityStore
	registry       *RegistryStore
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
	operationsConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("parse operations postgres pool config: %w", err)
	}
	operationsConfig.MaxConns = 1
	operationsConfig.MinConns = 0
	operationsConfig.ConnConfig.RuntimeParams["application_name"] = "team-memory-operations-collector"
	operationsConfig.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	operationsConfig.ConnConfig.RuntimeParams["statement_timeout"] = "1000"
	operationsConfig.ConnConfig.RuntimeParams["lock_timeout"] = "250"
	operationsPool, err := pgxpool.NewWithConfig(ctx, operationsConfig)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("open operations postgres pool: %w", err)
	}
	if err := operationsPool.Ping(ctx); err != nil {
		operationsPool.Close()
		pool.Close()
		return nil, fmt.Errorf("ping operations postgres pool: %w", err)
	}
	store := newStore(pool)
	store.operationsPool = operationsPool
	return store, nil
}

func newStore(pool *pgxpool.Pool) *Store {
	return &Store{
		pool:        pool,
		sessions:    &SessionRepository{pool: pool},
		episodes:    &EpisodeStore{pool: pool},
		credentials: &CredentialStore{pool: pool},
		channel:     &ChannelStore{pool: pool},
		identity:    &IdentityStore{pool: pool},
		registry:    &RegistryStore{pool: pool},
	}
}

func (s *Store) Close() {
	if s.operationsPool != nil {
		s.operationsPool.Close()
	}
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

func (s *Store) Credentials() *CredentialStore {
	return s.credentials
}

func (s *Store) Channel() *ChannelStore {
	return s.channel
}

func (s *Store) Identity() *IdentityStore {
	return s.identity
}

func (s *Store) Registry() *RegistryStore {
	return s.registry
}

// Operations returns an operations read-model store bound to the given
// scope, mirroring Explorer. Queries against scoped tables (team_notes,
// extraction_runs, session_events, team_note_recall_observations) filter on
// this scope; the onprem_* admin tables stay unscoped because they belong to
// the on-prem admin domain and carry no scope column.
func (s *Store) Operations(scopeID string) *OperationsStore {
	return &OperationsStore{pool: s.pool, readPool: s.operationsPool, scopeID: scopeID}
}

// Explorer returns a read-model store answering explorer queries for the
// given scope. The scope comes from the deployment wiring per call rather
// than being fixed at construction, so one process can serve any scope.
func (s *Store) Explorer(scopeID string) *ExplorerStore {
	return &ExplorerStore{pool: s.pool, scopeID: scopeID}
}

// likeEscaper escapes the LIKE/ILIKE metacharacters so user-supplied search
// text always matches literally under PostgreSQL's default backslash escape.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// escapeLike prepares a user-supplied search fragment for interpolation into
// an ILIKE pattern such as '%' || $n || '%'.
func escapeLike(value string) string {
	return likeEscaper.Replace(value)
}

// WithSchemaLock runs apply while holding a session-scoped advisory lock
// that covers every schema change a deployment applies, so concurrent
// migrators serialize instead of deadlocking against each other. Use it
// whenever more than one migration source runs together (this package's
// schema plus the extraction queue's River schema); Store.Migrate on its own
// is already serialized by its transaction-scoped lock.
//
// The lock is held on a dedicated connection for the duration of apply and
// is always released, including when apply panics.
func (s *Store) WithSchemaLock(ctx context.Context, apply func(context.Context) error) (resultErr error) {
	connection, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire postgres schema lock connection: %w", err)
	}
	defer connection.Release()
	if _, err := connection.Exec(
		ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, schemaAdvisoryLockName,
	); err != nil {
		return fmt.Errorf("acquire postgres schema lock: %w", err)
	}
	defer func() {
		// Release on the same connection that took the lock, and outside the
		// caller's context so a cancelled migration still unlocks.
		if _, err := connection.Exec(
			context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, schemaAdvisoryLockName,
		); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("release postgres schema lock: %w", err))
		}
	}()
	return apply(ctx)
}

func (s *Store) Migrate(ctx context.Context) (resultErr error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin postgres migration transaction: %w", err)
	}
	defer func() {
		rollbackErr := tx.Rollback(context.WithoutCancel(ctx))
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			resultErr = errors.Join(resultErr, fmt.Errorf("rollback postgres migration transaction: %w", rollbackErr))
		}
	}()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, migrationAdvisoryLockName); err != nil {
		return fmt.Errorf("acquire postgres migration lock: %w", err)
	}

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
		"migrations/015_onprem_credentials.sql",
		"migrations/016_onprem_channel_envelopes.sql",
		"migrations/017_onprem_identity_registry.sql",
		"migrations/018_onprem_operations.sql",
		"migrations/019_onprem_device_provisioning.sql",
		"migrations/020_pagewiki_session_consumer.sql",
		"migrations/021_evidence_streams.sql",
		"migrations/022_pagewiki_topic_trees.sql",
		"migrations/023_todoapp.sql",
		"migrations/024_pagewiki_generation_settings.sql",
		"migrations/025_llm_usage_events.sql",
		"migrations/026_pagewiki_curation.sql",
		"migrations/027_pagewiki_type_registry.sql",
		"migrations/028_session_audit.sql",
	} {
		migration, err := migrations.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read postgres migration %q: %w", path, err)
		}
		if _, err := tx.Exec(ctx, string(migration)); err != nil {
			return fmt.Errorf("apply postgres migration %q: %w", path, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit postgres migrations: %w", err)
	}
	return nil
}
