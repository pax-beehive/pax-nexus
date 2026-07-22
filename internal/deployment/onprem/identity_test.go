package onprem_test

import (
	"context"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/stretchr/testify/suite"
)

type identitySuite struct {
	suite.Suite
	store   *identityStore
	service *onprem.IdentityService
	now     time.Time
}

func TestIdentitySuite(t *testing.T) {
	suite.Run(t, new(identitySuite))
}

func (s *identitySuite) SetupTest() {
	s.now = time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	s.store = newIdentityStore()
	service, err := onprem.NewIdentityService(s.store, onprem.IdentityConfig{
		BootstrapSecret: "bootstrap-secret", SecretPepper: "0123456789abcdef0123456789abcdef",
		SessionTTL: time.Hour, InvitationTTL: 24 * time.Hour,
	}, onprem.WithIdentityClock(func() time.Time { return s.now }),
		onprem.WithIdentityIDSource(func() (string, error) { return "identity-id", nil }),
		onprem.WithIdentityTokenSource(func() (string, error) { return "identity-secret", nil }))
	s.Require().NoError(err)
	s.service = service
}

func (s *identitySuite) TestOIDCIdentityBootstrapsFirstOwnerAndAuthenticatesSession() {
	session, err := s.service.Login(context.Background(), onprem.ExternalIdentity{
		Issuer: "https://id.example", Subject: "subject-1", Email: "owner@example.com",
		EmailVerified: true, DisplayName: "Owner",
	})
	s.Require().NoError(err)
	s.Empty(session.Principal.MembershipID)

	owner, err := s.service.ClaimBootstrap(context.Background(), session.Principal, "bootstrap-secret")
	s.Require().NoError(err)
	s.Equal(onprem.RoleOwner, owner.Role)

	authenticated, err := s.service.AuthenticateSession(context.Background(), session.Token)
	s.Require().NoError(err)
	s.Equal(onprem.RoleOwner, authenticated.Role)
	_, err = s.service.ClaimBootstrap(context.Background(), onprem.HumanPrincipal{UserID: "other-user"}, "bootstrap-secret")
	s.Require().ErrorIs(err, onprem.ErrBootstrapClosed)
}

func (s *identitySuite) TestInvitationIsRoleBoundAndAcceptsVerifiedEmail() {
	owner := onprem.HumanPrincipal{
		UserID: "owner-user", MembershipID: "owner-membership", Role: onprem.RoleOwner,
		MembershipStatus: onprem.MembershipStatusActive,
	}
	invitation, err := s.service.CreateInvitation(context.Background(), owner, onprem.InvitationRequest{
		TargetEmail: "member@example.com", Role: onprem.RoleMember,
	})
	s.Require().NoError(err)
	s.Equal("tm_invite_identity-id.identity-secret", invitation.Token)

	member := onprem.HumanPrincipal{
		UserID: "member-user", Email: "member@example.com", EmailVerified: true,
	}
	accepted, err := s.service.AcceptInvitation(context.Background(), member, invitation.Token, "accept-1")
	s.Require().NoError(err)
	s.Equal(onprem.RoleMember, accepted.Role)
	s.Equal(onprem.MembershipStatusActive, accepted.MembershipStatus)

	_, err = s.service.CreateInvitation(context.Background(), accepted, onprem.InvitationRequest{
		TargetEmail: "other@example.com", Role: onprem.RoleMember,
	})
	s.Require().ErrorIs(err, onprem.ErrForbidden)
}

func (s *identitySuite) TestSessionLogoutAndInvalidTokens() {
	_, err := s.service.AuthenticateSession(context.Background(), "not-a-session")
	s.Require().ErrorIs(err, onprem.ErrUnauthorized)
	s.Require().ErrorIs(s.service.Logout(context.Background(), "not-a-session"), onprem.ErrUnauthorized)

	session, err := s.service.Login(context.Background(), onprem.ExternalIdentity{
		Issuer: "https://id.example", Subject: "subject-1", Email: "owner@example.com",
	})
	s.Require().NoError(err)
	s.Require().NoError(s.service.Logout(context.Background(), session.Token))
	_, err = s.service.AuthenticateSession(context.Background(), session.Token)
	s.Require().ErrorIs(err, onprem.ErrUnauthorized)
}

