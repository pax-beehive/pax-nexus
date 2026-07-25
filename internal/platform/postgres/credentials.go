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
	membershipID, agentID, kind, err := lockEnrollmentExchangeOwner(ctx, tx, enrollmentID, digest, now)
	if err != nil {
		return onprem.EnrollmentRecord{}, err
	}
	var permissions []string
	var grantablePermissions []string
	err = tx.QueryRow(ctx, `
		UPDATE agent_enrollments
		SET consumed_at = $3, consumed_credential_id = $4
		WHERE token_digest = $2 AND ($1 = '' OR enrollment_id = $1)
		  AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at > $3
		  AND membership_id = $5 AND agent_id = $6
		RETURNING enrollment_id, user_id, membership_id, agent_id, credential_label,
		          permissions, created_at, expires_at, credential_expires_at, consumed_at,
		          kind, grantable_permissions
	`, enrollmentID, digest[:], now, credential.ID, membershipID, agentID).Scan(
		&enrollment.ID, &enrollment.UserID, &enrollment.MembershipID, &enrollment.AgentID,
		&enrollment.CredentialLabel, &permissions, &enrollment.CreatedAt, &enrollment.ExpiresAt,
		&enrollment.CredentialExpiresAt, &enrollment.ConsumedAt, &enrollment.Kind, &grantablePermissions,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.EnrollmentRecord{}, onprem.ErrEnrollmentInvalid
	}
	if err != nil {
		return onprem.EnrollmentRecord{}, fmt.Errorf("consume postgres agent enrollment: %w", err)
	}
	enrollment.TokenDigest = digest
	enrollment.Permissions = permissionsFromStrings(permissions)
	enrollment.GrantablePermissions = permissionsFromStrings(grantablePermissions)
	credential.UserID = enrollment.UserID
	credential.MembershipID = enrollment.MembershipID
	credential.AgentID = enrollment.AgentID
	credential.Label = enrollment.CredentialLabel
	credential.Permissions = enrollment.Permissions
	credential.ExpiresAt = enrollment.CredentialExpiresAt
	credential.Kind = enrollment.Kind
	credential.GrantablePermissions = enrollment.GrantablePermissions
	isDevice := kind == string(onprem.CredentialKindDevice)
	if !isDevice {
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
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_credentials (
			credential_id, key_digest, user_id, owner_membership_id, agent_id,
			label, permissions, created_at, expires_at, rotated_from_credential_id, digest_key_version,
			kind, provisioned_by, grantable_permissions
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`, credential.ID, credential.KeyDigest[:], credential.UserID, credential.MembershipID,
		credential.AgentID, credential.Label, permissionStrings(credential.Permissions),
		credential.CreatedAt, credential.ExpiresAt, nullableText(credential.RotatedFromCredentialID),
		credential.DigestKeyVersion, credentialKindOrDefault(credential.Kind),
		nullableText(credential.ProvisionedBy), permissionStrings(credential.GrantablePermissions)); err != nil {
		return onprem.EnrollmentRecord{}, fmt.Errorf("save exchanged agent credential: %w", err)
	}
	actorKind := "agent"
	if isDevice {
		actorKind = "device"
	}
	if err := insertAuditEvent(ctx, tx, actorKind, credential.UserID, credential.MembershipID,
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
) (string, string, string, error) {
	var membershipID, agentID, kind string
	err := tx.QueryRow(ctx, `
		SELECT membership_id, agent_id, kind
		FROM agent_enrollments
		WHERE token_digest = $2 AND ($1 = '' OR enrollment_id = $1)
		  AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at > $3
	`, enrollmentID, digest[:], now).Scan(&membershipID, &agentID, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", onprem.ErrEnrollmentInvalid
	}
	if err != nil {
		return "", "", "", fmt.Errorf("resolve enrollment owner before exchange: %w", err)
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
		return "", "", "", onprem.ErrEnrollmentInvalid
	}
	if err != nil {
		return "", "", "", fmt.Errorf("lock enrollment membership before exchange: %w", err)
	}
	if kind == string(onprem.CredentialKindDevice) {
		return membershipID, agentID, kind, nil
	}
	err = tx.QueryRow(ctx, `
		SELECT true FROM onprem_agents
		WHERE agent_id = $1 AND owner_membership_id = $2 AND status = 'active'
		FOR UPDATE
	`, agentID, membershipID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", onprem.ErrEnrollmentInvalid
	}
	if err != nil {
		return "", "", "", fmt.Errorf("lock enrollment agent before exchange: %w", err)
	}
	return membershipID, agentID, kind, nil
}

func (s *CredentialStore) ResolveCredential(
	ctx context.Context,
	credentialID string,
	digest onprem.Digest,
	now time.Time,
) (record onprem.CredentialRecord, err error) {
	var permissions []string
	var grantablePermissions []string
	var storedDigest []byte
	err = s.pool.QueryRow(ctx, `
		UPDATE agent_credentials credentials
		SET last_used_at = $3
		FROM onprem_memberships memberships
		JOIN onprem_users users ON users.user_id = memberships.user_id
		WHERE ($1 = '' OR credentials.credential_id = $1) AND credentials.key_digest = $2
		  AND credentials.owner_membership_id = memberships.membership_id
		  AND credentials.user_id = memberships.user_id
		  AND credentials.revoked_at IS NULL
		  AND (credentials.expires_at IS NULL OR credentials.expires_at > $3)
		  AND memberships.status = 'active'
		  AND users.identity_status IN ('active', 'unclaimed')
		  AND (
		      credentials.kind = 'device'
		      OR EXISTS (
		          SELECT 1 FROM onprem_agents agents
		          WHERE agents.agent_id = credentials.agent_id
		            AND agents.owner_membership_id = credentials.owner_membership_id
		            AND agents.status = 'active'
		      )
		  )
		RETURNING credentials.credential_id, credentials.key_digest, credentials.user_id,
		          credentials.owner_membership_id, credentials.agent_id, credentials.label,
		          credentials.permissions, credentials.created_at, credentials.expires_at,
		          credentials.revoked_at, credentials.last_used_at,
		          COALESCE(credentials.rotated_from_credential_id, ''),
		          credentials.kind, credentials.grantable_permissions,
		          COALESCE(credentials.provisioned_by, '')
	`, credentialID, digest[:], now).Scan(
		&record.ID, &storedDigest, &record.UserID, &record.MembershipID, &record.AgentID,
		&record.Label, &permissions, &record.CreatedAt, &record.ExpiresAt, &record.RevokedAt,
		&record.LastUsedAt, &record.RotatedFromCredentialID,
		&record.Kind, &grantablePermissions, &record.ProvisionedBy,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.CredentialRecord{}, onprem.ErrUnauthorized
	}
	if err != nil {
		return onprem.CredentialRecord{}, fmt.Errorf("resolve postgres agent credential: %w", err)
	}
	copy(record.KeyDigest[:], storedDigest)
	record.Permissions = permissionsFromStrings(permissions)
	record.GrantablePermissions = permissionsFromStrings(grantablePermissions)
	return record, nil
}

// RotateCredential expires the current credential (currentID) and inserts
// replacement as its successor. Device credentials are revoke-and-rebuild,
// not rotatable (docs/decisions/2026-07-24-device-scoped-agent-provisioning.md):
// if currentID names a device-kind credential, rotation is rejected with
// onprem.ErrForbidden and the transaction rolls back untouched. Otherwise the
// replacement inherits provisioned_by from the row it rotates away (not from
// replacement.ProvisionedBy, which callers such as CredentialService never
// populate) so a device-provisioned agent credential remains reachable by the
// device's cascade revocation across rotations.
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
	var kind, provisionedBy string
	err = tx.QueryRow(ctx, `
		UPDATE agent_credentials
		SET expires_at = CASE
			WHEN expires_at IS NULL OR expires_at > $2 THEN $2
			ELSE expires_at
		END
		WHERE credential_id = $1 AND revoked_at IS NULL
		RETURNING kind, COALESCE(provisioned_by, '')
	`, currentID, overlapUntil).Scan(&kind, &provisionedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.ErrUnauthorized
	}
	if err != nil {
		return fmt.Errorf("expire rotated agent credential: %w", err)
	}
	if kind == string(onprem.CredentialKindDevice) {
		return onprem.ErrForbidden
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_credentials (
			credential_id, key_digest, user_id, owner_membership_id, agent_id,
			label, permissions, created_at, expires_at, rotated_from_credential_id, digest_key_version,
			kind, provisioned_by, grantable_permissions
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`, replacement.ID, replacement.KeyDigest[:], replacement.UserID, replacement.MembershipID,
		replacement.AgentID, replacement.Label, permissionStrings(replacement.Permissions),
		replacement.CreatedAt, replacement.ExpiresAt, nullableText(replacement.RotatedFromCredentialID),
		replacement.DigestKeyVersion, credentialKindOrDefault(replacement.Kind),
		nullableText(provisionedBy), permissionStrings(replacement.GrantablePermissions)); err != nil {
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

// ProvisionAgentCredential creates or rotates the credential for an agent
// that a device credential (deviceCredentialID) provisions, enforcing the
// device's active-agent cap (activeAgentLimit). See
// docs/decisions/2026-07-24-device-scoped-agent-provisioning.md for the
// ordered transaction this implements.
func (s *CredentialStore) ProvisionAgentCredential(
	ctx context.Context,
	deviceCredentialID string,
	profile onprem.AgentProfile,
	credential onprem.CredentialRecord,
	activeAgentLimit int,
	now time.Time,
) (outcome onprem.ProvisionOutcome, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.ProvisionOutcome{}, fmt.Errorf("begin device agent provisioning: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(context.Background()); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("rollback device agent provisioning: %w", rollbackErr))
		}
	}()

	deviceMembershipID, err := lockProvisioningDevice(ctx, tx, deviceCredentialID, now)
	if err != nil {
		return onprem.ProvisionOutcome{}, err
	}
	if err := lockProvisioningDeviceOwner(ctx, tx, deviceMembershipID); err != nil {
		return onprem.ProvisionOutcome{}, err
	}

	var existingProvisionedBy, existingStatus string
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(provisioned_by, ''), status FROM onprem_agents
		WHERE agent_id = $1
		FOR UPDATE
	`, profile.AgentID).Scan(&existingProvisionedBy, &existingStatus)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if err := createProvisionedAgent(ctx, tx, deviceCredentialID, profile, now); err != nil {
			return onprem.ProvisionOutcome{}, err
		}
		outcome.AgentCreated = true
	case err != nil:
		return onprem.ProvisionOutcome{}, fmt.Errorf("resolve agent for device provisioning: %w", err)
	case existingProvisionedBy == deviceCredentialID && existingStatus == string(onprem.AgentStatusActive):
		rotatedFrom, err := revokeRotatedProvisionedCredentials(ctx, tx, deviceCredentialID, profile, now)
		if err != nil {
			return onprem.ProvisionOutcome{}, err
		}
		outcome.RotatedFromCredentialID = rotatedFrom
	default:
		return onprem.ProvisionOutcome{}, onprem.ErrAgentProvisionConflict
	}

	var activeCount int
	err = tx.QueryRow(ctx, `
		SELECT count(DISTINCT agent_id) FROM agent_credentials
		WHERE provisioned_by = $1 AND revoked_at IS NULL AND agent_id <> $2
	`, deviceCredentialID, profile.AgentID).Scan(&activeCount)
	if err != nil {
		return onprem.ProvisionOutcome{}, fmt.Errorf("count device-provisioned agents: %w", err)
	}
	if activeCount >= activeAgentLimit {
		return onprem.ProvisionOutcome{}, onprem.ErrDeviceAgentLimitExceeded
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_credentials (
			credential_id, key_digest, user_id, owner_membership_id, agent_id,
			label, permissions, created_at, expires_at, rotated_from_credential_id, digest_key_version,
			kind, provisioned_by, grantable_permissions
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`, credential.ID, credential.KeyDigest[:], credential.UserID, credential.MembershipID,
		credential.AgentID, credential.Label, permissionStrings(credential.Permissions),
		credential.CreatedAt, credential.ExpiresAt, nullableText(credential.RotatedFromCredentialID),
		credential.DigestKeyVersion, credentialKindOrDefault(credential.Kind),
		nullableText(credential.ProvisionedBy), permissionStrings(credential.GrantablePermissions)); err != nil {
		return onprem.ProvisionOutcome{}, fmt.Errorf("save device-provisioned agent credential: %w", err)
	}

	if err := insertAuditEvent(ctx, tx, "device", profile.OwnerUserID, profile.OwnerMembershipID, profile.AgentID,
		deviceCredentialID, "identity.agent.provisioned", "credential", credential.ID, now); err != nil {
		return onprem.ProvisionOutcome{}, err
	}
	if err := insertAuditEvent(ctx, tx, "device", credential.UserID, credential.MembershipID, credential.AgentID,
		deviceCredentialID, "identity.credential.issued", "credential", credential.ID, now); err != nil {
		return onprem.ProvisionOutcome{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return onprem.ProvisionOutcome{}, fmt.Errorf("commit device agent provisioning: %w", err)
	}
	return outcome, nil
}

// lockProvisioningDevice locks the device credential row that authorizes a
// provisioning request and returns its owning membership ID. A missing,
// revoked, expired, or non-device credential is unauthorized.
// ListDeviceProvisionedAgents returns every credential row (including
// revoked history) the device credential deviceCredentialID has
// provisioned, ordered by agent ID then created_at DESC.
func (s *CredentialStore) ListDeviceProvisionedAgents(
	ctx context.Context, deviceCredentialID string,
) ([]onprem.DeviceProvisionedAgent, error) {
	return listDeviceProvisionedAgents(ctx, s.pool, deviceCredentialID)
}

// listDeviceProvisionedAgents is the shared query behind both
// CredentialStore.ListDeviceProvisionedAgents (Task 6) and
// RegistryStore.GetDevice (Task 8's admin device detail), so both surfaces
// agree on ordering and included revoked history.
func listDeviceProvisionedAgents(
	ctx context.Context, pool *pgxpool.Pool, deviceCredentialID string,
) ([]onprem.DeviceProvisionedAgent, error) {
	rows, err := pool.Query(ctx, `
		SELECT agents.agent_id, agents.display_name, agents.agent_type, agents.status,
		       credentials.credential_id, credentials.created_at, credentials.revoked_at, credentials.last_used_at
		FROM agent_credentials credentials
		JOIN onprem_agents agents ON agents.agent_id = credentials.agent_id
		WHERE credentials.provisioned_by = $1
		ORDER BY agents.agent_id, credentials.created_at DESC
	`, deviceCredentialID)
	if err != nil {
		return nil, fmt.Errorf("list postgres device-provisioned agents: %w", err)
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
			return nil, fmt.Errorf("scan postgres device-provisioned agent: %w", err)
		}
		agent.AgentStatus = onprem.AgentStatus(status)
		agents = append(agents, agent)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres device-provisioned agents: %w", err)
	}
	return agents, nil
}

