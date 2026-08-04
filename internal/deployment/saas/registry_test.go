package saas_test

import (
	"context"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/deployment/saas"
	"github.com/pax-beehive/pax-nexus/internal/deployment/saas/mocks"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"
)

type registrySuite struct {
	suite.Suite
	controller  *gomock.Controller
	agents      *mocks.MockTeamRegistryStore
	credentials *mocks.MockTeamCredentialStore
	service     *saas.Registry
	now         time.Time
}

func TestRegistrySuite(t *testing.T) {
	suite.Run(t, new(registrySuite))
}

func (s *registrySuite) SetupTest() {
	s.controller = gomock.NewController(s.T())
	s.agents = mocks.NewMockTeamRegistryStore(s.controller)
	s.credentials = mocks.NewMockTeamCredentialStore(s.controller)
	s.now = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	service, err := saas.NewRegistry(s.agents, s.credentials, saas.RegistryConfig{
		SecretPepper: testPepper,
		MemberGrantablePermissions: []onprem.Permission{
			onprem.PermissionObserve, onprem.PermissionSearch, onprem.PermissionGet,
		},
		PortalURL: "https://portal.example.com",
	},
		saas.WithRegistryClock(func() time.Time { return s.now }),
		saas.WithRegistryIDSource(fixedSource("generated-id")),
		saas.WithRegistryTokenSource(fixedSource("generated-secret")),
	)
	s.Require().NoError(err)
	s.service = service
}

func (s *registrySuite) activeAgent() onprem.AgentProfile {
	return onprem.AgentProfile{
		AgentID: "agent-1", OwnerMembershipID: "mbr_owner", OwnerUserID: "usr_owner",
		DisplayName: "Build Bot", Status: onprem.AgentStatusActive, ResourceVersion: 1,
		CreatedAt: s.now, UpdatedAt: s.now,
	}
}

func (s *registrySuite) TestConstructorValidation() {
	_, err := saas.NewRegistry(nil, s.credentials, saas.RegistryConfig{SecretPepper: testPepper,
		MemberGrantablePermissions: []onprem.Permission{onprem.PermissionGet}})
	s.Require().Error(err)
	_, err = saas.NewRegistry(s.agents, nil, saas.RegistryConfig{SecretPepper: testPepper,
		MemberGrantablePermissions: []onprem.Permission{onprem.PermissionGet}})
	s.Require().Error(err)
	_, err = saas.NewRegistry(s.agents, s.credentials, saas.RegistryConfig{SecretPepper: "short",
		MemberGrantablePermissions: []onprem.Permission{onprem.PermissionGet}})
	s.Require().Error(err)
	_, err = saas.NewRegistry(s.agents, s.credentials, saas.RegistryConfig{SecretPepper: testPepper,
		MemberGrantablePermissions: []onprem.Permission{onprem.PermissionAdmin}})
	s.Require().ErrorIs(err, onprem.ErrInvalidIdentityInput)
}

func (s *registrySuite) TestCreateAgent() {
	s.Run("creates the agent in the principal's team", func() {
		s.agents.EXPECT().CreateAgent(gomock.Any(), "team_alpha", gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, profile onprem.AgentProfile) (onprem.AgentProfile, error) {
				s.Equal("agent-1", profile.AgentID)
				s.Equal("mbr_owner", profile.OwnerMembershipID)
				s.Equal(onprem.AgentStatusActive, profile.Status)
				s.Equal("key-1", profile.CreationIdempotencyKey)
				return profile, nil
			})
		profile, err := s.service.CreateAgent(context.Background(), ownerPrincipal(), onprem.CreateAgentRequest{
			AgentID: "agent-1", DisplayName: "Build Bot", IdempotencyKey: "key-1",
		})
		s.Require().NoError(err)
		s.Equal("agent-1", profile.AgentID)
	})

	cases := []struct {
		name      string
		principal onprem.HumanPrincipal
		request   onprem.CreateAgentRequest
		expectErr error
	}{
		{
			"unauthenticated principal", onprem.HumanPrincipal{},
			onprem.CreateAgentRequest{AgentID: "agent-1", DisplayName: "Bot"}, onprem.ErrUnauthorized,
		},
		{
			"suspended membership", func() onprem.HumanPrincipal {
				principal := ownerPrincipal()
				principal.MembershipStatus = onprem.MembershipStatusSuspended
				return principal
			}(),
			onprem.CreateAgentRequest{AgentID: "agent-1", DisplayName: "Bot"}, onprem.ErrForbidden,
		},
		{
			"missing display name", ownerPrincipal(),
			onprem.CreateAgentRequest{AgentID: "agent-1"}, onprem.ErrInvalidIdentityInput,
		},
		{
			"invalid agent id", ownerPrincipal(),
			onprem.CreateAgentRequest{AgentID: "agent/1", DisplayName: "Bot"}, onprem.ErrInvalidIdentityInput,
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			_, err := s.service.CreateAgent(context.Background(), tc.principal, tc.request)
			s.Require().ErrorIs(err, tc.expectErr)
		})
	}
}

