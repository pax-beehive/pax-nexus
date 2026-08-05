package postgres_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/stretchr/testify/suite"
)

type identityRegistryStoreSuite struct {
	suite.Suite
	store *postgres.Store
}

func TestIdentityRegistryStoreSuite(t *testing.T) {
	suite.Run(t, new(identityRegistryStoreSuite))
}

func (s *identityRegistryStoreSuite) SetupSuite() {
	store, err := postgres.Open(context.Background(), testDSN(s.T()))
	s.Require().NoError(err)
	s.Require().NoError(store.Migrate(context.Background()))
	s.store = store
}

func (s *identityRegistryStoreSuite) TearDownSuite() {
	if s.store != nil {
		s.store.Close()
	}
}

func (s *identityRegistryStoreSuite) insertActiveUserWithMembership(label string) (string, string) {
	ctx := context.Background()
	userID := uniqueCredentialValue(label + "-user")
	membershipID := uniqueCredentialValue(label + "-membership")
	_, err := s.store.Pool().Exec(ctx, `
		INSERT INTO onprem_users (user_id, display_name, identity_status, created_at, updated_at)
		VALUES ($1, 'Store Test User', 'active', now(), now())
	`, userID)
	s.Require().NoError(err)
	_, err = s.store.Pool().Exec(ctx, `
		INSERT INTO onprem_memberships (membership_id, user_id, role, status, joined_at, updated_at)
		VALUES ($1, $2, 'member', 'active', now(), now())
	`, membershipID, userID)
	s.Require().NoError(err)
	return userID, membershipID
}

