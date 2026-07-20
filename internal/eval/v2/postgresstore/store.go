// Package postgresstore persists resumable Eval v2 runs in PostgreSQL.
package postgresstore

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	v2 "github.com/pax-beehive/pax-nexus/internal/eval/v2"
)

//go:embed migrations/001_eval_v2.sql
var migrations embed.FS

type Store struct {
	pool  *pgxpool.Pool
	mu    sync.Mutex
	locks map[string]*pgxpool.Conn
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("open eval postgres store: DSN is required")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open eval postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping eval postgres: %w", err)
	}
	return &Store{pool: pool, locks: make(map[string]*pgxpool.Conn)}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Acquire(ctx context.Context, runID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.locks[runID]; exists {
		return false, nil
	}
	connection, err := s.pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("acquire eval lock connection: %w", err)
	}
	var acquired bool
	if err := connection.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, runID).Scan(&acquired); err != nil {
		connection.Release()
		return false, fmt.Errorf("acquire eval advisory lock: %w", err)
	}
	if !acquired {
		connection.Release()
		return false, nil
	}
	s.locks[runID] = connection
	return true, nil
}

func (s *Store) Release(ctx context.Context, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	connection, exists := s.locks[runID]
	if !exists {
		return fmt.Errorf("release eval advisory lock: run %q is not acquired", runID)
	}
	delete(s.locks, runID)
	defer connection.Release()
	var released bool
	if err := connection.QueryRow(ctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, runID).Scan(&released); err != nil {
		return fmt.Errorf("release eval advisory lock: %w", err)
	}
	if !released {
		return fmt.Errorf("release eval advisory lock: database lock was not held")
	}
	return nil
}

func (s *Store) Migrate(ctx context.Context) error {
	migration, err := migrations.ReadFile("migrations/001_eval_v2.sql")
	if err != nil {
		return fmt.Errorf("read eval postgres migration: %w", err)
	}
	if _, err := s.pool.Exec(ctx, string(migration)); err != nil {
		return fmt.Errorf("apply eval postgres migration: %w", err)
	}
	return nil
}

func (s *Store) Initialize(ctx context.Context, run v2.RunRecord, trials []v2.TrialKey) (returnedErr error) {
	config, err := json.Marshal(struct {
		Config  v2.Config         `json:"config"`
		Runtime map[string]string `json:"runtime,omitempty"`
	}{Config: run.Config, Runtime: run.Runtime})
	if err != nil {
		return fmt.Errorf("marshal eval run config: %w", err)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin eval initialization: %w", err)
	}
	defer func() {
		rollbackErr := tx.Rollback(context.Background())
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("rollback eval initialization: %w", rollbackErr))
		}
	}()
	if _, err := tx.Exec(ctx, `
INSERT INTO eval_v2_runs (run_id, dataset, dataset_revision, config_hash, config)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (run_id) DO NOTHING`, run.ID, run.Dataset, run.DatasetRevision, run.ConfigHash, config); err != nil {
		return fmt.Errorf("insert eval run: %w", err)
	}
	var storedHash string
	if err := tx.QueryRow(ctx, `SELECT config_hash FROM eval_v2_runs WHERE run_id = $1`, run.ID).Scan(&storedHash); err != nil {
		return fmt.Errorf("read eval run config hash: %w", err)
	}
	if storedHash != run.ConfigHash {
		return fmt.Errorf("initialize eval run: run ID %q already uses a different config", run.ID)
	}
	for _, trial := range trials {
		if _, err := tx.Exec(ctx, `
INSERT INTO eval_v2_trials (run_id, case_id, arm)
VALUES ($1, $2, $3)
ON CONFLICT (run_id, case_id, arm) DO NOTHING`, trial.RunID, trial.CaseID, trial.Arm); err != nil {
			return fmt.Errorf("insert eval trial %s/%s: %w", trial.CaseID, trial.Arm, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit eval initialization: %w", err)
	}
	return nil
}

func (s *Store) ResetRunning(ctx context.Context, runID string) (returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin reset running eval trials: %w", err)
	}
	defer func() {
		rollbackErr := tx.Rollback(context.Background())
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("rollback reset running eval trials: %w", rollbackErr))
		}
	}()
	if _, err := tx.Exec(ctx, `
UPDATE eval_v2_trial_attempts
SET status = 'interrupted', failure_class = 'interrupted',
    error = 'runner stopped before attempt finalization', completed_at = NOW()
WHERE run_id = $1 AND status = 'running'`, runID); err != nil {
		return fmt.Errorf("interrupt running eval attempts: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE eval_v2_trials
SET status = 'pending', updated_at = NOW()
WHERE run_id = $1 AND status = 'running'`, runID); err != nil {
		return fmt.Errorf("reset running eval trials: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit reset running eval trials: %w", err)
	}
	return nil
}

func (s *Store) HasRunnable(ctx context.Context, runID string, retryFailed bool, maxAttempts int) (bool, error) {
	var runnable bool
	err := s.pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM eval_v2_trials
    WHERE run_id = $1
      AND (status = 'pending' OR ($2 AND status = 'failed' AND attempts < $3))
)`, runID, retryFailed, maxAttempts).Scan(&runnable)
	if err != nil {
		return false, fmt.Errorf("query runnable eval trials: %w", err)
	}
	return runnable, nil
}

