package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/deployment/saas"
)

// SaaSIdentityStore is the postgres adapter for the multi-team SaaS control
// plane: teams, team memberships, team invitations, and team human
// sessions. team_id IS the scope_id; every query filters by it.
type SaaSIdentityStore struct {
	pool *pgxpool.Pool
}

var (
	_ saas.TeamStore           = (*SaaSIdentityStore)(nil)
	_ saas.TeamInvitationStore = (*SaaSIdentityStore)(nil)
)

func (s *SaaSIdentityStore) CreateTeam(
	ctx context.Context,
	team saas.Team,
	ownerMembershipID string,
	idempotencyKey string,
	now time.Time,
) (created saas.Team, owner onprem.Member, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return saas.Team{}, onprem.Member{}, fmt.Errorf("begin team creation: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "team creation")
	if idempotencyKey != "" {
		replayedTeam, replayedOwner, found, err := findTeamCreationReplay(ctx, tx, team.CreatedByUserID, idempotencyKey)
		if err != nil {
			return saas.Team{}, onprem.Member{}, err
		}
		if found {
			if replayedTeam.Name != team.Name || replayedTeam.Slug != team.Slug {
				return saas.Team{}, onprem.Member{}, onprem.ErrIdempotencyConflict
			}
			if err := tx.Commit(ctx); err != nil {
				return saas.Team{}, onprem.Member{}, fmt.Errorf("commit team creation replay: %w", err)
			}
			return replayedTeam, replayedOwner, nil
		}
	}
	created, err = scanTeam(tx.QueryRow(ctx, `
		INSERT INTO teams (team_id, name, slug, created_by_user_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)
		RETURNING team_id, name, slug, created_by_user_id, created_at, updated_at, resource_version
	`, team.TeamID, team.Name, team.Slug, team.CreatedByUserID, now))
	if isUniqueViolation(err) {
		// The failed insert aborted the transaction, so the classifier
		// runs on the pool; the deferred rollback discards the tx.
		return saas.Team{}, onprem.Member{}, classifyTeamInsertConflict(ctx, s.pool, team.Slug)
	}
	if err != nil {
		return saas.Team{}, onprem.Member{}, fmt.Errorf("create postgres team: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO team_memberships (
			membership_id, team_id, user_id, role, status, create_idempotency_key, joined_at, updated_at
		) VALUES ($1, $2, $3, 'owner', 'active', $4, $5, $5)
	`, ownerMembershipID, created.TeamID, team.CreatedByUserID, idempotencyKey, now)
	if isUniqueViolation(err) {
		return saas.Team{}, onprem.Member{}, onprem.ErrIdempotencyConflict
	}
	if err != nil {
		return saas.Team{}, onprem.Member{}, fmt.Errorf("create postgres team owner membership: %w", err)
	}
	if err := insertScopedAuditEvent(ctx, tx, created.TeamID, "human", team.CreatedByUserID, ownerMembershipID,
		"", "", "identity.team.created", "team", created.TeamID, now); err != nil {
		return saas.Team{}, onprem.Member{}, err
	}
	owner, err = queryTeamMember(ctx, tx, created.TeamID, ownerMembershipID)
	if err != nil {
		return saas.Team{}, onprem.Member{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return saas.Team{}, onprem.Member{}, fmt.Errorf("commit team creation: %w", err)
	}
	return created, owner, nil
}

// findTeamCreationReplay looks up a previous CreateTeam call by its
// (user, create idempotency key) pair so a replayed create returns the
// original team instead of failing on the slug uniqueness constraint.
func findTeamCreationReplay(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	idempotencyKey string,
) (saas.Team, onprem.Member, bool, error) {
	var team saas.Team
	var membershipID string
	err := tx.QueryRow(ctx, `
		SELECT teams.team_id, teams.name, teams.slug, teams.created_by_user_id,
		       teams.created_at, teams.updated_at, teams.resource_version,
		       memberships.membership_id
		FROM team_memberships memberships
		JOIN teams ON teams.team_id = memberships.team_id
		WHERE memberships.user_id = $1 AND memberships.create_idempotency_key = $2
	`, userID, idempotencyKey).Scan(
		&team.TeamID, &team.Name, &team.Slug, &team.CreatedByUserID,
		&team.CreatedAt, &team.UpdatedAt, &team.ResourceVersion, &membershipID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return saas.Team{}, onprem.Member{}, false, nil
	}
	if err != nil {
		return saas.Team{}, onprem.Member{}, false, fmt.Errorf("resolve team creation replay: %w", err)
	}
	owner, err := queryTeamMember(ctx, tx, team.TeamID, membershipID)
	if err != nil {
		return saas.Team{}, onprem.Member{}, false, err
	}
	return team, owner, true, nil
}

// classifyTeamInsertConflict distinguishes a taken slug from an idempotency
// replay race (a concurrent CreateTeam with the same key committed first).
// It runs outside the failed transaction, which Postgres has already
// aborted.
func classifyTeamInsertConflict(ctx context.Context, pool *pgxpool.Pool, slug string) error {
	var slugTaken bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM teams WHERE slug = $1)
	`, slug).Scan(&slugTaken); err != nil {
		return fmt.Errorf("classify team insert conflict: %w", err)
	}
	if slugTaken {
		return saas.ErrTeamSlugConflict
	}
	return onprem.ErrIdempotencyConflict
}

func (s *SaaSIdentityStore) GetTeam(ctx context.Context, teamID string) (saas.Team, error) {
	team, err := scanTeam(s.pool.QueryRow(ctx, `
		SELECT team_id, name, slug, created_by_user_id, created_at, updated_at, resource_version
		FROM teams WHERE team_id = $1
	`, teamID))
	if errors.Is(err, pgx.ErrNoRows) {
		return saas.Team{}, saas.ErrTeamNotFound
	}
	if err != nil {
		return saas.Team{}, fmt.Errorf("get postgres team: %w", err)
	}
	return team, nil
}

func (s *SaaSIdentityStore) ListTeamSummaries(
	ctx context.Context,
	userID string,
) ([]saas.TeamSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT teams.team_id, teams.name, teams.slug, teams.created_by_user_id,
		       teams.created_at, teams.updated_at, teams.resource_version,
		       memberships.membership_id, memberships.role
		FROM team_memberships memberships
		JOIN teams ON teams.team_id = memberships.team_id
		WHERE memberships.user_id = $1 AND memberships.status = 'active'
		ORDER BY memberships.joined_at DESC, memberships.membership_id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list postgres team summaries: %w", err)
	}
	defer rows.Close()
	result := make([]saas.TeamSummary, 0)
	for rows.Next() {
		var summary saas.TeamSummary
		if err := rows.Scan(
			&summary.Team.TeamID, &summary.Team.Name, &summary.Team.Slug, &summary.Team.CreatedByUserID,
			&summary.Team.CreatedAt, &summary.Team.UpdatedAt, &summary.Team.ResourceVersion,
			&summary.MembershipID, &summary.Role,
		); err != nil {
			return nil, fmt.Errorf("scan postgres team summary: %w", err)
		}
		result = append(result, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres team summaries: %w", err)
	}
	return result, nil
}

func (s *SaaSIdentityStore) CreateUserSession(
	ctx context.Context,
	identity onprem.ExternalIdentity,
	record saas.TeamSessionRecord,
) (principal onprem.HumanPrincipal, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("begin team human login: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "team human login")
	userID := externalUserID(identity.Issuer, identity.Subject)
	err = tx.QueryRow(ctx, `
		INSERT INTO onprem_users (
			user_id, identity_issuer, identity_subject, email, email_verified,
			display_name, identity_status, created_at, updated_at, last_login_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'active', $7, $7, $7)
		ON CONFLICT (identity_issuer, identity_subject)
		WHERE identity_issuer IS NOT NULL AND identity_subject IS NOT NULL
		DO UPDATE SET email = EXCLUDED.email, email_verified = EXCLUDED.email_verified,
		              display_name = EXCLUDED.display_name, updated_at = EXCLUDED.updated_at,
		              last_login_at = EXCLUDED.last_login_at
		RETURNING user_id
	`, userID, identity.Issuer, identity.Subject, nullableText(identity.Email), identity.EmailVerified,
		identity.DisplayName, record.CreatedAt).Scan(&principal.UserID)
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("upsert OIDC user: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO team_human_sessions (
			session_id, user_id, secret_digest, digest_key_version, current_team_id,
			created_at, expires_at, last_seen_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $6)
	`, record.SessionID, principal.UserID, record.SecretDigest[:], record.DigestKeyVersion,
		nullableText(record.CurrentTeamID), record.CreatedAt, record.ExpiresAt)
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("save team human session: %w", err)
	}
	principal, err = queryTeamPrincipal(ctx, tx, principal.UserID, record.CurrentTeamID, record.SessionID)
	if err != nil {
		return onprem.HumanPrincipal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("commit team human login: %w", err)
	}
	return principal, nil
}

func (s *SaaSIdentityStore) ResolveSession(
	ctx context.Context,
	sessionID string,
	digest onprem.Digest,
	now time.Time,
) (onprem.HumanPrincipal, error) {
	row := s.pool.QueryRow(ctx, `
		WITH resolved AS (
			UPDATE team_human_sessions
			SET last_seen_at = $3
			WHERE session_id = $1 AND secret_digest = $2 AND revoked_at IS NULL AND expires_at > $3
			RETURNING session_id, user_id, current_team_id
		)
		SELECT users.user_id, COALESCE(users.email, ''), users.email_verified, resolved.session_id,
		       COALESCE(memberships.membership_id, ''), COALESCE(memberships.role, ''),
		       COALESCE(memberships.status, ''), COALESCE(resolved.current_team_id, '')
		FROM resolved
		JOIN onprem_users users ON users.user_id = resolved.user_id
		LEFT JOIN team_memberships memberships
		  ON memberships.user_id = users.user_id
		 AND memberships.team_id = resolved.current_team_id
		 AND memberships.status IN ('active', 'suspended')
		WHERE users.identity_status = 'active'
	`, sessionID, digest[:], now)
	var principal onprem.HumanPrincipal
	err := row.Scan(
		&principal.UserID, &principal.Email, &principal.EmailVerified, &principal.SessionID,
		&principal.MembershipID, &principal.Role, &principal.MembershipStatus, &principal.ScopeID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.HumanPrincipal{}, onprem.ErrUnauthorized
	}
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("resolve postgres team human session: %w", err)
	}
	return principal, nil
}

func (s *SaaSIdentityStore) RevokeSession(
	ctx context.Context,
	sessionID string,
	digest onprem.Digest,
	now time.Time,
) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE team_human_sessions SET revoked_at = $3
		WHERE session_id = $1 AND secret_digest = $2 AND revoked_at IS NULL
	`, sessionID, digest[:], now)
	if err != nil {
		return fmt.Errorf("revoke postgres team human session: %w", err)
	}
	if command.RowsAffected() != 1 {
		return onprem.ErrUnauthorized
	}
	return nil
}

func (s *SaaSIdentityStore) SwitchTeam(
	ctx context.Context,
	sessionID string,
	userID string,
	teamID string,
	now time.Time,
) (returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin team switch: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "team switch")
	command, err := tx.Exec(ctx, `
		UPDATE team_human_sessions sessions
		SET current_team_id = $3
		FROM team_memberships memberships
		WHERE sessions.session_id = $1 AND sessions.user_id = $2
		  AND sessions.revoked_at IS NULL AND sessions.expires_at > $4
		  AND memberships.user_id = sessions.user_id AND memberships.team_id = $3
		  AND memberships.status = 'active'
	`, sessionID, userID, teamID, now)
	if err != nil {
		return fmt.Errorf("switch postgres team session: %w", err)
	}
	if command.RowsAffected() != 1 {
		return saas.ErrNotTeamMember
	}
	if err := insertScopedAuditEvent(ctx, tx, teamID, "human", userID, "", "", "",
		"identity.team.switched", "team", teamID, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit team switch: %w", err)
	}
	return nil
}

