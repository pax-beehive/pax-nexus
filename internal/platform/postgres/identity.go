package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
)

type IdentityStore struct {
	pool *pgxpool.Pool
}

func (s *IdentityStore) UpsertUserSession(
	ctx context.Context,
	identity onprem.ExternalIdentity,
	record onprem.HumanSessionRecord,
) (principal onprem.HumanPrincipal, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("begin human login: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "human login")
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
		INSERT INTO onprem_human_sessions (
			session_id, user_id, secret_digest, digest_key_version, created_at, expires_at, last_seen_at
		) VALUES ($1, $2, $3, $4, $5, $6, $5)
	`, record.SessionID, principal.UserID, record.SecretDigest[:], record.DigestKeyVersion,
		record.CreatedAt, record.ExpiresAt)
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("save human session: %w", err)
	}
	principal, err = queryHumanPrincipal(ctx, tx, principal.UserID, record.SessionID)
	if err != nil {
		return onprem.HumanPrincipal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("commit human login: %w", err)
	}
	return principal, nil
}

func (s *IdentityStore) ResolveHumanSession(
	ctx context.Context,
	sessionID string,
	digest onprem.Digest,
	now time.Time,
) (onprem.HumanPrincipal, error) {
	row := s.pool.QueryRow(ctx, `
		WITH resolved AS (
			UPDATE onprem_human_sessions
			SET last_seen_at = $3
			WHERE session_id = $1 AND secret_digest = $2 AND revoked_at IS NULL AND expires_at > $3
			RETURNING session_id, user_id
		)
		SELECT users.user_id, users.email, users.email_verified, resolved.session_id,
		       COALESCE(memberships.membership_id, ''), COALESCE(memberships.role, ''),
		       COALESCE(memberships.status, '')
		FROM resolved
		JOIN onprem_users users ON users.user_id = resolved.user_id
		LEFT JOIN onprem_memberships memberships
		  ON memberships.user_id = users.user_id
		 AND memberships.status IN ('active', 'suspended')
		WHERE users.identity_status = 'active'
	`, sessionID, digest[:], now)
	principal, err := scanHumanPrincipal(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.HumanPrincipal{}, onprem.ErrUnauthorized
	}
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("resolve postgres human session: %w", err)
	}
	return principal, nil
}

func (s *IdentityStore) RevokeHumanSession(
	ctx context.Context,
	sessionID string,
	digest onprem.Digest,
	now time.Time,
) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE onprem_human_sessions SET revoked_at = $3
		WHERE session_id = $1 AND secret_digest = $2 AND revoked_at IS NULL
	`, sessionID, digest[:], now)
	if err != nil {
		return fmt.Errorf("revoke postgres human session: %w", err)
	}
	if command.RowsAffected() != 1 {
		return onprem.ErrUnauthorized
	}
	return nil
}