// TestListExpiringEnrollmentsIsTeamWideAndFiltersLifecycleState exercises
// RegistryStore.ListExpiringEnrollments directly (bypassing
// RegistryService, whose authorization is covered in
// internal/deployment/onprem/registry_test.go). It asserts the method is
// team-wide (spans two different owning memberships, unlike the
// owner-scoped ListOwnedEnrollments), excludes consumed/revoked/out-of-range
// rows, orders soonest-expiry first, and — the status baseline bug this test
// was extended to catch — reports a row that hasn't actually expired yet
// (expires_at is after `now` but before the lookahead cutoff `before`) as
// 'pending', not 'expired'. A row already past `now` is still included (it's
// within the lookahead window) but correctly reported 'expired'.
func (s *identityRegistryStoreSuite) TestListExpiringEnrollmentsIsTeamWideAndFiltersLifecycleState() {
	ctx := context.Background()
	registry := s.store.Registry()
	now := time.Now().UTC().Truncate(time.Microsecond)
	cutoff := now.Add(24 * time.Hour)

	userA, membershipA := s.insertActiveUserWithMembership("expiring-owner-a")
	userB, membershipB := s.insertActiveUserWithMembership("expiring-owner-b")

	newRecord := func(label string, userID string, membershipID string, expiresAt time.Time) onprem.EnrollmentRecord {
		return onprem.EnrollmentRecord{
			ID:               uniqueCredentialValue("expiring-enrollment-" + label),
			TokenDigest:      credentialDigest(uniqueCredentialValue("expiring-token-" + label)),
			DigestKeyVersion: 1,
			UserID:           userID,
			MembershipID:     membershipID,
			Kind:             onprem.CredentialKindDevice,
			AgentID:          "",
			CredentialLabel:  "device-" + label,
			Permissions:      []onprem.Permission{onprem.PermissionAgentProvision},
			CreatedAt:        now,
			ExpiresAt:        expiresAt,
		}
	}

	// soonB and soonA belong to different owning memberships (team-wide, not
	// owner-scoped), have not actually expired yet (expires_at is after
	// `now`), and both fall inside the cutoff window: they must both appear,
	// soonest expiry first, with status 'pending' — not 'expired'.
	soonB := newRecord("soon-b", userB, membershipB, now.Add(2*time.Hour))
	s.Require().NoError(registry.CreateDeviceEnrollment(ctx, membershipB, soonB))
	soonA := newRecord("soon-a", userA, membershipA, now.Add(1*time.Hour))
	s.Require().NoError(registry.CreateDeviceEnrollment(ctx, membershipA, soonA))

	// alreadyExpired is already past `now` (expires_at is in the past) but
	// still inside the lookahead window relative to `cutoff`: it must appear
	// (it's unconsumed and unrevoked) and be reported status 'expired'.
	alreadyExpired := newRecord("already-expired", userA, membershipA, now.Add(-1*time.Hour))
	s.Require().NoError(registry.CreateDeviceEnrollment(ctx, membershipA, alreadyExpired))

	// consumedSoon is inside the cutoff window but already claimed: must be
	// excluded even though its expiry alone would qualify it.
	consumedSoon := newRecord("consumed-soon", userA, membershipA, now.Add(3*time.Hour))
	s.Require().NoError(registry.CreateDeviceEnrollment(ctx, membershipA, consumedSoon))
	_, err := s.store.Pool().Exec(ctx, `
		UPDATE agent_enrollments SET consumed_at = $1 WHERE enrollment_id = $2
	`, now, consumedSoon.ID)
	s.Require().NoError(err)

	// revokedSoon is inside the cutoff window but revoked: must be excluded.
	revokedSoon := newRecord("revoked-soon", userB, membershipB, now.Add(4*time.Hour))
	s.Require().NoError(registry.CreateDeviceEnrollment(ctx, membershipB, revokedSoon))
	_, err = s.store.Pool().Exec(ctx, `
		UPDATE agent_enrollments SET revoked_at = $1 WHERE enrollment_id = $2
	`, now, revokedSoon.ID)
	s.Require().NoError(err)

	// outOfRange expires well beyond the cutoff: must be excluded.
	outOfRange := newRecord("out-of-range", userA, membershipA, now.Add(100*time.Hour))
	s.Require().NoError(registry.CreateDeviceEnrollment(ctx, membershipA, outOfRange))

	results, err := registry.ListExpiringEnrollments(ctx, cutoff, now, 500)
	s.Require().NoError(err)

	type idStatus struct {
		id     string
		status string
	}
	var matches []idStatus
	for _, result := range results {
		if result.EnrollmentID == soonA.ID || result.EnrollmentID == soonB.ID ||
			result.EnrollmentID == alreadyExpired.ID || result.EnrollmentID == consumedSoon.ID ||
			result.EnrollmentID == revokedSoon.ID || result.EnrollmentID == outOfRange.ID {
			matches = append(matches, idStatus{id: result.EnrollmentID, status: result.Status})
		}
	}
	s.Equal([]idStatus{
		{id: alreadyExpired.ID, status: "expired"},
		{id: soonA.ID, status: "pending"},
		{id: soonB.ID, status: "pending"},
	}, matches, "expects only the pending/expired, in-range enrollments, soonest expiry first, with correct status")
}