func lockProvisioningDevice(ctx context.Context, tx pgx.Tx, deviceCredentialID string, now time.Time) (string, error) {
	var membershipID string
	err := tx.QueryRow(ctx, `
		SELECT owner_membership_id FROM agent_credentials
		WHERE credential_id = $1 AND kind = 'device' AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > $2)
		FOR UPDATE
	`, deviceCredentialID, now).Scan(&membershipID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", onprem.ErrUnauthorized
	}
	if err != nil {
		return "", fmt.Errorf("lock device credential for provisioning: %w", err)
	}
	return membershipID, nil
}

// lockProvisioningDeviceOwner locks the device's owning membership and user,
// mirroring lockEnrollmentExchangeOwner's active-membership check.
func lockProvisioningDeviceOwner(ctx context.Context, tx pgx.Tx, membershipID string) error {
	var active bool
	err := tx.QueryRow(ctx, `
		SELECT true
		FROM onprem_memberships memberships
		JOIN onprem_users users ON users.user_id = memberships.user_id
		WHERE memberships.membership_id = $1 AND memberships.status = 'active'
		  AND users.identity_status IN ('active', 'unclaimed')
		FOR UPDATE OF memberships, users
	`, membershipID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.ErrUnauthorized
	}
	if err != nil {
		return fmt.Errorf("lock device membership for provisioning: %w", err)
	}
	return nil
}

