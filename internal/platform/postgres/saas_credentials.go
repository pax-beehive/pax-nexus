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
	teamID, membershipID, agentID, kind, err := lockTeamEnrollmentExchangeOwner(ctx, tx, enrollmentID, digest, now)
	if err != nil {
		return onprem.EnrollmentRecord{}, err
	}
	var permissions []string
	var grantablePermissions []string
	err = tx.QueryRow(ctx, `
		UPDATE team_agent_enrollments
		SET consumed_at = $3, consumed_credential_id = $4
		WHERE token_digest = $2 AND ($1 = '' OR enrollment_id = $1)
		  AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at > $3
		  AND team_id = $5 AND membership_id = $6 AND agent_id = $7
		RETURNING enrollment_id, user_id, membership_id, agent_id, credential_label,
		          permissions, created_at, expires_at, credential_expires_at, consumed_at,
		          grantable_permissions
	`, enrollmentID, digest[:], now, credential.ID, teamID, membershipID, agentID).Scan(
		&enrollment.ID, &enrollment.UserID, &enrollment.MembershipID, &enrollment.AgentID,
		&enrollment.CredentialLabel, &permissions, &enrollment.CreatedAt, &enrollment.ExpiresAt,
		&enrollment.CredentialExpiresAt, &enrollment.ConsumedAt, &grantablePermissions,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.EnrollmentRecord{}, onprem.ErrEnrollmentInvalid
	}
	if err != nil {
		return onprem.EnrollmentRecord{}, fmt.Errorf("consume postgres team agent enrollment: %w", err)
	}
	enrollment.TokenDigest = digest
	enrollment.Permissions = permissionsFromStrings(permissions)
	enrollment.GrantablePermissions = permissionsFromStrings(grantablePermissions)
	enrollment.Kind = onprem.CredentialKind(kind)
	credential.UserID = enrollment.UserID
	credential.MembershipID = enrollment.MembershipID
	credential.AgentID = enrollment.AgentID
	credential.Label = enrollment.CredentialLabel
	credential.Permissions = enrollment.Permissions
	credential.ExpiresAt = enrollment.CredentialExpiresAt
	credential.Kind = enrollment.Kind
	credential.GrantablePermissions = enrollment.GrantablePermissions
	if _, err := tx.Exec(ctx, `
		INSERT INTO team_agent_credentials (
			credential_id, team_id, key_digest, user_id, owner_membership_id, agent_id,
			label, permissions, created_at, expires_at, rotated_from_credential_id, digest_key_version,
			kind, grantable_permissions
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`, credential.ID, teamID, credential.KeyDigest[:], credential.UserID, credential.MembershipID,
		credential.AgentID, credential.Label, permissionStrings(credential.Permissions),
		credential.CreatedAt, credential.ExpiresAt, nullableText(credential.RotatedFromCredentialID),
		credential.DigestKeyVersion, credentialKindOrDefault(credential.Kind),
		permissionStrings(credential.GrantablePermissions)); err != nil {
		return onprem.EnrollmentRecord{}, fmt.Errorf("save exchanged team agent credential: %w", err)
	}
	actorKind := "agent"
	if kind == string(onprem.CredentialKindDevice) {
		actorKind = "device"
	}
	if err := insertScopedAuditEvent(ctx, tx, teamID, actorKind, credential.UserID, credential.MembershipID,
		credential.AgentID, credential.ID, "identity.credential.issued", "credential", credential.ID, now); err != nil {
		return onprem.EnrollmentRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.EnrollmentRecord{}, fmt.Errorf("commit team enrollment exchange: %w", err)
	}
	return enrollment, nil
}

