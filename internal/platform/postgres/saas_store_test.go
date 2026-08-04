package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/deployment/saas"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/stretchr/testify/suite"
)

type saasStoreSuite struct {
	suite.Suite
	store *postgres.Store
}

func TestSaaSStoreSuite(t *testing.T) {
	suite.Run(t, new(saasStoreSuite))
}

func (s *saasStoreSuite) SetupSuite() {
	store, err := postgres.Open(context.Background(), testDSN(s.T()))
	s.Require().NoError(err)
	s.Require().NoError(store.Migrate(context.Background()))
	s.store = store
}

func (s *saasStoreSuite) TearDownSuite() {
	if s.store != nil {
		s.store.Close()
	}
}

func (s *saasStoreSuite) insertUser(label string) string {
	return s.insertUserWithEmail(label, "")
}

func (s *saasStoreSuite) insertUserWithEmail(label string, email string) string {
	userID := uniqueCredentialValue(label + "-user")
	var emailArg any
	verified := false
	if email != "" {
		emailArg = email
		verified = true
	}
	_, err := s.store.Pool().Exec(context.Background(), `
		INSERT INTO onprem_users (user_id, email, email_verified, display_name, identity_status, created_at, updated_at)
		VALUES ($1, $2, $3, 'SaaS Store Test User', 'active', now(), now())
	`, userID, emailArg, verified)
	s.Require().NoError(err)
	return userID
}

func (s *saasStoreSuite) createTeam(ownerUserID string, label string) (saas.Team, onprem.Member) {
	now := time.Now().UTC()
	team, owner, err := s.store.SaaSIdentity().CreateTeam(context.Background(), saas.Team{
		TeamID: "team_" + uniqueCredentialValue(label), Name: "Team " + label,
		Slug: uniqueCredentialValue(label), CreatedByUserID: ownerUserID,
		CreatedAt: now, UpdatedAt: now,
	}, uniqueCredentialValue(label+"-owner"), "", now)
	s.Require().NoError(err)
	return team, owner
}

func (s *saasStoreSuite) auditScope(targetID string) string {
	var scope string
	err := s.store.Pool().QueryRow(context.Background(), `
		SELECT scope_id FROM onprem_audit_events
		WHERE target_id = $1
		ORDER BY audit_event_id DESC LIMIT 1
	`, targetID).Scan(&scope)
	s.Require().NoError(err)
	return scope
}

func (s *saasStoreSuite) TestCreateTeamOwnerAndIdempotentReplay() {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	userID := s.insertUser("create-team")
	request := saas.Team{
		TeamID: "team_" + uniqueCredentialValue("create-team"), Name: "Acme",
		Slug: uniqueCredentialValue("acme"), CreatedByUserID: userID,
		CreatedAt: now, UpdatedAt: now,
	}
	ownerMembershipID := uniqueCredentialValue("create-team-owner")
	idempotencyKey := uniqueCredentialValue("create-team-key")

	team, owner, err := s.store.SaaSIdentity().CreateTeam(ctx, request, ownerMembershipID, idempotencyKey, now)
	s.Require().NoError(err)
	s.Equal(request.TeamID, team.TeamID)
	s.Equal("Acme", team.Name)
	s.Equal(request.Slug, team.Slug)
	s.Equal(userID, team.CreatedByUserID)
	s.Equal(int64(1), team.ResourceVersion)
	s.Equal(ownerMembershipID, owner.MembershipID)
	s.Equal(userID, owner.UserID)
	s.Equal(onprem.RoleOwner, owner.Role)
	s.Equal(onprem.MembershipStatusActive, owner.Status)

	loaded, err := s.store.SaaSIdentity().GetTeam(ctx, team.TeamID)
	s.Require().NoError(err)
	s.Equal(team, loaded)
	_, err = s.store.SaaSIdentity().GetTeam(ctx, "team_"+uniqueCredentialValue("missing"))
	s.Require().ErrorIs(err, saas.ErrTeamNotFound)

	summaries, err := s.store.SaaSIdentity().ListTeamSummaries(ctx, userID)
	s.Require().NoError(err)
	s.Require().Len(summaries, 1)
	s.Equal(team.TeamID, summaries[0].Team.TeamID)
	s.Equal(onprem.RoleOwner, summaries[0].Role)
	s.Equal(ownerMembershipID, summaries[0].MembershipID)

	// Replays with the same (user, idempotency key) return the original
	// team; the same key with a changed request conflicts.
	replay := request
	replay.TeamID = "team_" + uniqueCredentialValue("create-team-replay")
	replayedTeam, replayedOwner, err := s.store.SaaSIdentity().CreateTeam(
		ctx, replay, uniqueCredentialValue("create-team-replay-owner"), idempotencyKey, now)
	s.Require().NoError(err)
	s.Equal(team.TeamID, replayedTeam.TeamID)
	s.Equal(ownerMembershipID, replayedOwner.MembershipID)

	conflicting := replay
	conflicting.Name = "Acme Renamed"
	_, _, err = s.store.SaaSIdentity().CreateTeam(
		ctx, conflicting, uniqueCredentialValue("create-team-conflict-owner"), idempotencyKey, now)
	s.Require().ErrorIs(err, onprem.ErrIdempotencyConflict)

	// A slug taken by another team is rejected globally.
	s.Run("slug uniqueness across teams", func() {
		otherUserID := s.insertUser("create-team-slug")
		taken := saas.Team{
			TeamID: "team_" + uniqueCredentialValue("create-team-slug"), Name: "Other",
			Slug: request.Slug, CreatedByUserID: otherUserID, CreatedAt: now, UpdatedAt: now,
		}
		_, _, err := s.store.SaaSIdentity().CreateTeam(
			ctx, taken, uniqueCredentialValue("create-team-slug-owner"), "", now)
		s.Require().ErrorIs(err, saas.ErrTeamSlugConflict)
	})

	s.Equal(team.TeamID, s.auditScope(team.TeamID), "team creation audit row must carry the team scope")
}