// createProvisionedAgent claims the agent_id's channel identity for the
// device's owning user (exactly like ExchangeEnrollment's identity claim)
// and inserts the new onprem_agents row.
func createProvisionedAgent(
	ctx context.Context, tx pgx.Tx, deviceCredentialID string, profile onprem.AgentProfile, now time.Time,
) error {
	var claimedUserID string
	err := tx.QueryRow(ctx, `
		INSERT INTO onprem_agent_identities (agent_id, user_id, created_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (agent_id) DO UPDATE
		SET agent_id = EXCLUDED.agent_id
		WHERE onprem_agent_identities.user_id = EXCLUDED.user_id
		RETURNING user_id
	`, profile.AgentID, profile.OwnerUserID, now).Scan(&claimedUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.ErrAgentProvisionConflict
	}
	if err != nil {
		return fmt.Errorf("claim device-provisioned agent identity: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO onprem_agents (
			agent_id, owner_membership_id, display_name, description, agent_type,
			status, directory_visible, created_at, updated_at, provisioned_by
		) VALUES ($1, $2, $3, $4, $5, 'active', true, $6, $6, $7)
	`, profile.AgentID, profile.OwnerMembershipID, profile.DisplayName, profile.Description,
		profile.AgentType, now, deviceCredentialID); err != nil {
		return fmt.Errorf("create device-provisioned agent: %w", err)
	}
	if err := insertAuditEvent(ctx, tx, "device", profile.OwnerUserID, profile.OwnerMembershipID, "",
		deviceCredentialID, "identity.agent.created", "agent", profile.AgentID, now); err != nil {
		return err
	}
	return nil
}

// revokeRotatedProvisionedCredentials revokes every active credential the
// device previously provisioned for this agent, audits each revocation, and
// returns the newest revoked credential ID (there is normally exactly one).
func revokeRotatedProvisionedCredentials(
	ctx context.Context, tx pgx.Tx, deviceCredentialID string, profile onprem.AgentProfile, now time.Time,
) (string, error) {
	rows, err := tx.Query(ctx, `
		WITH revoked AS (
			UPDATE agent_credentials SET revoked_at = $3
			WHERE agent_id = $1 AND provisioned_by = $2 AND revoked_at IS NULL
			RETURNING credential_id, created_at
		)
		SELECT credential_id FROM revoked ORDER BY created_at DESC
	`, profile.AgentID, deviceCredentialID, now)
	if err != nil {
		return "", fmt.Errorf("revoke rotated device-provisioned credentials: %w", err)
	}
	revokedIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "", fmt.Errorf("collect rotated device-provisioned credentials: %w", err)
	}
	for _, revokedID := range revokedIDs {
		if err := insertAuditEvent(ctx, tx, "device", profile.OwnerUserID, profile.OwnerMembershipID, profile.AgentID,
			deviceCredentialID, "identity.credential.revoked", "credential", revokedID, now); err != nil {
			return "", err
		}
	}
	if len(revokedIDs) == 0 {
		return "", nil
	}
	return revokedIDs[0], nil
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
	var kind string
	err = tx.QueryRow(ctx, `
		UPDATE agent_credentials
		SET revoked_at = $2
		WHERE credential_id = $1 AND revoked_at IS NULL
		RETURNING kind
	`, credentialID, now).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.ErrCredentialNotFound
	}
	if err != nil {
		return fmt.Errorf("revoke postgres agent credential: %w", err)
	}
	if err := insertAuditEvent(ctx, tx, "system", "", "", "", "",
		"identity.credential.revoked", "credential", credentialID, now); err != nil {
		return err
	}
	if kind == string(onprem.CredentialKindDevice) {
		if _, err := cascadeRevokeProvisionedCredentials(ctx, tx, credentialID, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit credential revocation: %w", err)
	}
	return nil
}

// cascadeRevokeProvisionedCredentials revokes every still-active credential a
// device credential (deviceCredentialID) has provisioned, auditing each
// revocation with actor_kind 'system' and actor_credential_id set to the
// revoked device, so a device revocation immediately invalidates every agent
// key it provisioned (docs/decisions/2026-07-24-device-scoped-agent-provisioning.md).
func cascadeRevokeProvisionedCredentials(
	ctx context.Context, tx pgx.Tx, deviceCredentialID string, now time.Time,
) (int64, error) {
	rows, err := tx.Query(ctx, `
		UPDATE agent_credentials
		SET revoked_at = $2
		WHERE provisioned_by = $1 AND revoked_at IS NULL
		RETURNING credential_id, user_id, owner_membership_id, agent_id
	`, deviceCredentialID, now)
	if err != nil {
		return 0, fmt.Errorf("cascade revoke provisioned credentials: %w", err)
	}
	type revoked struct{ credentialID, userID, membershipID, agentID string }
	var all []revoked
	for rows.Next() {
		var current revoked
		if err := rows.Scan(&current.credentialID, &current.userID, &current.membershipID, &current.agentID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan cascade-revoked credential: %w", err)
		}
		all = append(all, current)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate cascade-revoked credentials: %w", err)
	}
	for _, current := range all {
		if err := insertAuditEvent(ctx, tx, "system", current.userID, current.membershipID,
			current.agentID, deviceCredentialID, "identity.credential.revoked",
			"credential", current.credentialID, now); err != nil {
			return 0, err
		}
	}
	return int64(len(all)), nil
}

func credentialKindOrDefault(kind onprem.CredentialKind) string {
	if kind == "" {
		return string(onprem.CredentialKindAgent)
	}
	return string(kind)
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
