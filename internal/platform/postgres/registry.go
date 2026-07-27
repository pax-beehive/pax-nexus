package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
)

type RegistryStore struct {
	pool *pgxpool.Pool
}

type agentRowScanner interface {
	Scan(...any) error
}

func (s *RegistryStore) CreateAgent(
	ctx context.Context,
	profile onprem.AgentProfile,
) (created onprem.AgentProfile, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("begin agent creation: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "agent creation")
	created, err = scanAgent(tx.QueryRow(ctx, `
		INSERT INTO onprem_agents (
			agent_id, owner_membership_id, display_name, description, agent_type,
			status, directory_visible, created_at, updated_at, resource_version, creation_idempotency_key
		)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $8, 1, $9
		FROM onprem_memberships memberships
		JOIN onprem_users users ON users.user_id = memberships.user_id
		WHERE memberships.membership_id = $2 AND memberships.status = 'active'
		  AND users.identity_status = 'active'
		RETURNING agent_id, owner_membership_id,
		          (SELECT user_id FROM onprem_memberships WHERE membership_id = owner_membership_id),
		          display_name, description, agent_type, status, directory_visible,
		          created_at, updated_at, retired_at, resource_version, COALESCE(provisioned_by, '')
	`, profile.AgentID, profile.OwnerMembershipID, profile.DisplayName, profile.Description,
		profile.AgentType, profile.Status, profile.DirectoryVisible, profile.CreatedAt,
		profile.CreationIdempotencyKey))
	if isUniqueViolation(err) {
		if profile.CreationIdempotencyKey != "" {
			return s.resolveIdempotentAgentCreate(ctx, profile)
		}
		return onprem.AgentProfile{}, onprem.ErrAgentIDConflict
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.AgentProfile{}, onprem.ErrForbidden
	}
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("create postgres agent profile: %w", err)
	}
	if err := insertAuditEvent(ctx, tx, "human", profile.OwnerUserID, profile.OwnerMembershipID, "", "",
		"identity.agent.created", "agent", profile.AgentID, profile.CreatedAt); err != nil {
		return onprem.AgentProfile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("commit agent creation: %w", err)
	}
	return created, nil
}

func (s *RegistryStore) resolveIdempotentAgentCreate(
	ctx context.Context,
	requested onprem.AgentProfile,
) (onprem.AgentProfile, error) {
	created, err := scanAgent(s.pool.QueryRow(ctx, `
		SELECT agents.agent_id, agents.owner_membership_id, memberships.user_id,
		       agents.display_name, agents.description, agents.agent_type, agents.status,
		       agents.directory_visible, agents.created_at, agents.updated_at,
		       agents.retired_at, agents.resource_version, COALESCE(agents.provisioned_by, '')
		FROM onprem_agents agents
		JOIN onprem_memberships memberships ON memberships.membership_id = agents.owner_membership_id
		WHERE agents.owner_membership_id = $1 AND agents.creation_idempotency_key = $2
	`, requested.OwnerMembershipID, requested.CreationIdempotencyKey))
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.AgentProfile{}, onprem.ErrAgentIDConflict
	}
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("resolve idempotent agent creation: %w", err)
	}
	if created.AgentID != requested.AgentID || created.DisplayName != requested.DisplayName ||
		created.Description != requested.Description || created.AgentType != requested.AgentType ||
		created.DirectoryVisible != requested.DirectoryVisible {
		return onprem.AgentProfile{}, onprem.ErrIdempotencyConflict
	}
	return created, nil
}