// lockTeamEnrollmentExchangeOwner resolves and locks the enrollment's owning
// membership, user, and (for agent enrollments) agent inside the
// enrollment's team, mirroring the on-prem exchange's active-state checks.
// Device enrollments carry no agent, so their agent check is skipped.
func lockTeamEnrollmentExchangeOwner(
	ctx context.Context,
	tx pgx.Tx,
	enrollmentID string,
	digest onprem.Digest,
	now time.Time,
) (teamID string, membershipID string, agentID string, kind string, err error) {
	err = tx.QueryRow(ctx, `
		SELECT team_id, membership_id, agent_id, kind
		FROM team_agent_enrollments
		WHERE token_digest = $2 AND ($1 = '' OR enrollment_id = $1)
		  AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at > $3
	`, enrollmentID, digest[:], now).Scan(&teamID, &membershipID, &agentID, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", "", onprem.ErrEnrollmentInvalid
	}
	if err != nil {
		return "", "", "", "", fmt.Errorf("resolve team enrollment owner before exchange: %w", err)
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
		return "", "", "", "", onprem.ErrEnrollmentInvalid
	}
	if err != nil {
		return "", "", "", "", fmt.Errorf("lock team enrollment membership before exchange: %w", err)
	}
	if kind == string(onprem.CredentialKindDevice) {
		return teamID, membershipID, agentID, kind, nil
	}
	err = tx.QueryRow(ctx, `
		SELECT true FROM team_agents
		WHERE agent_id = $1 AND team_id = $2 AND owner_membership_id = $3 AND status = 'active'
		FOR UPDATE
	`, agentID, teamID, membershipID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", "", onprem.ErrEnrollmentInvalid
	}
	if err != nil {
		return "", "", "", "", fmt.Errorf("lock team enrollment agent before exchange: %w", err)
	}
	return teamID, membershipID, agentID, kind, nil
}

