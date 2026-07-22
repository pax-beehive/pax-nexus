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

func (s *CredentialStore) LegacyAdminEnabled(ctx context.Context) (bool, error) {
	var enabled bool
	if err := s.pool.QueryRow(ctx, `
		SELECT bootstrap_claimed_at IS NULL
		FROM onprem_installation_state WHERE singleton_id = 1
	`).Scan(&enabled); err != nil {
		return false, fmt.Errorf("check postgres legacy admin state: %w", err)
	}
	return enabled, nil
}

func (s *CredentialStore) SaveEnrollment(
	ctx context.Context,
	record onprem.EnrollmentRecord,
) (returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin legacy enrollment: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "legacy enrollment")
	var membershipID string
	if record.AllowLegacyAgentCreation {
		membershipID, err = ensureLegacyAgent(ctx, tx, record.UserID, record.AgentID, record.CreatedAt)
	} else {
		membershipID, err = resolveLegacyAgentOwner(ctx, tx, record.UserID, record.AgentID)
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO agent_enrollments (
			enrollment_id, token_digest, user_id, membership_id, agent_id,
			credential_label, permissions, created_at, expires_at, credential_expires_at, digest_key_version
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, record.ID, record.TokenDigest[:], record.UserID, membershipID, record.AgentID,
		record.CredentialLabel, permissionStrings(record.Permissions), record.CreatedAt,
		record.ExpiresAt, record.CredentialExpiresAt, record.DigestKeyVersion)
	if err != nil {
		return fmt.Errorf("save postgres agent enrollment: %w", err)
	}
	if err := insertAuditEvent(ctx, tx, "system", record.UserID, membershipID, "", "",
		"identity.legacy-admin-enrollment.created", "enrollment", record.ID, record.CreatedAt); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit legacy enrollment: %w", err)
	}
	return nil
}