func (s *RegistryStore) ListOwnedAgents(
	ctx context.Context,
	membershipID string,
	filter onprem.AgentFilter,
) ([]onprem.AgentProfile, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT agents.agent_id, agents.owner_membership_id, memberships.user_id,
		       agents.display_name, agents.description, agents.agent_type, agents.status,
		       agents.directory_visible, agents.created_at, agents.updated_at,
		       agents.retired_at, agents.resource_version, COALESCE(agents.provisioned_by, '')
		FROM onprem_agents agents
		JOIN onprem_memberships memberships ON memberships.membership_id = agents.owner_membership_id
		WHERE agents.owner_membership_id = $1
		  AND ($2 = '' OR agents.status = $2)
		  AND ($3 = '' OR agents.agent_id > $3)
		ORDER BY agents.agent_id
		LIMIT $4
	`, membershipID, filter.Status, filter.Cursor, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list postgres owned agents: %w", err)
	}
	return collectAgents(rows, "owned agents")
}

func (s *RegistryStore) GetOwnedAgent(
	ctx context.Context,
	membershipID string,
	agentID string,
) (onprem.AgentProfile, error) {
	profile, err := scanAgent(s.pool.QueryRow(ctx, `
		SELECT agents.agent_id, agents.owner_membership_id, memberships.user_id,
		       agents.display_name, agents.description, agents.agent_type, agents.status,
		       agents.directory_visible, agents.created_at, agents.updated_at,
		       agents.retired_at, agents.resource_version, COALESCE(agents.provisioned_by, '')
		FROM onprem_agents agents
		JOIN onprem_memberships memberships ON memberships.membership_id = agents.owner_membership_id
		WHERE agents.owner_membership_id = $1 AND agents.agent_id = $2
	`, membershipID, agentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.AgentProfile{}, onprem.ErrAgentNotFound
	}
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("get postgres owned agent: %w", err)
	}
	return profile, nil
}

func (s *RegistryStore) UpdateOwnedAgent(
	ctx context.Context,
	membershipID string,
	actor onprem.HumanPrincipal,
	profile onprem.AgentProfile,
) (updated onprem.AgentProfile, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("begin agent update: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "agent update")
	updated, err = scanAgent(tx.QueryRow(ctx, `
		UPDATE onprem_agents agents
		SET display_name = $3, description = $4, agent_type = $5, status = $6,
		    directory_visible = $7, updated_at = $8, retired_at = $9,
		    resource_version = $10
		WHERE agent_id = $1 AND owner_membership_id = $2
		  AND resource_version = $10 - 1 AND status <> 'retired'
		RETURNING agent_id, owner_membership_id,
		          (SELECT user_id FROM onprem_memberships WHERE membership_id = owner_membership_id),
		          display_name, description, agent_type, status, directory_visible,
		          created_at, updated_at, retired_at, resource_version, COALESCE(provisioned_by, '')
	`, profile.AgentID, membershipID, profile.DisplayName, profile.Description, profile.AgentType,
		profile.Status, profile.DirectoryVisible, profile.UpdatedAt, profile.RetiredAt, profile.ResourceVersion))
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.AgentProfile{}, onprem.ErrResourceVersionConflict
	}
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("update postgres owned agent: %w", err)
	}
	if profile.Status == onprem.AgentStatusSuspended || profile.Status == onprem.AgentStatusRetired {
		if _, err := tx.Exec(ctx, `
			UPDATE agent_credentials SET revoked_at = $2
			WHERE agent_id = $1 AND revoked_at IS NULL
		`, profile.AgentID, profile.UpdatedAt); err != nil {
			return onprem.AgentProfile{}, fmt.Errorf("revoke credentials for agent status: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE agent_enrollments SET revoked_at = $2
			WHERE agent_id = $1 AND consumed_at IS NULL AND revoked_at IS NULL
		`, profile.AgentID, profile.UpdatedAt); err != nil {
			return onprem.AgentProfile{}, fmt.Errorf("revoke enrollments for agent status: %w", err)
		}
	}
	if err := insertAuditEvent(ctx, tx, "human", actor.UserID, actor.MembershipID, "", "",
		"identity.agent.updated", "agent", profile.AgentID, profile.UpdatedAt); err != nil {
		return onprem.AgentProfile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("commit agent update: %w", err)
	}
	return updated, nil
}

func (s *RegistryStore) RetireOwnedAgent(
	ctx context.Context,
	membershipID string,
	actor onprem.HumanPrincipal,
	agentID string,
	resourceVersion int64,
	idempotencyKey string,
	now time.Time,
) (updated onprem.AgentProfile, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("begin agent retirement: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "agent retirement")
	if idempotencyKey != "" {
		replayed, replayErr := scanAgent(tx.QueryRow(ctx, `
			SELECT agents.agent_id, agents.owner_membership_id, memberships.user_id,
			       agents.display_name, agents.description, agents.agent_type, agents.status,
			       agents.directory_visible, agents.created_at, agents.updated_at,
			       agents.retired_at, agents.resource_version, COALESCE(agents.provisioned_by, '')
			FROM onprem_agents agents
			JOIN onprem_memberships memberships ON memberships.membership_id = agents.owner_membership_id
			WHERE agents.owner_membership_id = $1 AND agents.retire_idempotency_key = $2
		`, membershipID, idempotencyKey))
		if replayErr == nil {
			if replayed.AgentID != agentID {
				return onprem.AgentProfile{}, onprem.ErrIdempotencyConflict
			}
			if err := tx.Commit(ctx); err != nil {
				return onprem.AgentProfile{}, fmt.Errorf("commit agent retirement replay: %w", err)
			}
			return replayed, nil
		}
		if !errors.Is(replayErr, pgx.ErrNoRows) {
			return onprem.AgentProfile{}, fmt.Errorf("resolve agent retirement replay: %w", replayErr)
		}
	}
	updated, err = scanAgent(tx.QueryRow(ctx, `
		UPDATE onprem_agents agents
		SET status = 'retired', updated_at = $5, retired_at = $5,
		    resource_version = agents.resource_version + 1, retire_idempotency_key = $4
		WHERE agent_id = $1 AND owner_membership_id = $2
		  AND resource_version = $3 AND status <> 'retired'
		RETURNING agent_id, owner_membership_id,
		          (SELECT user_id FROM onprem_memberships WHERE membership_id = owner_membership_id),
		          display_name, description, agent_type, status, directory_visible,
		          created_at, updated_at, retired_at, resource_version, COALESCE(provisioned_by, '')
	`, agentID, membershipID, resourceVersion, idempotencyKey, now))
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.AgentProfile{}, onprem.ErrResourceVersionConflict
	}
	if isUniqueViolation(err) {
		return onprem.AgentProfile{}, onprem.ErrIdempotencyConflict
	}
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("retire postgres owned agent: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_credentials SET revoked_at = $2
		WHERE agent_id = $1 AND revoked_at IS NULL
	`, agentID, now); err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("revoke credentials for agent retirement: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_enrollments SET revoked_at = $2
		WHERE agent_id = $1 AND consumed_at IS NULL AND revoked_at IS NULL
	`, agentID, now); err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("revoke enrollments for agent retirement: %w", err)
	}
	if err := insertAuditEvent(ctx, tx, "human", actor.UserID, actor.MembershipID, "", "",
		"identity.agent.retired", "agent", agentID, now); err != nil {
		return onprem.AgentProfile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("commit agent retirement: %w", err)
	}
	return updated, nil
}