func (s *registrySuite) TestOwnedAgentReads() {
	s.Run("list and get are team-filtered", func() {
		profiles := []onprem.AgentProfile{s.activeAgent()}
		s.agents.EXPECT().ListOwnedAgents(gomock.Any(), "team_alpha", "mbr_owner", gomock.Any()).
			Return(profiles, nil)
		result, err := s.service.ListOwnedAgents(context.Background(), ownerPrincipal(), onprem.AgentFilter{})
		s.Require().NoError(err)
		s.Equal(profiles, result)

		s.agents.EXPECT().GetOwnedAgent(gomock.Any(), "team_alpha", "mbr_owner", "agent-1").
			Return(s.activeAgent(), nil)
		profile, err := s.service.GetOwnedAgent(context.Background(), ownerPrincipal(), "agent-1")
		s.Require().NoError(err)
		s.Equal("agent-1", profile.AgentID)

		_, err = s.service.GetOwnedAgent(context.Background(), ownerPrincipal(), " ")
		s.Require().ErrorIs(err, onprem.ErrAgentNotFound)
	})
}

func (s *registrySuite) TestUpdateOwnedAgent() {
	newName := "Build Bot v2"
	s.Run("applies partial updates with version bump", func() {
		s.agents.EXPECT().GetOwnedAgent(gomock.Any(), "team_alpha", "mbr_owner", "agent-1").
			Return(s.activeAgent(), nil)
		s.agents.EXPECT().UpdateOwnedAgent(gomock.Any(), "team_alpha", "mbr_owner", gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _, _ string, _ onprem.HumanPrincipal, profile onprem.AgentProfile) (onprem.AgentProfile, error) {
				s.Equal(newName, profile.DisplayName)
				s.Equal(int64(2), profile.ResourceVersion)
				return profile, nil
			})
		updated, err := s.service.UpdateOwnedAgent(context.Background(), ownerPrincipal(), "agent-1", onprem.UpdateAgentRequest{
			DisplayName: &newName, ResourceVersion: 1,
		})
		s.Require().NoError(err)
		s.Equal(newName, updated.DisplayName)
	})

	s.Run("stale version conflicts", func() {
		s.agents.EXPECT().GetOwnedAgent(gomock.Any(), "team_alpha", "mbr_owner", "agent-1").
			Return(s.activeAgent(), nil)
		_, err := s.service.UpdateOwnedAgent(context.Background(), ownerPrincipal(), "agent-1", onprem.UpdateAgentRequest{
			DisplayName: &newName, ResourceVersion: 7,
		})
		s.Require().ErrorIs(err, onprem.ErrResourceVersionConflict)
	})

	s.Run("retirement through update is rejected", func() {
		s.agents.EXPECT().GetOwnedAgent(gomock.Any(), "team_alpha", "mbr_owner", "agent-1").
			Return(s.activeAgent(), nil)
		retired := onprem.AgentStatusRetired
		_, err := s.service.UpdateOwnedAgent(context.Background(), ownerPrincipal(), "agent-1", onprem.UpdateAgentRequest{
			Status: &retired, ResourceVersion: 1,
		})
		s.Require().ErrorIs(err, onprem.ErrInvalidStateTransition)
	})
}