func (s *SaaSCredentialStore) ResolveCredential(
	ctx context.Context,
	credentialID string,
	digest onprem.Digest,
	now time.Time,
) (scoped saas.ScopedCredential, err error) {
	var record onprem.CredentialRecord
	var permissions []string
	var grantablePermissions []string
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
		  AND (
		      credentials.kind = 'device'
		      OR EXISTS (
		          SELECT 1 FROM team_agents agents
		          WHERE agents.agent_id = credentials.agent_id
		            AND agents.team_id = credentials.team_id
		            AND agents.owner_membership_id = credentials.owner_membership_id
		            AND agents.status = 'active'
		      )
		  )
		RETURNING credentials.credential_id, credentials.key_digest, credentials.team_id,
		          credentials.user_id, credentials.owner_membership_id, credentials.agent_id,
		          credentials.label, credentials.permissions, credentials.created_at,
		          credentials.expires_at, credentials.revoked_at, credentials.last_used_at,
		          COALESCE(credentials.rotated_from_credential_id, ''),
		          credentials.kind, credentials.grantable_permissions,
		          COALESCE(credentials.provisioned_by, '')
	`, credentialID, digest[:], now).Scan(
		&record.ID, &storedDigest, &scoped.TeamID, &record.UserID, &record.MembershipID,
		&record.AgentID, &record.Label, &permissions, &record.CreatedAt, &record.ExpiresAt,
		&record.RevokedAt, &record.LastUsedAt, &record.RotatedFromCredentialID,
		&record.Kind, &grantablePermissions, &record.ProvisionedBy,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return saas.ScopedCredential{}, onprem.ErrUnauthorized
	}
	if err != nil {
		return saas.ScopedCredential{}, fmt.Errorf("resolve postgres team agent credential: %w", err)
	}
	copy(record.KeyDigest[:], storedDigest)
	record.Permissions = permissionsFromStrings(permissions)
	record.GrantablePermissions = permissionsFromStrings(grantablePermissions)
	scoped.CredentialRecord = record
	return scoped, nil
}

// RotateCredential expires the current credential (currentID) inside the
// given team and inserts replacement as its successor. Device credentials
// are revoke-and-rebuild, not rotatable: a device-kind row is rejected with
// onprem.ErrForbidden and the transaction rolls back untouched.
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
	var kind string
	err = tx.QueryRow(ctx, `
		UPDATE team_agent_credentials
		SET expires_at = CASE
			WHEN expires_at IS NULL OR expires_at > $3 THEN $3
			ELSE expires_at
		END
		WHERE credential_id = $1 AND team_id = $2 AND revoked_at IS NULL
		RETURNING kind
	`, currentID, teamID, overlapUntil).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.ErrUnauthorized
	}
	if err != nil {
		return fmt.Errorf("expire rotated team agent credential: %w", err)
	}
	if kind == string(onprem.CredentialKindDevice) {
		return onprem.ErrForbidden
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO team_agent_credentials (
			credential_id, team_id, key_digest, user_id, owner_membership_id, agent_id,
			label, permissions, created_at, expires_at, rotated_from_credential_id, digest_key_version,
			kind, grantable_permissions
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`, replacement.ID, teamID, replacement.KeyDigest[:], replacement.UserID, replacement.MembershipID,
		replacement.AgentID, replacement.Label, permissionStrings(replacement.Permissions),
		replacement.CreatedAt, replacement.ExpiresAt, nullableText(replacement.RotatedFromCredentialID),
		replacement.DigestKeyVersion, credentialKindOrDefault(replacement.Kind),
		permissionStrings(replacement.GrantablePermissions)); err != nil {
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

// ListExpiringEnrollments returns unclaimed, unrevoked enrollments in one
// team whose token expiry falls before the cutoff, soonest first — the
// multi-tenant counterpart of RegistryStore.ListExpiringEnrollments, scoped
// by team_id since team_agent_enrollments (unlike agent_enrollments) is
// shared across teams.
func (s *SaaSCredentialStore) ListExpiringEnrollments(
	ctx context.Context,
	teamID string,
	before time.Time,
	now time.Time,
	limit int,
) ([]onprem.AgentEnrollmentMetadata, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT enrollment_id, agent_id, credential_label, permissions, status,
		       created_at, expires_at, credential_expires_at
		FROM (
			SELECT enrollments.*,
			       CASE
			           WHEN consumed_at IS NOT NULL THEN 'consumed'
			           WHEN revoked_at IS NOT NULL THEN 'revoked'
			           WHEN expires_at <= $3 THEN 'expired'
			           ELSE 'pending'
			       END AS status
			FROM team_agent_enrollments enrollments
			WHERE team_id = $1
		) team_enrollments
		WHERE consumed_at IS NULL AND revoked_at IS NULL AND expires_at < $2
		ORDER BY expires_at ASC, enrollment_id ASC
		LIMIT $4
	`, teamID, before, now, limit)
	if err != nil {
		return nil, fmt.Errorf("query postgres team expiring enrollments: %w", err)
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
			return nil, fmt.Errorf("scan postgres team expiring enrollment: %w", err)
		}
		metadata.Permissions = permissionsFromStrings(permissions)
		result = append(result, metadata)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres team expiring enrollments: %w", err)
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

func (s *SaaSCredentialStore) CreateDeviceEnrollment(
	ctx context.Context,
	teamID string,
	membershipID string,
	record onprem.EnrollmentRecord,
) (returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin team device enrollment creation: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "team device enrollment creation")
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
		return onprem.ErrForbidden
	}
	if err != nil {
		return fmt.Errorf("lock team device enrollment membership: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO team_agent_enrollments (
			enrollment_id, team_id, token_digest, user_id, membership_id, agent_id,
			credential_label, permissions, created_at, expires_at, credential_expires_at,
			digest_key_version, kind, grantable_permissions
		) VALUES ($1, $2, $3, $4, $5, '', $6, $7, $8, $9, NULL, $10, 'device', $11)
	`, record.ID, teamID, record.TokenDigest[:], record.UserID, membershipID, record.CredentialLabel,
		permissionStrings(record.Permissions), record.CreatedAt, record.ExpiresAt,
		record.DigestKeyVersion, permissionStrings(record.GrantablePermissions)); err != nil {
		return fmt.Errorf("create postgres team device enrollment: %w", err)
	}
	if err := insertScopedAuditEvent(ctx, tx, teamID, "human", record.UserID, membershipID, "", "",
		"identity.enrollment.created", "enrollment", record.ID, record.CreatedAt); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit team device enrollment creation: %w", err)
	}
	return nil
}