func (s *RegistryStore) TransferAgent(
	ctx context.Context,
	actor onprem.HumanPrincipal,
	agentID string,
	targetMembershipID string,
	resourceVersion int64,
	now time.Time,
) (updated onprem.AgentProfile, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("begin agent transfer: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "agent transfer")
	updated, err = scanAgent(tx.QueryRow(ctx, `
		UPDATE onprem_agents agents
		SET owner_membership_id = target.membership_id, updated_at = $4,
		    resource_version = agents.resource_version + 1
		FROM onprem_memberships target
		JOIN onprem_users users ON users.user_id = target.user_id
		WHERE agents.agent_id = $1 AND agents.resource_version = $3
		  AND agents.status <> 'retired' AND target.membership_id = $2
		  AND target.status = 'active' AND users.identity_status = 'active'
		RETURNING agents.agent_id, agents.owner_membership_id, target.user_id,
		          agents.display_name, agents.description, agents.agent_type, agents.status,
		          agents.directory_visible, agents.created_at, agents.updated_at,
		          agents.retired_at, agents.resource_version, COALESCE(agents.provisioned_by, '')
	`, agentID, targetMembershipID, resourceVersion, now))
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.AgentProfile{}, classifyTransferAgentConflict(
			ctx, tx, agentID, targetMembershipID, resourceVersion,
		)
	}
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("transfer postgres agent ownership: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_credentials SET revoked_at = $2
		WHERE agent_id = $1 AND revoked_at IS NULL
	`, agentID, now); err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("revoke transferred agent credentials: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_enrollments SET revoked_at = $2
		WHERE agent_id = $1 AND consumed_at IS NULL AND revoked_at IS NULL
	`, agentID, now); err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("revoke transferred agent enrollments: %w", err)
	}
	if err := insertAuditEvent(ctx, tx, "human", actor.UserID, actor.MembershipID, "", "",
		"identity.agent.transferred", "agent", agentID, now); err != nil {
		return onprem.AgentProfile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("commit agent transfer: %w", err)
	}
	return updated, nil
}