func (s *registrySuite) TestRetireOwnedAgent() {
	s.Run("retires inside the principal's team", func() {
		retired := s.activeAgent()
		retired.Status = onprem.AgentStatusRetired
		s.agents.EXPECT().RetireOwnedAgent(gomock.Any(), "team_alpha", "mbr_owner", gomock.Any(), "agent-1", int64(1), "key-1", s.now).
			Return(retired, nil)
		profile, err := s.service.RetireOwnedAgent(context.Background(), ownerPrincipal(), "agent-1", 1, "key-1")
		s.Require().NoError(err)
		s.Equal(onprem.AgentStatusRetired, profile.Status)
	})

	s.Run("validation", func() {
		_, err := s.service.RetireOwnedAgent(context.Background(), ownerPrincipal(), " ", 1, "")
		s.Require().ErrorIs(err, onprem.ErrAgentNotFound)
		_, err = s.service.RetireOwnedAgent(context.Background(), ownerPrincipal(), "agent-1", 0, "")
		s.Require().ErrorIs(err, onprem.ErrResourceVersionConflict)
	})
}

func (s *registrySuite) TestCreateEnrollment() {
	s.Run("creates an owner enrollment in the team", func() {
		s.agents.EXPECT().GetOwnedAgent(gomock.Any(), "team_alpha", "mbr_owner", "agent-1").
			Return(s.activeAgent(), nil)
		s.credentials.EXPECT().CreateOwnedEnrollment(gomock.Any(), "team_alpha", "mbr_owner", gomock.Any()).
			DoAndReturn(func(_ context.Context, _, _ string, record onprem.EnrollmentRecord) error {
				s.Equal("agent-1", record.AgentID)
				s.Equal([]onprem.Permission{onprem.PermissionGet}, record.Permissions)
				s.Equal(s.now.Add(15*time.Minute), record.ExpiresAt)
				return nil
			})
		enrollment, err := s.service.CreateEnrollment(context.Background(), ownerPrincipal(), "agent-1", onprem.OwnerEnrollmentRequest{
			Permissions: []onprem.Permission{onprem.PermissionGet},
		})
		s.Require().NoError(err)
		s.Contains(enrollment.Token, "tm_enroll_generated-id.generated-secret")
	})

	cases := []struct {
		name      string
		agent     onprem.AgentProfile
		request   onprem.OwnerEnrollmentRequest
		expectErr error
	}{
		{
			"suspended agents are forbidden", func() onprem.AgentProfile {
				profile := s.activeAgent()
				profile.Status = onprem.AgentStatusSuspended
				return profile
			}(),
			onprem.OwnerEnrollmentRequest{Permissions: []onprem.Permission{onprem.PermissionGet}}, onprem.ErrForbidden,
		},
		{
			"permissions are required", s.activeAgent(),
			onprem.OwnerEnrollmentRequest{}, onprem.ErrInvalidIdentityInput,
		},
		{
			"permissions must be grantable", s.activeAgent(),
			onprem.OwnerEnrollmentRequest{Permissions: []onprem.Permission{onprem.PermissionChannelSend}}, onprem.ErrInvalidIdentityInput,
		},
		{
			"negative expiry is invalid", s.activeAgent(),
			onprem.OwnerEnrollmentRequest{
				Permissions: []onprem.Permission{onprem.PermissionGet}, ExpiresIn: -time.Minute,
			}, onprem.ErrInvalidIdentityInput,
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.agents.EXPECT().GetOwnedAgent(gomock.Any(), "team_alpha", "mbr_owner", "agent-1").
				Return(tc.agent, nil)
			_, err := s.service.CreateEnrollment(context.Background(), ownerPrincipal(), "agent-1", tc.request)
			s.Require().ErrorIs(err, tc.expectErr)
		})
	}
}

