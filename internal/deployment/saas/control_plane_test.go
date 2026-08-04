package saas_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/deployment/saas"
	"github.com/pax-beehive/pax-nexus/internal/deployment/saas/mocks"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"
)

const testPepper = "test-pepper-with-at-least-32-characters"

type controlPlaneSuite struct {
	suite.Suite
	controller  *gomock.Controller
	teams       *mocks.MockTeamStore
	invitations *mocks.MockTeamInvitationStore
	service     *saas.ControlPlane
	now         time.Time
}

func TestControlPlaneSuite(t *testing.T) {
	suite.Run(t, new(controlPlaneSuite))
}

func (s *controlPlaneSuite) SetupTest() {
	s.controller = gomock.NewController(s.T())
	s.teams = mocks.NewMockTeamStore(s.controller)
	s.invitations = mocks.NewMockTeamInvitationStore(s.controller)
	s.now = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	service, err := saas.NewControlPlane(s.teams, s.invitations, saas.ControlPlaneConfig{
		SecretPepper: testPepper, SessionTTL: time.Hour, InvitationTTL: 24 * time.Hour,
	},
		saas.WithControlPlaneClock(func() time.Time { return s.now }),
		saas.WithControlPlaneIDSource(fixedSource("generated-id")),
		saas.WithControlPlaneTokenSource(fixedSource("generated-secret")),
	)
	s.Require().NoError(err)
	s.service = service
}

func fixedSource(value string) func() (string, error) {
	return func() (string, error) { return value, nil }
}

func ownerPrincipal() onprem.HumanPrincipal {
	return onprem.HumanPrincipal{
		UserID: "usr_owner", MembershipID: "mbr_owner", Role: onprem.RoleOwner,
		MembershipStatus: onprem.MembershipStatusActive, ScopeID: "team_alpha",
		Email: "owner@example.com", EmailVerified: true, SessionID: "sess_1",
	}
}

func (s *controlPlaneSuite) TestConstructorValidation() {
	_, err := saas.NewControlPlane(nil, s.invitations, saas.ControlPlaneConfig{
		SecretPepper: testPepper, SessionTTL: time.Hour, InvitationTTL: time.Hour,
	})
	s.Require().Error(err)
	_, err = saas.NewControlPlane(s.teams, nil, saas.ControlPlaneConfig{
		SecretPepper: testPepper, SessionTTL: time.Hour, InvitationTTL: time.Hour,
	})
	s.Require().Error(err)
	_, err = saas.NewControlPlane(s.teams, s.invitations, saas.ControlPlaneConfig{
		SecretPepper: testPepper, SessionTTL: time.Hour,
	})
	s.Require().Error(err)
	_, err = saas.NewControlPlane(s.teams, s.invitations, saas.ControlPlaneConfig{
		SecretPepper: "short", SessionTTL: time.Hour, InvitationTTL: time.Hour,
	})
	s.Require().Error(err)
}