func (s *identitySuite) TestMemberAdministrationLifecycle() {
	owner := onprem.HumanPrincipal{
		UserID: "owner-user", MembershipID: "owner-membership", Role: onprem.RoleOwner,
		MembershipStatus: onprem.MembershipStatusActive,
	}
	member := onprem.Member{
		MembershipID: "member-membership", UserID: "member-user", Role: onprem.RoleMember,
		Status: onprem.MembershipStatusActive, ResourceVersion: 1,
	}
	s.store.members[member.MembershipID] = member
	members, err := s.service.ListMembers(context.Background(), owner, onprem.MemberFilter{Limit: 200})
	s.Require().NoError(err)
	s.Require().Len(members, 1)
	loaded, err := s.service.GetMember(context.Background(), owner, member.MembershipID)
	s.Require().NoError(err)
	s.Equal(member.UserID, loaded.UserID)

	adminRole := onprem.RoleAdmin
	suspended := onprem.MembershipStatusSuspended
	tests := []struct {
		name       string
		request    onprem.UpdateMemberRequest
		wantRole   onprem.Role
		wantStatus onprem.MembershipStatus
		wantErr    error
	}{
		{name: "promote member", request: onprem.UpdateMemberRequest{Role: &adminRole, ResourceVersion: 1}, wantRole: onprem.RoleAdmin, wantStatus: onprem.MembershipStatusActive},
		{name: "suspend member", request: onprem.UpdateMemberRequest{Status: &suspended, ResourceVersion: 1}, wantRole: onprem.RoleMember, wantStatus: onprem.MembershipStatusSuspended},
		{name: "reject stale version", request: onprem.UpdateMemberRequest{Status: &suspended, ResourceVersion: 2}, wantErr: onprem.ErrMembershipConflict},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			s.store.members[member.MembershipID] = member
			updated, updateErr := s.service.UpdateMember(
				context.Background(), owner, member.MembershipID, test.request,
			)
			if test.wantErr != nil {
				s.Require().ErrorIs(updateErr, test.wantErr)
				return
			}
			s.Require().NoError(updateErr)
			s.Equal(test.wantRole, updated.Role)
			s.Equal(test.wantStatus, updated.Status)
			s.Equal(int64(2), updated.ResourceVersion)
		})
	}
}

func (s *identitySuite) TestRolePolicyRejectsPrivilegeEscalationAndInactivePrincipals() {
	member := onprem.Member{
		MembershipID: "member-membership", UserID: "member-user", Role: onprem.RoleMember,
		Status: onprem.MembershipStatusActive, ResourceVersion: 1,
	}
	s.store.members[member.MembershipID] = member
	admin := onprem.HumanPrincipal{
		UserID: "admin-user", MembershipID: "admin-membership", Role: onprem.RoleAdmin,
		MembershipStatus: onprem.MembershipStatusActive,
	}
	ownerRole := onprem.RoleOwner
	_, err := s.service.UpdateMember(context.Background(), admin, member.MembershipID, onprem.UpdateMemberRequest{
		Role: &ownerRole, ResourceVersion: 1,
	})
	s.Require().ErrorIs(err, onprem.ErrForbidden)

	inactive := admin
	inactive.MembershipStatus = onprem.MembershipStatusSuspended
	_, err = s.service.ListMembers(context.Background(), inactive, onprem.MemberFilter{})
	s.Require().ErrorIs(err, onprem.ErrForbidden)
	_, err = s.service.CreateInvitation(context.Background(), inactive, onprem.InvitationRequest{
		TargetEmail: "member@example.com", Role: onprem.RoleMember,
	})
	s.Require().ErrorIs(err, onprem.ErrForbidden)
}