func (s *SaaSIdentityStore) ListMembers(
	ctx context.Context,
	teamID string,
	filter onprem.MemberFilter,
) ([]onprem.Member, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT memberships.membership_id, users.user_id, COALESCE(users.email, ''),
		       users.email_verified, users.display_name, memberships.role, memberships.status,
		       memberships.joined_at, memberships.updated_at, memberships.resource_version
		FROM team_memberships memberships
		JOIN onprem_users users ON users.user_id = memberships.user_id
		WHERE memberships.team_id = $1
		  AND ($2 = '' OR memberships.role = $2)
		  AND ($3 = '' OR memberships.status = $3)
		  AND ($4 = '' OR memberships.membership_id > $4)
		ORDER BY memberships.membership_id
		LIMIT $5
	`, teamID, filter.Role, filter.Status, filter.Cursor, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list postgres team members: %w", err)
	}
	defer rows.Close()
	members := make([]onprem.Member, 0)
	for rows.Next() {
		member, err := scanMember(rows)
		if err != nil {
			return nil, fmt.Errorf("scan postgres team member: %w", err)
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres team members: %w", err)
	}
	return members, nil
}

func (s *SaaSIdentityStore) GetMember(
	ctx context.Context,
	teamID string,
	membershipID string,
) (onprem.Member, error) {
	member, err := queryTeamMember(ctx, s.pool, teamID, membershipID)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.Member{}, onprem.ErrMembershipConflict
	}
	if err != nil {
		return onprem.Member{}, err
	}
	return member, nil
}

func (s *SaaSIdentityStore) UpdateMember(
	ctx context.Context,
	teamID string,
	actorMembershipID string,
	member onprem.Member,
	now time.Time,
) (updated onprem.Member, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.Member{}, fmt.Errorf("begin team member update: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "team member update")
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "saas.active-owner."+teamID); err != nil {
		return onprem.Member{}, fmt.Errorf("lock team active owner invariant: %w", err)
	}
	if err := validateTeamMemberUpdateInvariant(ctx, tx, teamID, member); err != nil {
		return onprem.Member{}, err
	}
	updated, err = scanMember(tx.QueryRow(ctx, `
		UPDATE team_memberships memberships
		SET role = $3, status = $4, updated_at = $5, resource_version = $6,
		    suspended_at = CASE WHEN $4 = 'suspended' THEN $5 ELSE suspended_at END,
		    removed_at = CASE WHEN $4 = 'removed' THEN $5 ELSE removed_at END
		FROM onprem_users users
		WHERE memberships.membership_id = $1 AND memberships.team_id = $2
		  AND users.user_id = memberships.user_id
		  AND memberships.resource_version = $6 - 1 AND memberships.status <> 'removed'
		RETURNING memberships.membership_id, users.user_id, COALESCE(users.email, ''),
		          users.email_verified, users.display_name, memberships.role, memberships.status,
		          memberships.joined_at, memberships.updated_at, memberships.resource_version
	`, member.MembershipID, teamID, member.Role, member.Status, now, member.ResourceVersion))
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.Member{}, onprem.ErrResourceVersionConflict
	}
	if err != nil {
		return onprem.Member{}, fmt.Errorf("update postgres team member: %w", err)
	}
	if member.Status == onprem.MembershipStatusSuspended || member.Status == onprem.MembershipStatusRemoved {
		if err := revokeTeamMemberArtifacts(ctx, tx, teamID, member, now); err != nil {
			return onprem.Member{}, err
		}
	}
	if err := insertScopedAuditEvent(ctx, tx, teamID, "human", "", actorMembershipID, "", "",
		"identity.membership.updated", "membership", member.MembershipID, now); err != nil {
		return onprem.Member{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.Member{}, fmt.Errorf("commit team member update: %w", err)
	}
	return updated, nil
}

// revokeTeamMemberArtifacts revokes the member's sessions currently pointed
// at the team plus every agent credential and pending enrollment the
// membership owns inside it, mirroring the on-prem member-status cascade.
func revokeTeamMemberArtifacts(
	ctx context.Context,
	tx pgx.Tx,
	teamID string,
	member onprem.Member,
	now time.Time,
) error {
	if _, err := tx.Exec(ctx, `
		UPDATE team_human_sessions SET revoked_at = $3
		WHERE user_id = $1 AND current_team_id = $2 AND revoked_at IS NULL
	`, member.UserID, teamID, now); err != nil {
		return fmt.Errorf("revoke team member sessions: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE team_agent_credentials SET revoked_at = $2
		WHERE owner_membership_id = $1 AND revoked_at IS NULL
	`, member.MembershipID, now); err != nil {
		return fmt.Errorf("revoke team member credentials: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE team_agent_enrollments SET revoked_at = $2
		WHERE membership_id = $1 AND consumed_at IS NULL AND revoked_at IS NULL
	`, member.MembershipID, now); err != nil {
		return fmt.Errorf("revoke team member enrollments: %w", err)
	}
	return nil
}

// validateTeamMemberUpdateInvariant enforces resource-version optimistic
// concurrency and the per-team last-active-owner rule, scoped to one team.
func validateTeamMemberUpdateInvariant(
	ctx context.Context,
	tx pgx.Tx,
	teamID string,
	member onprem.Member,
) error {
	var currentRole onprem.Role
	var currentStatus onprem.MembershipStatus
	var currentResourceVersion int64
	err := tx.QueryRow(ctx, `
		SELECT role, status, resource_version FROM team_memberships
		WHERE membership_id = $1 AND team_id = $2 FOR UPDATE
	`, member.MembershipID, teamID).Scan(&currentRole, &currentStatus, &currentResourceVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.ErrMembershipConflict
	}
	if err != nil {
		return fmt.Errorf("lock team member update: %w", err)
	}
	if currentResourceVersion != member.ResourceVersion-1 {
		return onprem.ErrResourceVersionConflict
	}
	removesActiveOwner := currentRole == onprem.RoleOwner && currentStatus == onprem.MembershipStatusActive &&
		(member.Role != onprem.RoleOwner || member.Status != onprem.MembershipStatusActive)
	if !removesActiveOwner {
		return nil
	}
	var activeOwners int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM team_memberships
		WHERE team_id = $1 AND role = 'owner' AND status = 'active'
	`, teamID).Scan(&activeOwners); err != nil {
		return fmt.Errorf("count team active owners: %w", err)
	}
	if activeOwners <= 1 {
		return onprem.ErrLastActiveOwner
	}
	return nil
}