func (s *controlPlaneSuite) TestLoginCurrentTeamResolution() {
	identity := onprem.ExternalIdentity{
		Issuer: "https://issuer.example.com", Subject: "sub-1",
		Email: "USER@example.com", EmailVerified: true, DisplayName: "User",
	}
	teamSummary := func(teamID string) saas.TeamSummary {
		return saas.TeamSummary{Team: saas.Team{TeamID: teamID}, Role: onprem.RoleMember, MembershipID: "mbr_" + teamID}
	}
	cases := []struct {
		name          string
		summaries     []saas.TeamSummary
		expectedScope string
	}{
		{"no memberships keeps the no-team state", nil, ""},
		{"single membership selects its team", []saas.TeamSummary{teamSummary("team_one")}, "team_one"},
		{"several memberships select the most recently joined", []saas.TeamSummary{teamSummary("team_new"), teamSummary("team_old")}, "team_new"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.teams.EXPECT().ListTeamSummaries(gomock.Any(), testExternalUserID(identity.Issuer, identity.Subject)).
				Return(tc.summaries, nil)
			s.teams.EXPECT().CreateUserSession(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, got onprem.ExternalIdentity, record saas.TeamSessionRecord) (onprem.HumanPrincipal, error) {
					s.Equal("user@example.com", got.Email, "login normalizes the email")
					s.Equal(tc.expectedScope, record.CurrentTeamID)
					s.Equal(
						testDigest(testPepper, "human-session", "tm_session_generated-id.generated-secret"),
						record.SecretDigest,
					)
					return onprem.HumanPrincipal{
						UserID: "usr_1", ScopeID: record.CurrentTeamID, SessionID: record.SessionID,
					}, nil
				})
			session, err := s.service.Login(context.Background(), identity)
			s.Require().NoError(err)
			s.Equal("tm_session_generated-id.generated-secret", session.Token)
			s.Equal(tc.expectedScope, session.Principal.ScopeID)
			s.Equal("generated-id", session.Principal.SessionID)
			s.Equal(s.now.Add(time.Hour), session.ExpiresAt)
		})
	}
}

func (s *controlPlaneSuite) TestLoginValidationAndStoreErrors() {
	_, err := s.service.Login(context.Background(), onprem.ExternalIdentity{Issuer: "https://issuer.example.com"})
	s.Require().Error(err)

	s.teams.EXPECT().ListTeamSummaries(gomock.Any(), gomock.Any()).Return(nil, errors.New("store down"))
	_, err = s.service.Login(context.Background(), onprem.ExternalIdentity{
		Issuer: "https://issuer.example.com", Subject: "sub-1",
	})
	s.Require().ErrorContains(err, "resolve login team memberships")
}

func (s *controlPlaneSuite) TestAuthenticateSession() {
	cases := []struct {
		name  string
		token string
	}{
		{"missing prefix", "tm_key_whatever.secret"},
		{"malformed secret", "tm_session_no-dot"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			_, err := s.service.AuthenticateSession(context.Background(), tc.token)
			s.Require().ErrorIs(err, onprem.ErrUnauthorized)
		})
	}

	s.Run("valid token returns the session principal", func() {
		principal := onprem.HumanPrincipal{UserID: "usr_1", ScopeID: "team_alpha", SessionID: "generated-id"}
		s.teams.EXPECT().ResolveSession(gomock.Any(), "generated-id", gomock.Any(), s.now).
			Return(principal, nil)
		authenticated, err := s.service.AuthenticateSession(context.Background(), "tm_session_generated-id.secret")
		s.Require().NoError(err)
		s.Equal(principal, authenticated)
	})

	s.Run("store rejection is unauthorized", func() {
		s.teams.EXPECT().ResolveSession(gomock.Any(), "generated-id", gomock.Any(), s.now).
			Return(onprem.HumanPrincipal{}, onprem.ErrUnauthorized).Times(2)
		_, err := s.service.AuthenticateSession(context.Background(), "tm_session_generated-id.secret")
		s.Require().ErrorIs(err, onprem.ErrUnauthorized)
	})
}

func (s *controlPlaneSuite) TestLogout() {
	s.teams.EXPECT().RevokeSession(gomock.Any(), "generated-id", gomock.Any(), s.now).Return(nil)
	s.Require().NoError(s.service.Logout(context.Background(), "tm_session_generated-id.secret"))
	s.Require().ErrorIs(s.service.Logout(context.Background(), "not-a-session"), onprem.ErrUnauthorized)
}

func (s *controlPlaneSuite) TestClaimBootstrapUnsupported() {
	_, err := s.service.ClaimBootstrap(context.Background(), onprem.HumanPrincipal{UserID: "usr_1"}, "secret")
	s.Require().ErrorIs(err, saas.ErrUnsupportedInSaaS)
}

