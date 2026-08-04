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

// SaaSRegistryStore is the postgres adapter for the per-team agent registry
// (team_agents). It mirrors the on-prem registry queries with every read
// and write filtered by team_id.
type SaaSRegistryStore struct {
	pool *pgxpool.Pool
}

var _ saas.TeamRegistryStore = (*SaaSRegistryStore)(nil)

// teamAgentColumns is the shared select list for team_agents joined to the
// owner's membership; unlike on-prem agents there is no provisioned_by
// (devices are an on-prem-only concept).
const teamAgentColumns = `
	agents.agent_id, agents.owner_membership_id, memberships.user_id,
	agents.display_name, agents.description, agents.agent_type, agents.status,
	agents.directory_visible, agents.created_at, agents.updated_at,
	agents.retired_at, agents.resource_version`

func (s *SaaSRegistryStore) CreateAgent(
	ctx context.Context,
	teamID string,
	profile onprem.AgentProfile,
) (created onprem.AgentProfile, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("begin team agent creation: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "team agent creation")
	created, err = scanTeamAgent(tx.QueryRow(ctx, `
		INSERT INTO team_agents (
			agent_id, team_id, owner_membership_id, display_name, description, agent_type,
			status, directory_visible, created_at, updated_at, resource_version, creation_idempotency_key
		)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $9, 1, $10
		FROM team_memberships memberships
		JOIN onprem_users users ON users.user_id = memberships.user_id
		WHERE memberships.membership_id = $3 AND memberships.team_id = $2
		  AND memberships.status = 'active' AND users.identity_status = 'active'
		RETURNING agent_id, owner_membership_id,
		          (SELECT user_id FROM team_memberships WHERE membership_id = owner_membership_id),
		          display_name, description, agent_type, status, directory_visible,
		          created_at, updated_at, retired_at, resource_version
	`, profile.AgentID, teamID, profile.OwnerMembershipID, profile.DisplayName, profile.Description,
		profile.AgentType, profile.Status, profile.DirectoryVisible, profile.CreatedAt,
		profile.CreationIdempotencyKey))
	if isUniqueViolation(err) {
		if profile.CreationIdempotencyKey != "" {
			return s.resolveIdempotentTeamAgentCreate(ctx, teamID, profile)
		}
		return onprem.AgentProfile{}, onprem.ErrAgentIDConflict
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.AgentProfile{}, onprem.ErrForbidden
	}
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("create postgres team agent profile: %w", err)
	}
	if err := insertScopedAuditEvent(ctx, tx, teamID, "human", profile.OwnerUserID, profile.OwnerMembershipID,
		"", "", "identity.agent.created", "agent", profile.AgentID, profile.CreatedAt); err != nil {
		return onprem.AgentProfile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("commit team agent creation: %w", err)
	}
	return created, nil
}

func (s *SaaSRegistryStore) resolveIdempotentTeamAgentCreate(
	ctx context.Context,
	teamID string,
	requested onprem.AgentProfile,
) (onprem.AgentProfile, error) {
	created, err := scanTeamAgent(s.pool.QueryRow(ctx, `
		SELECT `+teamAgentColumns+`
		FROM team_agents agents
		JOIN team_memberships memberships ON memberships.membership_id = agents.owner_membership_id
		WHERE agents.team_id = $1 AND agents.owner_membership_id = $2
		  AND agents.creation_idempotency_key = $3
	`, teamID, requested.OwnerMembershipID, requested.CreationIdempotencyKey))
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.AgentProfile{}, onprem.ErrAgentIDConflict
	}
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("resolve idempotent team agent creation: %w", err)
	}
	if created.AgentID != requested.AgentID || created.DisplayName != requested.DisplayName ||
		created.Description != requested.Description || created.AgentType != requested.AgentType ||
		created.DirectoryVisible != requested.DirectoryVisible {
		return onprem.AgentProfile{}, onprem.ErrIdempotencyConflict
	}
	return created, nil
}

func (s *SaaSRegistryStore) ListOwnedAgents(
	ctx context.Context,
	teamID string,
	membershipID string,
	filter onprem.AgentFilter,
) ([]onprem.AgentProfile, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+teamAgentColumns+`
		FROM team_agents agents
		JOIN team_memberships memberships ON memberships.membership_id = agents.owner_membership_id
		WHERE agents.team_id = $1 AND agents.owner_membership_id = $2
		  AND ($3 = '' OR agents.status = $3)
		  AND ($4 = '' OR agents.agent_id > $4)
		ORDER BY agents.agent_id
		LIMIT $5
	`, teamID, membershipID, filter.Status, filter.Cursor, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list postgres team owned agents: %w", err)
	}
	return collectTeamAgents(rows, "team owned agents")
}