// ProvisionAgentCredential creates or rotates the credential for an agent
// that a device credential (deviceCredentialID) provisions inside the same
// team, enforcing the device's active-agent cap. It mirrors the on-prem
// ordered transaction; the team scope replaces the on-prem deployment pin.
func (s *SaaSCredentialStore) ProvisionAgentCredential(
	ctx context.Context,
	teamID string,
	deviceCredentialID string,
	profile onprem.AgentProfile,
	credential onprem.CredentialRecord,
	activeAgentLimit int,
	now time.Time,
) (outcome onprem.ProvisionOutcome, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.ProvisionOutcome{}, fmt.Errorf("begin team device agent provisioning: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "team device agent provisioning")

	deviceMembershipID, err := lockTeamProvisioningDevice(ctx, tx, teamID, deviceCredentialID, now)
	if err != nil {
		return onprem.ProvisionOutcome{}, err
	}
	if err := lockTeamProvisioningDeviceOwner(ctx, tx, teamID, deviceMembershipID); err != nil {
		return onprem.ProvisionOutcome{}, err
	}

	var existingProvisionedBy, existingStatus string
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(provisioned_by, ''), status FROM team_agents
		WHERE agent_id = $1 AND team_id = $2
		FOR UPDATE
	`, profile.AgentID, teamID).Scan(&existingProvisionedBy, &existingStatus)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if err := createTeamProvisionedAgent(ctx, tx, teamID, deviceCredentialID, profile, now); err != nil {
			return onprem.ProvisionOutcome{}, err
		}
		outcome.AgentCreated = true
	case err != nil:
		return onprem.ProvisionOutcome{}, fmt.Errorf("resolve team agent for device provisioning: %w", err)
	case existingProvisionedBy == deviceCredentialID && existingStatus == string(onprem.AgentStatusActive):
		rotatedFrom, err := revokeRotatedTeamProvisionedCredentials(ctx, tx, teamID, deviceCredentialID, profile, now)
		if err != nil {
			return onprem.ProvisionOutcome{}, err
		}
		outcome.RotatedFromCredentialID = rotatedFrom
	default:
		return onprem.ProvisionOutcome{}, onprem.ErrAgentProvisionConflict
	}

	var activeCount int
	err = tx.QueryRow(ctx, `
		SELECT count(DISTINCT agent_id) FROM team_agent_credentials
		WHERE provisioned_by = $1 AND team_id = $2 AND revoked_at IS NULL AND agent_id <> $3
	`, deviceCredentialID, teamID, profile.AgentID).Scan(&activeCount)
	if err != nil {
		return onprem.ProvisionOutcome{}, fmt.Errorf("count team device-provisioned agents: %w", err)
	}
	if activeCount >= activeAgentLimit {
		return onprem.ProvisionOutcome{}, onprem.ErrDeviceAgentLimitExceeded
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO team_agent_credentials (
			credential_id, team_id, key_digest, user_id, owner_membership_id, agent_id,
			label, permissions, created_at, expires_at, rotated_from_credential_id, digest_key_version,
			kind, provisioned_by, grantable_permissions
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'agent', $13, $14)
	`, credential.ID, teamID, credential.KeyDigest[:], credential.UserID, credential.MembershipID,
		credential.AgentID, credential.Label, permissionStrings(credential.Permissions),
		credential.CreatedAt, credential.ExpiresAt, nullableText(credential.RotatedFromCredentialID),
		credential.DigestKeyVersion, deviceCredentialID,
		permissionStrings(credential.GrantablePermissions)); err != nil {
		return onprem.ProvisionOutcome{}, fmt.Errorf("save team device-provisioned credential: %w", err)
	}

	if err := insertScopedAuditEvent(ctx, tx, teamID, "device", profile.OwnerUserID, profile.OwnerMembershipID,
		profile.AgentID, deviceCredentialID, "identity.agent.provisioned", "credential", credential.ID, now); err != nil {
		return onprem.ProvisionOutcome{}, err
	}
	if err := insertScopedAuditEvent(ctx, tx, teamID, "device", credential.UserID, credential.MembershipID,
		credential.AgentID, credential.ID, "identity.credential.issued", "credential", credential.ID, now); err != nil {
		return onprem.ProvisionOutcome{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return onprem.ProvisionOutcome{}, fmt.Errorf("commit team device agent provisioning: %w", err)
	}
	return outcome, nil
}

// lockTeamProvisioningDevice locks the device credential row that authorizes
// a provisioning request inside the team and returns its owning membership
// ID. A missing, revoked, expired, foreign-team, or non-device credential is
// unauthorized.
func lockTeamProvisioningDevice(
	ctx context.Context,
	tx pgx.Tx,
	teamID string,
	deviceCredentialID string,
	now time.Time,
) (string, error) {
	var membershipID string
	err := tx.QueryRow(ctx, `
		SELECT owner_membership_id FROM team_agent_credentials
		WHERE credential_id = $1 AND team_id = $2 AND kind = 'device' AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > $3)
		FOR UPDATE
	`, deviceCredentialID, teamID, now).Scan(&membershipID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", onprem.ErrUnauthorized
	}
	if err != nil {
		return "", fmt.Errorf("lock team device credential for provisioning: %w", err)
	}
	return membershipID, nil
}

// lockTeamProvisioningDeviceOwner locks the device's owning membership and
// user inside the team, mirroring lockTeamEnrollmentExchangeOwner's
// active-membership check.
func lockTeamProvisioningDeviceOwner(ctx context.Context, tx pgx.Tx, teamID string, membershipID string) error {
	var active bool
	err := tx.QueryRow(ctx, `
		SELECT true
		FROM team_memberships memberships
		JOIN onprem_users users ON users.user_id = memberships.user_id
		WHERE memberships.membership_id = $1 AND memberships.team_id = $2
		  AND memberships.status = 'active' AND users.identity_status = 'active'
		FOR UPDATE OF memberships, users
	`, membershipID, teamID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.ErrUnauthorized
	}
	if err != nil {
		return fmt.Errorf("lock team device membership for provisioning: %w", err)
	}
	return nil
}

// createTeamProvisionedAgent inserts the team_agents row for an agent a
// device credential provisions, marked with the device's lineage.
func createTeamProvisionedAgent(
	ctx context.Context,
	tx pgx.Tx,
	teamID string,
	deviceCredentialID string,
	profile onprem.AgentProfile,
	now time.Time,
) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO team_agents (
			agent_id, team_id, owner_membership_id, display_name, description, agent_type,
			status, directory_visible, created_at, updated_at, provisioned_by
		) VALUES ($1, $2, $3, $4, $5, $6, 'active', true, $7, $7, $8)
	`, profile.AgentID, teamID, profile.OwnerMembershipID, profile.DisplayName, profile.Description,
		profile.AgentType, now, deviceCredentialID); err != nil {
		return fmt.Errorf("create team device-provisioned agent: %w", err)
	}
	if err := insertScopedAuditEvent(ctx, tx, teamID, "device", profile.OwnerUserID, profile.OwnerMembershipID,
		"", deviceCredentialID, "identity.agent.created", "agent", profile.AgentID, now); err != nil {
		return err
	}
	return nil
}

// revokeRotatedTeamProvisionedCredentials revokes every active credential
// the device previously provisioned for this agent in the team, audits each
// revocation, and returns the newest revoked credential ID (normally one).
func revokeRotatedTeamProvisionedCredentials(
	ctx context.Context,
	tx pgx.Tx,
	teamID string,
	deviceCredentialID string,
	profile onprem.AgentProfile,
	now time.Time,
) (string, error) {
	rows, err := tx.Query(ctx, `
		WITH revoked AS (
			UPDATE team_agent_credentials SET revoked_at = $4
			WHERE agent_id = $1 AND team_id = $2 AND provisioned_by = $3 AND revoked_at IS NULL
			RETURNING credential_id, created_at
		)
		SELECT credential_id FROM revoked ORDER BY created_at DESC
	`, profile.AgentID, teamID, deviceCredentialID, now)
	if err != nil {
		return "", fmt.Errorf("revoke rotated team device-provisioned credentials: %w", err)
	}
	revokedIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "", fmt.Errorf("collect rotated team device-provisioned credentials: %w", err)
	}
	for _, revokedID := range revokedIDs {
		if err := insertScopedAuditEvent(ctx, tx, teamID, "device", profile.OwnerUserID,
			profile.OwnerMembershipID, profile.AgentID, deviceCredentialID,
			"identity.credential.revoked", "credential", revokedID, now); err != nil {
			return "", err
		}
	}
	if len(revokedIDs) == 0 {
		return "", nil
	}
	return revokedIDs[0], nil
}

func (s *SaaSCredentialStore) ListDeviceProvisionedAgents(
	ctx context.Context,
	teamID string,
	deviceCredentialID string,
) ([]onprem.DeviceProvisionedAgent, error) {
	return listTeamDeviceProvisionedAgents(ctx, s.pool, teamID, deviceCredentialID)
}

// listTeamDeviceProvisionedAgents is the shared query behind
// ListDeviceProvisionedAgents and GetDevice, so both surfaces agree on
// ordering and included revoked history.
func listTeamDeviceProvisionedAgents(
	ctx context.Context,
	pool *pgxpool.Pool,
	teamID string,
	deviceCredentialID string,
) ([]onprem.DeviceProvisionedAgent, error) {
	rows, err := pool.Query(ctx, `
		SELECT agents.agent_id, agents.display_name, agents.agent_type, agents.status,
		       credentials.credential_id, credentials.created_at, credentials.revoked_at, credentials.last_used_at
		FROM team_agent_credentials credentials
		JOIN team_agents agents ON agents.agent_id = credentials.agent_id
		WHERE credentials.provisioned_by = $1 AND credentials.team_id = $2
		ORDER BY agents.agent_id, credentials.created_at DESC
	`, deviceCredentialID, teamID)
	if err != nil {
		return nil, fmt.Errorf("list postgres team device-provisioned agents: %w", err)
	}
	defer rows.Close()
	var agents []onprem.DeviceProvisionedAgent
	for rows.Next() {
		var agent onprem.DeviceProvisionedAgent
		var status string
		if err := rows.Scan(
			&agent.AgentID, &agent.DisplayName, &agent.AgentType, &status,
			&agent.CredentialID, &agent.CreatedAt, &agent.RevokedAt, &agent.LastUsedAt,
		); err != nil {
			return nil, fmt.Errorf("scan postgres team device-provisioned agent: %w", err)
		}
		agent.AgentStatus = onprem.AgentStatus(status)
		agents = append(agents, agent)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres team device-provisioned agents: %w", err)
	}
	return agents, nil
}

// RevokeDevice revokes a device credential in the team and, in the same
// transaction, cascades to every agent credential the device provisioned.
// It mirrors the on-prem lock-then-branch idempotent-replay pattern.
func (s *SaaSCredentialStore) RevokeDevice(
	ctx context.Context,
	teamID string,
	actor onprem.HumanPrincipal,
	credentialID string,
	idempotencyKey string,
	now time.Time,
) (summary onprem.DeviceSummary, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.DeviceSummary{}, fmt.Errorf("begin team device revocation: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "team device revocation")

	var (
		deviceName, createdByUserID, createdByMembershipID       string
		createdAt                                                time.Time
		revokedAt, lastUsedAt                                    *time.Time
		grantablePermissions                                     []string
		revokeIdempotencyKey, revokeIdempotencyActorMembershipID string
	)
	err = tx.QueryRow(ctx, `
		SELECT label, user_id, owner_membership_id, created_at, revoked_at, last_used_at,
		       grantable_permissions, revoke_idempotency_key, revoke_idempotency_actor_membership_id
		FROM team_agent_credentials
		WHERE credential_id = $1 AND team_id = $2 AND kind = 'device'
		FOR UPDATE
	`, credentialID, teamID).Scan(
		&deviceName, &createdByUserID, &createdByMembershipID, &createdAt, &revokedAt, &lastUsedAt,
		&grantablePermissions, &revokeIdempotencyKey, &revokeIdempotencyActorMembershipID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.DeviceSummary{}, onprem.ErrCredentialNotFound
	}
	if err != nil {
		return onprem.DeviceSummary{}, fmt.Errorf("lock postgres team device credential: %w", err)
	}

	if revokedAt != nil {
		if idempotencyKey == "" || revokeIdempotencyKey != idempotencyKey ||
			revokeIdempotencyActorMembershipID != actor.MembershipID {
			return onprem.DeviceSummary{}, onprem.ErrIdempotencyConflict
		}
		summary, err = teamDeviceSummaryWithProvisionedCount(ctx, tx, teamID, credentialID, deviceName,
			createdByUserID, createdByMembershipID, createdAt, revokedAt, lastUsedAt, grantablePermissions)
		if err != nil {
			return onprem.DeviceSummary{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return onprem.DeviceSummary{}, fmt.Errorf("commit team device revocation replay: %w", err)
		}
		return summary, nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE team_agent_credentials
		SET revoked_at = $3, revoke_idempotency_key = $4, revoke_idempotency_actor_membership_id = $5
		WHERE credential_id = $1 AND team_id = $2
	`, credentialID, teamID, now, idempotencyKey, actor.MembershipID); err != nil {
		if isUniqueViolation(err) {
			return onprem.DeviceSummary{}, onprem.ErrIdempotencyConflict
		}
		return onprem.DeviceSummary{}, fmt.Errorf("revoke postgres team device credential: %w", err)
	}
	if err := insertScopedAuditEvent(ctx, tx, teamID, "human", actor.UserID, actor.MembershipID, "", "",
		"identity.credential.revoked", "credential", credentialID, now); err != nil {
		return onprem.DeviceSummary{}, err
	}
	if _, err := cascadeRevokeTeamProvisionedCredentials(ctx, tx, teamID, credentialID, now); err != nil {
		return onprem.DeviceSummary{}, err
	}
	revokedAt = &now
	summary, err = teamDeviceSummaryWithProvisionedCount(ctx, tx, teamID, credentialID, deviceName,
		createdByUserID, createdByMembershipID, createdAt, revokedAt, lastUsedAt, grantablePermissions)
	if err != nil {
		return onprem.DeviceSummary{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.DeviceSummary{}, fmt.Errorf("commit team device revocation: %w", err)
	}
	return summary, nil
}