func (s *controlPlaneSuite) TestCreateTeam() {
	s.Run("derives the slug from the name", func() {
		s.teams.EXPECT().CreateTeam(gomock.Any(), gomock.Any(), gomock.Any(), "key-1", s.now).
			DoAndReturn(func(_ context.Context, team saas.Team, membershipID string, _ string, _ time.Time) (saas.Team, onprem.Member, error) {
				s.Equal("acme-corp", team.Slug)
				s.Equal("usr_owner", team.CreatedByUserID)
				s.NotEmpty(team.TeamID)
				s.NotEmpty(membershipID)
				return team, onprem.Member{}, nil
			})
		team, err := s.service.CreateTeam(context.Background(), ownerPrincipal(), "Acme Corp", "key-1")
		s.Require().NoError(err)
		s.Equal("acme-corp", team.Slug)
	})

	s.Run("retries with a suffixed slug on conflict", func() {
		gomock.InOrder(
			s.teams.EXPECT().CreateTeam(gomock.Any(), gomock.Any(), gomock.Any(), "", s.now).
				Return(saas.Team{}, onprem.Member{}, saas.ErrTeamSlugConflict),
			s.teams.EXPECT().CreateTeam(gomock.Any(), gomock.Any(), gomock.Any(), "", s.now).
				DoAndReturn(func(_ context.Context, team saas.Team, _ string, _ string, _ time.Time) (saas.Team, onprem.Member, error) {
					s.Equal("acme-corp-2", team.Slug)
					return team, onprem.Member{}, nil
				}),
		)
		team, err := s.service.CreateTeam(context.Background(), ownerPrincipal(), "Acme Corp", "")
		s.Require().NoError(err)
		s.Equal("acme-corp-2", team.Slug)
	})

	s.Run("gives up after three slug conflicts", func() {
		s.teams.EXPECT().CreateTeam(gomock.Any(), gomock.Any(), gomock.Any(), "", s.now).
			Return(saas.Team{}, onprem.Member{}, saas.ErrTeamSlugConflict).Times(3)
		_, err := s.service.CreateTeam(context.Background(), ownerPrincipal(), "Acme Corp", "")
		s.Require().ErrorIs(err, saas.ErrTeamSlugConflict)
	})

	s.Run("validation", func() {
		_, err := s.service.CreateTeam(context.Background(), onprem.HumanPrincipal{}, "Acme", "")
		s.Require().ErrorIs(err, onprem.ErrUnauthorized)
		_, err = s.service.CreateTeam(context.Background(), ownerPrincipal(), "   ", "")
		s.Require().ErrorIs(err, onprem.ErrInvalidIdentityInput)
	})

	s.Run("store errors propagate", func() {
		s.teams.EXPECT().CreateTeam(gomock.Any(), gomock.Any(), gomock.Any(), "", s.now).
			Return(saas.Team{}, onprem.Member{}, onprem.ErrIdempotencyConflict)
		_, err := s.service.CreateTeam(context.Background(), ownerPrincipal(), "Acme", "")
		s.Require().ErrorIs(err, onprem.ErrIdempotencyConflict)
	})
}

func (s *controlPlaneSuite) TestListTeams() {
	summaries := []saas.TeamSummary{{Team: saas.Team{TeamID: "team_alpha"}}}
	s.teams.EXPECT().ListTeamSummaries(gomock.Any(), "usr_owner").Return(summaries, nil)
	result, err := s.service.ListTeams(context.Background(), ownerPrincipal())
	s.Require().NoError(err)
	s.Equal(summaries, result)

	_, err = s.service.ListTeams(context.Background(), onprem.HumanPrincipal{})
	s.Require().ErrorIs(err, onprem.ErrUnauthorized)
}