func (s *IdentityStore) ClaimBootstrap(
	ctx context.Context,
	userID string,
	now time.Time,
) (principal onprem.HumanPrincipal, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("begin bootstrap claim: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "bootstrap claim")
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('onprem.active-owner'))`); err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("lock bootstrap owner invariant: %w", err)
	}
	var claimedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT bootstrap_claimed_at FROM onprem_installation_state
		WHERE singleton_id = 1 FOR UPDATE
	`).Scan(&claimedAt)
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("lock installation bootstrap: %w", err)
	}
	if claimedAt != nil {
		return onprem.HumanPrincipal{}, onprem.ErrBootstrapClosed
	}
	var activeOwners, disallowedMemberships int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM onprem_memberships
		WHERE role = 'owner' AND status = 'active'
	`).Scan(&activeOwners); err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("count bootstrap active owners: %w", err)
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM onprem_memberships memberships
		JOIN onprem_users users ON users.user_id = memberships.user_id
		WHERE NOT (
			memberships.membership_id LIKE 'legacy-membership-%'
			AND memberships.role = 'member' AND memberships.status = 'active'
			AND memberships.invited_by_membership_id IS NULL
			AND users.identity_status = 'unclaimed'
			AND users.identity_issuer IS NULL AND users.identity_subject IS NULL
		)
	`).Scan(&disallowedMemberships); err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("validate bootstrap membership state: %w", err)
	}
	if activeOwners > 0 || disallowedMemberships > 0 {
		return onprem.HumanPrincipal{}, onprem.ErrBootstrapClosed
	}
	membershipID, err := newPostgresID("mbr")
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("create bootstrap membership ID: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO onprem_memberships (
			membership_id, user_id, role, status, joined_at, updated_at
		) VALUES ($1, $2, 'owner', 'active', $3, $3)
	`, membershipID, userID, now)
	if isUniqueViolation(err) {
		return onprem.HumanPrincipal{}, onprem.ErrMembershipConflict
	}
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("create bootstrap owner: %w", err)
	}
	_, err = tx.Exec(ctx, `
		UPDATE onprem_installation_state
		SET bootstrap_claimed_at = $1, bootstrap_claimed_by_membership_id = $2, updated_at = $1
		WHERE singleton_id = 1
	`, now, membershipID)
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("close installation bootstrap: %w", err)
	}
	if err := insertAuditEvent(ctx, tx, "bootstrap", userID, membershipID, "", "",
		"identity.bootstrap.claimed", "membership", membershipID, now); err != nil {
		return onprem.HumanPrincipal{}, err
	}
	principal, err = queryHumanPrincipal(ctx, tx, userID, "")
	if err != nil {
		return onprem.HumanPrincipal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("commit bootstrap claim: %w", err)
	}
	return principal, nil
}

func (s *IdentityStore) CreateInvitation(
	ctx context.Context,
	record onprem.InvitationRecord,
) (returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin invitation creation: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "invitation creation")
	_, err = tx.Exec(ctx, `
		INSERT INTO onprem_membership_invitations (
			invitation_id, token_digest, digest_key_version, target_issuer, target_subject, target_email,
			role, created_by_membership_id, created_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, record.InvitationID, record.TokenDigest[:], record.DigestKeyVersion, nullableText(record.TargetIssuer),
		nullableText(record.TargetSubject), nullableText(record.TargetEmail), record.Role,
		record.CreatedByMembershipID, record.CreatedAt, record.ExpiresAt)
	if err != nil {
		return fmt.Errorf("save postgres membership invitation: %w", err)
	}
	if err := insertAuditEvent(ctx, tx, "human", "", record.CreatedByMembershipID, "", "",
		"identity.invitation.created", "invitation", record.InvitationID, record.CreatedAt); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit invitation creation: %w", err)
	}
	return nil
}