func classifyTransferAgentConflict(
	ctx context.Context,
	tx pgx.Tx,
	agentID string,
	targetMembershipID string,
	resourceVersion int64,
) error {
	var currentVersion int64
	var currentStatus onprem.AgentStatus
	err := tx.QueryRow(ctx, `
		SELECT resource_version, status FROM onprem_agents WHERE agent_id = $1
	`, agentID).Scan(&currentVersion, &currentStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.ErrAgentNotFound
	}
	if err != nil {
		return fmt.Errorf("classify agent transfer conflict: %w", err)
	}
	if currentVersion != resourceVersion {
		return onprem.ErrResourceVersionConflict
	}
	if currentStatus == onprem.AgentStatusRetired {
		return onprem.ErrInvalidStateTransition
	}
	var targetActive bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM onprem_memberships memberships
			JOIN onprem_users users ON users.user_id = memberships.user_id
			WHERE memberships.membership_id = $1 AND memberships.status = 'active'
			  AND users.identity_status = 'active'
		)
	`, targetMembershipID).Scan(&targetActive); err != nil {
		return fmt.Errorf("classify agent transfer target: %w", err)
	}
	if !targetActive {
		return onprem.ErrMembershipConflict
	}
	return onprem.ErrAgentConflict
}

func (s *RegistryStore) CreateOwnedEnrollment(
	ctx context.Context,
	membershipID string,
	record onprem.EnrollmentRecord,
) (returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin owned enrollment creation: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "owned enrollment creation")
	command, err := tx.Exec(ctx, `
		INSERT INTO agent_enrollments (
			enrollment_id, token_digest, user_id, membership_id, agent_id,
			credential_label, permissions, created_at, expires_at, credential_expires_at, digest_key_version
		)
		SELECT $1, $2, memberships.user_id, memberships.membership_id, agents.agent_id,
		       $5, $6, $7, $8, $9, $10
		FROM onprem_agents agents
		JOIN onprem_memberships memberships ON memberships.membership_id = agents.owner_membership_id
		JOIN onprem_users users ON users.user_id = memberships.user_id
		WHERE agents.agent_id = $3 AND memberships.membership_id = $4
		  AND agents.status = 'active' AND memberships.status = 'active'
		  AND users.identity_status = 'active'
	`, record.ID, record.TokenDigest[:], record.AgentID, membershipID, record.CredentialLabel,
		permissionStrings(record.Permissions), record.CreatedAt, record.ExpiresAt,
		record.CredentialExpiresAt, record.DigestKeyVersion)
	if err != nil {
		return fmt.Errorf("create postgres owned enrollment: %w", err)
	}
	if command.RowsAffected() != 1 {
		return onprem.ErrForbidden
	}
	if err := insertAuditEvent(ctx, tx, "human", record.UserID, membershipID, "", "",
		"identity.enrollment.created", "enrollment", record.ID, record.CreatedAt); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit owned enrollment creation: %w", err)
	}
	return nil
}

func (s *RegistryStore) CreateDeviceEnrollment(
	ctx context.Context,
	membershipID string,
	record onprem.EnrollmentRecord,
) (returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin device enrollment creation: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "device enrollment creation")
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
		return onprem.ErrForbidden
	}
	if err != nil {
		return fmt.Errorf("lock device enrollment membership: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_enrollments (
			enrollment_id, token_digest, user_id, membership_id, agent_id,
			credential_label, permissions, created_at, expires_at, credential_expires_at,
			digest_key_version, kind, grantable_permissions
		) VALUES ($1, $2, $3, $4, '', $5, $6, $7, $8, NULL, $9, 'device', $10)
	`, record.ID, record.TokenDigest[:], record.UserID, membershipID, record.CredentialLabel,
		permissionStrings(record.Permissions), record.CreatedAt, record.ExpiresAt,
		record.DigestKeyVersion, permissionStrings(record.GrantablePermissions)); err != nil {
		return fmt.Errorf("create postgres device enrollment: %w", err)
	}
	if err := insertAuditEvent(ctx, tx, "human", record.UserID, membershipID, "", "",
		"identity.enrollment.created", "enrollment", record.ID, record.CreatedAt); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit device enrollment creation: %w", err)
	}
	return nil
}