func (s *controlPlaneSuite) TestSwitchTeam() {
	summaries := []saas.TeamSummary{
		{Team: saas.Team{TeamID: "team_alpha"}, Role: onprem.RoleOwner, MembershipID: "mbr_owner"},
		{Team: saas.Team{TeamID: "team_beta"}, Role: onprem.RoleMember, MembershipID: "mbr_beta"},
	}
	s.Run("re-scopes the principal to the target team", func() {
		s.teams.EXPECT().ListTeamSummaries(gomock.Any(), "usr_owner").Return(summaries, nil)
		s.teams.EXPECT().SwitchTeam(gomock.Any(), "sess_1", "usr_owner", "team_beta", s.now).Return(nil)
		switched, err := s.service.SwitchTeam(context.Background(), ownerPrincipal(), "team_beta")
		s.Require().NoError(err)
		s.Equal("team_beta", switched.ScopeID)
		s.Equal("mbr_beta", switched.MembershipID)
		s.Equal(onprem.RoleMember, switched.Role)
	})

	s.Run("rejects a team the user is not a member of", func() {
		s.teams.EXPECT().ListTeamSummaries(gomock.Any(), "usr_owner").Return(summaries, nil)
		_, err := s.service.SwitchTeam(context.Background(), ownerPrincipal(), "team_gamma")
		s.Require().ErrorIs(err, saas.ErrNotTeamMember)
	})

	s.Run("validation", func() {
		_, err := s.service.SwitchTeam(context.Background(), onprem.HumanPrincipal{UserID: "usr_owner"}, "team_beta")
		s.Require().ErrorIs(err, onprem.ErrUnauthorized, "a session is required to switch")
		_, err = s.service.SwitchTeam(context.Background(), ownerPrincipal(), "  ")
		s.Require().ErrorIs(err, onprem.ErrInvalidIdentityInput)
	})
}

func (s *controlPlaneSuite) TestCreateInvitationRoleMatrix() {
	principalWith := func(role onprem.Role, status onprem.MembershipStatus) onprem.HumanPrincipal {
		return onprem.HumanPrincipal{
			UserID: "usr_1", MembershipID: "mbr_1", Role: role, MembershipStatus: status, ScopeID: "team_alpha",
		}
	}
	cases := []struct {
		name      string
		actor     onprem.HumanPrincipal
		target    onprem.Role
		expectErr error
	}{
		{"owner invites admin", principalWith(onprem.RoleOwner, onprem.MembershipStatusActive), onprem.RoleAdmin, nil},
		{"owner invites member", principalWith(onprem.RoleOwner, onprem.MembershipStatusActive), onprem.RoleMember, nil},
		{"admin invites member", principalWith(onprem.RoleAdmin, onprem.MembershipStatusActive), onprem.RoleMember, nil},
		{"admin cannot invite admin", principalWith(onprem.RoleAdmin, onprem.MembershipStatusActive), onprem.RoleAdmin, onprem.ErrForbidden},
		{"member cannot invite", principalWith(onprem.RoleMember, onprem.MembershipStatusActive), onprem.RoleMember, onprem.ErrForbidden},
		{"suspended owner cannot invite", principalWith(onprem.RoleOwner, onprem.MembershipStatusSuspended), onprem.RoleMember, onprem.ErrForbidden},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			if tc.expectErr == nil {
				s.invitations.EXPECT().CreateInvitation(gomock.Any(), "team_alpha", gomock.Any()).
					DoAndReturn(func(_ context.Context, _ string, record onprem.InvitationRecord) error {
						s.Equal(tc.target, record.Role)
						s.Equal("target@example.com", record.TargetEmail)
						s.Equal("mbr_1", record.CreatedByMembershipID)
						return nil
					})
			}
			invitation, err := s.service.CreateInvitation(context.Background(), tc.actor, onprem.InvitationRequest{
				TargetEmail: "Target@Example.com", Role: tc.target,
			})
			if tc.expectErr != nil {
				s.Require().ErrorIs(err, tc.expectErr)
				return
			}
			s.Require().NoError(err)
			s.Equal("tm_invite_generated-id.generated-secret", invitation.Token)
			s.Equal(onprem.InvitationStatusPending, invitation.Status)
			s.Equal(s.now.Add(24*time.Hour), invitation.ExpiresAt)
		})
	}
}