func (s *registrySuite) TestArtifactSurfaces() {
	s.Run("list enrollments", func() {
		s.agents.EXPECT().GetOwnedAgent(gomock.Any(), "team_alpha", "mbr_owner", "agent-1").
			Return(s.activeAgent(), nil)
		s.credentials.EXPECT().ListOwnedEnrollments(gomock.Any(), "team_alpha", "mbr_owner", "agent-1", gomock.Any(), s.now).
			Return([]onprem.AgentEnrollmentMetadata{{EnrollmentID: "enr-1"}}, nil)
		result, err := s.service.ListEnrollments(context.Background(), ownerPrincipal(), "agent-1", onprem.AgentArtifactFilter{})
		s.Require().NoError(err)
		s.Len(result, 1)
	})

	s.Run("revoke enrollment", func() {
		s.agents.EXPECT().GetOwnedAgent(gomock.Any(), "team_alpha", "mbr_owner", "agent-1").
			Return(s.activeAgent(), nil).Times(2)
		s.credentials.EXPECT().RevokeOwnedEnrollment(
			gomock.Any(), "team_alpha", "mbr_owner", gomock.Any(), "agent-1", "enr-1", "key-1", s.now,
		).Return(onprem.AgentEnrollmentMetadata{EnrollmentID: "enr-1", Status: "revoked"}, nil)
		result, err := s.service.RevokeEnrollment(context.Background(), ownerPrincipal(), "agent-1", "enr-1", "key-1")
		s.Require().NoError(err)
		s.Equal("revoked", result.Status)

		_, err = s.service.RevokeEnrollment(context.Background(), ownerPrincipal(), "agent-1", " ", "")
		s.Require().ErrorIs(err, onprem.ErrEnrollmentInvalid)
	})

	s.Run("list credentials", func() {
		s.agents.EXPECT().GetOwnedAgent(gomock.Any(), "team_alpha", "mbr_owner", "agent-1").
			Return(s.activeAgent(), nil)
		s.credentials.EXPECT().ListOwnedCredentials(gomock.Any(), "team_alpha", "mbr_owner", "agent-1", gomock.Any(), s.now).
			Return([]onprem.AgentCredentialMetadata{{CredentialID: "cred-1"}}, nil)
		result, err := s.service.ListCredentials(context.Background(), ownerPrincipal(), "agent-1", onprem.AgentArtifactFilter{})
		s.Require().NoError(err)
		s.Len(result, 1)
	})

	s.Run("revoke credential", func() {
		s.agents.EXPECT().GetOwnedAgent(gomock.Any(), "team_alpha", "mbr_owner", "agent-1").
			Return(s.activeAgent(), nil).Times(2)
		s.credentials.EXPECT().RevokeOwnedCredential(
			gomock.Any(), "team_alpha", "mbr_owner", gomock.Any(), "agent-1", "cred-1", "key-1", s.now,
		).Return(onprem.AgentCredentialMetadata{CredentialID: "cred-1"}, nil)
		result, err := s.service.RevokeOwnedCredential(context.Background(), ownerPrincipal(), "agent-1", "cred-1", "key-1")
		s.Require().NoError(err)
		s.Equal("cred-1", result.CredentialID)

		_, err = s.service.RevokeOwnedCredential(context.Background(), ownerPrincipal(), "agent-1", " ", "")
		s.Require().ErrorIs(err, onprem.ErrCredentialNotFound)
	})

	s.Run("store errors propagate", func() {
		s.agents.EXPECT().GetOwnedAgent(gomock.Any(), "team_alpha", "mbr_owner", "agent-1").
			Return(onprem.AgentProfile{}, onprem.ErrAgentNotFound)
		_, err := s.service.ListCredentials(context.Background(), ownerPrincipal(), "agent-1", onprem.AgentArtifactFilter{})
		s.Require().ErrorIs(err, onprem.ErrAgentNotFound)
	})
}