func (s *identityRegistryStoreSuite) TestOwnedAgentMutationsDisambiguateZeroRowConflicts() {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	ownerUserID, ownerMembershipID := s.insertActiveUserWithMembership("conflict-owner")
	_, foreignMembershipID := s.insertActiveUserWithMembership("conflict-foreign")
	agentID := uniqueCredentialValue("conflict-agent")
	_, err := s.store.Pool().Exec(ctx, `
		INSERT INTO onprem_agents (
			agent_id, owner_membership_id, display_name, agent_type, status,
			directory_visible, created_at, updated_at, resource_version
		) VALUES ($1, $2, 'Conflict Agent', 'codex', 'active', false, $3, $3, 1)
	`, agentID, ownerMembershipID, now)
	s.Require().NoError(err)
	actor := onprem.HumanPrincipal{UserID: ownerUserID, MembershipID: ownerMembershipID}
	updateProfile := func(agent string, version int64) onprem.AgentProfile {
		return onprem.AgentProfile{
			AgentID: agent, DisplayName: "Conflict Agent", AgentType: "codex",
			Status: onprem.AgentStatusActive, DirectoryVisible: false,
			UpdatedAt: now.Add(time.Minute), ResourceVersion: version,
		}
	}

	preRetirementCases := []struct {
		name string
		run  func() error
		want error
	}{
		{name: "update missing agent", run: func() error {
			_, err := s.store.Registry().UpdateOwnedAgent(
				ctx, ownerMembershipID, actor, updateProfile(uniqueCredentialValue("absent-agent"), 2),
			)
			return err
		}, want: onprem.ErrAgentNotFound},
		{name: "update foreign agent", run: func() error {
			_, err := s.store.Registry().UpdateOwnedAgent(
				ctx, foreignMembershipID, actor, updateProfile(agentID, 2),
			)
			return err
		}, want: onprem.ErrAgentNotFound},
		{name: "update stale version", run: func() error {
			_, err := s.store.Registry().UpdateOwnedAgent(ctx, ownerMembershipID, actor, updateProfile(agentID, 5))
			return err
		}, want: onprem.ErrResourceVersionConflict},
		{name: "retire missing agent", run: func() error {
			_, err := s.store.Registry().RetireOwnedAgent(
				ctx, ownerMembershipID, actor, uniqueCredentialValue("absent-agent"), 1, "", now,
			)
			return err
		}, want: onprem.ErrAgentNotFound},
		{name: "retire foreign agent", run: func() error {
			_, err := s.store.Registry().RetireOwnedAgent(ctx, foreignMembershipID, actor, agentID, 1, "", now)
			return err
		}, want: onprem.ErrAgentNotFound},
		{name: "retire stale version", run: func() error {
			_, err := s.store.Registry().RetireOwnedAgent(ctx, ownerMembershipID, actor, agentID, 7, "", now)
			return err
		}, want: onprem.ErrResourceVersionConflict},
	}
	for _, testCase := range preRetirementCases {
		s.Run(testCase.name, func() {
			s.Require().ErrorIs(testCase.run(), testCase.want)
		})
	}

	retired, err := s.store.Registry().RetireOwnedAgent(ctx, ownerMembershipID, actor, agentID, 1, "", now)
	s.Require().NoError(err)
	s.Equal(onprem.AgentStatusRetired, retired.Status)

	s.Run("update retired agent", func() {
		_, err := s.store.Registry().UpdateOwnedAgent(
			ctx, ownerMembershipID, actor, updateProfile(agentID, retired.ResourceVersion+1),
		)
		s.Require().ErrorIs(err, onprem.ErrInvalidStateTransition)
	})
	s.Run("retire retired agent", func() {
		_, err := s.store.Registry().RetireOwnedAgent(
			ctx, ownerMembershipID, actor, agentID, retired.ResourceVersion, "", now,
		)
		s.Require().ErrorIs(err, onprem.ErrInvalidStateTransition)
	})
}

func (s *identityRegistryStoreSuite) TestSubjectOnlyInvitationFailsEmailAcceptanceDeterministically() {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, creatorMembershipID := s.insertActiveUserWithMembership("subject-invite-creator")
	invitationID := uniqueCredentialValue("subject-invite")
	digest := onprem.Digest(sha256.Sum256([]byte(invitationID)))
	s.Require().NoError(s.store.Identity().CreateInvitation(ctx, onprem.InvitationRecord{
		InvitationID: invitationID, TokenDigest: digest, DigestKeyVersion: 1,
		TargetIssuer: "https://identity.test", TargetSubject: uniqueCredentialValue("subject-only"),
		Role: onprem.RoleMember, CreatedByMembershipID: creatorMembershipID,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}))

	_, err := s.store.Identity().AcceptInvitation(
		ctx, invitationID, digest, uniqueCredentialValue("acceptor"),
		"someone@example.com", true, "", now,
	)

	// The email acceptance flow must reject a subject-only invitation with
	// the contract sentinel instead of failing a NULL-into-bool scan.
	s.Require().ErrorIs(err, onprem.ErrInvitationInvalid)
}