func (s *saasStoreSuite) TestInvitationAcceptAndReplay() {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	ownerUserID := s.insertUser("invite-owner")
	team, owner := s.createTeam(ownerUserID, "invite")
	inviteeEmail := uniqueCredentialValue("invitee") + "@example.com"
	inviteeUserID := s.insertUserWithEmail("invitee", inviteeEmail)

	token := "tm_invite_" + uniqueCredentialValue("token")
	record := onprem.InvitationRecord{
		InvitationID: uniqueCredentialValue("invitation"), TokenDigest: credentialDigest(token),
		TargetEmail: inviteeEmail, Role: onprem.RoleMember,
		CreatedByMembershipID: owner.MembershipID, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}
	s.Require().NoError(s.store.SaaSIdentity().CreateInvitation(ctx, team.TeamID, record))

	idempotencyKey := uniqueCredentialValue("accept-key")
	principal, err := s.store.SaaSIdentity().AcceptInvitation(
		ctx, record.InvitationID, record.TokenDigest, inviteeUserID, inviteeEmail, true, idempotencyKey, now)
	s.Require().NoError(err)
	s.Equal(inviteeUserID, principal.UserID)
	s.NotEmpty(principal.MembershipID)
	s.Equal(onprem.RoleMember, principal.Role)
	s.Equal(onprem.MembershipStatusActive, principal.MembershipStatus)
	s.Equal(team.TeamID, principal.ScopeID, "acceptance scope must be the invitation's team")

	members, err := s.store.SaaSIdentity().ListMembers(ctx, team.TeamID, onprem.MemberFilter{Limit: 10})
	s.Require().NoError(err)
	s.Len(members, 2)

	// Replay with the same key returns the same membership; a different
	// user cannot replay the acceptance (mirrors the on-prem semantics:
	// the same user re-accepting is always a replay).
	replayed, err := s.store.SaaSIdentity().AcceptInvitation(
		ctx, record.InvitationID, record.TokenDigest, inviteeUserID, inviteeEmail, true, idempotencyKey, now)
	s.Require().NoError(err)
	s.Equal(principal.MembershipID, replayed.MembershipID)
	strangerUserID := s.insertUserWithEmail("stranger", uniqueCredentialValue("stranger")+"@example.com")
	_, err = s.store.SaaSIdentity().AcceptInvitation(
		ctx, record.InvitationID, record.TokenDigest, strangerUserID, inviteeEmail, true,
		uniqueCredentialValue("stranger-key"), now)
	s.Require().ErrorIs(err, onprem.ErrInvitationInvalid)

	// The same user may also join a second team: the core multi-team
	// difference from the on-prem single-membership rule.
	otherTeam, otherOwner := s.createTeam(ownerUserID, "invite-second")
	otherRecord := onprem.InvitationRecord{
		InvitationID: uniqueCredentialValue("invitation-second"), TokenDigest: credentialDigest(uniqueCredentialValue("token-second")),
		TargetEmail: inviteeEmail, Role: onprem.RoleAdmin,
		CreatedByMembershipID: otherOwner.MembershipID, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}
	s.Require().NoError(s.store.SaaSIdentity().CreateInvitation(ctx, otherTeam.TeamID, otherRecord))
	second, err := s.store.SaaSIdentity().AcceptInvitation(
		ctx, otherRecord.InvitationID, otherRecord.TokenDigest, inviteeUserID, inviteeEmail, true,
		uniqueCredentialValue("accept-key-second"), now)
	s.Require().NoError(err)
	s.Equal(otherTeam.TeamID, second.ScopeID)
	s.Equal(onprem.RoleAdmin, second.Role)

	summaries, err := s.store.SaaSIdentity().ListTeamSummaries(ctx, inviteeUserID)
	s.Require().NoError(err)
	s.Len(summaries, 2)

	// Error matrix: unverified email, wrong email, unknown token.
	cases := []struct {
		name     string
		email    string
		verified bool
	}{
		{"unverified email", inviteeEmail, false},
		{"mismatched email", "other@example.com", true},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			freshRecord := onprem.InvitationRecord{
				InvitationID: uniqueCredentialValue("invitation-" + tc.name), TokenDigest: credentialDigest(uniqueCredentialValue("token-" + tc.name)),
				TargetEmail: inviteeEmail, Role: onprem.RoleMember,
				CreatedByMembershipID: owner.MembershipID, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
			}
			s.Require().NoError(s.store.SaaSIdentity().CreateInvitation(ctx, team.TeamID, freshRecord))
			_, err := s.store.SaaSIdentity().AcceptInvitation(
				ctx, freshRecord.InvitationID, freshRecord.TokenDigest, inviteeUserID, tc.email, tc.verified, "", now)
			s.Require().ErrorIs(err, onprem.ErrInvitationInvalid)
		})
	}
	_, err = s.store.SaaSIdentity().AcceptInvitation(
		ctx, uniqueCredentialValue("missing-invitation"), credentialDigest(uniqueCredentialValue("missing-token")),
		inviteeUserID, inviteeEmail, true, "", now)
	s.Require().ErrorIs(err, onprem.ErrInvitationInvalid)

	// An already-accepted invitation cannot be revoked.
	_, err = s.store.SaaSIdentity().RevokeInvitation(
		ctx, otherTeam.TeamID, otherRecord.InvitationID, otherOwner.MembershipID, true, now)
	s.Require().ErrorIs(err, onprem.ErrInvitationInvalid)
	pendingRecord := onprem.InvitationRecord{
		InvitationID: uniqueCredentialValue("invitation-pending"), TokenDigest: credentialDigest(uniqueCredentialValue("token-pending")),
		TargetEmail: "pending@example.com", Role: onprem.RoleMember,
		CreatedByMembershipID: owner.MembershipID, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}
	s.Require().NoError(s.store.SaaSIdentity().CreateInvitation(ctx, team.TeamID, pendingRecord))
	revokedInvitation, err := s.store.SaaSIdentity().RevokeInvitation(
		ctx, team.TeamID, pendingRecord.InvitationID, owner.MembershipID, false, now)
	s.Require().NoError(err)
	s.Equal(onprem.InvitationStatusRevoked, revokedInvitation.Status)
	_, err = s.store.SaaSIdentity().AcceptInvitation(
		ctx, pendingRecord.InvitationID, pendingRecord.TokenDigest, inviteeUserID, "pending@example.com", true, "", now)
	s.Require().ErrorIs(err, onprem.ErrInvitationInvalid)

	teamInvitations, err := s.store.SaaSIdentity().ListInvitations(ctx, team.TeamID, onprem.InvitationFilter{Limit: 50}, now)
	s.Require().NoError(err)
	for _, invitation := range teamInvitations {
		s.NotEqual(otherRecord.InvitationID, invitation.InvitationID, "invitations must not leak across teams")
	}
	otherTeamInvitations, err := s.store.SaaSIdentity().ListInvitations(
		ctx, otherTeam.TeamID, onprem.InvitationFilter{Status: onprem.InvitationStatusAccepted, Limit: 50}, now)
	s.Require().NoError(err)
	s.Require().Len(otherTeamInvitations, 1)
	s.Equal(otherRecord.InvitationID, otherTeamInvitations[0].InvitationID)

	s.Equal(team.TeamID, s.auditScope(record.InvitationID), "invitation audit rows must carry the team scope")
}