func (s *Store) Claim(ctx context.Context, key v2.TrialKey, retryFailed bool, maxAttempts int) (_ v2.TrialAttemptHandle, claimed bool, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return v2.TrialAttemptHandle{}, false, fmt.Errorf("begin claim eval trial: %w", err)
	}
	defer func() {
		rollbackErr := tx.Rollback(context.Background())
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("rollback claim eval trial: %w", rollbackErr))
		}
	}()
	var attempt int
	err = tx.QueryRow(ctx, `
UPDATE eval_v2_trials
SET status = 'running', attempts = attempts + 1, started_at = NOW(), completed_at = NULL,
    result = NULL, updated_at = NOW()
WHERE run_id = $1 AND case_id = $2 AND arm = $3
  AND (status = 'pending' OR ($4 AND status = 'failed' AND attempts < $5))
RETURNING attempts`, key.RunID, key.CaseID, key.Arm, retryFailed, maxAttempts).Scan(&attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v2.TrialAttemptHandle{}, false, nil
	}
	if err != nil {
		return v2.TrialAttemptHandle{}, false, fmt.Errorf("claim eval trial: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO eval_v2_trial_attempts (run_id, case_id, arm, attempt)
VALUES ($1, $2, $3, $4)`, key.RunID, key.CaseID, key.Arm, attempt); err != nil {
		return v2.TrialAttemptHandle{}, false, fmt.Errorf("insert eval trial attempt: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return v2.TrialAttemptHandle{}, false, fmt.Errorf("commit claim eval trial: %w", err)
	}
	return v2.TrialAttemptHandle{RunID: key.RunID, CaseID: key.CaseID, Arm: key.Arm, Number: attempt}, true, nil
}

func (s *Store) ClaimRejudge(ctx context.Context, key v2.TrialKey) (_ v2.TrialAttemptHandle, claimed bool, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return v2.TrialAttemptHandle{}, false, fmt.Errorf("begin claim eval rejudge: %w", err)
	}
	defer func() {
		rollbackErr := tx.Rollback(context.Background())
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("rollback claim eval rejudge: %w", rollbackErr))
		}
	}()
	var attempt int
	err = tx.QueryRow(ctx, `
UPDATE eval_v2_trials
SET status = 'running', attempts = attempts + 1, started_at = NOW(), completed_at = NULL,
    updated_at = NOW()
WHERE run_id = $1 AND case_id = $2 AND arm = $3 AND status = 'completed'
  AND COALESCE((result->>'judged')::boolean, FALSE) = FALSE
RETURNING attempts`, key.RunID, key.CaseID, key.Arm).Scan(&attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v2.TrialAttemptHandle{}, false, nil
	}
	if err != nil {
		return v2.TrialAttemptHandle{}, false, fmt.Errorf("claim eval rejudge: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO eval_v2_trial_attempts (run_id, case_id, arm, attempt)
VALUES ($1, $2, $3, $4)`, key.RunID, key.CaseID, key.Arm, attempt); err != nil {
		return v2.TrialAttemptHandle{}, false, fmt.Errorf("insert eval rejudge attempt: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return v2.TrialAttemptHandle{}, false, fmt.Errorf("commit claim eval rejudge: %w", err)
	}
	return v2.TrialAttemptHandle{RunID: key.RunID, CaseID: key.CaseID, Arm: key.Arm, Number: attempt}, true, nil
}

func (s *Store) UpdateAttempt(ctx context.Context, handle v2.TrialAttemptHandle, stage v2.TrialStage, artifactRefs map[string]string) error {
	encoded, err := json.Marshal(artifactRefs)
	if err != nil {
		return fmt.Errorf("marshal eval attempt artifact references: %w", err)
	}
	command, err := s.pool.Exec(ctx, `
UPDATE eval_v2_trial_attempts
SET stage = $5, artifact_refs = artifact_refs || $6::jsonb
WHERE run_id = $1 AND case_id = $2 AND arm = $3 AND attempt = $4 AND status = 'running'`,
		handle.RunID, handle.CaseID, handle.Arm, handle.Number, stage, encoded)
	if err != nil {
		return fmt.Errorf("update eval trial attempt: %w", err)
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("update eval trial attempt: attempt is not running")
	}
	return nil
}

func (s *Store) Complete(ctx context.Context, handle v2.TrialAttemptHandle, result v2.TrialResult) error {
	return s.storeResult(ctx, handle, result, "completed")
}

func (s *Store) Fail(ctx context.Context, handle v2.TrialAttemptHandle, result v2.TrialResult) error {
	return s.storeResult(ctx, handle, result, "failed")
}

func (s *Store) Attempts(ctx context.Context, runID string) ([]v2.TrialAttempt, error) {
	rows, err := s.pool.Query(ctx, `
SELECT case_id, arm, attempt, status, stage, failure_class, error, artifact_refs, started_at, completed_at
FROM eval_v2_trial_attempts
WHERE run_id = $1
ORDER BY case_id, arm, attempt`, runID)
	if err != nil {
		return nil, fmt.Errorf("query eval trial attempts: %w", err)
	}
	defer rows.Close()
	attempts := make([]v2.TrialAttempt, 0)
	for rows.Next() {
		var attempt v2.TrialAttempt
		var encoded []byte
		attempt.RunID = runID
		if err := rows.Scan(
			&attempt.CaseID, &attempt.Arm, &attempt.Number, &attempt.Status, &attempt.Stage,
			&attempt.FailureClass, &attempt.Error, &encoded, &attempt.StartedAt, &attempt.CompletedAt,
		); err != nil {
			return nil, fmt.Errorf("scan eval trial attempt: %w", err)
		}
		if err := json.Unmarshal(encoded, &attempt.ArtifactRefs); err != nil {
			return nil, fmt.Errorf("decode eval trial attempt artifact references: %w", err)
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate eval trial attempts: %w", err)
	}
	return attempts, nil
}

func (s *Store) Results(ctx context.Context, runID string) ([]v2.TrialResult, error) {
	rows, err := s.pool.Query(ctx, `
SELECT result
FROM eval_v2_trials
WHERE run_id = $1 AND status IN ('completed', 'failed')
ORDER BY case_id, arm`, runID)
	if err != nil {
		return nil, fmt.Errorf("query eval results: %w", err)
	}
	defer rows.Close()
	results := make([]v2.TrialResult, 0)
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			return nil, fmt.Errorf("scan eval result: %w", err)
		}
		var result v2.TrialResult
		if err := json.Unmarshal(encoded, &result); err != nil {
			return nil, fmt.Errorf("decode eval result: %w", err)
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate eval results: %w", err)
	}
	return results, nil
}

func (s *Store) Finish(ctx context.Context, runID string) error {
	var unfinished int
	if err := s.pool.QueryRow(ctx, `
SELECT COUNT(*) FROM eval_v2_trials WHERE run_id = $1 AND status IN ('pending', 'running')`, runID).Scan(&unfinished); err != nil {
		return fmt.Errorf("count unfinished eval trials: %w", err)
	}
	if unfinished > 0 {
		return fmt.Errorf("finish eval run: %d trials are unfinished", unfinished)
	}
	_, err := s.pool.Exec(ctx, `
UPDATE eval_v2_runs
SET status = CASE WHEN EXISTS (
        SELECT 1 FROM eval_v2_trials WHERE run_id = $1 AND status = 'failed'
    ) THEN 'completed_with_failures' ELSE 'completed' END,
    completed_at = NOW(), updated_at = NOW()
WHERE run_id = $1`, runID)
	if err != nil {
		return fmt.Errorf("update eval run status: %w", err)
	}
	return nil
}

func (s *Store) storeResult(ctx context.Context, handle v2.TrialAttemptHandle, result v2.TrialResult, status string) (returnedErr error) {
	if handle.RunID != result.RunID || handle.CaseID != result.CaseID || handle.Arm != result.Arm {
		return fmt.Errorf("store eval result: attempt handle and result identity do not match")
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal eval result: %w", err)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin store eval result: %w", err)
	}
	defer func() {
		rollbackErr := tx.Rollback(context.Background())
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("rollback store eval result: %w", rollbackErr))
		}
	}()
	command, err := tx.Exec(ctx, `
UPDATE eval_v2_trials
SET status = $4, result = $5, completed_at = NOW(), updated_at = NOW()
WHERE run_id = $1 AND case_id = $2 AND arm = $3 AND status = 'running'`, handle.RunID, handle.CaseID, handle.Arm, status, encoded)
	if err != nil {
		return fmt.Errorf("store eval result: %w", err)
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("store eval result: trial is not running")
	}
	stage := result.FailureStage
	failureClass := result.FailureClass
	attemptStatus := status
	attemptError := result.Error
	if status == "completed" && stage == "" {
		stage = v2.TrialStageCompleted
		failureClass = ""
	} else if status == "completed" {
		attemptStatus = "failed"
		if attemptError == "" {
			attemptError = result.JudgeError
		}
	}
	if stage == "" {
		stage = v2.TrialStageClaimed
	}
	if status == "failed" && failureClass == "" {
		failureClass = v2.FailureClassUnknown
	}
	command, err = tx.Exec(ctx, `
UPDATE eval_v2_trial_attempts
SET status = $5, stage = $6, failure_class = $7, error = $8, completed_at = NOW()
WHERE run_id = $1 AND case_id = $2 AND arm = $3 AND attempt = $4 AND status = 'running'`,
		handle.RunID, handle.CaseID, handle.Arm, handle.Number, attemptStatus, stage, failureClass, attemptError)
	if err != nil {
		return fmt.Errorf("store eval attempt result: %w", err)
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("store eval attempt result: attempt is not running")
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit store eval result: %w", err)
	}
	return nil
}