// cascadeRevokeTeamProvisionedCredentials revokes every still-active
// credential a device credential has provisioned in the team, auditing each
// with actor_kind 'system' and actor_credential_id set to the revoked
// device, so a device revocation immediately invalidates every agent key it
// provisioned.
func cascadeRevokeTeamProvisionedCredentials(
	ctx context.Context,
	tx pgx.Tx,
	teamID string,
	deviceCredentialID string,
	now time.Time,
) (int64, error) {
	rows, err := tx.Query(ctx, `
		UPDATE team_agent_credentials
		SET revoked_at = $3
		WHERE provisioned_by = $1 AND team_id = $2 AND revoked_at IS NULL
		RETURNING credential_id, user_id, owner_membership_id, agent_id
	`, deviceCredentialID, teamID, now)
	if err != nil {
		return 0, fmt.Errorf("cascade revoke team provisioned credentials: %w", err)
	}
	type revoked struct{ credentialID, userID, membershipID, agentID string }
	var all []revoked
	for rows.Next() {
		var current revoked
		if err := rows.Scan(&current.credentialID, &current.userID, &current.membershipID, &current.agentID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan cascade-revoked team credential: %w", err)
		}
		all = append(all, current)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate cascade-revoked team credentials: %w", err)
	}
	for _, current := range all {
		if err := insertScopedAuditEvent(ctx, tx, teamID, "system", current.userID, current.membershipID,
			current.agentID, deviceCredentialID, "identity.credential.revoked",
			"credential", current.credentialID, now); err != nil {
			return 0, err
		}
	}
	return int64(len(all)), nil
}