func (s *SaaSRegistryStore) GetOwnedAgent(
	ctx context.Context,
	teamID string,
	membershipID string,
	agentID string,
) (onprem.AgentProfile, error) {
	profile, err := scanTeamAgent(s.pool.QueryRow(ctx, `
		SELECT `+teamAgentColumns+`
		FROM team_agents agents
		JOIN team_memberships memberships ON memberships.membership_id = agents.owner_membership_id
		WHERE agents.team_id = $1 AND agents.owner_membership_id = $2 AND agents.agent_id = $3
	`, teamID, membershipID, agentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.AgentProfile{}, onprem.ErrAgentNotFound
	}
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("get postgres team owned agent: %w", err)
	}
	return profile, nil
}

func (s *SaaSRegistryStore) UpdateOwnedAgent(
	ctx context.Context,
	teamID string,
	membershipID string,
	actor onprem.HumanPrincipal,
	profile onprem.AgentProfile,
) (updated onprem.AgentProfile, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("begin team agent update: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "team agent update")
	updated, err = scanTeamAgent(tx.QueryRow(ctx, `
		UPDATE team_agents agents
		SET display_name = $4, description = $5, agent_type = $6, status = $7,
		    directory_visible = $8, updated_at = $9, retired_at = $10,
		    resource_version = $11
		WHERE agent_id = $1 AND team_id = $2 AND owner_membership_id = $3
		  AND resource_version = $11 - 1 AND status <> 'retired'
		RETURNING agent_id, owner_membership_id,
		          (SELECT user_id FROM team_memberships WHERE membership_id = owner_membership_id),
		          display_name, description, agent_type, status, directory_visible,
		          created_at, updated_at, retired_at, resource_version
	`, profile.AgentID, teamID, membershipID, profile.DisplayName, profile.Description,
		profile.AgentType, profile.Status, profile.DirectoryVisible, profile.UpdatedAt,
		profile.RetiredAt, profile.ResourceVersion))
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.AgentProfile{}, classifyTeamOwnedAgentConflict(
			ctx, tx, teamID, membershipID, profile.AgentID, profile.ResourceVersion-1,
		)
	}
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("update postgres team owned agent: %w", err)
	}
	if profile.Status == onprem.AgentStatusSuspended || profile.Status == onprem.AgentStatusRetired {
		if err := revokeTeamAgentArtifacts(ctx, tx, profile.AgentID, profile.UpdatedAt); err != nil {
			return onprem.AgentProfile{}, err
		}
	}
	if err := insertScopedAuditEvent(ctx, tx, teamID, "human", actor.UserID, actor.MembershipID, "", "",
		"identity.agent.updated", "agent", profile.AgentID, profile.UpdatedAt); err != nil {
		return onprem.AgentProfile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("commit team agent update: %w", err)
	}
	return updated, nil
}