func (s *controlPlaneSuite) TestCreateInvitationValidation() {
	_, err := s.service.CreateInvitation(context.Background(), ownerPrincipal(), onprem.InvitationRequest{
		Role: onprem.RoleMember,
	})
	s.Require().ErrorIs(err, onprem.ErrInvalidIdentityInput, "target email is required")

	_, err = s.service.CreateInvitation(context.Background(), ownerPrincipal(), onprem.InvitationRequest{
		TargetEmail: "target@example.com", Role: onprem.RoleMember, ExpiresIn: 8 * 24 * time.Hour,
	})
	s.Require().ErrorIs(err, onprem.ErrInvalidIdentityInput, "expiry beyond seven days is rejected")
}

func (s *controlPlaneSuite) TestAcceptInvitation() {
	token := "tm_invite_inv-1.secret"
	s.Run("switches the session to the invited team", func() {
		accepted := onprem.HumanPrincipal{
			UserID: "usr_owner", MembershipID: "mbr_new", Role: onprem.RoleMember,
			MembershipStatus: onprem.MembershipStatusActive, ScopeID: "team_beta",
		}
		s.invitations.EXPECT().AcceptInvitation(
			gomock.Any(), "inv-1", gomock.Any(), "usr_owner", "owner@example.com", true, "key-1", s.now,
		).Return(accepted, nil)
		s.teams.EXPECT().SwitchTeam(gomock.Any(), "sess_1", "usr_owner", "team_beta", s.now).Return(nil)
		principal, err := s.service.AcceptInvitation(context.Background(), ownerPrincipal(), token, "key-1")
		s.Require().NoError(err)
		s.Equal("team_beta", principal.ScopeID)
		s.Equal("mbr_new", principal.MembershipID)
		s.Equal("sess_1", principal.SessionID)
	})

	s.Run("accepting into the current team does not switch", func() {
		accepted := onprem.HumanPrincipal{UserID: "usr_owner", MembershipID: "mbr_owner", ScopeID: "team_alpha"}
		s.invitations.EXPECT().AcceptInvitation(gomock.Any(), "inv-1", gomock.Any(), "usr_owner", "owner@example.com", true, "", s.now).
			Return(accepted, nil)
		_, err := s.service.AcceptInvitation(context.Background(), ownerPrincipal(), token, "")
		s.Require().NoError(err)
	})

	s.Run("validation and store errors", func() {
		_, err := s.service.AcceptInvitation(context.Background(), onprem.HumanPrincipal{}, token, "")
		s.Require().ErrorIs(err, onprem.ErrMembershipConflict)
		_, err = s.service.AcceptInvitation(context.Background(), ownerPrincipal(), "not-an-invite", "")
		s.Require().ErrorIs(err, onprem.ErrInvitationInvalid)

		s.invitations.EXPECT().AcceptInvitation(gomock.Any(), "inv-1", gomock.Any(), "usr_owner", "owner@example.com", true, "", s.now).
			Return(onprem.HumanPrincipal{}, onprem.ErrInvitationInvalid).Times(2)
		_, err = s.service.AcceptInvitation(context.Background(), ownerPrincipal(), token, "")
		s.Require().ErrorIs(err, onprem.ErrInvitationInvalid)
	})
}