func (s *RegistryStore) ListOwnedEnrollments(
	ctx context.Context,
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
			           WHEN expires_at <= $3 THEN 'expired'
			           ELSE 'pending'
			       END AS status
			FROM agent_enrollments enrollments
			WHERE membership_id = $1 AND agent_id = $2
		) owned
		WHERE ($4 = '' OR status = $4) AND ($5 = '' OR enrollment_id > $5)
		ORDER BY enrollment_id
		LIMIT $6
	`, membershipID, agentID, now, filter.Status, filter.Cursor, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list postgres owned enrollments: %w", err)
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
			return nil, fmt.Errorf("scan postgres owned enrollment: %w", err)
		}
		metadata.Permissions = permissionsFromStrings(permissions)
		result = append(result, metadata)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres owned enrollments: %w", err)
	}
	return result, nil
}

func (s *RegistryStore) RevokeOwnedEnrollment(
	ctx context.Context,
	membershipID string,
	actor onprem.HumanPrincipal,
	agentID string,
	enrollmentID string,
	idempotencyKey string,
	now time.Time,
) (metadata onprem.AgentEnrollmentMetadata, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.AgentEnrollmentMetadata{}, fmt.Errorf("begin owned enrollment revocation: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "owned enrollment revocation")
	var permissions []string
	if idempotencyKey != "" {
		err = tx.QueryRow(ctx, `
			SELECT enrollment_id, agent_id, credential_label, permissions, 'revoked',
			       created_at, expires_at, credential_expires_at
			FROM agent_enrollments
			WHERE revoke_idempotency_actor_membership_id = $1 AND revoke_idempotency_key = $2
		`, actor.MembershipID, idempotencyKey).Scan(
			&metadata.EnrollmentID, &metadata.AgentID, &metadata.CredentialLabel, &permissions,
			&metadata.Status, &metadata.CreatedAt, &metadata.ExpiresAt, &metadata.CredentialExpiresAt,
		)
		if err == nil {
			if metadata.AgentID != agentID || metadata.EnrollmentID != enrollmentID {
				return onprem.AgentEnrollmentMetadata{}, onprem.ErrIdempotencyConflict
			}
			metadata.Permissions = permissionsFromStrings(permissions)
			if err := tx.Commit(ctx); err != nil {
				return onprem.AgentEnrollmentMetadata{}, fmt.Errorf("commit enrollment revocation replay: %w", err)
			}
			return metadata, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return onprem.AgentEnrollmentMetadata{}, fmt.Errorf("resolve enrollment revocation replay: %w", err)
		}
	}
	err = tx.QueryRow(ctx, `
		UPDATE agent_enrollments
		SET revoked_at = $4, revoke_idempotency_key = $5,
		    revoke_idempotency_actor_membership_id = $6
		WHERE enrollment_id = $1 AND membership_id = $2 AND agent_id = $3
		  AND consumed_at IS NULL AND revoked_at IS NULL
		RETURNING enrollment_id, agent_id, credential_label, permissions, 'revoked',
		          created_at, expires_at, credential_expires_at
	`, enrollmentID, membershipID, agentID, now, idempotencyKey, actor.MembershipID).Scan(
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
		return onprem.AgentEnrollmentMetadata{}, fmt.Errorf("revoke postgres owned enrollment: %w", err)
	}
	metadata.Permissions = permissionsFromStrings(permissions)
	if err := insertAuditEvent(ctx, tx, "human", actor.UserID, actor.MembershipID, "", "",
		"identity.enrollment.revoked", "enrollment", enrollmentID, now); err != nil {
		return onprem.AgentEnrollmentMetadata{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.AgentEnrollmentMetadata{}, fmt.Errorf("commit owned enrollment revocation: %w", err)
	}
	return metadata, nil
}

func (s *RegistryStore) ListOwnedCredentials(
	ctx context.Context,
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
			           WHEN expires_at IS NOT NULL AND expires_at <= $3 THEN 'expired'
			           ELSE 'active'
			       END AS status
			FROM agent_credentials credentials
			WHERE owner_membership_id = $1 AND agent_id = $2
		) owned
		WHERE ($4 = '' OR status = $4) AND ($5 = '' OR credential_id > $5)
		ORDER BY credential_id
		LIMIT $6
	`, membershipID, agentID, now, filter.Status, filter.Cursor, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list postgres owned credentials: %w", err)
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
			return nil, fmt.Errorf("scan postgres owned credential: %w", err)
		}
		metadata.Permissions = permissionsFromStrings(permissions)
		result = append(result, metadata)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres owned credentials: %w", err)
	}
	return result, nil
}