// teamDeviceSummaryWithProvisionedCount builds a DeviceSummary from a device
// credential's already-locked row fields plus a fresh count of the agents it
// still actively provisions (0 immediately after a fresh revoke).
func teamDeviceSummaryWithProvisionedCount(
	ctx context.Context,
	tx pgx.Tx,
	teamID string,
	credentialID, deviceName, userID, membershipID string,
	createdAt time.Time,
	revokedAt, lastUsedAt *time.Time,
	grantablePermissions []string,
) (onprem.DeviceSummary, error) {
	var count int64
	if err := tx.QueryRow(ctx, `
		SELECT count(DISTINCT p.agent_id) FROM team_agent_credentials p
		WHERE p.provisioned_by = $1 AND p.team_id = $2 AND p.revoked_at IS NULL
	`, credentialID, teamID).Scan(&count); err != nil {
		return onprem.DeviceSummary{}, fmt.Errorf("count postgres team active provisioned agents: %w", err)
	}
	return onprem.DeviceSummary{
		CredentialID: credentialID, DeviceName: deviceName,
		CreatedByUserID: userID, CreatedByMembershipID: membershipID,
		CreatedAt: createdAt, RevokedAt: revokedAt, LastUsedAt: lastUsedAt,
		GrantablePermissions:  permissionsFromStrings(grantablePermissions),
		ProvisionedAgentCount: count,
	}, nil
}