func (s *SaaSIdentityStore) CreateInvitation(
	ctx context.Context,
	teamID string,
	record onprem.InvitationRecord,
) (returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin team invitation creation: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "team invitation creation")
	_, err = tx.Exec(ctx, `
		INSERT INTO team_membership_invitations (
			invitation_id, team_id, token_digest, digest_key_version, target_issuer, target_subject,
			target_email, role, created_by_membership_id, created_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, record.InvitationID, teamID, record.TokenDigest[:], record.DigestKeyVersion,
		nullableText(record.TargetIssuer), nullableText(record.TargetSubject),
		nullableText(record.TargetEmail), record.Role, record.CreatedByMembershipID,
		record.CreatedAt, record.ExpiresAt)
	if err != nil {
		return fmt.Errorf("save postgres team membership invitation: %w", err)
	}
	if err := insertScopedAuditEvent(ctx, tx, teamID, "human", "", record.CreatedByMembershipID, "", "",
		"identity.invitation.created", "invitation", record.InvitationID, record.CreatedAt); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit team invitation creation: %w", err)
	}
	return nil
}

func (s *SaaSIdentityStore) AcceptInvitation(
	ctx context.Context,
	invitationPublicID string,
	digest onprem.Digest,
	userID string,
	email string,
	emailVerified bool,
	idempotencyKey string,
	now time.Time,
) (principal onprem.HumanPrincipal, returnedErr error) {
	if !emailVerified {
		return onprem.HumanPrincipal{}, onprem.ErrInvitationInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("begin team invitation acceptance: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "team invitation acceptance")
	if err := ensureTeamInvitationIdempotencyAvailable(ctx, tx, userID, invitationPublicID, idempotencyKey); err != nil {
		return onprem.HumanPrincipal{}, err
	}
	invitation, err := lockTeamInvitationAcceptance(ctx, tx, invitationPublicID, digest, email, now)
	if err != nil {
		return onprem.HumanPrincipal{}, err
	}
	if invitation.acceptedByUserID != "" {
		return commitTeamInvitationAcceptanceReplay(ctx, tx, invitation, userID)
	}
	if invitation.expired || invitation.revoked || !invitation.emailMatches {
		return onprem.HumanPrincipal{}, onprem.ErrInvitationInvalid
	}
	membershipID, err := newPostgresID("mbr")
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("create team membership ID: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO team_memberships (
			membership_id, team_id, user_id, role, status, invited_by_membership_id, joined_at, updated_at
		) VALUES ($1, $2, $3, $4, 'active', $5, $6, $6)
	`, membershipID, invitation.teamID, userID, invitation.role, invitation.createdByMembershipID, now)
	if isUniqueViolation(err) {
		return onprem.HumanPrincipal{}, onprem.ErrMembershipConflict
	}
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("create invited team membership: %w", err)
	}
	_, err = tx.Exec(ctx, `
		UPDATE team_membership_invitations
		SET accepted_at = $2, accepted_by_user_id = $3, created_membership_id = $4,
		    accept_idempotency_key = $5
		WHERE invitation_id = $1
	`, invitation.invitationID, now, userID, membershipID, idempotencyKey)
	if isUniqueViolation(err) {
		return onprem.HumanPrincipal{}, onprem.ErrIdempotencyConflict
	}
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("complete team invitation acceptance: %w", err)
	}
	if err := insertScopedAuditEvent(ctx, tx, invitation.teamID, "human", userID, membershipID, "", "",
		"identity.invitation.accepted", "invitation", invitation.invitationID, now); err != nil {
		return onprem.HumanPrincipal{}, err
	}
	principal, err = queryTeamPrincipal(ctx, tx, userID, invitation.teamID, "")
	if err != nil {
		return onprem.HumanPrincipal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("commit team invitation acceptance: %w", err)
	}
	return principal, nil
}