func (s *IdentityStore) ListInvitations(
	ctx context.Context,
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
			FROM onprem_membership_invitations
		) invitations
		WHERE ($2 = '' OR status = $2) AND ($3 = '' OR invitation_id > $3)
		ORDER BY invitation_id
		LIMIT $4
	`, now, filter.Status, filter.Cursor, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list postgres membership invitations: %w", err)
	}
	defer rows.Close()
	result := make([]onprem.Invitation, 0)
	for rows.Next() {
		var invitation onprem.Invitation
		if err := rows.Scan(
			&invitation.InvitationID, &invitation.TargetEmail, &invitation.Role,
			&invitation.Status, &invitation.CreatedAt, &invitation.ExpiresAt,
		); err != nil {
			return nil, fmt.Errorf("scan postgres membership invitation: %w", err)
		}
		result = append(result, invitation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres membership invitations: %w", err)
	}
	return result, nil
}

func (s *IdentityStore) RevokeInvitation(
	ctx context.Context,
	invitationID string,
	actorMembershipID string,
	canRevokeAdmin bool,
	now time.Time,
) (invitation onprem.Invitation, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.Invitation{}, fmt.Errorf("begin invitation revocation: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "invitation revocation")
	err = tx.QueryRow(ctx, `
		UPDATE onprem_membership_invitations
		SET revoked_at = $3
		WHERE invitation_id = $1 AND ($2 OR role = 'member')
		  AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > $3
		RETURNING invitation_id, COALESCE(target_email, ''), role, 'revoked', created_at, expires_at
	`, invitationID, canRevokeAdmin, now).Scan(
		&invitation.InvitationID, &invitation.TargetEmail, &invitation.Role,
		&invitation.Status, &invitation.CreatedAt, &invitation.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.Invitation{}, onprem.ErrInvitationInvalid
	}
	if err != nil {
		return onprem.Invitation{}, fmt.Errorf("revoke postgres membership invitation: %w", err)
	}
	if err := insertAuditEvent(ctx, tx, "human", "", actorMembershipID, "", "",
		"identity.invitation.revoked", "invitation", invitationID, now); err != nil {
		return onprem.Invitation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.Invitation{}, fmt.Errorf("commit invitation revocation: %w", err)
	}
	return invitation, nil
}

func (s *IdentityStore) AcceptInvitation(
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
		return onprem.HumanPrincipal{}, fmt.Errorf("begin invitation acceptance: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "invitation acceptance")
	if err := ensureInvitationIdempotencyAvailable(
		ctx, tx, userID, invitationPublicID, idempotencyKey,
	); err != nil {
		return onprem.HumanPrincipal{}, err
	}
	invitation, err := lockInvitationAcceptance(ctx, tx, invitationPublicID, digest, email, now)
	if err != nil {
		return onprem.HumanPrincipal{}, err
	}
	if invitation.acceptedByUserID != "" {
		return commitInvitationAcceptanceReplay(ctx, tx, invitation, userID)
	}
	if invitation.expired || invitation.revoked || !invitation.emailMatches {
		return onprem.HumanPrincipal{}, onprem.ErrInvitationInvalid
	}
	membershipID, err := newPostgresID("mbr")
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("create membership ID: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO onprem_memberships (
			membership_id, user_id, role, status, invited_by_membership_id, joined_at, updated_at
		) VALUES ($1, $2, $3, 'active', $4, $5, $5)
	`, membershipID, userID, invitation.role, invitation.createdByMembershipID, now)
	if isUniqueViolation(err) {
		return onprem.HumanPrincipal{}, onprem.ErrMembershipConflict
	}
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("create invited membership: %w", err)
	}
	_, err = tx.Exec(ctx, `
		UPDATE onprem_membership_invitations
		SET accepted_at = $2, accepted_by_user_id = $3, created_membership_id = $4,
		    accept_idempotency_key = $5
		WHERE invitation_id = $1
	`, invitation.invitationID, now, userID, membershipID, idempotencyKey)
	if isUniqueViolation(err) {
		return onprem.HumanPrincipal{}, onprem.ErrIdempotencyConflict
	}
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("complete invitation acceptance: %w", err)
	}
	if err := insertAuditEvent(ctx, tx, "human", userID, membershipID, "", "",
		"identity.invitation.accepted", "invitation", invitation.invitationID, now); err != nil {
		return onprem.HumanPrincipal{}, err
	}
	principal, err = queryHumanPrincipal(ctx, tx, userID, "")
	if err != nil {
		return onprem.HumanPrincipal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("commit invitation acceptance: %w", err)
	}
	return principal, nil
}

type invitationAcceptanceState struct {
	invitationID          string
	createdByMembershipID string
	acceptedByUserID      string
	acceptedMembershipID  string
	role                  onprem.Role
	expired               bool
	revoked               bool
	emailMatches          bool
}