func (s *CredentialStore) ExchangeEnrollment(
	ctx context.Context,
	enrollmentID string,
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
	membershipID, agentID, err := lockEnrollmentExchangeOwner(ctx, tx, enrollmentID, digest, now)
	if err != nil {
		return onprem.EnrollmentRecord{}, err
	}
	var permissions []string
	err = tx.QueryRow(ctx, `
		UPDATE agent_enrollments
		SET consumed_at = $3, consumed_credential_id = $4
		WHERE token_digest = $2 AND ($1 = '' OR enrollment_id = $1)
		  AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at > $3
		  AND membership_id = $5 AND agent_id = $6
		RETURNING enrollment_id, user_id, membership_id, agent_id, credential_label,
		          permissions, created_at, expires_at, credential_expires_at, consumed_at
	`, enrollmentID, digest[:], now, credential.ID, membershipID, agentID).Scan(
		&enrollment.ID, &enrollment.UserID, &enrollment.MembershipID, &enrollment.AgentID,
		&enrollment.CredentialLabel, &permissions, &enrollment.CreatedAt, &enrollment.ExpiresAt,
		&enrollment.CredentialExpiresAt, &enrollment.ConsumedAt,
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
	credential.MembershipID = enrollment.MembershipID
	credential.AgentID = enrollment.AgentID
	credential.Label = enrollment.CredentialLabel
	credential.Permissions = enrollment.Permissions
	credential.ExpiresAt = enrollment.CredentialExpiresAt
	var claimedUserID string
	err = tx.QueryRow(ctx, `
		INSERT INTO onprem_agent_identities (agent_id, user_id, created_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (agent_id) DO UPDATE
		SET agent_id = EXCLUDED.agent_id
		WHERE onprem_agent_identities.user_id = EXCLUDED.user_id
		RETURNING user_id
	`, credential.AgentID, credential.UserID, credential.CreatedAt).Scan(&claimedUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.EnrollmentRecord{}, onprem.ErrAgentIdentityConflict
	}
	if err != nil {
		return onprem.EnrollmentRecord{}, fmt.Errorf("claim postgres agent identity: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_credentials (
			credential_id, key_digest, user_id, owner_membership_id, agent_id,
			label, permissions, created_at, expires_at, rotated_from_credential_id, digest_key_version
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, credential.ID, credential.KeyDigest[:], credential.UserID, credential.MembershipID,
		credential.AgentID, credential.Label, permissionStrings(credential.Permissions),
		credential.CreatedAt, credential.ExpiresAt, nullableText(credential.RotatedFromCredentialID),
		credential.DigestKeyVersion); err != nil {
		return onprem.EnrollmentRecord{}, fmt.Errorf("save exchanged agent credential: %w", err)
	}
	if err := insertAuditEvent(ctx, tx, "agent", credential.UserID, credential.MembershipID,
		credential.AgentID, credential.ID, "identity.credential.issued", "credential", credential.ID, now); err != nil {
		return onprem.EnrollmentRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.EnrollmentRecord{}, fmt.Errorf("commit enrollment exchange: %w", err)
	}
	return enrollment, nil
}

func lockEnrollmentExchangeOwner(
	ctx context.Context,
	tx pgx.Tx,
	enrollmentID string,
	digest onprem.Digest,
	now time.Time,
) (string, string, error) {
	var membershipID, agentID string
	err := tx.QueryRow(ctx, `
		SELECT membership_id, agent_id
		FROM agent_enrollments
		WHERE token_digest = $2 AND ($1 = '' OR enrollment_id = $1)
		  AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at > $3
	`, enrollmentID, digest[:], now).Scan(&membershipID, &agentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", onprem.ErrEnrollmentInvalid
	}
	if err != nil {
		return "", "", fmt.Errorf("resolve enrollment owner before exchange: %w", err)
	}
	var active bool
	err = tx.QueryRow(ctx, `
		SELECT true
		FROM onprem_memberships memberships
		JOIN onprem_users users ON users.user_id = memberships.user_id
		WHERE memberships.membership_id = $1 AND memberships.status = 'active'
		  AND users.identity_status IN ('active', 'unclaimed')
		FOR UPDATE OF memberships, users
	`, membershipID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", onprem.ErrEnrollmentInvalid
	}
	if err != nil {
		return "", "", fmt.Errorf("lock enrollment membership before exchange: %w", err)
	}
	err = tx.QueryRow(ctx, `
		SELECT true FROM onprem_agents
		WHERE agent_id = $1 AND owner_membership_id = $2 AND status = 'active'
		FOR UPDATE
	`, agentID, membershipID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", onprem.ErrEnrollmentInvalid
	}
	if err != nil {
		return "", "", fmt.Errorf("lock enrollment agent before exchange: %w", err)
	}
	return membershipID, agentID, nil
}

func (s *CredentialStore) ResolveCredential(
	ctx context.Context,
	credentialID string,
	digest onprem.Digest,
	now time.Time,
) (record onprem.CredentialRecord, err error) {
	var permissions []string
	var storedDigest []byte
	err = s.pool.QueryRow(ctx, `
		UPDATE agent_credentials credentials
		SET last_used_at = $3
		FROM onprem_agents agents
		JOIN onprem_memberships memberships ON memberships.membership_id = agents.owner_membership_id
		JOIN onprem_users users ON users.user_id = memberships.user_id
		WHERE ($1 = '' OR credentials.credential_id = $1) AND credentials.key_digest = $2
		  AND credentials.owner_membership_id = agents.owner_membership_id
		  AND credentials.agent_id = agents.agent_id
		  AND credentials.user_id = memberships.user_id
		  AND credentials.revoked_at IS NULL
		  AND (credentials.expires_at IS NULL OR credentials.expires_at > $3)
		  AND agents.status = 'active' AND memberships.status = 'active'
		  AND users.identity_status IN ('active', 'unclaimed')
		RETURNING credentials.credential_id, credentials.key_digest, credentials.user_id,
		          credentials.owner_membership_id, credentials.agent_id, credentials.label,
		          credentials.permissions, credentials.created_at, credentials.expires_at,
		          credentials.revoked_at, credentials.last_used_at,
		          COALESCE(credentials.rotated_from_credential_id, '')
	`, credentialID, digest[:], now).Scan(
		&record.ID, &storedDigest, &record.UserID, &record.MembershipID, &record.AgentID,
		&record.Label, &permissions, &record.CreatedAt, &record.ExpiresAt, &record.RevokedAt,
		&record.LastUsedAt, &record.RotatedFromCredentialID,
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
			credential_id, key_digest, user_id, owner_membership_id, agent_id,
			label, permissions, created_at, expires_at, rotated_from_credential_id, digest_key_version
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, replacement.ID, replacement.KeyDigest[:], replacement.UserID, replacement.MembershipID,
		replacement.AgentID, replacement.Label, permissionStrings(replacement.Permissions),
		replacement.CreatedAt, replacement.ExpiresAt, nullableText(replacement.RotatedFromCredentialID),
		replacement.DigestKeyVersion); err != nil {
		return fmt.Errorf("save rotated agent credential: %w", err)
	}
	if err := insertAuditEvent(ctx, tx, "agent", replacement.UserID, replacement.MembershipID,
		replacement.AgentID, currentID, "identity.credential.rotated", "credential", replacement.ID,
		replacement.CreatedAt); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit credential rotation: %w", err)
	}
	return nil
}

func resolveLegacyAgentOwner(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	agentID string,
) (string, error) {
	var membershipID string
	err := tx.QueryRow(ctx, `
		SELECT memberships.membership_id
		FROM onprem_agents agents
		JOIN onprem_memberships memberships ON memberships.membership_id = agents.owner_membership_id
		WHERE agents.agent_id = $1 AND memberships.user_id = $2
		  AND agents.status = 'active' AND memberships.status = 'active'
	`, agentID, userID).Scan(&membershipID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", onprem.ErrAgentIdentityConflict
	}
	if err != nil {
		return "", fmt.Errorf("resolve legacy agent owner: %w", err)
	}
	return membershipID, nil
}

func ensureLegacyAgent(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	agentID string,
	now time.Time,
) (string, error) {
	_, err := tx.Exec(ctx, `
		INSERT INTO onprem_users (
			user_id, display_name, identity_status, created_at, updated_at
		) VALUES ($1, $1, 'unclaimed', $2, $2)
		ON CONFLICT (user_id) DO NOTHING
	`, userID, now)
	if err != nil {
		return "", fmt.Errorf("ensure legacy on-prem user: %w", err)
	}
	var membershipID string
	err = tx.QueryRow(ctx, `
		SELECT membership_id FROM onprem_memberships
		WHERE user_id = $1 AND status IN ('active', 'suspended')
	`, userID).Scan(&membershipID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
			INSERT INTO onprem_memberships (
				membership_id, user_id, role, status, joined_at, updated_at
			) VALUES ('legacy-membership-' || md5($1), $1, 'member', 'active', $2, $2)
			RETURNING membership_id
		`, userID, now).Scan(&membershipID)
	}
	if err != nil {
		return "", fmt.Errorf("ensure legacy on-prem membership: %w", err)
	}
	var claimedMembershipID string
	err = tx.QueryRow(ctx, `
		INSERT INTO onprem_agents (
			agent_id, owner_membership_id, display_name, status,
			directory_visible, created_at, updated_at
		) VALUES ($1, $2, $1, 'active', true, $3, $3)
		ON CONFLICT (agent_id) DO UPDATE SET agent_id = EXCLUDED.agent_id
		WHERE onprem_agents.owner_membership_id = EXCLUDED.owner_membership_id
		RETURNING owner_membership_id
	`, agentID, membershipID, now).Scan(&claimedMembershipID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", onprem.ErrAgentIdentityConflict
	}
	if err != nil {
		return "", fmt.Errorf("ensure legacy on-prem agent: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO onprem_agent_identities (agent_id, user_id, created_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (agent_id) DO UPDATE SET agent_id = EXCLUDED.agent_id
		WHERE onprem_agent_identities.user_id = EXCLUDED.user_id
	`, agentID, userID, now)
	if err != nil {
		return "", fmt.Errorf("ensure legacy channel identity: %w", err)
	}
	return claimedMembershipID, nil
}

func (s *CredentialStore) RevokeCredential(
	ctx context.Context,
	credentialID string,
	now time.Time,
) (returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin credential revocation: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "credential revocation")
	command, err := tx.Exec(ctx, `
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
	if err := insertAuditEvent(ctx, tx, "system", "", "", "", "",
		"identity.credential.revoked", "credential", credentialID, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit credential revocation: %w", err)
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
