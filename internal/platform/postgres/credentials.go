package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
)

type CredentialStore struct {
	pool *pgxpool.Pool
}

func (s *CredentialStore) SaveEnrollment(ctx context.Context, record onprem.EnrollmentRecord) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO agent_enrollments (
			enrollment_id, token_digest, user_id, agent_id, permissions, created_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, record.ID, record.TokenDigest[:], record.UserID, record.AgentID, permissionStrings(record.Permissions),
		record.CreatedAt, record.ExpiresAt)
	if err != nil {
		return fmt.Errorf("save postgres agent enrollment: %w", err)
	}
	return nil
}

func (s *CredentialStore) ExchangeEnrollment(
	ctx context.Context,
	digest onprem.Digest,
	credential onprem.CredentialRecord,
	now time.Time,
) (enrollment onprem.EnrollmentRecord, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.EnrollmentRecord{}, fmt.Errorf("begin enrollment exchange: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(context.Background()); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("rollback enrollment exchange: %w", rollbackErr))
		}
	}()
	var permissions []string
	err = tx.QueryRow(ctx, `
		UPDATE agent_enrollments
		SET consumed_at = $2
		WHERE token_digest = $1 AND consumed_at IS NULL AND expires_at > $2
		RETURNING enrollment_id, user_id, agent_id, permissions, created_at, expires_at, consumed_at
	`, digest[:], now).Scan(
		&enrollment.ID, &enrollment.UserID, &enrollment.AgentID, &permissions,
		&enrollment.CreatedAt, &enrollment.ExpiresAt, &enrollment.ConsumedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.EnrollmentRecord{}, onprem.ErrEnrollmentInvalid
	}
	if err != nil {
		return onprem.EnrollmentRecord{}, fmt.Errorf("consume postgres agent enrollment: %w", err)
	}
	enrollment.TokenDigest = digest
	enrollment.Permissions = permissionsFromStrings(permissions)
	credential.UserID = enrollment.UserID
	credential.AgentID = enrollment.AgentID
	credential.Permissions = enrollment.Permissions
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_credentials (
			credential_id, key_digest, user_id, agent_id, permissions, created_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, credential.ID, credential.KeyDigest[:], credential.UserID, credential.AgentID,
		permissionStrings(credential.Permissions), credential.CreatedAt, credential.ExpiresAt); err != nil {
		return onprem.EnrollmentRecord{}, fmt.Errorf("save exchanged agent credential: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.EnrollmentRecord{}, fmt.Errorf("commit enrollment exchange: %w", err)
	}
	return enrollment, nil
}

func (s *CredentialStore) ResolveCredential(
	ctx context.Context,
	digest onprem.Digest,
	now time.Time,
) (record onprem.CredentialRecord, err error) {
	var permissions []string
	var storedDigest []byte
	err = s.pool.QueryRow(ctx, `
		UPDATE agent_credentials
		SET last_used_at = $2
		WHERE key_digest = $1
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > $2)
		RETURNING credential_id, key_digest, user_id, agent_id, permissions,
		          created_at, expires_at, revoked_at, last_used_at
	`, digest[:], now).Scan(
		&record.ID, &storedDigest, &record.UserID, &record.AgentID, &permissions,
		&record.CreatedAt, &record.ExpiresAt, &record.RevokedAt, &record.LastUsedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.CredentialRecord{}, onprem.ErrUnauthorized
	}
	if err != nil {
		return onprem.CredentialRecord{}, fmt.Errorf("resolve postgres agent credential: %w", err)
	}
	copy(record.KeyDigest[:], storedDigest)
	record.Permissions = permissionsFromStrings(permissions)
	return record, nil
}

func (s *CredentialStore) RotateCredential(
	ctx context.Context,
	currentID string,
	replacement onprem.CredentialRecord,
	overlapUntil time.Time,
) (returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin credential rotation: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(context.Background()); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("rollback credential rotation: %w", rollbackErr))
		}
	}()
	command, err := tx.Exec(ctx, `
		UPDATE agent_credentials
		SET expires_at = CASE
			WHEN expires_at IS NULL OR expires_at > $2 THEN $2
			ELSE expires_at
		END
		WHERE credential_id = $1 AND revoked_at IS NULL
	`, currentID, overlapUntil)
	if err != nil {
		return fmt.Errorf("expire rotated agent credential: %w", err)
	}
	if command.RowsAffected() != 1 {
		return onprem.ErrUnauthorized
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_credentials (
			credential_id, key_digest, user_id, agent_id, permissions, created_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, replacement.ID, replacement.KeyDigest[:], replacement.UserID, replacement.AgentID,
		permissionStrings(replacement.Permissions), replacement.CreatedAt, replacement.ExpiresAt); err != nil {
		return fmt.Errorf("save rotated agent credential: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit credential rotation: %w", err)
	}
	return nil
}

func (s *CredentialStore) RevokeCredential(ctx context.Context, credentialID string, now time.Time) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE agent_credentials
		SET revoked_at = $2
		WHERE credential_id = $1 AND revoked_at IS NULL
	`, credentialID, now)
	if err != nil {
		return fmt.Errorf("revoke postgres agent credential: %w", err)
	}
	if command.RowsAffected() != 1 {
		return onprem.ErrCredentialNotFound
	}
	return nil
}

func permissionStrings(permissions []onprem.Permission) []string {
	result := make([]string, len(permissions))
	for index, permission := range permissions {
		result[index] = string(permission)
	}
	return result
}

func permissionsFromStrings(permissions []string) []onprem.Permission {
	result := make([]onprem.Permission, len(permissions))
	for index, permission := range permissions {
		result[index] = onprem.Permission(permission)
	}
	return result
}