func lockInvitationAcceptance(
	ctx context.Context,
	tx pgx.Tx,
	invitationID string,
	digest onprem.Digest,
	email string,
	now time.Time,
) (invitationAcceptanceState, error) {
	var state invitationAcceptanceState
	err := tx.QueryRow(ctx, `
		SELECT invitation_id, role, created_by_membership_id,
		       expires_at <= $3, revoked_at IS NOT NULL,
		       COALESCE(accepted_by_user_id, ''), COALESCE(created_membership_id, ''),
		       lower(target_email) = lower($4)
		FROM onprem_membership_invitations
		WHERE invitation_id = $1 AND token_digest = $2
		FOR UPDATE
	`, invitationID, digest[:], now, email).Scan(
		&state.invitationID, &state.role, &state.createdByMembershipID, &state.expired, &state.revoked,
		&state.acceptedByUserID, &state.acceptedMembershipID, &state.emailMatches,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return invitationAcceptanceState{}, onprem.ErrInvitationInvalid
	}
	if err != nil {
		return invitationAcceptanceState{}, fmt.Errorf("lock membership invitation: %w", err)
	}
	return state, nil
}

func commitInvitationAcceptanceReplay(
	ctx context.Context,
	tx pgx.Tx,
	invitation invitationAcceptanceState,
	userID string,
) (onprem.HumanPrincipal, error) {
	if invitation.acceptedByUserID != userID || invitation.acceptedMembershipID == "" {
		return onprem.HumanPrincipal{}, onprem.ErrInvitationInvalid
	}
	principal, err := queryHumanPrincipal(ctx, tx, userID, "")
	if err != nil {
		return onprem.HumanPrincipal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("commit invitation acceptance replay: %w", err)
	}
	return principal, nil
}

func ensureInvitationIdempotencyAvailable(
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
		SELECT invitation_id FROM onprem_membership_invitations
		WHERE accepted_by_user_id = $1 AND accept_idempotency_key = $2
	`, userID, idempotencyKey).Scan(&replayedInvitationID)
	if err == nil && replayedInvitationID != invitationID {
		return onprem.ErrIdempotencyConflict
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("resolve invitation acceptance replay: %w", err)
	}
	return nil
}

func (s *IdentityStore) ListMembers(
	ctx context.Context,
	filter onprem.MemberFilter,
) ([]onprem.Member, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT memberships.membership_id, users.user_id, COALESCE(users.email, ''),
		       users.email_verified, users.display_name, memberships.role, memberships.status,
		       memberships.joined_at, memberships.updated_at, memberships.resource_version
		FROM onprem_memberships memberships
		JOIN onprem_users users ON users.user_id = memberships.user_id
		WHERE ($1 = '' OR memberships.role = $1)
		  AND ($2 = '' OR memberships.status = $2)
		  AND ($3 = '' OR memberships.membership_id > $3)
		ORDER BY memberships.membership_id
		LIMIT $4
	`, filter.Role, filter.Status, filter.Cursor, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list postgres members: %w", err)
	}
	defer rows.Close()
	members := make([]onprem.Member, 0)
	for rows.Next() {
		member, err := scanMember(rows)
		if err != nil {
			return nil, fmt.Errorf("scan postgres member: %w", err)
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres members: %w", err)
	}
	return members, nil
}

func (s *IdentityStore) GetMember(ctx context.Context, membershipID string) (onprem.Member, error) {
	member, err := scanMember(s.pool.QueryRow(ctx, `
		SELECT memberships.membership_id, users.user_id, COALESCE(users.email, ''),
		       users.email_verified, users.display_name, memberships.role, memberships.status,
		       memberships.joined_at, memberships.updated_at, memberships.resource_version
		FROM onprem_memberships memberships
		JOIN onprem_users users ON users.user_id = memberships.user_id
		WHERE memberships.membership_id = $1
	`, membershipID))
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.Member{}, onprem.ErrMembershipConflict
	}
	if err != nil {
		return onprem.Member{}, fmt.Errorf("get postgres member: %w", err)
	}
	return member, nil
}