func (s *saasStoreSuite) TestSessionLifecycleAndSwitchTeam() {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	identity := onprem.ExternalIdentity{
		Issuer: "https://issuer.example.com", Subject: uniqueCredentialValue("subject"),
		Email: uniqueCredentialValue("session") + "@example.com", EmailVerified: true, DisplayName: "Session User",
	}

	// Sign-up with no team yet: the session resolves with an empty scope
	// and no membership.
	noTeamRecord := saas.TeamSessionRecord{
		SessionID: uniqueCredentialValue("session-noteam"), SecretDigest: credentialDigest(uniqueCredentialValue("secret-noteam")),
		DigestKeyVersion: 1, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	noTeamPrincipal, err := s.store.SaaSIdentity().CreateUserSession(ctx, identity, noTeamRecord)
	s.Require().NoError(err)
	s.NotEmpty(noTeamPrincipal.UserID)
	s.Empty(noTeamPrincipal.ScopeID)
	s.Empty(noTeamPrincipal.MembershipID)

	userID := noTeamPrincipal.UserID
	teamA, ownerA := s.createTeam(userID, "session-a")
	teamB, _ := s.createTeam(userID, "session-b")
	foreignTeam, _ := s.createTeam(s.insertUser("session-foreign"), "session-foreign")

	tokenDigest := credentialDigest(uniqueCredentialValue("secret"))
	record := saas.TeamSessionRecord{
		SessionID: uniqueCredentialValue("session"), SecretDigest: tokenDigest,
		DigestKeyVersion: 1, CreatedAt: now, ExpiresAt: now.Add(time.Hour), CurrentTeamID: teamA.TeamID,
	}
	principal, err := s.store.SaaSIdentity().CreateUserSession(ctx, identity, record)
	s.Require().NoError(err)
	s.Equal(teamA.TeamID, principal.ScopeID)
	s.Equal(ownerA.MembershipID, principal.MembershipID)
	s.Equal(record.SessionID, principal.SessionID)

	resolved, err := s.store.SaaSIdentity().ResolveSession(ctx, record.SessionID, tokenDigest, now)
	s.Require().NoError(err)
	s.Equal(principal, resolved)

	_, err = s.store.SaaSIdentity().ResolveSession(ctx, record.SessionID, credentialDigest(uniqueCredentialValue("wrong")), now)
	s.Require().ErrorIs(err, onprem.ErrUnauthorized)

	s.Require().NoError(s.store.SaaSIdentity().SwitchTeam(ctx, record.SessionID, userID, teamB.TeamID, now))
	switched, err := s.store.SaaSIdentity().ResolveSession(ctx, record.SessionID, tokenDigest, now)
	s.Require().NoError(err)
	s.Equal(teamB.TeamID, switched.ScopeID)
	s.NotEqual(ownerA.MembershipID, switched.MembershipID)

	err = s.store.SaaSIdentity().SwitchTeam(ctx, record.SessionID, userID, foreignTeam.TeamID, now)
	s.Require().ErrorIs(err, saas.ErrNotTeamMember)

	s.Require().NoError(s.store.SaaSIdentity().RevokeSession(ctx, record.SessionID, tokenDigest, now))
	_, err = s.store.SaaSIdentity().ResolveSession(ctx, record.SessionID, tokenDigest, now)
	s.Require().ErrorIs(err, onprem.ErrUnauthorized)
}

func (s *saasStoreSuite) TestMemberUpdateAndSuspensionCascade() {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	ownerUserID := s.insertUser("member-owner")
	team, owner := s.createTeam(ownerUserID, "member")
	memberEmail := uniqueCredentialValue("member") + "@example.com"

	// The member's user row is created through the OIDC login path so its
	// user ID matches what later sessions for the same identity resolve to.
	memberIdentity := onprem.ExternalIdentity{
		Issuer: "https://issuer.example.com", Subject: uniqueCredentialValue("member-subject"),
		Email: memberEmail, EmailVerified: true,
	}
	memberSignup, err := s.store.SaaSIdentity().CreateUserSession(ctx, memberIdentity, saas.TeamSessionRecord{
		SessionID: uniqueCredentialValue("member-signup"), SecretDigest: credentialDigest(uniqueCredentialValue("signup-secret")),
		DigestKeyVersion: 1, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	})
	s.Require().NoError(err)
	memberUserID := memberSignup.UserID

	record := onprem.InvitationRecord{
		InvitationID: uniqueCredentialValue("member-invitation"), TokenDigest: credentialDigest(uniqueCredentialValue("member-token")),
		TargetEmail: memberEmail, Role: onprem.RoleMember,
		CreatedByMembershipID: owner.MembershipID, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}
	s.Require().NoError(s.store.SaaSIdentity().CreateInvitation(ctx, team.TeamID, record))
	memberPrincipal, err := s.store.SaaSIdentity().AcceptInvitation(
		ctx, record.InvitationID, record.TokenDigest, memberUserID, memberEmail, true, "", now)
	s.Require().NoError(err)

	member, err := s.store.SaaSIdentity().GetMember(ctx, team.TeamID, memberPrincipal.MembershipID)
	s.Require().NoError(err)
	s.Equal(onprem.RoleMember, member.Role)
	_, err = s.store.SaaSIdentity().GetMember(ctx, "team_"+uniqueCredentialValue("other"), memberPrincipal.MembershipID)
	s.Require().ErrorIs(err, onprem.ErrMembershipConflict)

	promoted := member
	promoted.Role = onprem.RoleAdmin
	promoted.ResourceVersion++
	promoted.UpdatedAt = now
	updated, err := s.store.SaaSIdentity().UpdateMember(ctx, team.TeamID, owner.MembershipID, promoted, now)
	s.Require().NoError(err)
	s.Equal(onprem.RoleAdmin, updated.Role)
	s.Equal(int64(2), updated.ResourceVersion)

	stale := updated
	stale.Role = onprem.RoleMember
	_, err = s.store.SaaSIdentity().UpdateMember(ctx, team.TeamID, owner.MembershipID, stale, now)
	s.Require().ErrorIs(err, onprem.ErrResourceVersionConflict, "replaying the same version must conflict")

	// The last active owner of a team cannot be demoted.
	ownerMember, err := s.store.SaaSIdentity().GetMember(ctx, team.TeamID, owner.MembershipID)
	s.Require().NoError(err)
	demoted := ownerMember
	demoted.Role = onprem.RoleAdmin
	demoted.ResourceVersion++
	_, err = s.store.SaaSIdentity().UpdateMember(ctx, team.TeamID, owner.MembershipID, demoted, now)
	s.Require().ErrorIs(err, onprem.ErrLastActiveOwner)

	// Suspension revokes the member's sessions pointed at this team.
	sessionDigest := credentialDigest(uniqueCredentialValue("member-secret"))
	_, err = s.store.SaaSIdentity().CreateUserSession(ctx, memberIdentity, saas.TeamSessionRecord{
		SessionID: uniqueCredentialValue("member-session"), SecretDigest: sessionDigest,
		DigestKeyVersion: 1, CreatedAt: now, ExpiresAt: now.Add(time.Hour), CurrentTeamID: team.TeamID,
	})
	s.Require().NoError(err)
	suspended := updated
	suspended.Status = onprem.MembershipStatusSuspended
	suspended.ResourceVersion++
	suspended.UpdatedAt = now
	_, err = s.store.SaaSIdentity().UpdateMember(ctx, team.TeamID, owner.MembershipID, suspended, now)
	s.Require().NoError(err)

	var sessionRevoked bool
	s.Require().NoError(s.store.Pool().QueryRow(ctx, `
		SELECT revoked_at IS NOT NULL FROM team_human_sessions
		WHERE user_id = $1 AND current_team_id = $2
	`, memberUserID, team.TeamID).Scan(&sessionRevoked))
	s.True(sessionRevoked, "suspending a member must revoke their sessions for that team")

	s.Equal(team.TeamID, s.auditScope(member.MembershipID))
}

func (s *saasStoreSuite) TestTeamAgentLifecycle() {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	ownerUserID := s.insertUser("agent-owner")
	team, owner := s.createTeam(ownerUserID, "agent")
	actor := onprem.HumanPrincipal{UserID: ownerUserID, MembershipID: owner.MembershipID}

	profile := onprem.AgentProfile{
		AgentID: uniqueCredentialValue("agent"), OwnerMembershipID: owner.MembershipID,
		DisplayName: "Build Bot", AgentType: "codex", Status: onprem.AgentStatusActive,
		DirectoryVisible: true, CreatedAt: now, UpdatedAt: now,
		CreationIdempotencyKey: uniqueCredentialValue("agent-key"),
	}
	created, err := s.store.SaaSRegistry().CreateAgent(ctx, team.TeamID, profile)
	s.Require().NoError(err)
	s.Equal(profile.AgentID, created.AgentID)
	s.Equal(ownerUserID, created.OwnerUserID)
	s.Equal(int64(1), created.ResourceVersion)

	replayed, err := s.store.SaaSRegistry().CreateAgent(ctx, team.TeamID, profile)
	s.Require().NoError(err)
	s.Equal(created, replayed, "creation idempotency replay returns the stored agent")

	conflicting := profile
	conflicting.DisplayName = "Renamed Bot"
	_, err = s.store.SaaSRegistry().CreateAgent(ctx, team.TeamID, conflicting)
	s.Require().ErrorIs(err, onprem.ErrIdempotencyConflict)

	duplicate := profile
	duplicate.CreationIdempotencyKey = ""
	_, err = s.store.SaaSRegistry().CreateAgent(ctx, team.TeamID, duplicate)
	s.Require().ErrorIs(err, onprem.ErrAgentIDConflict)

	loaded, err := s.store.SaaSRegistry().GetOwnedAgent(ctx, team.TeamID, owner.MembershipID, profile.AgentID)
	s.Require().NoError(err)
	s.Equal(created, loaded)

	agents, err := s.store.SaaSRegistry().ListOwnedAgents(ctx, team.TeamID, owner.MembershipID, onprem.AgentFilter{Limit: 10})
	s.Require().NoError(err)
	s.Len(agents, 1)

	// Cross-team reads see nothing.
	otherTeam, otherOwner := s.createTeam(ownerUserID, "agent-other")
	_, err = s.store.SaaSRegistry().GetOwnedAgent(ctx, otherTeam.TeamID, otherOwner.MembershipID, profile.AgentID)
	s.Require().ErrorIs(err, onprem.ErrAgentNotFound)
	agents, err = s.store.SaaSRegistry().ListOwnedAgents(ctx, otherTeam.TeamID, otherOwner.MembershipID, onprem.AgentFilter{Limit: 10})
	s.Require().NoError(err)
	s.Empty(agents)

	updatedProfile := created
	updatedProfile.DisplayName = "Build Bot v2"
	updatedProfile.ResourceVersion = 2
	updatedProfile.UpdatedAt = now
	updated, err := s.store.SaaSRegistry().UpdateOwnedAgent(ctx, team.TeamID, owner.MembershipID, actor, updatedProfile)
	s.Require().NoError(err)
	s.Equal("Build Bot v2", updated.DisplayName)

	_, err = s.store.SaaSRegistry().UpdateOwnedAgent(ctx, team.TeamID, owner.MembershipID, actor, updatedProfile)
	s.Require().ErrorIs(err, onprem.ErrResourceVersionConflict)

	retireKey := uniqueCredentialValue("retire-key")
	retired, err := s.store.SaaSRegistry().RetireOwnedAgent(
		ctx, team.TeamID, owner.MembershipID, actor, profile.AgentID, 2, retireKey, now)
	s.Require().NoError(err)
	s.Equal(onprem.AgentStatusRetired, retired.Status)
	s.Require().NotNil(retired.RetiredAt)

	retiredReplay, err := s.store.SaaSRegistry().RetireOwnedAgent(
		ctx, team.TeamID, owner.MembershipID, actor, profile.AgentID, 2, retireKey, now)
	s.Require().NoError(err)
	s.Equal(retired, retiredReplay)

	_, err = s.store.SaaSRegistry().RetireOwnedAgent(
		ctx, team.TeamID, owner.MembershipID, actor, profile.AgentID, 3, "", now)
	s.Require().ErrorIs(err, onprem.ErrInvalidStateTransition)

	s.Equal(team.TeamID, s.auditScope(profile.AgentID))
}

func (s *saasStoreSuite) TestCredentialIssueAuthenticateRotateRevoke() {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	ownerUserID := s.insertUser("cred-owner")
	team, owner := s.createTeam(ownerUserID, "cred")
	actor := onprem.HumanPrincipal{UserID: ownerUserID, MembershipID: owner.MembershipID}

	agent, err := s.store.SaaSRegistry().CreateAgent(ctx, team.TeamID, onprem.AgentProfile{
		AgentID: uniqueCredentialValue("cred-agent"), OwnerMembershipID: owner.MembershipID,
		DisplayName: "Cred Bot", Status: onprem.AgentStatusActive, CreatedAt: now, UpdatedAt: now,
	})
	s.Require().NoError(err)

	enrollmentToken := uniqueCredentialValue("enrollment-secret")
	enrollment := onprem.EnrollmentRecord{
		ID: uniqueCredentialValue("enrollment"), TokenDigest: credentialDigest(enrollmentToken),
		UserID: ownerUserID, AgentID: agent.AgentID, CredentialLabel: "laptop",
		Permissions: []onprem.Permission{onprem.PermissionGet, onprem.PermissionSearch},
		CreatedAt:   now, ExpiresAt: now.Add(time.Hour), DigestKeyVersion: 1,
	}
	s.Require().NoError(s.store.SaaSCredentials().CreateOwnedEnrollment(ctx, team.TeamID, owner.MembershipID, enrollment))

	pending, err := s.store.SaaSCredentials().ListOwnedEnrollments(
		ctx, team.TeamID, owner.MembershipID, agent.AgentID, onprem.AgentArtifactFilter{Limit: 10}, now)
	s.Require().NoError(err)
	s.Require().Len(pending, 1)
	s.Equal("pending", pending[0].Status)

	// Enrollments cannot be created for a foreign team/membership pair.
	err = s.store.SaaSCredentials().CreateOwnedEnrollment(ctx, "team_"+uniqueCredentialValue("foreign"), owner.MembershipID, onprem.EnrollmentRecord{
		ID: uniqueCredentialValue("enrollment-foreign"), TokenDigest: credentialDigest(uniqueCredentialValue("foreign-secret")),
		AgentID: agent.AgentID, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	})
	s.Require().ErrorIs(err, onprem.ErrForbidden)

	apiKey := uniqueCredentialValue("api-key")
	credential := onprem.CredentialRecord{
		ID: uniqueCredentialValue("credential"), KeyDigest: credentialDigest(apiKey),
		CreatedAt: now, DigestKeyVersion: 1,
	}
	consumed, err := s.store.SaaSCredentials().ExchangeEnrollment(ctx, enrollment.ID, enrollment.TokenDigest, credential, now)
	s.Require().NoError(err)
	s.Equal(enrollment.ID, consumed.ID)
	s.Require().NotNil(consumed.ConsumedAt)
	s.Equal(agent.AgentID, consumed.AgentID)

	// A consumed enrollment cannot be exchanged again.
	_, err = s.store.SaaSCredentials().ExchangeEnrollment(ctx, enrollment.ID, enrollment.TokenDigest, onprem.CredentialRecord{
		ID: uniqueCredentialValue("credential-second"), KeyDigest: credentialDigest(uniqueCredentialValue("second-key")),
		CreatedAt: now,
	}, now)
	s.Require().ErrorIs(err, onprem.ErrEnrollmentInvalid)

	scoped, err := s.store.SaaSCredentials().ResolveCredential(ctx, credential.ID, credential.KeyDigest, now)
	s.Require().NoError(err)
	s.Equal(team.TeamID, scoped.TeamID, "authentication must resolve the credential's team as the scope")
	s.Equal(agent.AgentID, scoped.AgentID)
	s.Equal(onprem.CredentialKindAgent, scoped.Kind)
	s.ElementsMatch([]onprem.Permission{onprem.PermissionGet, onprem.PermissionSearch}, scoped.Permissions)
	s.Require().NotNil(scoped.LastUsedAt)

	_, err = s.store.SaaSCredentials().ResolveCredential(ctx, credential.ID, credentialDigest(uniqueCredentialValue("wrong-key")), now)
	s.Require().ErrorIs(err, onprem.ErrUnauthorized)

	// Rotation issues a successor inside the same team; the wrong team
	// cannot rotate it.
	rotatedKey := uniqueCredentialValue("rotated-key")
	replacement := onprem.CredentialRecord{
		ID: uniqueCredentialValue("credential-rotated"), KeyDigest: credentialDigest(rotatedKey),
		UserID: ownerUserID, MembershipID: owner.MembershipID, AgentID: agent.AgentID,
		Label: "laptop", Permissions: scoped.Permissions, CreatedAt: now, DigestKeyVersion: 1,
		RotatedFromCredentialID: credential.ID,
	}
	err = s.store.SaaSCredentials().RotateCredential(ctx, "team_"+uniqueCredentialValue("wrong-team"), credential.ID, replacement, now.Add(time.Minute))
	s.Require().ErrorIs(err, onprem.ErrUnauthorized)
	s.Require().NoError(s.store.SaaSCredentials().RotateCredential(ctx, team.TeamID, credential.ID, replacement, now.Add(time.Minute)))
	rotated, err := s.store.SaaSCredentials().ResolveCredential(ctx, replacement.ID, replacement.KeyDigest, now)
	s.Require().NoError(err)
	s.Equal(team.TeamID, rotated.TeamID)

	listed, err := s.store.SaaSCredentials().ListOwnedCredentials(
		ctx, team.TeamID, owner.MembershipID, agent.AgentID, onprem.AgentArtifactFilter{Limit: 10}, now)
	s.Require().NoError(err)
	s.Len(listed, 2)

	// Owner revocation with idempotency replay.
	revokeKey := uniqueCredentialValue("revoke-key")
	revoked, err := s.store.SaaSCredentials().RevokeOwnedCredential(
		ctx, team.TeamID, owner.MembershipID, actor, agent.AgentID, replacement.ID, revokeKey, now)
	s.Require().NoError(err)
	s.Equal(replacement.ID, revoked.CredentialID)
	s.Require().NotNil(revoked.RevokedAt)

	revokedReplay, err := s.store.SaaSCredentials().RevokeOwnedCredential(
		ctx, team.TeamID, owner.MembershipID, actor, agent.AgentID, replacement.ID, revokeKey, now)
	s.Require().NoError(err)
	s.Equal(revoked.CredentialID, revokedReplay.CredentialID)

	_, err = s.store.SaaSCredentials().ResolveCredential(ctx, replacement.ID, replacement.KeyDigest, now)
	s.Require().ErrorIs(err, onprem.ErrUnauthorized, "a revoked credential must not authenticate")

	_, err = s.store.SaaSCredentials().RevokeOwnedCredential(
		ctx, team.TeamID, owner.MembershipID, actor, agent.AgentID, replacement.ID, uniqueCredentialValue("other-revoke-key"), now)
	s.Require().ErrorIs(err, onprem.ErrCredentialNotFound)

	// Owner-scoped enrollment revocation with replay.
	secondEnrollment := onprem.EnrollmentRecord{
		ID: uniqueCredentialValue("enrollment-revoke"), TokenDigest: credentialDigest(uniqueCredentialValue("revoke-secret")),
		AgentID: agent.AgentID, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	s.Require().NoError(s.store.SaaSCredentials().CreateOwnedEnrollment(ctx, team.TeamID, owner.MembershipID, secondEnrollment))
	enrollRevokeKey := uniqueCredentialValue("enroll-revoke-key")
	revokedEnrollment, err := s.store.SaaSCredentials().RevokeOwnedEnrollment(
		ctx, team.TeamID, owner.MembershipID, actor, agent.AgentID, secondEnrollment.ID, enrollRevokeKey, now)
	s.Require().NoError(err)
	s.Equal("revoked", revokedEnrollment.Status)
	revokedEnrollmentReplay, err := s.store.SaaSCredentials().RevokeOwnedEnrollment(
		ctx, team.TeamID, owner.MembershipID, actor, agent.AgentID, secondEnrollment.ID, enrollRevokeKey, now)
	s.Require().NoError(err)
	s.Equal(revokedEnrollment.EnrollmentID, revokedEnrollmentReplay.EnrollmentID)

	s.Equal(team.TeamID, s.auditScope(credential.ID), "credential audit rows must carry the team scope")
}

func (s *saasStoreSuite) TestScopedAuditQueries() {
	ctx := context.Background()
	ownerUserID := s.insertUser("audit-owner")
	team, _ := s.createTeam(ownerUserID, "audit")
	otherTeam, _ := s.createTeam(ownerUserID, "audit-other")

	events, err := s.store.SaaSIdentity().ListAuditEvents(ctx, team.TeamID, onprem.AuditFilter{Limit: 10})
	s.Require().NoError(err)
	s.Require().NotEmpty(events)
	s.Equal("identity.team.created", events[0].Action)
	s.Equal(team.TeamID, events[0].TargetID)

	otherEvents, err := s.store.SaaSIdentity().ListAuditEvents(
		ctx, otherTeam.TeamID, onprem.AuditFilter{Action: "identity.team.created", TargetID: team.TeamID, Limit: 10})
	s.Require().NoError(err)
	s.Empty(otherEvents, "audit events must not leak across teams")

	loaded, err := s.store.SaaSIdentity().GetAuditEvent(ctx, team.TeamID, events[0].AuditEventID)
	s.Require().NoError(err)
	s.Equal(events[0], loaded)
	_, err = s.store.SaaSIdentity().GetAuditEvent(ctx, otherTeam.TeamID, events[0].AuditEventID)
	s.Require().ErrorIs(err, onprem.ErrAuditEventNotFound)

	_, err = s.store.SaaSIdentity().ListAuditEvents(ctx, team.TeamID, onprem.AuditFilter{Cursor: "not-a-number", Limit: 10})
	s.Require().ErrorIs(err, onprem.ErrInvalidIdentityInput)
}

func (s *saasStoreSuite) TestAgentSuspensionRevokesCredentials() {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	ownerUserID := s.insertUser("suspend-owner")
	team, owner := s.createTeam(ownerUserID, "suspend")
	actor := onprem.HumanPrincipal{UserID: ownerUserID, MembershipID: owner.MembershipID}

	agent, err := s.store.SaaSRegistry().CreateAgent(ctx, team.TeamID, onprem.AgentProfile{
		AgentID: uniqueCredentialValue("suspend-agent"), OwnerMembershipID: owner.MembershipID,
		DisplayName: "Suspend Bot", Status: onprem.AgentStatusActive, CreatedAt: now, UpdatedAt: now,
	})
	s.Require().NoError(err)
	enrollment := onprem.EnrollmentRecord{
		ID: uniqueCredentialValue("suspend-enrollment"), TokenDigest: credentialDigest(uniqueCredentialValue("suspend-secret")),
		AgentID: agent.AgentID, Permissions: []onprem.Permission{onprem.PermissionGet},
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	s.Require().NoError(s.store.SaaSCredentials().CreateOwnedEnrollment(ctx, team.TeamID, owner.MembershipID, enrollment))
	credential := onprem.CredentialRecord{
		ID: uniqueCredentialValue("suspend-credential"), KeyDigest: credentialDigest(uniqueCredentialValue("suspend-key")),
		CreatedAt: now, DigestKeyVersion: 1,
	}
	_, err = s.store.SaaSCredentials().ExchangeEnrollment(ctx, enrollment.ID, enrollment.TokenDigest, credential, now)
	s.Require().NoError(err)

	suspended := agent
	suspended.Status = onprem.AgentStatusSuspended
	suspended.ResourceVersion = 2
	suspended.UpdatedAt = now
	_, err = s.store.SaaSRegistry().UpdateOwnedAgent(ctx, team.TeamID, owner.MembershipID, actor, suspended)
	s.Require().NoError(err)

	_, err = s.store.SaaSCredentials().ResolveCredential(ctx, credential.ID, credential.KeyDigest, now)
	s.Require().ErrorIs(err, onprem.ErrUnauthorized, "suspending the agent must revoke its credentials")
}