func (s *identitySuite) TestInvitationValidation() {
	owner := onprem.HumanPrincipal{
		UserID: "owner-user", MembershipID: "owner-membership", Role: onprem.RoleOwner,
		MembershipStatus: onprem.MembershipStatusActive,
	}
	tests := []struct {
		name    string
		request onprem.InvitationRequest
	}{
		{name: "email required", request: onprem.InvitationRequest{Role: onprem.RoleMember}},
		{name: "expiry positive", request: onprem.InvitationRequest{TargetEmail: "x@example.com", Role: onprem.RoleMember, ExpiresIn: -time.Minute}},
		{name: "expiry bounded", request: onprem.InvitationRequest{TargetEmail: "x@example.com", Role: onprem.RoleMember, ExpiresIn: 8 * 24 * time.Hour}},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			_, err := s.service.CreateInvitation(context.Background(), owner, test.request)
			s.Require().Error(err)
		})
	}
	_, err := s.service.AcceptInvitation(context.Background(), onprem.HumanPrincipal{UserID: "user"}, "invalid", "")
	s.Require().ErrorIs(err, onprem.ErrInvitationInvalid)
}

func (s *identitySuite) TestAdministratorListsAndRevokesInvitation() {
	owner := onprem.HumanPrincipal{
		UserID: "owner-user", MembershipID: "owner-membership", Role: onprem.RoleOwner,
		MembershipStatus: onprem.MembershipStatusActive,
	}
	created, err := s.service.CreateInvitation(context.Background(), owner, onprem.InvitationRequest{
		TargetEmail: "member@example.com", Role: onprem.RoleMember,
	})
	s.Require().NoError(err)
	invitations, err := s.service.ListInvitations(context.Background(), owner, onprem.InvitationFilter{Limit: 200})
	s.Require().NoError(err)
	s.Require().Len(invitations, 1)
	s.Empty(invitations[0].Token)
	revoked, err := s.service.RevokeInvitation(context.Background(), owner, created.InvitationID)
	s.Require().NoError(err)
	s.Equal(onprem.InvitationStatusRevoked, revoked.Status)
	auditEvents, err := s.service.ListAuditEvents(context.Background(), owner, onprem.AuditFilter{})
	s.Require().NoError(err)
	s.Require().Len(auditEvents, 1)
	auditEvent, err := s.service.GetAuditEvent(context.Background(), owner, auditEvents[0].AuditEventID)
	s.Require().NoError(err)
	s.Equal("identity.agent.created", auditEvent.Action)
}

type identityStore struct {
	users            map[string]onprem.HumanPrincipal
	sessions         map[onprem.Digest]string
	members          map[string]onprem.Member
	bootstrapClaimed bool
	invitation       onprem.InvitationRecord
}

func newIdentityStore() *identityStore {
	return &identityStore{
		users: make(map[string]onprem.HumanPrincipal), sessions: make(map[onprem.Digest]string),
		members: make(map[string]onprem.Member),
	}
}

func (s *identityStore) UpsertUserSession(
	_ context.Context,
	identity onprem.ExternalIdentity,
	record onprem.HumanSessionRecord,
) (onprem.HumanPrincipal, error) {
	userID := identity.Subject
	principal := s.users[userID]
	principal.UserID = userID
	principal.Email = identity.Email
	principal.EmailVerified = identity.EmailVerified
	principal.SessionID = record.SessionID
	s.users[userID] = principal
	s.sessions[record.SecretDigest] = userID
	return principal, nil
}

func (s *identityStore) ResolveHumanSession(
	_ context.Context,
	_ string,
	digest onprem.Digest,
	_ time.Time,
) (onprem.HumanPrincipal, error) {
	userID, ok := s.sessions[digest]
	if !ok {
		return onprem.HumanPrincipal{}, onprem.ErrUnauthorized
	}
	return s.users[userID], nil
}

func (s *identityStore) RevokeHumanSession(
	_ context.Context,
	_ string,
	tokenDigest onprem.Digest,
	_ time.Time,
) error {
	delete(s.sessions, tokenDigest)
	return nil
}