func (s *controlPlaneSuite) TestInvitationListingAndRevocation() {
	s.Run("list is admin-gated and team-filtered", func() {
		invitations := []onprem.Invitation{{InvitationID: "inv-1"}}
		s.invitations.EXPECT().ListInvitations(gomock.Any(), "team_alpha", gomock.Any(), s.now).
			Return(invitations, nil)
		result, err := s.service.ListInvitations(context.Background(), ownerPrincipal(), onprem.InvitationFilter{})
		s.Require().NoError(err)
		s.Equal(invitations, result)

		member := ownerPrincipal()
		member.Role = onprem.RoleMember
		_, err = s.service.ListInvitations(context.Background(), member, onprem.InvitationFilter{})
		s.Require().ErrorIs(err, onprem.ErrForbidden)
	})

	s.Run("revoke passes owner privileges and team scope", func() {
		s.invitations.EXPECT().RevokeInvitation(gomock.Any(), "team_alpha", "inv-1", "mbr_owner", true, s.now).
			Return(onprem.Invitation{InvitationID: "inv-1", Status: onprem.InvitationStatusRevoked}, nil)
		revoked, err := s.service.RevokeInvitation(context.Background(), ownerPrincipal(), "inv-1")
		s.Require().NoError(err)
		s.Equal(onprem.InvitationStatusRevoked, revoked.Status)

		_, err = s.service.RevokeInvitation(context.Background(), ownerPrincipal(), "  ")
		s.Require().ErrorIs(err, onprem.ErrInvitationInvalid)
	})
}

func (s *controlPlaneSuite) TestMembers() {
	s.Run("list and get are admin-gated and team-filtered", func() {
		members := []onprem.Member{{MembershipID: "mbr_owner"}}
		s.teams.EXPECT().ListMembers(gomock.Any(), "team_alpha", gomock.Any()).Return(members, nil)
		result, err := s.service.ListMembers(context.Background(), ownerPrincipal(), onprem.MemberFilter{})
		s.Require().NoError(err)
		s.Equal(members, result)

		s.teams.EXPECT().GetMember(gomock.Any(), "team_alpha", "mbr_owner").
			Return(onprem.Member{MembershipID: "mbr_owner"}, nil)
		member, err := s.service.GetMember(context.Background(), ownerPrincipal(), "mbr_owner")
		s.Require().NoError(err)
		s.Equal("mbr_owner", member.MembershipID)

		_, err = s.service.GetMember(context.Background(), ownerPrincipal(), " ")
		s.Require().ErrorIs(err, onprem.ErrMembershipConflict)

		memberActor := ownerPrincipal()
		memberActor.Role = onprem.RoleMember
		_, err = s.service.ListMembers(context.Background(), memberActor, onprem.MemberFilter{})
		s.Require().ErrorIs(err, onprem.ErrForbidden)
	})
}