type teamInvitationAcceptanceState struct {
	invitationID          string
	teamID                string
	createdByMembershipID string
	acceptedByUserID      string
	acceptedMembershipID  string
	role                  onprem.Role
	expired               bool
	revoked               bool
	emailMatches          bool
}

// commitTeamInvitationAcceptanceReplay returns the principal of an
// already-completed acceptance, mirroring the on-prem replay: only the user
// who originally accepted may replay it.
func commitTeamInvitationAcceptanceReplay(
	ctx context.Context,
	tx pgx.Tx,
	invitation teamInvitationAcceptanceState,
	userID string,
) (onprem.HumanPrincipal, error) {
	if invitation.acceptedByUserID != userID || invitation.acceptedMembershipID == "" {
		return onprem.HumanPrincipal{}, onprem.ErrInvitationInvalid
	}
	principal, err := queryTeamPrincipal(ctx, tx, userID, invitation.teamID, "")
	if err != nil {
		return onprem.HumanPrincipal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("commit team invitation acceptance replay: %w", err)
	}
	return principal, nil
}

func lockTeamInvitationAcceptance(
	ctx context.Context,
	tx pgx.Tx,
	invitationID string,
	digest onprem.Digest,
	email string,
	now time.Time,
) (teamInvitationAcceptanceState, error) {
	var state teamInvitationAcceptanceState
	// target_email is nullable, so the email comparison is coalesced to
	// false: a subject-only invitation is deterministically rejected by the
	// email acceptance flow instead of failing the NULL-into-bool scan.
	err := tx.QueryRow(ctx, `
		SELECT invitation_id, team_id, role, created_by_membership_id,
		       expires_at <= $3, revoked_at IS NOT NULL,
		       COALESCE(accepted_by_user_id, ''), COALESCE(created_membership_id, ''),
		       COALESCE(lower(target_email) = lower($4), false)
		FROM team_membership_invitations
		WHERE invitation_id = $1 AND token_digest = $2
		FOR UPDATE
	`, invitationID, digest[:], now, email).Scan(
		&state.invitationID, &state.teamID, &state.role, &state.createdByMembershipID,
		&state.expired, &state.revoked, &state.acceptedByUserID, &state.acceptedMembershipID,
		&state.emailMatches,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return teamInvitationAcceptanceState{}, onprem.ErrInvitationInvalid
	}
	if err != nil {
		return teamInvitationAcceptanceState{}, fmt.Errorf("lock team membership invitation: %w", err)
	}
	return state, nil
}

