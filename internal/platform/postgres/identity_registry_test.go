package postgres_test

import (
	"context"
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