// ListDevices returns the team's device-kind credentials, newest first,
// with the same created_at-DESC keyset cursor as the on-prem listing; the
// team_device partial index covers it.
func (s *SaaSCredentialStore) ListDevices(
	ctx context.Context,
	teamID string,
	filter onprem.DeviceFilter,
) ([]onprem.DeviceSummary, error) {
	var cursorTime time.Time
	var cursorID string
	hasCursor := filter.Cursor != ""
	if hasCursor {
		var err error
		cursorTime, cursorID, err = decodeDeviceCursor(filter.Cursor)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid device cursor", onprem.ErrInvalidIdentityInput)
		}
	}
	rows, err := s.pool.Query(ctx, `
		SELECT credentials.credential_id, credentials.label, credentials.user_id,
		       credentials.owner_membership_id, credentials.created_at, credentials.revoked_at,
		       credentials.last_used_at, credentials.grantable_permissions,
		       (SELECT count(DISTINCT p.agent_id) FROM team_agent_credentials p
		        WHERE p.provisioned_by = credentials.credential_id AND p.team_id = $1 AND p.revoked_at IS NULL)
		FROM team_agent_credentials credentials
		WHERE credentials.team_id = $1 AND credentials.kind = 'device'
		  AND ($2 = '' OR ($2 = 'active' AND credentials.revoked_at IS NULL)
		               OR ($2 = 'revoked' AND credentials.revoked_at IS NOT NULL))
		  AND (NOT $3 OR credentials.created_at < $4
		       OR (credentials.created_at = $4 AND credentials.credential_id > $5))
		ORDER BY credentials.created_at DESC, credentials.credential_id
		LIMIT $6
	`, teamID, filter.Status, hasCursor, cursorTime, cursorID, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list postgres team devices: %w", err)
	}
	defer rows.Close()
	result := make([]onprem.DeviceSummary, 0)
	for rows.Next() {
		summary, err := scanDeviceSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("scan postgres team device: %w", err)
		}
		result = append(result, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres team devices: %w", err)
	}
	return result, nil
}