func (s *identityStore) ClaimBootstrap(
	_ context.Context,
	userID string,
	_ time.Time,
) (onprem.HumanPrincipal, error) {
	if s.bootstrapClaimed {
		return onprem.HumanPrincipal{}, onprem.ErrBootstrapClosed
	}
	s.bootstrapClaimed = true
	principal := s.users[userID]
	principal.MembershipID = "owner-membership"
	principal.Role = onprem.RoleOwner
	principal.MembershipStatus = onprem.MembershipStatusActive
	s.users[userID] = principal
	return principal, nil
}

func (s *identityStore) CreateInvitation(_ context.Context, record onprem.InvitationRecord) error {
	s.invitation = record
	return nil
}

func (s *identityStore) AcceptInvitation(
	_ context.Context,
	_ string,
	digest onprem.Digest,
	userID string,
	email string,
	emailVerified bool,
	_ string,
	_ time.Time,
) (onprem.HumanPrincipal, error) {
	if digest != s.invitation.TokenDigest || !emailVerified || email != s.invitation.TargetEmail {
		return onprem.HumanPrincipal{}, onprem.ErrInvitationInvalid
	}
	principal := s.users[userID]
	principal.UserID = userID
	principal.Email = email
	principal.EmailVerified = emailVerified
	principal.MembershipID = "member-membership"
	principal.Role = s.invitation.Role
	principal.MembershipStatus = onprem.MembershipStatusActive
	s.users[userID] = principal
	return principal, nil
}

func (s *identityStore) ListInvitations(
	_ context.Context,
	_ onprem.InvitationFilter,
	_ time.Time,
) ([]onprem.Invitation, error) {
	return []onprem.Invitation{{
		InvitationID: s.invitation.InvitationID, TargetEmail: s.invitation.TargetEmail,
		Role: s.invitation.Role, Status: onprem.InvitationStatusPending,
		CreatedAt: s.invitation.CreatedAt, ExpiresAt: s.invitation.ExpiresAt,
	}}, nil
}

func (s *identityStore) RevokeInvitation(
	_ context.Context,
	invitationID string,
	_ string,
	_ bool,
	_ time.Time,
) (onprem.Invitation, error) {
	if invitationID != s.invitation.InvitationID {
		return onprem.Invitation{}, onprem.ErrInvitationInvalid
	}
	return onprem.Invitation{
		InvitationID: invitationID, TargetEmail: s.invitation.TargetEmail,
		Role: s.invitation.Role, Status: onprem.InvitationStatusRevoked,
		CreatedAt: s.invitation.CreatedAt, ExpiresAt: s.invitation.ExpiresAt,
	}, nil
}

func (s *identityStore) ListMembers(_ context.Context, _ onprem.MemberFilter) ([]onprem.Member, error) {
	result := make([]onprem.Member, 0, len(s.members))
	for _, member := range s.members {
		result = append(result, member)
	}
	return result, nil
}

func (s *identityStore) GetMember(_ context.Context, membershipID string) (onprem.Member, error) {
	member, ok := s.members[membershipID]
	if !ok {
		return onprem.Member{}, onprem.ErrMembershipConflict
	}
	return member, nil
}

func (s *identityStore) UpdateMember(
	_ context.Context,
	_ string,
	member onprem.Member,
	_ time.Time,
) (onprem.Member, error) {
	s.members[member.MembershipID] = member
	return member, nil
}

func (s *identityStore) ListAuditEvents(
	context.Context,
	onprem.AuditFilter,
) ([]onprem.AuditEvent, error) {
	return []onprem.AuditEvent{{AuditEventID: 1, Action: "identity.agent.created"}}, nil
}

func (s *identityStore) GetAuditEvent(context.Context, int64) (onprem.AuditEvent, error) {
	return onprem.AuditEvent{AuditEventID: 1, Action: "identity.agent.created"}, nil
}