func (s *controlPlaneSuite) TestUpdateMemberMatrix() {
	storedMember := onprem.Member{
		MembershipID: "mbr_target", UserID: "usr_target", Role: onprem.RoleMember,
		Status: onprem.MembershipStatusActive, ResourceVersion: 1,
	}
	expectGet := func(member onprem.Member) {
		s.teams.EXPECT().GetMember(gomock.Any(), "team_alpha", "mbr_target").Return(member, nil)
	}
	adminActor := ownerPrincipal()
	adminActor.Role = onprem.RoleAdmin

	s.Run("owner promotes a member", func() {
		expectGet(storedMember)
		s.teams.EXPECT().UpdateMember(gomock.Any(), "team_alpha", "mbr_owner", gomock.Any(), s.now).
			DoAndReturn(func(_ context.Context, _, _ string, member onprem.Member, _ time.Time) (onprem.Member, error) {
				s.Equal(onprem.RoleAdmin, member.Role)
				s.Equal(int64(2), member.ResourceVersion)
				return member, nil
			})
		updated, err := s.service.UpdateMember(context.Background(), ownerPrincipal(), "mbr_target", onprem.UpdateMemberRequest{
			Role: rolePtr(onprem.RoleAdmin), ResourceVersion: 1,
		})
		s.Require().NoError(err)
		s.Equal(onprem.RoleAdmin, updated.Role)
	})

	cases := []struct {
		name      string
		actor     onprem.HumanPrincipal
		target    onprem.Member
		request   onprem.UpdateMemberRequest
		expectErr error
	}{
		{
			"admin cannot change roles", adminActor, storedMember,
			onprem.UpdateMemberRequest{Role: rolePtr(onprem.RoleAdmin), ResourceVersion: 1}, onprem.ErrForbidden,
		},
		{
			"admin cannot touch a non-member target", adminActor,
			onprem.Member{MembershipID: "mbr_target", Role: onprem.RoleAdmin, Status: onprem.MembershipStatusActive, ResourceVersion: 1},
			onprem.UpdateMemberRequest{Status: statusPtr(onprem.MembershipStatusSuspended), ResourceVersion: 1}, onprem.ErrForbidden,
		},
		{
			"stale resource version conflicts", ownerPrincipal(), storedMember,
			onprem.UpdateMemberRequest{Status: statusPtr(onprem.MembershipStatusSuspended), ResourceVersion: 3}, onprem.ErrResourceVersionConflict,
		},
		{
			"invalid role value", ownerPrincipal(), storedMember,
			onprem.UpdateMemberRequest{Role: rolePtr(onprem.Role("superuser")), ResourceVersion: 1}, onprem.ErrInvalidIdentityInput,
		},
		{
			"removed members reject status transitions", ownerPrincipal(),
			onprem.Member{MembershipID: "mbr_target", Role: onprem.RoleMember, Status: onprem.MembershipStatusRemoved, ResourceVersion: 2},
			onprem.UpdateMemberRequest{Status: statusPtr(onprem.MembershipStatusActive), ResourceVersion: 2}, onprem.ErrInvalidStateTransition,
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			expectGet(tc.target)
			_, err := s.service.UpdateMember(context.Background(), tc.actor, "mbr_target", tc.request)
			s.Require().ErrorIs(err, tc.expectErr)
		})
	}

	s.Run("store errors propagate", func() {
		expectGet(storedMember)
		s.teams.EXPECT().UpdateMember(gomock.Any(), "team_alpha", "mbr_owner", gomock.Any(), s.now).
			Return(onprem.Member{}, onprem.ErrLastActiveOwner)
		_, err := s.service.UpdateMember(context.Background(), ownerPrincipal(), "mbr_target", onprem.UpdateMemberRequest{
			Role: rolePtr(onprem.RoleAdmin), ResourceVersion: 1,
		})
		s.Require().ErrorIs(err, onprem.ErrLastActiveOwner)
	})
}

func (s *controlPlaneSuite) TestAuditEvents() {
	s.Run("list is admin-gated and team-filtered", func() {
		events := []onprem.AuditEvent{{AuditEventID: 7, Action: "identity.team.created"}}
		s.teams.EXPECT().ListAuditEvents(gomock.Any(), "team_alpha", gomock.Any()).Return(events, nil)
		result, err := s.service.ListAuditEvents(context.Background(), ownerPrincipal(), onprem.AuditFilter{})
		s.Require().NoError(err)
		s.Equal(events, result)

		memberActor := ownerPrincipal()
		memberActor.Role = onprem.RoleMember
		_, err = s.service.ListAuditEvents(context.Background(), memberActor, onprem.AuditFilter{})
		s.Require().ErrorIs(err, onprem.ErrForbidden)
	})

	s.Run("get validates the ID", func() {
		s.teams.EXPECT().GetAuditEvent(gomock.Any(), "team_alpha", int64(7)).
			Return(onprem.AuditEvent{AuditEventID: 7}, nil)
		event, err := s.service.GetAuditEvent(context.Background(), ownerPrincipal(), 7)
		s.Require().NoError(err)
		s.Equal(int64(7), event.AuditEventID)

		_, err = s.service.GetAuditEvent(context.Background(), ownerPrincipal(), 0)
		s.Require().ErrorIs(err, onprem.ErrAuditEventNotFound)
	})
}

func rolePtr(role onprem.Role) *onprem.Role {
	return &role
}

func statusPtr(status onprem.MembershipStatus) *onprem.MembershipStatus {
	return &status
}
