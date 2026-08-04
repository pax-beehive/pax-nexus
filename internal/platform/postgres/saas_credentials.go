package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/deployment/saas"
)

// SaaSCredentialStore is the postgres adapter for team agent enrollments
// and credentials (team_agent_enrollments / team_agent_credentials).
// Authentication resolves the scope from the credential row's team_id
// instead of pinning the on-prem constant.
type SaaSCredentialStore struct {
	pool *pgxpool.Pool
}

var _ saas.TeamCredentialStore = (*SaaSCredentialStore)(nil)

func (s *SaaSCredentialStore) CreateOwnedEnrollment(
	ctx context.Context,
	teamID string,
	membershipID string,
	record onprem.EnrollmentRecord,
) (returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin team owned enrollment creation: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "team owned enrollment creation")
	command, err := tx.Exec(ctx, `
		INSERT INTO team_agent_enrollments (
			enrollment_id, team_id, token_digest, user_id, membership_id, agent_id,
			credential_label, permissions, created_at, expires_at, credential_expires_at, digest_key_version
		)
		SELECT $1, $2, $3, memberships.user_id, memberships.membership_id, agents.agent_id,
		       $6, $7, $8, $9, $10, $11
		FROM team_agents agents
		JOIN team_memberships memberships ON memberships.membership_id = agents.owner_membership_id
		JOIN onprem_users users ON users.user_id = memberships.user_id
		WHERE agents.agent_id = $4 AND agents.team_id = $2 AND memberships.membership_id = $5
		  AND agents.status = 'active' AND memberships.status = 'active'
		  AND users.identity_status = 'active'
	`, record.ID, teamID, record.TokenDigest[:], record.AgentID, membershipID, record.CredentialLabel,
		permissionStrings(record.Permissions), record.CreatedAt, record.ExpiresAt,
		record.CredentialExpiresAt, record.DigestKeyVersion)
	if err != nil {
		return fmt.Errorf("create postgres team owned enrollment: %w", err)
	}
	if command.RowsAffected() != 1 {
		return onprem.ErrForbidden
	}
	if err := insertScopedAuditEvent(ctx, tx, teamID, "human", record.UserID, membershipID, "", "",
		"identity.enrollment.created", "enrollment", record.ID, record.CreatedAt); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit team owned enrollment creation: %w", err)
	}
	return nil
}