func (s *identityRegistryStoreSuite) TestBootstrapRejectsAnExistingActiveOwner() {
	ctx := context.Background()
	identity, err := onprem.NewIdentityService(s.store.Identity(), onprem.IdentityConfig{
		BootstrapSecret: "bootstrap-secret",
		SecretPepper:    "0123456789abcdef0123456789abcdef",
		SessionTTL:      time.Hour, InvitationTTL: time.Hour,
	})
	s.Require().NoError(err)
	session, err := identity.Login(ctx, onprem.ExternalIdentity{
		Issuer: "https://identity.test", Subject: uniqueCredentialValue("bootstrap-candidate"),
		Email: uniqueCredentialValue("bootstrap") + "@example.com", EmailVerified: true,
	})
	s.Require().NoError(err)
	_, err = s.store.Pool().Exec(ctx, `
		INSERT INTO onprem_memberships (
			membership_id, user_id, role, status, joined_at, updated_at
		) VALUES ($1, $2, 'owner', 'active', now(), now())
	`, uniqueCredentialValue("existing-owner"), session.Principal.UserID)
	s.Require().NoError(err)
	_, err = s.store.Pool().Exec(ctx, `
		UPDATE onprem_installation_state
		SET bootstrap_claimed_at = NULL, bootstrap_claimed_by_membership_id = NULL
		WHERE singleton_id = 1
	`)
	s.Require().NoError(err)

	_, err = identity.ClaimBootstrap(ctx, session.Principal, "bootstrap-secret")

	s.Require().ErrorIs(err, onprem.ErrBootstrapClosed)
}