func ensureTeamInvitationIdempotencyAvailable(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	invitationID string,
	idempotencyKey string,
) error {
	if idempotencyKey == "" {
		return nil
	}
	var replayedInvitationID string
	err := tx.QueryRow(ctx, `
		SELECT invitation_id FROM team_membership_invitations
		WHERE accepted_by_user_id = $1 AND accept_idempotency_key = $2
	`, userID, idempotencyKey).Scan(&replayedInvitationID)
	if err == nil && replayedInvitationID != invitationID {
		return onprem.ErrIdempotencyConflict
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("resolve team invitation acceptance replay: %w", err)
	}
	return nil
}

func (s *SaaSIdentityStore) ListInvitations(
	ctx context.Context,
	teamID string,
	filter onprem.InvitationFilter,
	now time.Time,
) ([]onprem.Invitation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT invitation_id, COALESCE(target_email, ''), role, status, created_at, expires_at
		FROM (
			SELECT invitation_id, target_email, role, created_at, expires_at,
			       CASE
			           WHEN accepted_at IS NOT NULL THEN 'accepted'
			           WHEN revoked_at IS NOT NULL THEN 'revoked'
			           WHEN expires_at <= $1 THEN 'expired'
			           ELSE 'pending'
			       END AS status
			FROM team_membership_invitations
			WHERE team_id = $2
		) invitations
		WHERE ($3 = '' OR status = $3) AND ($4 = '' OR invitation_id > $4)
		ORDER BY invitation_id
		LIMIT $5
	`, now, teamID, filter.Status, filter.Cursor, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list postgres team membership invitations: %w", err)
	}
	defer rows.Close()
	result := make([]onprem.Invitation, 0)
	for rows.Next() {
		var invitation onprem.Invitation
		if err := rows.Scan(
			&invitation.InvitationID, &invitation.TargetEmail, &invitation.Role,
			&invitation.Status, &invitation.CreatedAt, &invitation.ExpiresAt,
		); err != nil {
			return nil, fmt.Errorf("scan postgres team membership invitation: %w", err)
		}
		result = append(result, invitation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres team membership invitations: %w", err)
	}
	return result, nil
}

func (s *SaaSIdentityStore) RevokeInvitation(
	ctx context.Context,
	teamID string,
	invitationID string,
	actorMembershipID string,
	canRevokeAdmin bool,
	now time.Time,
) (invitation onprem.Invitation, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.Invitation{}, fmt.Errorf("begin team invitation revocation: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "team invitation revocation")
	err = tx.QueryRow(ctx, `
		UPDATE team_membership_invitations
		SET revoked_at = $4
		WHERE invitation_id = $1 AND team_id = $2 AND ($3 OR role = 'member')
		  AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > $4
		RETURNING invitation_id, COALESCE(target_email, ''), role, 'revoked', created_at, expires_at
	`, invitationID, teamID, canRevokeAdmin, now).Scan(
		&invitation.InvitationID, &invitation.TargetEmail, &invitation.Role,
		&invitation.Status, &invitation.CreatedAt, &invitation.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.Invitation{}, onprem.ErrInvitationInvalid
	}
	if err != nil {
		return onprem.Invitation{}, fmt.Errorf("revoke postgres team membership invitation: %w", err)
	}
	if err := insertScopedAuditEvent(ctx, tx, teamID, "human", "", actorMembershipID, "", "",
		"identity.invitation.revoked", "invitation", invitationID, now); err != nil {
		return onprem.Invitation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.Invitation{}, fmt.Errorf("commit team invitation revocation: %w", err)
	}
	return invitation, nil
}

// ListAuditEvents reads the shared audit trail scoped to one team, mirroring
// the on-prem listing's filters and audit_event_id keyset cursor.
func (s *SaaSIdentityStore) ListAuditEvents(
	ctx context.Context,
	teamID string,
	filter onprem.AuditFilter,
) ([]onprem.AuditEvent, error) {
	var cursor int64
	if filter.Cursor != "" {
		parsed, err := strconv.ParseInt(filter.Cursor, 10, 64)
		if err != nil || parsed <= 0 {
			return nil, onprem.ErrInvalidIdentityInput
		}
		cursor = parsed
	}
	rows, err := s.pool.Query(ctx, `
		SELECT audit_event_id, actor_kind, COALESCE(actor_user_id, ''),
		       COALESCE(actor_membership_id, ''), COALESCE(actor_agent_id, ''),
		       COALESCE(actor_credential_id, ''), action, target_kind, target_id, occurred_at
		FROM onprem_audit_events
		WHERE scope_id = $1
		  AND ($2 = '' OR actor_kind = $2) AND ($3 = '' OR action = $3)
		  AND ($4 = '' OR target_kind = $4) AND ($5 = '' OR target_id = $5)
		  AND ($6 = 0 OR audit_event_id < $6)
		ORDER BY audit_event_id DESC
		LIMIT $7
	`, teamID, filter.ActorKind, filter.Action, filter.TargetKind, filter.TargetID, cursor, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list postgres team audit events: %w", err)
	}
	defer rows.Close()
	result := make([]onprem.AuditEvent, 0)
	for rows.Next() {
		event, err := scanAuditEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan postgres team audit event: %w", err)
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres team audit events: %w", err)
	}
	return result, nil
}

func (s *SaaSIdentityStore) GetAuditEvent(
	ctx context.Context,
	teamID string,
	auditEventID int64,
) (onprem.AuditEvent, error) {
	event, err := scanAuditEvent(s.pool.QueryRow(ctx, `
		SELECT audit_event_id, actor_kind, COALESCE(actor_user_id, ''),
		       COALESCE(actor_membership_id, ''), COALESCE(actor_agent_id, ''),
		       COALESCE(actor_credential_id, ''), action, target_kind, target_id, occurred_at
		FROM onprem_audit_events WHERE audit_event_id = $1 AND scope_id = $2
	`, auditEventID, teamID))
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.AuditEvent{}, onprem.ErrAuditEventNotFound
	}
	if err != nil {
		return onprem.AuditEvent{}, fmt.Errorf("get postgres team audit event: %w", err)
	}
	return event, nil
}

// queryTeamPrincipal resolves a user and their live membership in one team
// (empty teamID resolves the user with no membership, the "signed up but in
// no team" state) and stamps the team as the principal's scope.
func queryTeamPrincipal(
	ctx context.Context,
	querier queryRower,
	userID string,
	teamID string,
	sessionID string,
) (onprem.HumanPrincipal, error) {
	principal, err := scanHumanPrincipal(querier.QueryRow(ctx, `
		SELECT users.user_id, COALESCE(users.email, ''), users.email_verified, $3,
		       COALESCE(memberships.membership_id, ''), COALESCE(memberships.role, ''),
		       COALESCE(memberships.status, '')
		FROM onprem_users users
		LEFT JOIN team_memberships memberships
		  ON memberships.user_id = users.user_id
		 AND memberships.team_id = $2
		 AND memberships.status IN ('active', 'suspended')
		WHERE users.user_id = $1 AND users.identity_status = 'active'
	`, userID, nullableText(teamID), sessionID))
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("query team human principal: %w", err)
	}
	principal.ScopeID = teamID
	return principal, nil
}

func queryTeamMember(
	ctx context.Context,
	querier queryRower,
	teamID string,
	membershipID string,
) (onprem.Member, error) {
	member, err := scanMember(querier.QueryRow(ctx, `
		SELECT memberships.membership_id, users.user_id, COALESCE(users.email, ''),
		       users.email_verified, users.display_name, memberships.role, memberships.status,
		       memberships.joined_at, memberships.updated_at, memberships.resource_version
		FROM team_memberships memberships
		JOIN onprem_users users ON users.user_id = memberships.user_id
		WHERE memberships.membership_id = $1 AND memberships.team_id = $2
	`, membershipID, teamID))
	if err != nil {
		return onprem.Member{}, fmt.Errorf("query postgres team member: %w", err)
	}
	return member, nil
}

func scanTeam(scanner interface{ Scan(...any) error }) (saas.Team, error) {
	var team saas.Team
	err := scanner.Scan(
		&team.TeamID, &team.Name, &team.Slug, &team.CreatedByUserID,
		&team.CreatedAt, &team.UpdatedAt, &team.ResourceVersion,
	)
	return team, err
}

// insertScopedAuditEvent mirrors insertAuditEvent but stamps the SaaS scope
// (team_id) instead of relying on the 'local-team' column default, so saas
// audit rows are attributable to the team that produced them.
func insertScopedAuditEvent(
	ctx context.Context,
	tx pgx.Tx,
	scopeID string,
	actorKind string,
	userID string,
	membershipID string,
	agentID string,
	credentialID string,
	action string,
	targetKind string,
	targetID string,
	now time.Time,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO onprem_audit_events (
			scope_id, actor_kind, actor_user_id, actor_membership_id, actor_agent_id,
			actor_credential_id, action, target_kind, target_id, occurred_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, scopeID, actorKind, nullableText(userID), nullableText(membershipID), nullableText(agentID),
		nullableText(credentialID), action, targetKind, targetID, now)
	if err != nil {
		return fmt.Errorf("save scoped audit event: %w", err)
	}
	return nil
}