func (s *SaaSCredentialStore) ExchangeEnrollment(
	ctx context.Context,
	enrollmentID string,
	digest onprem.Digest,
	credential onprem.CredentialRecord,
	now time.Time,
) (enrollment onprem.EnrollmentRecord, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.EnrollmentRecord{}, fmt.Errorf("begin team enrollment exchange: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "team enrollment exchange")
	teamID, membershipID, agentID, err := lockTeamEnrollmentExchangeOwner(ctx, tx, enrollmentID, digest, now)
	if err != nil {
		return onprem.EnrollmentRecord{}, err
	}
	var permissions []string
	err = tx.QueryRow(ctx, `
		UPDATE team_agent_enrollments
		SET consumed_at = $3, consumed_credential_id = $4
		WHERE token_digest = $2 AND ($1 = '' OR enrollment_id = $1)
		  AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at > $3
		  AND team_id = $5 AND membership_id = $6 AND agent_id = $7
		RETURNING enrollment_id, user_id, membership_id, agent_id, credential_label,
		          permissions, created_at, expires_at, credential_expires_at, consumed_at
	`, enrollmentID, digest[:], now, credential.ID, teamID, membershipID, agentID).Scan(
		&enrollment.ID, &enrollment.UserID, &enrollment.MembershipID, &enrollment.AgentID,
		&enrollment.CredentialLabel, &permissions, &enrollment.CreatedAt, &enrollment.ExpiresAt,
		&enrollment.CredentialExpiresAt, &enrollment.ConsumedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.EnrollmentRecord{}, onprem.ErrEnrollmentInvalid
	}
	if err != nil {
		return onprem.EnrollmentRecord{}, fmt.Errorf("consume postgres team agent enrollment: %w", err)
	}
	enrollment.TokenDigest = digest
	enrollment.Permissions = permissionsFromStrings(permissions)
	credential.UserID = enrollment.UserID
	credential.MembershipID = enrollment.MembershipID
	credential.AgentID = enrollment.AgentID
	credential.Label = enrollment.CredentialLabel
	credential.Permissions = enrollment.Permissions
	credential.ExpiresAt = enrollment.CredentialExpiresAt
	if _, err := tx.Exec(ctx, `
		INSERT INTO team_agent_credentials (
			credential_id, team_id, key_digest, user_id, owner_membership_id, agent_id,
			label, permissions, created_at, expires_at, rotated_from_credential_id, digest_key_version
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, credential.ID, teamID, credential.KeyDigest[:], credential.UserID, credential.MembershipID,
		credential.AgentID, credential.Label, permissionStrings(credential.Permissions),
		credential.CreatedAt, credential.ExpiresAt, nullableText(credential.RotatedFromCredentialID),
		credential.DigestKeyVersion); err != nil {
		return onprem.EnrollmentRecord{}, fmt.Errorf("save exchanged team agent credential: %w", err)
	}
	if err := insertScopedAuditEvent(ctx, tx, teamID, "agent", credential.UserID, credential.MembershipID,
		credential.AgentID, credential.ID, "identity.credential.issued", "credential", credential.ID, now); err != nil {
		return onprem.EnrollmentRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.EnrollmentRecord{}, fmt.Errorf("commit team enrollment exchange: %w", err)
	}
	return enrollment, nil
}

// lockTeamEnrollmentExchangeOwner resolves and locks the enrollment's owning
// membership, user, and agent inside the enrollment's team, mirroring the
// on-prem exchange's active-state checks.
func lockTeamEnrollmentExchangeOwner(
	ctx context.Context,
	tx pgx.Tx,
	enrollmentID string,
	digest onprem.Digest,
	now time.Time,
) (teamID string, membershipID string, agentID string, err error) {
	err = tx.QueryRow(ctx, `
		SELECT team_id, membership_id, agent_id
		FROM team_agent_enrollments
		WHERE token_digest = $2 AND ($1 = '' OR enrollment_id = $1)
		  AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at > $3
	`, enrollmentID, digest[:], now).Scan(&teamID, &membershipID, &agentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", onprem.ErrEnrollmentInvalid
	}
	if err != nil {
		return "", "", "", fmt.Errorf("resolve team enrollment owner before exchange: %w", err)
	}
	var active bool
	err = tx.QueryRow(ctx, `
		SELECT true
		FROM team_memberships memberships
		JOIN onprem_users users ON users.user_id = memberships.user_id
		WHERE memberships.membership_id = $1 AND memberships.team_id = $2
		  AND memberships.status = 'active' AND users.identity_status = 'active'
		FOR UPDATE OF memberships, users
	`, membershipID, teamID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", onprem.ErrEnrollmentInvalid
	}
	if err != nil {
		return "", "", "", fmt.Errorf("lock team enrollment membership before exchange: %w", err)
	}
	err = tx.QueryRow(ctx, `
		SELECT true FROM team_agents
		WHERE agent_id = $1 AND team_id = $2 AND owner_membership_id = $3 AND status = 'active'
		FOR UPDATE
	`, agentID, teamID, membershipID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", onprem.ErrEnrollmentInvalid
	}
	if err != nil {
		return "", "", "", fmt.Errorf("lock team enrollment agent before exchange: %w", err)
	}
	return teamID, membershipID, agentID, nil
}

func (s *SaaSCredentialStore) ResolveCredential(
	ctx context.Context,
	credentialID string,
	digest onprem.Digest,
	now time.Time,
) (scoped saas.ScopedCredential, err error) {
	var record onprem.CredentialRecord
	var permissions []string
	var storedDigest []byte
	err = s.pool.QueryRow(ctx, `
		UPDATE team_agent_credentials credentials
		SET last_used_at = $3
		FROM team_memberships memberships
		JOIN onprem_users users ON users.user_id = memberships.user_id
		WHERE ($1 = '' OR credentials.credential_id = $1) AND credentials.key_digest = $2
		  AND credentials.owner_membership_id = memberships.membership_id
		  AND credentials.user_id = memberships.user_id
		  AND credentials.revoked_at IS NULL
		  AND (credentials.expires_at IS NULL OR credentials.expires_at > $3)
		  AND memberships.status = 'active'
		  AND users.identity_status = 'active'
		  AND EXISTS (
		      SELECT 1 FROM team_agents agents
		      WHERE agents.agent_id = credentials.agent_id
		        AND agents.team_id = credentials.team_id
		        AND agents.owner_membership_id = credentials.owner_membership_id
		        AND agents.status = 'active'
		  )
		RETURNING credentials.credential_id, credentials.key_digest, credentials.team_id,
		          credentials.user_id, credentials.owner_membership_id, credentials.agent_id,
		          credentials.label, credentials.permissions, credentials.created_at,
		          credentials.expires_at, credentials.revoked_at, credentials.last_used_at,
		          COALESCE(credentials.rotated_from_credential_id, '')
	`, credentialID, digest[:], now).Scan(
		&record.ID, &storedDigest, &scoped.TeamID, &record.UserID, &record.MembershipID,
		&record.AgentID, &record.Label, &permissions, &record.CreatedAt, &record.ExpiresAt,
		&record.RevokedAt, &record.LastUsedAt, &record.RotatedFromCredentialID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return saas.ScopedCredential{}, onprem.ErrUnauthorized
	}
	if err != nil {
		return saas.ScopedCredential{}, fmt.Errorf("resolve postgres team agent credential: %w", err)
	}
	copy(record.KeyDigest[:], storedDigest)
	record.Permissions = permissionsFromStrings(permissions)
	record.Kind = onprem.CredentialKindAgent
	scoped.CredentialRecord = record
	return scoped, nil
}

// RotateCredential expires the current credential (currentID) inside the
// given team and inserts replacement as its successor. Team credentials are
// always agent-kind (devices are on-prem only), so there is no kind gate.
func (s *SaaSCredentialStore) RotateCredential(
	ctx context.Context,
	teamID string,
	currentID string,
	replacement onprem.CredentialRecord,
	overlapUntil time.Time,
) (returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin team credential rotation: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "team credential rotation")
	command, err := tx.Exec(ctx, `
		UPDATE team_agent_credentials
		SET expires_at = CASE
			WHEN expires_at IS NULL OR expires_at > $3 THEN $3
			ELSE expires_at
		END
		WHERE credential_id = $1 AND team_id = $2 AND revoked_at IS NULL
	`, currentID, teamID, overlapUntil)
	if err != nil {
		return fmt.Errorf("expire rotated team agent credential: %w", err)
	}
	if command.RowsAffected() != 1 {
		return onprem.ErrUnauthorized
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO team_agent_credentials (
			credential_id, team_id, key_digest, user_id, owner_membership_id, agent_id,
			label, permissions, created_at, expires_at, rotated_from_credential_id, digest_key_version
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, replacement.ID, teamID, replacement.KeyDigest[:], replacement.UserID, replacement.MembershipID,
		replacement.AgentID, replacement.Label, permissionStrings(replacement.Permissions),
		replacement.CreatedAt, replacement.ExpiresAt, nullableText(replacement.RotatedFromCredentialID),
		replacement.DigestKeyVersion); err != nil {
		return fmt.Errorf("save rotated team agent credential: %w", err)
	}
	if err := insertScopedAuditEvent(ctx, tx, teamID, "agent", replacement.UserID, replacement.MembershipID,
		replacement.AgentID, currentID, "identity.credential.rotated", "credential", replacement.ID,
		replacement.CreatedAt); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit team credential rotation: %w", err)
	}
	return nil
}

func (s *SaaSCredentialStore) ListOwnedEnrollments(
	ctx context.Context,
	teamID string,
	membershipID string,
	agentID string,
	filter onprem.AgentArtifactFilter,
	now time.Time,
) ([]onprem.AgentEnrollmentMetadata, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT enrollment_id, agent_id, credential_label, permissions, status,
		       created_at, expires_at, credential_expires_at
		FROM (
			SELECT enrollments.*,
			       CASE
			           WHEN consumed_at IS NOT NULL THEN 'consumed'
			           WHEN revoked_at IS NOT NULL THEN 'revoked'
			           WHEN expires_at <= $4 THEN 'expired'
			           ELSE 'pending'
			       END AS status
			FROM team_agent_enrollments enrollments
			WHERE team_id = $1 AND membership_id = $2 AND agent_id = $3
		) owned
		WHERE ($5 = '' OR status = $5) AND ($6 = '' OR enrollment_id > $6)
		ORDER BY enrollment_id
		LIMIT $7
	`, teamID, membershipID, agentID, now, filter.Status, filter.Cursor, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list postgres team owned enrollments: %w", err)
	}
	defer rows.Close()
	result := make([]onprem.AgentEnrollmentMetadata, 0)
	for rows.Next() {
		var metadata onprem.AgentEnrollmentMetadata
		var permissions []string
		if err := rows.Scan(
			&metadata.EnrollmentID, &metadata.AgentID, &metadata.CredentialLabel, &permissions,
			&metadata.Status, &metadata.CreatedAt, &metadata.ExpiresAt, &metadata.CredentialExpiresAt,
		); err != nil {
			return nil, fmt.Errorf("scan postgres team owned enrollment: %w", err)
		}
		metadata.Permissions = permissionsFromStrings(permissions)
		result = append(result, metadata)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres team owned enrollments: %w", err)
	}
	return result, nil
}

func (s *SaaSCredentialStore) RevokeOwnedEnrollment(
	ctx context.Context,
	teamID string,
	membershipID string,
	actor onprem.HumanPrincipal,
	agentID string,
	enrollmentID string,
	idempotencyKey string,
	now time.Time,
) (metadata onprem.AgentEnrollmentMetadata, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.AgentEnrollmentMetadata{}, fmt.Errorf("begin team owned enrollment revocation: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "team owned enrollment revocation")
	var permissions []string
	if idempotencyKey != "" {
		err = tx.QueryRow(ctx, `
			SELECT enrollment_id, agent_id, credential_label, permissions, 'revoked',
			       created_at, expires_at, credential_expires_at
			FROM team_agent_enrollments
			WHERE team_id = $1 AND revoke_idempotency_actor_membership_id = $2
			  AND revoke_idempotency_key = $3
		`, teamID, actor.MembershipID, idempotencyKey).Scan(
			&metadata.EnrollmentID, &metadata.AgentID, &metadata.CredentialLabel, &permissions,
			&metadata.Status, &metadata.CreatedAt, &metadata.ExpiresAt, &metadata.CredentialExpiresAt,
		)
		if err == nil {
			if metadata.AgentID != agentID || metadata.EnrollmentID != enrollmentID {
				return onprem.AgentEnrollmentMetadata{}, onprem.ErrIdempotencyConflict
			}
			metadata.Permissions = permissionsFromStrings(permissions)
			if err := tx.Commit(ctx); err != nil {
				return onprem.AgentEnrollmentMetadata{}, fmt.Errorf("commit team enrollment revocation replay: %w", err)
			}
			return metadata, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return onprem.AgentEnrollmentMetadata{}, fmt.Errorf("resolve team enrollment revocation replay: %w", err)
		}
	}
	err = tx.QueryRow(ctx, `
		UPDATE team_agent_enrollments
		SET revoked_at = $5, revoke_idempotency_key = $6,
		    revoke_idempotency_actor_membership_id = $7
		WHERE enrollment_id = $1 AND team_id = $2 AND membership_id = $3 AND agent_id = $4
		  AND consumed_at IS NULL AND revoked_at IS NULL
		RETURNING enrollment_id, agent_id, credential_label, permissions, 'revoked',
		          created_at, expires_at, credential_expires_at
	`, enrollmentID, teamID, membershipID, agentID, now, idempotencyKey, actor.MembershipID).Scan(
		&metadata.EnrollmentID, &metadata.AgentID, &metadata.CredentialLabel, &permissions,
		&metadata.Status, &metadata.CreatedAt, &metadata.ExpiresAt, &metadata.CredentialExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.AgentEnrollmentMetadata{}, onprem.ErrEnrollmentInvalid
	}
	if isUniqueViolation(err) {
		return onprem.AgentEnrollmentMetadata{}, onprem.ErrIdempotencyConflict
	}
	if err != nil {
		return onprem.AgentEnrollmentMetadata{}, fmt.Errorf("revoke postgres team owned enrollment: %w", err)
	}
	metadata.Permissions = permissionsFromStrings(permissions)
	if err := insertScopedAuditEvent(ctx, tx, teamID, "human", actor.UserID, actor.MembershipID, "", "",
		"identity.enrollment.revoked", "enrollment", enrollmentID, now); err != nil {
		return onprem.AgentEnrollmentMetadata{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.AgentEnrollmentMetadata{}, fmt.Errorf("commit team owned enrollment revocation: %w", err)
	}
	return metadata, nil
}

func (s *SaaSCredentialStore) ListOwnedCredentials(
	ctx context.Context,
	teamID string,
	membershipID string,
	agentID string,
	filter onprem.AgentArtifactFilter,
	now time.Time,
) ([]onprem.AgentCredentialMetadata, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT credential_id, agent_id, label, permissions, created_at,
		       expires_at, revoked_at, last_used_at
		FROM (
			SELECT credentials.*,
			       CASE
			           WHEN revoked_at IS NOT NULL THEN 'revoked'
			           WHEN expires_at IS NOT NULL AND expires_at <= $4 THEN 'expired'
			           ELSE 'active'
			       END AS status
			FROM team_agent_credentials credentials
			WHERE team_id = $1 AND owner_membership_id = $2 AND agent_id = $3
		) owned
		WHERE ($5 = '' OR status = $5) AND ($6 = '' OR credential_id > $6)
		ORDER BY credential_id
		LIMIT $7
	`, teamID, membershipID, agentID, now, filter.Status, filter.Cursor, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list postgres team owned credentials: %w", err)
	}
	defer rows.Close()
	result := make([]onprem.AgentCredentialMetadata, 0)
	for rows.Next() {
		var metadata onprem.AgentCredentialMetadata
		var permissions []string
		if err := rows.Scan(
			&metadata.CredentialID, &metadata.AgentID, &metadata.Label, &permissions,
			&metadata.CreatedAt, &metadata.ExpiresAt, &metadata.RevokedAt, &metadata.LastUsedAt,
		); err != nil {
			return nil, fmt.Errorf("scan postgres team owned credential: %w", err)
		}
		metadata.Permissions = permissionsFromStrings(permissions)
		result = append(result, metadata)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres team owned credentials: %w", err)
	}
	return result, nil
}

func (s *SaaSCredentialStore) RevokeOwnedCredential(
	ctx context.Context,
	teamID string,
	membershipID string,
	actor onprem.HumanPrincipal,
	agentID string,
	credentialID string,
	idempotencyKey string,
	now time.Time,
) (metadata onprem.AgentCredentialMetadata, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.AgentCredentialMetadata{}, fmt.Errorf("begin team owned credential revocation: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "team owned credential revocation")
	var permissions []string
	if idempotencyKey != "" {
		err = tx.QueryRow(ctx, `
			SELECT credential_id, agent_id, label, permissions, created_at,
			       expires_at, revoked_at, last_used_at
			FROM team_agent_credentials
			WHERE team_id = $1 AND revoke_idempotency_actor_membership_id = $2
			  AND revoke_idempotency_key = $3
		`, teamID, actor.MembershipID, idempotencyKey).Scan(
			&metadata.CredentialID, &metadata.AgentID, &metadata.Label, &permissions,
			&metadata.CreatedAt, &metadata.ExpiresAt, &metadata.RevokedAt, &metadata.LastUsedAt,
		)
		if err == nil {
			if metadata.AgentID != agentID || metadata.CredentialID != credentialID {
				return onprem.AgentCredentialMetadata{}, onprem.ErrIdempotencyConflict
			}
			metadata.Permissions = permissionsFromStrings(permissions)
			if err := tx.Commit(ctx); err != nil {
				return onprem.AgentCredentialMetadata{}, fmt.Errorf("commit team credential revocation replay: %w", err)
			}
			return metadata, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return onprem.AgentCredentialMetadata{}, fmt.Errorf("resolve team credential revocation replay: %w", err)
		}
	}
	err = tx.QueryRow(ctx, `
		UPDATE team_agent_credentials
		SET revoked_at = $5, revoke_idempotency_key = $6,
		    revoke_idempotency_actor_membership_id = $7
		WHERE credential_id = $1 AND team_id = $2 AND owner_membership_id = $3 AND agent_id = $4
		  AND revoked_at IS NULL
		RETURNING credential_id, agent_id, label, permissions, created_at,
		          expires_at, revoked_at, last_used_at
	`, credentialID, teamID, membershipID, agentID, now, idempotencyKey, actor.MembershipID).Scan(
		&metadata.CredentialID, &metadata.AgentID, &metadata.Label, &permissions,
		&metadata.CreatedAt, &metadata.ExpiresAt, &metadata.RevokedAt, &metadata.LastUsedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.AgentCredentialMetadata{}, onprem.ErrCredentialNotFound
	}
	if isUniqueViolation(err) {
		return onprem.AgentCredentialMetadata{}, onprem.ErrIdempotencyConflict
	}
	if err != nil {
		return onprem.AgentCredentialMetadata{}, fmt.Errorf("revoke postgres team owned credential: %w", err)
	}
	metadata.Permissions = permissionsFromStrings(permissions)
	if err := insertScopedAuditEvent(ctx, tx, teamID, "human", actor.UserID, actor.MembershipID, "", "",
		"identity.credential.revoked", "credential", credentialID, now); err != nil {
		return onprem.AgentCredentialMetadata{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.AgentCredentialMetadata{}, fmt.Errorf("commit team owned credential revocation: %w", err)
	}
	return metadata, nil
}