func (s *registrySuite) TestUnsupportedSurfaces() {
	agentPrincipal := onprem.Principal{UserID: "usr_owner", ScopeID: "team_alpha"}
	cases := []struct {
		name string
		call func() error
	}{
		{"ListDirectoryAgents", func() error {
			_, err := s.service.ListDirectoryAgents(context.Background(), agentPrincipal, onprem.AgentFilter{})
			return err
		}},
		{"GetDirectoryAgent", func() error {
			_, err := s.service.GetDirectoryAgent(context.Background(), agentPrincipal, "agent-1")
			return err
		}},
		{"ListAdminAgents", func() error {
			_, err := s.service.ListAdminAgents(context.Background(), ownerPrincipal(), onprem.AgentFilter{})
			return err
		}},
		{"GetAdminAgent", func() error {
			_, err := s.service.GetAdminAgent(context.Background(), ownerPrincipal(), "agent-1")
			return err
		}},
		{"UpdateAdminAgent", func() error {
			_, err := s.service.UpdateAdminAgent(context.Background(), ownerPrincipal(), "agent-1", onprem.UpdateAgentRequest{})
			return err
		}},
		{"RetireAdminAgent", func() error {
			_, err := s.service.RetireAdminAgent(context.Background(), ownerPrincipal(), "agent-1", 1, "")
			return err
		}},
		{"TransferAgent", func() error {
			_, err := s.service.TransferAgent(context.Background(), ownerPrincipal(), "agent-1", onprem.TransferAgentRequest{})
			return err
		}},
		{"ListAdminEnrollments", func() error {
			_, err := s.service.ListAdminEnrollments(context.Background(), ownerPrincipal(), "agent-1", onprem.AgentArtifactFilter{})
			return err
		}},
		{"RevokeAdminEnrollment", func() error {
			_, err := s.service.RevokeAdminEnrollment(context.Background(), ownerPrincipal(), "agent-1", "enr-1", "")
			return err
		}},
		{"ListAdminCredentials", func() error {
			_, err := s.service.ListAdminCredentials(context.Background(), ownerPrincipal(), "agent-1", onprem.AgentArtifactFilter{})
			return err
		}},
		{"RevokeAdminCredential", func() error {
			_, err := s.service.RevokeAdminCredential(context.Background(), ownerPrincipal(), "agent-1", "cred-1", "")
			return err
		}},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.Require().ErrorIs(tc.call(), saas.ErrUnsupportedInSaaS)
		})
	}
}

func (s *registrySuite) TestCreateDeviceEnrollment() {
	s.Run("creates a device enrollment with the configured default grantable set", func() {
		s.credentials.EXPECT().CreateDeviceEnrollment(gomock.Any(), "team_alpha", "mbr_owner", gomock.Any()).
			DoAndReturn(func(_ context.Context, _, _ string, record onprem.EnrollmentRecord) error {
				s.Equal(onprem.CredentialKindDevice, record.Kind)
				s.Empty(record.AgentID)
				s.Equal("workstation", record.CredentialLabel)
				s.Equal([]onprem.Permission{onprem.PermissionAgentProvision}, record.Permissions)
				s.ElementsMatch(
					[]onprem.Permission{onprem.PermissionObserve, onprem.PermissionSearch, onprem.PermissionGet},
					record.GrantablePermissions,
				)
				return nil
			})
		enrollment, grantable, err := s.service.CreateDeviceEnrollment(context.Background(), ownerPrincipal(), onprem.DeviceEnrollmentRequest{
			DeviceName: "workstation",
		})
		s.Require().NoError(err)
		s.Contains(enrollment.Token, "tm_enroll_generated-id.generated-secret")
		s.Len(grantable, 3)
	})

	s.Run("narrows the grantable set on request", func() {
		s.credentials.EXPECT().CreateDeviceEnrollment(gomock.Any(), "team_alpha", "mbr_owner", gomock.Any()).
			DoAndReturn(func(_ context.Context, _, _ string, record onprem.EnrollmentRecord) error {
				s.Equal([]onprem.Permission{onprem.PermissionGet}, record.GrantablePermissions)
				return nil
			})
		_, grantable, err := s.service.CreateDeviceEnrollment(context.Background(), ownerPrincipal(), onprem.DeviceEnrollmentRequest{
			DeviceName: "workstation", GrantablePermissions: []onprem.Permission{onprem.PermissionGet},
		})
		s.Require().NoError(err)
		s.Equal([]onprem.Permission{onprem.PermissionGet}, grantable)
	})

	cases := []struct {
		name      string
		actor     onprem.HumanPrincipal
		request   onprem.DeviceEnrollmentRequest
		expectErr error
	}{
		{
			"member cannot enroll devices", func() onprem.HumanPrincipal {
				principal := ownerPrincipal()
				principal.Role = onprem.RoleMember
				return principal
			}(),
			onprem.DeviceEnrollmentRequest{DeviceName: "workstation"}, onprem.ErrForbidden,
		},
		{
			"device name is required", ownerPrincipal(),
			onprem.DeviceEnrollmentRequest{}, onprem.ErrInvalidIdentityInput,
		},
		{
			"grantable must stay inside the configured set", ownerPrincipal(),
			onprem.DeviceEnrollmentRequest{
				DeviceName: "workstation", GrantablePermissions: []onprem.Permission{onprem.PermissionChannelSend},
			}, onprem.ErrInvalidIdentityInput,
		},
		{
			"negative expiry is invalid", ownerPrincipal(),
			onprem.DeviceEnrollmentRequest{DeviceName: "workstation", ExpiresIn: -time.Minute}, onprem.ErrInvalidIdentityInput,
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			_, _, err := s.service.CreateDeviceEnrollment(context.Background(), tc.actor, tc.request)
			s.Require().ErrorIs(err, tc.expectErr)
		})
	}
}