func (s *RegistryStore) RevokeOwnedCredential(
	ctx context.Context,
	membershipID string,
	actor onprem.HumanPrincipal,
	agentID string,
	credentialID string,
	idempotencyKey string,
	now time.Time,
) (metadata onprem.AgentCredentialMetadata, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.AgentCredentialMetadata{}, fmt.Errorf("begin owned credential revocation: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "owned credential revocation")
	var permissions []string
	if idempotencyKey != "" {
		err = tx.QueryRow(ctx, `
			SELECT credential_id, agent_id, label, permissions, created_at,
			       expires_at, revoked_at, last_used_at
			FROM agent_credentials
			WHERE revoke_idempotency_actor_membership_id = $1 AND revoke_idempotency_key = $2
		`, actor.MembershipID, idempotencyKey).Scan(
			&metadata.CredentialID, &metadata.AgentID, &metadata.Label, &permissions,
			&metadata.CreatedAt, &metadata.ExpiresAt, &metadata.RevokedAt, &metadata.LastUsedAt,
		)
		if err == nil {
			if metadata.AgentID != agentID || metadata.CredentialID != credentialID {
				return onprem.AgentCredentialMetadata{}, onprem.ErrIdempotencyConflict
			}
			metadata.Permissions = permissionsFromStrings(permissions)
			if err := tx.Commit(ctx); err != nil {
				return onprem.AgentCredentialMetadata{}, fmt.Errorf("commit credential revocation replay: %w", err)
			}
			return metadata, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return onprem.AgentCredentialMetadata{}, fmt.Errorf("resolve credential revocation replay: %w", err)
		}
	}
	err = tx.QueryRow(ctx, `
		UPDATE agent_credentials
		SET revoked_at = $4, revoke_idempotency_key = $5,
		    revoke_idempotency_actor_membership_id = $6
		WHERE credential_id = $1 AND owner_membership_id = $2 AND agent_id = $3
		  AND revoked_at IS NULL
		RETURNING credential_id, agent_id, label, permissions, created_at,
		          expires_at, revoked_at, last_used_at
	`, credentialID, membershipID, agentID, now, idempotencyKey, actor.MembershipID).Scan(
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
		return onprem.AgentCredentialMetadata{}, fmt.Errorf("revoke postgres owned credential: %w", err)
	}
	metadata.Permissions = permissionsFromStrings(permissions)
	if err := insertAuditEvent(ctx, tx, "human", actor.UserID, actor.MembershipID, "", "",
		"identity.credential.revoked", "credential", credentialID, now); err != nil {
		return onprem.AgentCredentialMetadata{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.AgentCredentialMetadata{}, fmt.Errorf("commit owned credential revocation: %w", err)
	}
	return metadata, nil
}

// RevokeDevice revokes a device credential and, in the same transaction,
// cascades to every agent credential the device provisioned via
// cascadeRevokeProvisionedCredentials (postgres/credentials.go). It mirrors
// RevokeOwnedCredential's idempotent-replay pattern but locks and branches on
// the current row state up front, since an already-revoked device must not
// re-run the cascade.
func (s *RegistryStore) RevokeDevice(
	ctx context.Context,
	actor onprem.HumanPrincipal,
	credentialID string,
	idempotencyKey string,
	now time.Time,
) (summary onprem.DeviceSummary, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.DeviceSummary{}, fmt.Errorf("begin device revocation: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "device revocation")

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
		FROM agent_credentials
		WHERE credential_id = $1 AND kind = 'device'
		FOR UPDATE
	`, credentialID).Scan(
		&deviceName, &createdByUserID, &createdByMembershipID, &createdAt, &revokedAt, &lastUsedAt,
		&grantablePermissions, &revokeIdempotencyKey, &revokeIdempotencyActorMembershipID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.DeviceSummary{}, onprem.ErrCredentialNotFound
	}
	if err != nil {
		return onprem.DeviceSummary{}, fmt.Errorf("lock postgres device credential: %w", err)
	}

	if revokedAt != nil {
		if idempotencyKey == "" || revokeIdempotencyKey != idempotencyKey ||
			revokeIdempotencyActorMembershipID != actor.MembershipID {
			return onprem.DeviceSummary{}, onprem.ErrIdempotencyConflict
		}
		summary, err = deviceSummaryWithProvisionedCount(ctx, tx, credentialID, deviceName, createdByUserID,
			createdByMembershipID, createdAt, revokedAt, lastUsedAt, grantablePermissions)
		if err != nil {
			return onprem.DeviceSummary{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return onprem.DeviceSummary{}, fmt.Errorf("commit device revocation replay: %w", err)
		}
		return summary, nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE agent_credentials
		SET revoked_at = $2, revoke_idempotency_key = $3, revoke_idempotency_actor_membership_id = $4
		WHERE credential_id = $1
	`, credentialID, now, idempotencyKey, actor.MembershipID); err != nil {
		if isUniqueViolation(err) {
			return onprem.DeviceSummary{}, onprem.ErrIdempotencyConflict
		}
		return onprem.DeviceSummary{}, fmt.Errorf("revoke postgres device credential: %w", err)
	}
	if err := insertAuditEvent(ctx, tx, "human", actor.UserID, actor.MembershipID, "", "",
		"identity.credential.revoked", "credential", credentialID, now); err != nil {
		return onprem.DeviceSummary{}, err
	}
	if _, err := cascadeRevokeProvisionedCredentials(ctx, tx, credentialID, now); err != nil {
		return onprem.DeviceSummary{}, err
	}
	revokedAt = &now
	summary, err = deviceSummaryWithProvisionedCount(ctx, tx, credentialID, deviceName, createdByUserID,
		createdByMembershipID, createdAt, revokedAt, lastUsedAt, grantablePermissions)
	if err != nil {
		return onprem.DeviceSummary{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.DeviceSummary{}, fmt.Errorf("commit device revocation: %w", err)
	}
	return summary, nil
}

// deviceSummaryWithProvisionedCount builds a DeviceSummary from a device
// credential's already-locked row fields plus a fresh count of the agents it
// still actively provisions (0 immediately after a fresh revoke, since the
// cascade just ran in the same transaction).
func deviceSummaryWithProvisionedCount(
	ctx context.Context,
	tx pgx.Tx,
	credentialID, deviceName, userID, membershipID string,
	createdAt time.Time,
	revokedAt, lastUsedAt *time.Time,
	grantablePermissions []string,
) (onprem.DeviceSummary, error) {
	var count int64
	if err := tx.QueryRow(ctx, `
		SELECT count(DISTINCT agent_id) FROM agent_credentials
		WHERE provisioned_by = $1 AND revoked_at IS NULL
	`, credentialID).Scan(&count); err != nil {
		return onprem.DeviceSummary{}, fmt.Errorf("count postgres active provisioned agents: %w", err)
	}
	return onprem.DeviceSummary{
		CredentialID: credentialID, DeviceName: deviceName,
		CreatedByUserID: userID, CreatedByMembershipID: membershipID,
		CreatedAt: createdAt, RevokedAt: revokedAt, LastUsedAt: lastUsedAt,
		GrantablePermissions:  permissionsFromStrings(grantablePermissions),
		ProvisionedAgentCount: count,
	}, nil
}

// ListDevices returns the admin device-management listing (device-kind
// credentials only), newest first. Pagination is a created_at-DESC keyset
// cursor tie-broken by credential_id ascending, matching the
// agent_credentials_device_kind_idx partial index added by migration 019;
// the opaque cursor format is "<created_at RFC3339Nano>|<credential_id>",
// built by the handler layer via onprem.EncodeDeviceCursor and parsed here
// by decodeDeviceCursor.
func (s *RegistryStore) ListDevices(
	ctx context.Context,
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
		       (SELECT count(DISTINCT p.agent_id) FROM agent_credentials p
		        WHERE p.provisioned_by = credentials.credential_id AND p.revoked_at IS NULL)
		FROM agent_credentials credentials
		WHERE credentials.kind = 'device'
		  AND ($1 = '' OR ($1 = 'active' AND credentials.revoked_at IS NULL)
		               OR ($1 = 'revoked' AND credentials.revoked_at IS NOT NULL))
		  AND (NOT $2 OR credentials.created_at < $3
		       OR (credentials.created_at = $3 AND credentials.credential_id > $4))
		ORDER BY credentials.created_at DESC, credentials.credential_id
		LIMIT $5
	`, filter.Status, hasCursor, cursorTime, cursorID, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list postgres devices: %w", err)
	}
	defer rows.Close()
	result := make([]onprem.DeviceSummary, 0)
	for rows.Next() {
		summary, err := scanDeviceSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("scan postgres device: %w", err)
		}
		result = append(result, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres devices: %w", err)
	}
	return result, nil
}

// GetDevice returns a device credential's summary plus every credential row
// it has provisioned (including revoked history, via the Task 6 query shared
// with CredentialStore.ListDeviceProvisionedAgents). A credential ID that
// does not name an active-or-revoked device-kind row returns
// ErrCredentialNotFound, matching RevokeDevice's kind check.
func (s *RegistryStore) GetDevice(ctx context.Context, credentialID string) (onprem.DeviceDetail, error) {
	summary, err := scanDeviceSummary(s.pool.QueryRow(ctx, `
		SELECT credentials.credential_id, credentials.label, credentials.user_id,
		       credentials.owner_membership_id, credentials.created_at, credentials.revoked_at,
		       credentials.last_used_at, credentials.grantable_permissions,
		       (SELECT count(DISTINCT p.agent_id) FROM agent_credentials p
		        WHERE p.provisioned_by = credentials.credential_id AND p.revoked_at IS NULL)
		FROM agent_credentials credentials
		WHERE credentials.credential_id = $1 AND credentials.kind = 'device'
	`, credentialID))
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.DeviceDetail{}, onprem.ErrCredentialNotFound
	}
	if err != nil {
		return onprem.DeviceDetail{}, fmt.Errorf("get postgres device: %w", err)
	}
	agents, err := listDeviceProvisionedAgents(ctx, s.pool, credentialID)
	if err != nil {
		return onprem.DeviceDetail{}, err
	}
	return onprem.DeviceDetail{Device: summary, Agents: agents}, nil
}

func scanDeviceSummary(scanner agentRowScanner) (onprem.DeviceSummary, error) {
	var summary onprem.DeviceSummary
	var grantablePermissions []string
	err := scanner.Scan(
		&summary.CredentialID, &summary.DeviceName, &summary.CreatedByUserID,
		&summary.CreatedByMembershipID, &summary.CreatedAt, &summary.RevokedAt,
		&summary.LastUsedAt, &grantablePermissions, &summary.ProvisionedAgentCount,
	)
	summary.GrantablePermissions = permissionsFromStrings(grantablePermissions)
	return summary, err
}

// decodeDeviceCursor parses ListDevices' keyset cursor, which the handler
// layer builds via onprem.EncodeDeviceCursor as
// "<created_at RFC3339Nano>|<credential_id>". credential_id never contains
// "|" (device credential IDs are opaque generated tokens), so splitting on
// the first "|" is unambiguous.
func decodeDeviceCursor(cursor string) (time.Time, string, error) {
	separator := strings.IndexByte(cursor, '|')
	if separator < 0 {
		return time.Time{}, "", fmt.Errorf("malformed device cursor")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, cursor[:separator])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("malformed device cursor timestamp: %w", err)
	}
	return createdAt, cursor[separator+1:], nil
}

func (s *RegistryStore) ListDirectoryAgents(
	ctx context.Context,
	filter onprem.AgentFilter,
	now time.Time,
) ([]onprem.AgentProfile, error) {
	rows, err := s.pool.Query(ctx, directoryAgentQuery+`
		  AND ($2 = '' OR agents.agent_id > $2)
		  AND ($3 = '' OR agents.agent_id ILIKE '%' || $3 || '%'
		       OR agents.display_name ILIKE '%' || $3 || '%'
		       OR agents.description ILIKE '%' || $3 || '%'
		       OR agents.agent_type ILIKE '%' || $3 || '%')
		ORDER BY agents.agent_id
		LIMIT $4
	`, now, filter.Cursor, filter.Query, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list postgres directory agents: %w", err)
	}
	return collectAgents(rows, "directory agents")
}

func (s *RegistryStore) GetDirectoryAgent(
	ctx context.Context,
	agentID string,
	now time.Time,
) (onprem.AgentProfile, error) {
	profile, err := scanAgent(s.pool.QueryRow(ctx, directoryAgentQuery+" AND agents.agent_id = $2", now, agentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.AgentProfile{}, onprem.ErrAgentNotFound
	}
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("get postgres directory agent: %w", err)
	}
	return profile, nil
}

func (s *RegistryStore) ListAdminAgents(
	ctx context.Context,
	filter onprem.AgentFilter,
) ([]onprem.AgentProfile, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT agents.agent_id, agents.owner_membership_id, memberships.user_id,
		       agents.display_name, agents.description, agents.agent_type, agents.status,
		       agents.directory_visible, agents.created_at, agents.updated_at,
		       agents.retired_at, agents.resource_version, COALESCE(agents.provisioned_by, '')
		FROM onprem_agents agents
		JOIN onprem_memberships memberships ON memberships.membership_id = agents.owner_membership_id
		WHERE ($1 = '' OR agents.owner_membership_id = $1)
		  AND ($2 = '' OR agents.status = $2)
		  AND ($3 = '' OR agents.agent_id > $3)
		  AND ($4 = '' OR agents.agent_id ILIKE '%' || $4 || '%'
		       OR agents.display_name ILIKE '%' || $4 || '%'
		       OR agents.description ILIKE '%' || $4 || '%')
		ORDER BY agents.agent_id
		LIMIT $5
	`, filter.OwnerMembershipID, filter.Status, filter.Cursor, filter.Query, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list postgres admin agents: %w", err)
	}
	return collectAgents(rows, "admin agents")
}

func (s *RegistryStore) GetAdminAgent(ctx context.Context, agentID string) (onprem.AgentProfile, error) {
	profile, err := scanAgent(s.pool.QueryRow(ctx, `
		SELECT agents.agent_id, agents.owner_membership_id, memberships.user_id,
		       agents.display_name, agents.description, agents.agent_type, agents.status,
		       agents.directory_visible, agents.created_at, agents.updated_at,
		       agents.retired_at, agents.resource_version, COALESCE(agents.provisioned_by, '')
		FROM onprem_agents agents
		JOIN onprem_memberships memberships ON memberships.membership_id = agents.owner_membership_id
		WHERE agents.agent_id = $1
	`, agentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.AgentProfile{}, onprem.ErrAgentNotFound
	}
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("get postgres admin agent: %w", err)
	}
	return profile, nil
}

const directoryAgentQuery = `
	SELECT agents.agent_id, agents.owner_membership_id, memberships.user_id,
	       agents.display_name, agents.description, agents.agent_type, agents.status,
	       agents.directory_visible, agents.created_at, agents.updated_at,
	       agents.retired_at, agents.resource_version, COALESCE(agents.provisioned_by, '')
	FROM onprem_agents agents
	JOIN onprem_memberships memberships ON memberships.membership_id = agents.owner_membership_id
	JOIN onprem_users users ON users.user_id = memberships.user_id
	WHERE agents.status = 'active' AND agents.directory_visible
	  AND memberships.status = 'active'
	  AND users.identity_status IN ('active', 'unclaimed')
	  AND EXISTS (
	      SELECT 1 FROM agent_credentials credentials
	      WHERE credentials.agent_id = agents.agent_id
	        AND credentials.owner_membership_id = agents.owner_membership_id
	        AND credentials.revoked_at IS NULL
	        AND (credentials.expires_at IS NULL OR credentials.expires_at > $1)
	        AND 'channel_receive' = ANY(credentials.permissions)
	  )`

func collectAgents(rows pgx.Rows, operation string) ([]onprem.AgentProfile, error) {
	defer rows.Close()
	profiles := make([]onprem.AgentProfile, 0)
	for rows.Next() {
		profile, err := scanAgent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan postgres %s: %w", operation, err)
		}
		profiles = append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres %s: %w", operation, err)
	}
	return profiles, nil
}

func scanAgent(scanner agentRowScanner) (onprem.AgentProfile, error) {
	var profile onprem.AgentProfile
	err := scanner.Scan(
		&profile.AgentID, &profile.OwnerMembershipID, &profile.OwnerUserID,
		&profile.DisplayName, &profile.Description, &profile.AgentType, &profile.Status,
		&profile.DirectoryVisible, &profile.CreatedAt, &profile.UpdatedAt,
		&profile.RetiredAt, &profile.ResourceVersion, &profile.ProvisionedBy,
	)
	return profile, err
}