func (s *IdentityStore) UpdateMember(
	ctx context.Context,
	actorMembershipID string,
	member onprem.Member,
	now time.Time,
) (updated onprem.Member, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return onprem.Member{}, fmt.Errorf("begin member update: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "member update")
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('onprem.active-owner'))`); err != nil {
		return onprem.Member{}, fmt.Errorf("lock active owner invariant: %w", err)
	}
	var currentRole onprem.Role
	var currentStatus onprem.MembershipStatus
	err = tx.QueryRow(ctx, `
		SELECT role, status FROM onprem_memberships
		WHERE membership_id = $1 FOR UPDATE
	`, member.MembershipID).Scan(&currentRole, &currentStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.Member{}, onprem.ErrMembershipConflict
	}
	if err != nil {
		return onprem.Member{}, fmt.Errorf("lock member update: %w", err)
	}
	removesActiveOwner := currentRole == onprem.RoleOwner && currentStatus == onprem.MembershipStatusActive &&
		(member.Role != onprem.RoleOwner || member.Status != onprem.MembershipStatusActive)
	if removesActiveOwner {
		var activeOwners int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM onprem_memberships
			WHERE role = 'owner' AND status = 'active'
		`).Scan(&activeOwners); err != nil {
			return onprem.Member{}, fmt.Errorf("count active owners: %w", err)
		}
		if activeOwners <= 1 {
			return onprem.Member{}, onprem.ErrMembershipConflict
		}
	}
	updated, err = scanMember(tx.QueryRow(ctx, `
		UPDATE onprem_memberships memberships
		SET role = $2, status = $3, updated_at = $4, resource_version = $5,
		    suspended_at = CASE WHEN $3 = 'suspended' THEN $4 ELSE suspended_at END,
		    removed_at = CASE WHEN $3 = 'removed' THEN $4 ELSE removed_at END
		FROM onprem_users users
		WHERE memberships.membership_id = $1 AND users.user_id = memberships.user_id
		  AND memberships.resource_version = $5 - 1 AND memberships.status <> 'removed'
		RETURNING memberships.membership_id, users.user_id, COALESCE(users.email, ''),
		          users.email_verified, users.display_name, memberships.role, memberships.status,
		          memberships.joined_at, memberships.updated_at, memberships.resource_version
	`, member.MembershipID, member.Role, member.Status, now, member.ResourceVersion))
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.Member{}, onprem.ErrMembershipConflict
	}
	if err != nil {
		return onprem.Member{}, fmt.Errorf("update postgres member: %w", err)
	}
	if member.Status == onprem.MembershipStatusSuspended || member.Status == onprem.MembershipStatusRemoved {
		if _, err := tx.Exec(ctx, `
			UPDATE onprem_human_sessions SET revoked_at = $2
			WHERE user_id = $1 AND revoked_at IS NULL
		`, member.UserID, now); err != nil {
			return onprem.Member{}, fmt.Errorf("revoke member sessions: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE agent_credentials SET revoked_at = $2
			WHERE owner_membership_id = $1 AND revoked_at IS NULL
		`, member.MembershipID, now); err != nil {
			return onprem.Member{}, fmt.Errorf("revoke member credentials: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE agent_enrollments SET revoked_at = $2
			WHERE membership_id = $1 AND consumed_at IS NULL AND revoked_at IS NULL
		`, member.MembershipID, now); err != nil {
			return onprem.Member{}, fmt.Errorf("revoke member enrollments: %w", err)
		}
	}
	if err := insertAuditEvent(ctx, tx, "human", "", actorMembershipID, "", "",
		"identity.membership.updated", "membership", member.MembershipID, now); err != nil {
		return onprem.Member{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return onprem.Member{}, fmt.Errorf("commit member update: %w", err)
	}
	return updated, nil
}

func (s *IdentityStore) ListAuditEvents(
	ctx context.Context,
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
		WHERE ($1 = '' OR actor_kind = $1) AND ($2 = '' OR action = $2)
		  AND ($3 = '' OR target_kind = $3) AND ($4 = '' OR target_id = $4)
		  AND ($5 = 0 OR audit_event_id < $5)
		ORDER BY audit_event_id DESC
		LIMIT $6
	`, filter.ActorKind, filter.Action, filter.TargetKind, filter.TargetID, cursor, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list postgres audit events: %w", err)
	}
	defer rows.Close()
	result := make([]onprem.AuditEvent, 0)
	for rows.Next() {
		event, err := scanAuditEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan postgres audit event: %w", err)
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres audit events: %w", err)
	}
	return result, nil
}

func (s *IdentityStore) GetAuditEvent(ctx context.Context, auditEventID int64) (onprem.AuditEvent, error) {
	event, err := scanAuditEvent(s.pool.QueryRow(ctx, `
		SELECT audit_event_id, actor_kind, COALESCE(actor_user_id, ''),
		       COALESCE(actor_membership_id, ''), COALESCE(actor_agent_id, ''),
		       COALESCE(actor_credential_id, ''), action, target_kind, target_id, occurred_at
		FROM onprem_audit_events WHERE audit_event_id = $1
	`, auditEventID))
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.AuditEvent{}, onprem.ErrAuditEventNotFound
	}
	if err != nil {
		return onprem.AuditEvent{}, fmt.Errorf("get postgres audit event: %w", err)
	}
	return event, nil
}

func scanAuditEvent(scanner interface{ Scan(...any) error }) (onprem.AuditEvent, error) {
	var event onprem.AuditEvent
	err := scanner.Scan(
		&event.AuditEventID, &event.ActorKind, &event.ActorUserID, &event.ActorMembershipID,
		&event.ActorAgentID, &event.ActorCredentialID, &event.Action, &event.TargetKind,
		&event.TargetID, &event.OccurredAt,
	)
	return event, err
}

func scanMember(scanner interface{ Scan(...any) error }) (onprem.Member, error) {
	var member onprem.Member
	err := scanner.Scan(
		&member.MembershipID, &member.UserID, &member.Email, &member.EmailVerified,
		&member.DisplayName, &member.Role, &member.Status, &member.JoinedAt,
		&member.UpdatedAt, &member.ResourceVersion,
	)
	return member, err
}

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func queryHumanPrincipal(
	ctx context.Context,
	querier queryRower,
	userID string,
	sessionID string,
) (onprem.HumanPrincipal, error) {
	principal, err := scanHumanPrincipal(querier.QueryRow(ctx, `
		SELECT users.user_id, COALESCE(users.email, ''), users.email_verified, $2,
		       COALESCE(memberships.membership_id, ''), COALESCE(memberships.role, ''),
		       COALESCE(memberships.status, '')
		FROM onprem_users users
		LEFT JOIN onprem_memberships memberships
		  ON memberships.user_id = users.user_id
		 AND memberships.status IN ('active', 'suspended')
		WHERE users.user_id = $1 AND users.identity_status = 'active'
	`, userID, sessionID))
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("query human principal: %w", err)
	}
	return principal, nil
}

func scanHumanPrincipal(row pgx.Row) (onprem.HumanPrincipal, error) {
	var principal onprem.HumanPrincipal
	err := row.Scan(
		&principal.UserID, &principal.Email, &principal.EmailVerified, &principal.SessionID,
		&principal.MembershipID, &principal.Role, &principal.MembershipStatus,
	)
	return principal, err
}

func externalUserID(issuer, subject string) string {
	sum := sha256.Sum256([]byte(issuer + "\x00" + subject))
	return "usr_" + base64.RawURLEncoding.EncodeToString(sum[:18])
}

func newPostgresID(prefix string) (string, error) {
	buffer := make([]byte, 18)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("read random ID: %w", err)
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(buffer), nil
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func isUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}

func rollbackTx(returnedErr *error, tx pgx.Tx, operation string) {
	if err := tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		*returnedErr = errors.Join(*returnedErr, fmt.Errorf("rollback %s: %w", operation, err))
	}
}

func insertAuditEvent(
	ctx context.Context,
	tx pgx.Tx,
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
			actor_kind, actor_user_id, actor_membership_id, actor_agent_id,
			actor_credential_id, action, target_kind, target_id, occurred_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, actorKind, nullableText(userID), nullableText(membershipID), nullableText(agentID),
		nullableText(credentialID), action, targetKind, targetID, now)
	if err != nil {
		return fmt.Errorf("save on-prem audit event: %w", err)
	}
	return nil
}