func (s *identityRegistryStoreSuite) TestInvitationToAgentDirectoryFlow() {
	ctx := context.Background()
	identity, err := onprem.NewIdentityService(s.store.Identity(), onprem.IdentityConfig{
		BootstrapSecret: uniqueCredentialValue("bootstrap"),
		SecretPepper:    "0123456789abcdef0123456789abcdef",
		SessionTTL:      time.Hour, InvitationTTL: time.Hour,
	})
	s.Require().NoError(err)
	registry, err := onprem.NewRegistryService(s.store.Registry(), onprem.RegistryConfig{
		SecretPepper: "0123456789abcdef0123456789abcdef",
		MemberGrantablePermissions: []onprem.Permission{
			onprem.PermissionObserve, onprem.PermissionSearch, onprem.PermissionGet,
			onprem.PermissionChannelSend, onprem.PermissionChannelReceive,
		},
	})
	s.Require().NoError(err)
	credentials, err := onprem.NewCredentialService(s.store.Credentials(), onprem.CredentialConfig{
		RotationOverlap: time.Minute, SecretPepper: "0123456789abcdef0123456789abcdef",
	})
	s.Require().NoError(err)

	ownerSession, err := identity.Login(ctx, onprem.ExternalIdentity{
		Issuer: "https://identity.test", Subject: uniqueCredentialValue("owner-subject"),
		Email: uniqueCredentialValue("owner") + "@example.com", EmailVerified: true,
		DisplayName: "Owner",
	})
	s.Require().NoError(err)
	ownerMembershipID := uniqueCredentialValue("owner-membership")
	_, err = s.store.Pool().Exec(ctx, `
		INSERT INTO onprem_memberships (
			membership_id, user_id, role, status, joined_at, updated_at
		) VALUES ($1, $2, 'owner', 'active', now(), now())
	`, ownerMembershipID, ownerSession.Principal.UserID)
	s.Require().NoError(err)
	owner, err := identity.AuthenticateSession(ctx, ownerSession.Token)
	s.Require().NoError(err)

	memberEmail := uniqueCredentialValue("member") + "@example.com"
	invitation, err := identity.CreateInvitation(ctx, owner, onprem.InvitationRequest{
		TargetEmail: memberEmail, Role: onprem.RoleMember,
	})
	s.Require().NoError(err)
	memberSession, err := identity.Login(ctx, onprem.ExternalIdentity{
		Issuer: "https://identity.test", Subject: uniqueCredentialValue("member-subject"),
		Email: memberEmail, EmailVerified: true, DisplayName: "Member",
	})
	s.Require().NoError(err)
	member, err := identity.AcceptInvitation(ctx, memberSession.Principal, invitation.Token, "accept-member")
	s.Require().NoError(err)
	replayedMember, err := identity.AcceptInvitation(ctx, member, invitation.Token, "accept-member")
	s.Require().NoError(err)
	s.Equal(member.MembershipID, replayedMember.MembershipID)

	receiverID := uniqueCredentialValue("receiver")
	receiverRequest := onprem.CreateAgentRequest{
		AgentID: receiverID, DisplayName: "Receiver", Description: "Receives capsules",
		AgentType: "codex", DirectoryVisible: true, IdempotencyKey: uniqueCredentialValue("create-receiver"),
	}
	_, err = registry.CreateAgent(ctx, member, receiverRequest)
	s.Require().NoError(err)
	replayed, err := registry.CreateAgent(ctx, member, receiverRequest)
	s.Require().NoError(err)
	s.Equal(receiverID, replayed.AgentID)
	conflictingRequest := receiverRequest
	conflictingRequest.DisplayName = "Different Receiver"
	_, err = registry.CreateAgent(ctx, member, conflictingRequest)
	s.Require().ErrorIs(err, onprem.ErrIdempotencyConflict)
	credentialExpiresAt := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Microsecond)
	receiverEnrollment, err := registry.CreateEnrollment(ctx, member, receiverID, onprem.OwnerEnrollmentRequest{
		CredentialLabel:     "receiver-device",
		Permissions:         []onprem.Permission{onprem.PermissionChannelReceive},
		CredentialExpiresAt: &credentialExpiresAt,
	})
	s.Require().NoError(err)
	receiverCredential, err := credentials.ExchangeEnrollment(ctx, receiverEnrollment.Token)
	s.Require().NoError(err)
	s.Require().NotNil(receiverCredential.ExpiresAt)
	s.WithinDuration(credentialExpiresAt, *receiverCredential.ExpiresAt, time.Microsecond)
	enrollments, err := registry.ListEnrollments(ctx, member, receiverID, onprem.AgentArtifactFilter{Status: "consumed"})
	s.Require().NoError(err)
	s.Require().Len(enrollments, 1)
	s.Equal(receiverEnrollment.ID, enrollments[0].EnrollmentID)
	credentialMetadata, err := registry.ListCredentials(ctx, member, receiverID, onprem.AgentArtifactFilter{Status: "active"})
	s.Require().NoError(err)
	s.Require().Len(credentialMetadata, 1)
	s.Equal(receiverCredential.CredentialID, credentialMetadata[0].CredentialID)

	revocableEnrollment, err := registry.CreateEnrollment(ctx, member, receiverID, onprem.OwnerEnrollmentRequest{
		CredentialLabel: "unused-device", Permissions: []onprem.Permission{onprem.PermissionChannelReceive},
	})
	s.Require().NoError(err)
	adminEnrollments, err := registry.ListAdminEnrollments(
		ctx, owner, receiverID, onprem.AgentArtifactFilter{Status: "pending"},
	)
	s.Require().NoError(err)
	s.Require().Len(adminEnrollments, 1)
	revokedEnrollment, err := registry.RevokeAdminEnrollment(
		ctx, owner, receiverID, revocableEnrollment.ID, "revoke-unused-enrollment",
	)
	s.Require().NoError(err)
	s.Equal("revoked", revokedEnrollment.Status)
	replayedEnrollment, err := registry.RevokeAdminEnrollment(
		ctx, owner, receiverID, revocableEnrollment.ID, "revoke-unused-enrollment",
	)
	s.Require().NoError(err)
	s.Equal(revokedEnrollment.EnrollmentID, replayedEnrollment.EnrollmentID)

	senderID := uniqueCredentialValue("sender")
	_, err = registry.CreateAgent(ctx, owner, onprem.CreateAgentRequest{
		AgentID: senderID, DisplayName: "Sender",
	})
	s.Require().NoError(err)
	senderEnrollment, err := registry.CreateEnrollment(ctx, owner, senderID, onprem.OwnerEnrollmentRequest{
		CredentialLabel: "sender-device",
		Permissions:     []onprem.Permission{onprem.PermissionChannelSend},
	})
	s.Require().NoError(err)
	senderCredential, err := credentials.ExchangeEnrollment(ctx, senderEnrollment.Token)
	s.Require().NoError(err)
	sender, err := credentials.Authenticate(ctx, senderCredential.APIKey)
	s.Require().NoError(err)

	directory, err := registry.ListDirectoryAgents(ctx, sender, onprem.AgentFilter{Query: receiverID})
	s.Require().NoError(err)
	s.Require().Len(directory, 1)
	s.Equal(receiverID, directory[0].AgentID)
	resolved, err := registry.GetDirectoryAgent(ctx, sender, receiverID)
	s.Require().NoError(err)
	s.Equal("Receives capsules", resolved.Description)
	adminProfiles, err := registry.ListAdminAgents(ctx, owner, onprem.AgentFilter{
		OwnerMembershipID: member.MembershipID,
	})
	s.Require().NoError(err)
	s.Require().Len(adminProfiles, 1)
	adminCredentials, err := registry.ListAdminCredentials(
		ctx, owner, receiverID, onprem.AgentArtifactFilter{Status: "active"},
	)
	s.Require().NoError(err)
	s.Require().Len(adminCredentials, 1)
	revokedCredential, err := registry.RevokeAdminCredential(
		ctx, owner, receiverID, receiverCredential.CredentialID, "revoke-receiver-credential",
	)
	s.Require().NoError(err)
	s.NotNil(revokedCredential.RevokedAt)
	replayedCredential, err := registry.RevokeAdminCredential(
		ctx, owner, receiverID, receiverCredential.CredentialID, "revoke-receiver-credential",
	)
	s.Require().NoError(err)
	s.Equal(revokedCredential.CredentialID, replayedCredential.CredentialID)
	_, err = credentials.Authenticate(ctx, receiverCredential.APIKey)
	s.Require().ErrorIs(err, onprem.ErrUnauthorized)
	suspended := onprem.AgentStatusSuspended
	governed, err := registry.UpdateAdminAgent(ctx, owner, receiverID, onprem.UpdateAgentRequest{
		Status: &suspended, ResourceVersion: 1,
	})
	s.Require().NoError(err)
	s.Equal(onprem.AgentStatusSuspended, governed.Status)
	_, err = s.store.Registry().TransferAgent(
		ctx, owner, receiverID, owner.MembershipID, governed.ResourceVersion+1, time.Now().UTC(),
	)
	s.Require().ErrorIs(err, onprem.ErrResourceVersionConflict)
	transferred, err := registry.TransferAgent(ctx, owner, receiverID, onprem.TransferAgentRequest{
		TargetMembershipID: owner.MembershipID, ResourceVersion: governed.ResourceVersion,
	})
	s.Require().NoError(err)
	s.Equal(owner.MembershipID, transferred.OwnerMembershipID)
	auditEvents, err := identity.ListAuditEvents(ctx, owner, onprem.AuditFilter{
		TargetKind: "agent", TargetID: receiverID,
	})
	s.Require().NoError(err)
	s.NotEmpty(auditEvents)
	auditEvent, err := identity.GetAuditEvent(ctx, owner, auditEvents[0].AuditEventID)
	s.Require().NoError(err)
	s.Equal(receiverID, auditEvent.TargetID)

	retiredAgentID := uniqueCredentialValue("retire-agent")
	createdForRetirement, err := registry.CreateAgent(ctx, owner, onprem.CreateAgentRequest{
		AgentID: retiredAgentID, DisplayName: "Retire Agent",
	})
	s.Require().NoError(err)
	retired, err := registry.RetireOwnedAgent(
		ctx, owner, retiredAgentID, createdForRetirement.ResourceVersion, "retire-agent-once",
	)
	s.Require().NoError(err)
	replayedRetirement, err := registry.RetireOwnedAgent(
		ctx, owner, retiredAgentID, createdForRetirement.ResourceVersion, "retire-agent-once",
	)
	s.Require().NoError(err)
	s.Equal(retired.ResourceVersion, replayedRetirement.ResourceVersion)

	adminRetiredAgentID := uniqueCredentialValue("admin-retire-agent")
	createdForAdminRetirement, err := registry.CreateAgent(ctx, member, onprem.CreateAgentRequest{
		AgentID: adminRetiredAgentID, DisplayName: "Admin Retire Agent",
	})
	s.Require().NoError(err)
	activeAdminRetirementEnrollment, err := registry.CreateEnrollment(
		ctx, member, adminRetiredAgentID, onprem.OwnerEnrollmentRequest{
			CredentialLabel: "active", Permissions: []onprem.Permission{onprem.PermissionChannelReceive},
		},
	)
	s.Require().NoError(err)
	activeAdminRetirementCredential, err := credentials.ExchangeEnrollment(
		ctx, activeAdminRetirementEnrollment.Token,
	)
	s.Require().NoError(err)
	pendingAdminRetirementEnrollment, err := registry.CreateEnrollment(
		ctx, member, adminRetiredAgentID, onprem.OwnerEnrollmentRequest{
			CredentialLabel: "pending", Permissions: []onprem.Permission{onprem.PermissionChannelReceive},
		},
	)
	s.Require().NoError(err)
	adminRetired, err := registry.RetireAdminAgent(
		ctx, owner, adminRetiredAgentID, createdForAdminRetirement.ResourceVersion, "admin-retire-once",
	)
	s.Require().NoError(err)
	s.Equal(onprem.AgentStatusRetired, adminRetired.Status)
	s.NotNil(adminRetired.RetiredAt)
	replayedAdminRetirement, err := registry.RetireAdminAgent(
		ctx, owner, adminRetiredAgentID, createdForAdminRetirement.ResourceVersion, "admin-retire-once",
	)
	s.Require().NoError(err)
	s.Equal(adminRetired.ResourceVersion, replayedAdminRetirement.ResourceVersion)
	_, err = credentials.Authenticate(ctx, activeAdminRetirementCredential.APIKey)
	s.Require().ErrorIs(err, onprem.ErrUnauthorized)
	_, err = credentials.ExchangeEnrollment(ctx, pendingAdminRetirementEnrollment.Token)
	s.Require().ErrorIs(err, onprem.ErrEnrollmentInvalid)
	adminRetirementAudit, err := identity.ListAuditEvents(ctx, owner, onprem.AuditFilter{
		Action: "identity.agent.retired", TargetKind: "agent", TargetID: adminRetiredAgentID,
	})
	s.Require().NoError(err)
	s.Require().Len(adminRetirementAudit, 1)

	staleRetirementAgentID := uniqueCredentialValue("stale-admin-retire-agent")
	createdForStaleRetirement, err := registry.CreateAgent(ctx, member, onprem.CreateAgentRequest{
		AgentID: staleRetirementAgentID, DisplayName: "Stale Admin Retire Agent",
	})
	s.Require().NoError(err)
	stalePendingEnrollment, err := registry.CreateEnrollment(
		ctx, member, staleRetirementAgentID, onprem.OwnerEnrollmentRequest{
			CredentialLabel: "pending", Permissions: []onprem.Permission{onprem.PermissionChannelReceive},
		},
	)
	s.Require().NoError(err)
	_, err = registry.RetireAdminAgent(
		ctx, owner, staleRetirementAgentID, createdForStaleRetirement.ResourceVersion+1, "stale-admin-retire",
	)
	s.Require().ErrorIs(err, onprem.ErrResourceVersionConflict)
	staleRetirementProfile, err := registry.GetAdminAgent(ctx, owner, staleRetirementAgentID)
	s.Require().NoError(err)
	s.Equal(onprem.AgentStatusActive, staleRetirementProfile.Status)
	stalePending, err := registry.ListAdminEnrollments(
		ctx, owner, staleRetirementAgentID, onprem.AgentArtifactFilter{Status: "pending"},
	)
	s.Require().NoError(err)
	s.Require().Len(stalePending, 1)
	s.Equal(stalePendingEnrollment.ID, stalePending[0].EnrollmentID)

	concurrentAgentID := uniqueCredentialValue("concurrent-suspend-agent")
	_, err = registry.CreateAgent(ctx, owner, onprem.CreateAgentRequest{
		AgentID: concurrentAgentID, DisplayName: "Concurrent Suspend Agent",
	})
	s.Require().NoError(err)
	concurrentEnrollment, err := registry.CreateEnrollment(
		ctx, owner, concurrentAgentID, onprem.OwnerEnrollmentRequest{
			CredentialLabel: "concurrent", Permissions: []onprem.Permission{onprem.PermissionChannelReceive},
		},
	)
	s.Require().NoError(err)
	suspensionTx, err := s.store.Pool().Begin(ctx)
	s.Require().NoError(err)
	defer func() {
		rollbackErr := suspensionTx.Rollback(context.Background())
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			s.T().Errorf("rollback suspension transaction: %v", rollbackErr)
		}
	}()
	_, err = suspensionTx.Exec(ctx, `
		UPDATE onprem_agents SET status = 'suspended', updated_at = now()
		WHERE agent_id = $1
	`, concurrentAgentID)
	s.Require().NoError(err)
	exchangeResult := make(chan error, 1)
	go func() {
		_, exchangeErr := credentials.ExchangeEnrollment(context.Background(), concurrentEnrollment.Token)
		exchangeResult <- exchangeErr
	}()
	select {
	case exchangeErr := <-exchangeResult:
		s.Fail("exchange completed before the Agent state transaction committed", exchangeErr)
	case <-time.After(100 * time.Millisecond):
	}
	s.Require().NoError(suspensionTx.Commit(ctx))
	exchangeErr := <-exchangeResult
	s.Require().ErrorIs(exchangeErr, onprem.ErrEnrollmentInvalid)

	suspendedAgentID := uniqueCredentialValue("suspended-owner-agent")
	_, err = registry.CreateAgent(ctx, member, onprem.CreateAgentRequest{
		AgentID: suspendedAgentID, DisplayName: "Suspended Owner Agent",
	})
	s.Require().NoError(err)
	pendingAtSuspension, err := registry.CreateEnrollment(ctx, member, suspendedAgentID, onprem.OwnerEnrollmentRequest{
		CredentialLabel: "pending", Permissions: []onprem.Permission{onprem.PermissionChannelReceive},
	})
	s.Require().NoError(err)
	memberRecord, err := identity.GetMember(ctx, owner, member.MembershipID)
	s.Require().NoError(err)
	suspendedStatus := onprem.MembershipStatusSuspended
	_, err = identity.UpdateMember(ctx, owner, member.MembershipID, onprem.UpdateMemberRequest{
		Status: &suspendedStatus, ResourceVersion: memberRecord.ResourceVersion,
	})
	s.Require().NoError(err)
	_, err = credentials.ExchangeEnrollment(ctx, pendingAtSuspension.Token)
	s.Require().ErrorIs(err, onprem.ErrEnrollmentInvalid)
	revokedAtSuspension, err := registry.ListAdminEnrollments(
		ctx, owner, suspendedAgentID, onprem.AgentArtifactFilter{Status: "revoked"},
	)
	s.Require().NoError(err)
	s.Require().Len(revokedAtSuspension, 1)
}