// GetDevice returns a device credential's summary plus every credential row
// it has provisioned (including revoked history), scoped to the team.
func (s *SaaSCredentialStore) GetDevice(
	ctx context.Context,
	teamID string,
	credentialID string,
) (onprem.DeviceDetail, error) {
	summary, err := scanDeviceSummary(s.pool.QueryRow(ctx, `
		SELECT credentials.credential_id, credentials.label, credentials.user_id,
		       credentials.owner_membership_id, credentials.created_at, credentials.revoked_at,
		       credentials.last_used_at, credentials.grantable_permissions,
		       (SELECT count(DISTINCT p.agent_id) FROM team_agent_credentials p
		        WHERE p.provisioned_by = credentials.credential_id AND p.team_id = $1 AND p.revoked_at IS NULL)
		FROM team_agent_credentials credentials
		WHERE credentials.team_id = $1 AND credentials.credential_id = $2 AND credentials.kind = 'device'
	`, teamID, credentialID))
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.DeviceDetail{}, onprem.ErrCredentialNotFound
	}
	if err != nil {
		return onprem.DeviceDetail{}, fmt.Errorf("get postgres team device: %w", err)
	}
	agents, err := listTeamDeviceProvisionedAgents(ctx, s.pool, teamID, credentialID)
	if err != nil {
		return onprem.DeviceDetail{}, err
	}
	return onprem.DeviceDetail{Device: summary, Agents: agents}, nil
}