func (s *registrySuite) TestDeviceAdministration() {
	s.Run("revoke device passes team scope and idempotency key", func() {
		s.credentials.EXPECT().RevokeDevice(gomock.Any(), "team_alpha", gomock.Any(), "cred-1", "key-1", s.now).
			DoAndReturn(func(_ context.Context, _ string, actor onprem.HumanPrincipal, _, _ string, _ time.Time) (onprem.DeviceSummary, error) {
				s.Equal("mbr_owner", actor.MembershipID)
				return onprem.DeviceSummary{CredentialID: "cred-1", DeviceName: "workstation"}, nil
			})
		summary, err := s.service.RevokeDevice(context.Background(), ownerPrincipal(), "cred-1", "key-1")
		s.Require().NoError(err)
		s.Equal("cred-1", summary.CredentialID)
	})

	s.Run("list devices is team-filtered", func() {
		s.credentials.EXPECT().ListDevices(gomock.Any(), "team_alpha", gomock.Any()).
			Return([]onprem.DeviceSummary{{CredentialID: "cred-1"}}, nil)
		devices, err := s.service.ListDevices(context.Background(), ownerPrincipal(), onprem.DeviceFilter{})
		s.Require().NoError(err)
		s.Len(devices, 1)
	})

	s.Run("get device is team-filtered", func() {
		s.credentials.EXPECT().GetDevice(gomock.Any(), "team_alpha", "cred-1").
			Return(onprem.DeviceDetail{Device: onprem.DeviceSummary{CredentialID: "cred-1"}}, nil)
		detail, err := s.service.GetDevice(context.Background(), ownerPrincipal(), "cred-1")
		s.Require().NoError(err)
		s.Equal("cred-1", detail.Device.CredentialID)
	})

	s.Run("member actor is forbidden and empty IDs are rejected", func() {
		member := ownerPrincipal()
		member.Role = onprem.RoleMember
		_, err := s.service.ListDevices(context.Background(), member, onprem.DeviceFilter{})
		s.Require().ErrorIs(err, onprem.ErrForbidden)
		_, err = s.service.RevokeDevice(context.Background(), ownerPrincipal(), " ", "")
		s.Require().ErrorIs(err, onprem.ErrCredentialNotFound)
		_, err = s.service.GetDevice(context.Background(), ownerPrincipal(), " ")
		s.Require().ErrorIs(err, onprem.ErrCredentialNotFound)
	})
}