func (s *SaaSRegistryStore) RetireOwnedAgent(
	ctx context.Context,
	teamID string,
	membershipID string,
	actor onprem.HumanPrincipal,
	agentID string,
	resourceVersion int64,
	idempotencyKey string,
	now time.Time,
) (updated onprem.AgentProfile, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("begin team agent retirement: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "team agent retirement")
	if idempotencyKey != "" {
		replayed, replayErr := scanTeamAgent(tx.QueryRow(ctx, `
			SELECT `+teamAgentColumns+`
			FROM team_agents agents
			JOIN team_memberships memberships ON memberships.membership_id = agents.owner_membership_id
			WHERE agents.team_id = $1 AND agents.owner_membership_id = $2
			  AND agents.retire_idempotency_key = $3
		`, teamID, membershipID, idempotencyKey))
		if replayErr == nil {
			if replayed.AgentID != agentID {
				return onprem.AgentProfile{}, onprem.ErrIdempotencyConflict
			}
			if err := tx.Commit(ctx); err != nil {
				return onprem.AgentProfile{}, fmt.Errorf("commit team agent retirement replay: %w", err)
			}
			return replayed, nil
		}
		if !errors.Is(replayErr, pgx.ErrNoRows) {
			return onprem.AgentProfile{}, fmt.Errorf("resolve team agent retirement replay: %w", replayErr)
		}
	}
	updated, err = scanTeamAgent(tx.QueryRow(ctx, `
		UPDATE team_agents agents
		SET status = 'retired', updated_at = $6, retired_at = $6,
		    resource_version = agents.resource_version + 1, retire_idempotency_key = $5
		WHERE agent_id = $1 AND team_id = $2 AND owner_membership_id = $3
		  AND resource_version = $4 AND status <> 'retired'
		RETURNING agent_id, owner_membership_id,
		          (SELECT user_id FROM team_memberships WHERE membership_id = owner_membership_id),
		          display_name, description, agent_type, status, directory_visible,
		          created_at, updated_at, retired_at, resource_version
	`, agentID, teamID, membershipID, resourceVersion, idempotencyKey, now))
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.AgentProfile{}, classifyTeamOwnedAgentConflict(ctx, tx, teamID, membershipID, agentID, resourceVersion)
	}
	if isUniqueViolation(err) {
		return onprem.AgentProfile{}, onprem.ErrIdempotencyConflict
	}
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("retire postgres team owned agent: %w", err)
	}
	if err := revokeTeamAgentArtifacts(ctx, tx, agentID, now); err != nil {
		return onprem.AgentProfile{}, err
	}
	if err := insertScopedAuditEvent(ctx, tx, teamID, "human", actor.UserID, actor.MembershipID, "", "",
		"identity.agent.retired", "agent", agentID, now); err != nil {
		return onprem.AgentProfile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("commit team agent retirement: %w", err)
	}
	return updated, nil
}

// revokeTeamAgentArtifacts revokes every active credential and pending
// enrollment of one team agent, mirroring the on-prem agent-status cascade.
func revokeTeamAgentArtifacts(ctx context.Context, tx pgx.Tx, agentID string, now time.Time) error {
	if _, err := tx.Exec(ctx, `
		UPDATE team_agent_credentials SET revoked_at = $2
		WHERE agent_id = $1 AND revoked_at IS NULL
	`, agentID, now); err != nil {
		return fmt.Errorf("revoke credentials for team agent status: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE team_agent_enrollments SET revoked_at = $2
		WHERE agent_id = $1 AND consumed_at IS NULL AND revoked_at IS NULL
	`, agentID, now); err != nil {
		return fmt.Errorf("revoke enrollments for team agent status: %w", err)
	}
	return nil
}

// classifyTeamOwnedAgentConflict disambiguates a zero-row owner-scoped team
// agent mutation, mirroring the on-prem classifier: missing or foreign is
// ErrAgentNotFound, stale expectation is ErrResourceVersionConflict, and an
// already-retired agent is ErrInvalidStateTransition.
func classifyTeamOwnedAgentConflict(
	ctx context.Context,
	tx pgx.Tx,
	teamID string,
	membershipID string,
	agentID string,
	expectedVersion int64,
) error {
	var currentVersion int64
	var currentStatus onprem.AgentStatus
	err := tx.QueryRow(ctx, `
		SELECT resource_version, status FROM team_agents
		WHERE agent_id = $1 AND team_id = $2 AND owner_membership_id = $3
	`, agentID, teamID, membershipID).Scan(&currentVersion, &currentStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.ErrAgentNotFound
	}
	if err != nil {
		return fmt.Errorf("classify team owned agent conflict: %w", err)
	}
	if currentVersion != expectedVersion {
		return onprem.ErrResourceVersionConflict
	}
	if currentStatus == onprem.AgentStatusRetired {
		return onprem.ErrInvalidStateTransition
	}
	return onprem.ErrAgentConflict
}

func collectTeamAgents(rows pgx.Rows, operation string) ([]onprem.AgentProfile, error) {
	defer rows.Close()
	profiles := make([]onprem.AgentProfile, 0)
	for rows.Next() {
		profile, err := scanTeamAgent(rows)
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

func scanTeamAgent(scanner agentRowScanner) (onprem.AgentProfile, error) {
	var profile onprem.AgentProfile
	err := scanner.Scan(
		&profile.AgentID, &profile.OwnerMembershipID, &profile.OwnerUserID,
		&profile.DisplayName, &profile.Description, &profile.AgentType, &profile.Status,
		&profile.DirectoryVisible, &profile.CreatedAt, &profile.UpdatedAt,
		&profile.RetiredAt, &profile.ResourceVersion,
	)
	return profile, err
}
