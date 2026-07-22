package onprem_test

import (
	"context"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/stretchr/testify/suite"
)

type registrySuite struct {
	suite.Suite
	store   *registryStore
	service *onprem.RegistryService
	now     time.Time
}

func TestRegistrySuite(t *testing.T) {
	suite.Run(t, new(registrySuite))
}

func (s *registrySuite) SetupTest() {
	s.now = time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	s.store = &registryStore{}
	service, err := onprem.NewRegistryService(
		s.store,
		onprem.RegistryConfig{
			SecretPepper: "0123456789abcdef0123456789abcdef",
			MemberGrantablePermissions: []onprem.Permission{
				onprem.PermissionObserve, onprem.PermissionSearch, onprem.PermissionGet,
				onprem.PermissionChannelSend, onprem.PermissionChannelReceive,
			},
		},
		onprem.WithRegistryClock(func() time.Time { return s.now }),
		onprem.WithRegistryIDSource(func() (string, error) { return "enrollment-id", nil }),
		onprem.WithRegistryTokenSource(func() (string, error) { return "enrollment-secret", nil }),
	)
	s.Require().NoError(err)
	s.service = service
}

func (s *registrySuite) TestMemberCreatesAgentAndListsOwnedProfiles() {
	principal := onprem.HumanPrincipal{
		UserID: "user-1", MembershipID: "membership-1", Role: onprem.RoleMember,
		MembershipStatus: onprem.MembershipStatusActive,
	}

	created, err := s.service.CreateAgent(context.Background(), principal, onprem.CreateAgentRequest{
		AgentID: "reviewer", DisplayName: "Reviewer", Description: "Reviews changes",
		AgentType: "codex", DirectoryVisible: true,
	})

	s.Require().NoError(err)
	s.Equal("membership-1", created.OwnerMembershipID)
	s.Equal(onprem.AgentStatusActive, created.Status)
	profiles, err := s.service.ListOwnedAgents(context.Background(), principal, onprem.AgentFilter{})
	s.Require().NoError(err)
	s.Require().Len(profiles, 1)
	s.Equal("reviewer", profiles[0].AgentID)
}

func (s *registrySuite) TestOwnerEnrollmentRequiresExplicitPermissions() {
	principal := onprem.HumanPrincipal{
		UserID: "user-1", MembershipID: "membership-1", Role: onprem.RoleMember,
		MembershipStatus: onprem.MembershipStatusActive,
	}
	_, err := s.service.CreateAgent(context.Background(), principal, onprem.CreateAgentRequest{
		AgentID: "reviewer", DisplayName: "Reviewer",
	})
	s.Require().NoError(err)

	_, err = s.service.CreateEnrollment(context.Background(), principal, "reviewer", onprem.OwnerEnrollmentRequest{})
	s.Require().Error(err)

	credentialExpiresAt := s.now.Add(30 * 24 * time.Hour)
	enrollment, err := s.service.CreateEnrollment(context.Background(), principal, "reviewer", onprem.OwnerEnrollmentRequest{
		CredentialLabel: "macbook", Permissions: []onprem.Permission{onprem.PermissionChannelReceive},
		CredentialExpiresAt: &credentialExpiresAt,
	})
	s.Require().NoError(err)
	s.Equal("tm_enroll_enrollment-id.enrollment-secret", enrollment.Token)
	s.Require().Len(s.store.enrollments, 1)
	s.Equal("reviewer", s.store.enrollments[0].AgentID)
	s.Equal("user-1", s.store.enrollments[0].UserID)
	s.Equal(&credentialExpiresAt, s.store.enrollments[0].CredentialExpiresAt)
	metadata, err := s.service.ListEnrollments(context.Background(), principal, "reviewer", onprem.AgentArtifactFilter{})
	s.Require().NoError(err)
	s.Require().Len(metadata, 1)
	s.Equal("macbook", metadata[0].CredentialLabel)
	revoked, err := s.service.RevokeEnrollment(
		context.Background(), principal, "reviewer", metadata[0].EnrollmentID, "revoke-enrollment-1",
	)
	s.Require().NoError(err)
	s.Equal("revoked", revoked.Status)
}

func (s *registrySuite) TestDirectoryRequiresChannelSendPermission() {
	principal := onprem.HumanPrincipal{
		UserID: "user-1", MembershipID: "membership-1", Role: onprem.RoleMember,
		MembershipStatus: onprem.MembershipStatusActive,
	}
	_, err := s.service.CreateAgent(context.Background(), principal, onprem.CreateAgentRequest{
		AgentID: "reviewer", DisplayName: "Reviewer", DirectoryVisible: true,
	})
	s.Require().NoError(err)

	_, err = s.service.ListDirectoryAgents(context.Background(), onprem.Principal{}, onprem.AgentFilter{})
	s.Require().ErrorIs(err, onprem.ErrForbidden)

	agents, err := s.service.ListDirectoryAgents(context.Background(), onprem.Principal{
		ScopeID: onprem.LocalScopeID, Permissions: []onprem.Permission{onprem.PermissionChannelSend},
	}, onprem.AgentFilter{})
	s.Require().NoError(err)
	s.Require().Len(agents, 1)
	s.Equal("reviewer", agents[0].AgentID)
}

func (s *registrySuite) TestAgentUpdateSuspendAndRetireLifecycle() {
	principal := activeMember()
	created, err := s.service.CreateAgent(context.Background(), principal, onprem.CreateAgentRequest{
		AgentID: "reviewer", DisplayName: "Reviewer", DirectoryVisible: true,
	})
	s.Require().NoError(err)

	displayName := "Security Reviewer"
	suspended := onprem.AgentStatusSuspended
	transitions := []struct {
		name       string
		apply      func(int64) (onprem.AgentProfile, error)
		wantStatus onprem.AgentStatus
	}{
		{
			name: "active to suspended",
			apply: func(version int64) (onprem.AgentProfile, error) {
				return s.service.UpdateOwnedAgent(context.Background(), principal, created.AgentID, onprem.UpdateAgentRequest{
					DisplayName: &displayName, Status: &suspended, ResourceVersion: version,
				})
			},
			wantStatus: onprem.AgentStatusSuspended,
		},
		{
			name: "suspended to retired",
			apply: func(version int64) (onprem.AgentProfile, error) {
				return s.service.RetireOwnedAgent(context.Background(), principal, created.AgentID, version, "retire-1")
			},
			wantStatus: onprem.AgentStatusRetired,
		},
	}
	current := created
	for _, transition := range transitions {
		s.Run(transition.name, func() {
			updated, updateErr := transition.apply(current.ResourceVersion)
			s.Require().NoError(updateErr)
			s.Equal(transition.wantStatus, updated.Status)
			current = updated
		})
	}
	s.Equal(displayName, current.DisplayName)
	s.NotNil(current.RetiredAt)
	_, err = s.service.CreateEnrollment(context.Background(), principal, created.AgentID, onprem.OwnerEnrollmentRequest{
		Permissions: []onprem.Permission{onprem.PermissionChannelReceive},
	})
	s.Require().ErrorIs(err, onprem.ErrForbidden)
}

func (s *registrySuite) TestOptimisticConcurrencyAndLifecycleValidation() {
	principal := activeMember()
	created, err := s.service.CreateAgent(context.Background(), principal, onprem.CreateAgentRequest{
		AgentID: "reviewer", DisplayName: "Reviewer",
	})
	s.Require().NoError(err)

	name := "Updated"
	active := onprem.AgentStatusActive
	tests := []struct {
		name    string
		request onprem.UpdateAgentRequest
	}{
		{name: "stale resource version", request: onprem.UpdateAgentRequest{DisplayName: &name, ResourceVersion: 2}},
		{name: "invalid active transition", request: onprem.UpdateAgentRequest{Status: &active, ResourceVersion: 1}},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			_, updateErr := s.service.UpdateOwnedAgent(context.Background(), principal, created.AgentID, test.request)
			s.Require().ErrorIs(updateErr, onprem.ErrAgentConflict)
		})
	}
}

func (s *registrySuite) TestDirectoryGetAndAdminDirectoryAuthorization() {
	principal := activeMember()
	_, err := s.service.CreateAgent(context.Background(), principal, onprem.CreateAgentRequest{
		AgentID: "receiver", DisplayName: "Receiver", DirectoryVisible: true,
	})
	s.Require().NoError(err)
	agentPrincipal := onprem.Principal{
		ScopeID: onprem.LocalScopeID, Permissions: []onprem.Permission{onprem.PermissionChannelSend},
	}
	profile, err := s.service.GetDirectoryAgent(context.Background(), agentPrincipal, "receiver")
	s.Require().NoError(err)
	s.Equal("receiver", profile.AgentID)

	_, err = s.service.ListAdminAgents(context.Background(), principal, onprem.AgentFilter{})
	s.Require().ErrorIs(err, onprem.ErrForbidden)
	owner := principal
	owner.Role = onprem.RoleOwner
	profiles, err := s.service.ListAdminAgents(context.Background(), owner, onprem.AgentFilter{Limit: 500})
	s.Require().NoError(err)
	s.Require().Len(profiles, 1)
	profile, err = s.service.GetAdminAgent(context.Background(), owner, "receiver")
	s.Require().NoError(err)
	s.Equal("receiver", profile.AgentID)
}

func (s *registrySuite) TestAdminSuspensionAndOwnerTransferPolicy() {
	member := activeMember()
	created, err := s.service.CreateAgent(context.Background(), member, onprem.CreateAgentRequest{
		AgentID: "governed-agent", DisplayName: "Governed Agent",
	})
	s.Require().NoError(err)
	admin := member
	admin.MembershipID = "admin-membership"
	admin.Role = onprem.RoleAdmin
	suspended := onprem.AgentStatusSuspended
	updated, err := s.service.UpdateAdminAgent(context.Background(), admin, created.AgentID, onprem.UpdateAgentRequest{
		Status: &suspended, ResourceVersion: created.ResourceVersion,
	})
	s.Require().NoError(err)
	s.Equal(onprem.AgentStatusSuspended, updated.Status)
	name := "Admin Rename"
	_, err = s.service.UpdateAdminAgent(context.Background(), admin, created.AgentID, onprem.UpdateAgentRequest{
		DisplayName: &name, ResourceVersion: updated.ResourceVersion,
	})
	s.Require().ErrorIs(err, onprem.ErrForbidden)

	_, err = s.service.TransferAgent(context.Background(), admin, created.AgentID, onprem.TransferAgentRequest{
		TargetMembershipID: "new-owner", ResourceVersion: updated.ResourceVersion,
	})
	s.Require().ErrorIs(err, onprem.ErrForbidden)
	owner := admin
	owner.Role = onprem.RoleOwner
	transferred, err := s.service.TransferAgent(context.Background(), owner, created.AgentID, onprem.TransferAgentRequest{
		TargetMembershipID: "new-owner", ResourceVersion: updated.ResourceVersion,
	})
	s.Require().NoError(err)
	s.Equal("new-owner", transferred.OwnerMembershipID)
}

func (s *registrySuite) TestAdminAgentArtifactGovernanceAndOwnerFilter() {
	member := activeMember()
	_, err := s.service.CreateAgent(context.Background(), member, onprem.CreateAgentRequest{
		AgentID: "governed-agent", DisplayName: "Governed Agent",
	})
	s.Require().NoError(err)
	_, err = s.service.CreateEnrollment(context.Background(), member, "governed-agent", onprem.OwnerEnrollmentRequest{
		CredentialLabel: "workstation", Permissions: []onprem.Permission{onprem.PermissionChannelReceive},
	})
	s.Require().NoError(err)
	admin := member
	admin.MembershipID = "admin-membership"
	admin.Role = onprem.RoleAdmin

	profiles, err := s.service.ListAdminAgents(context.Background(), admin, onprem.AgentFilter{
		OwnerMembershipID: " membership-1 ",
	})
	s.Require().NoError(err)
	s.Require().Len(profiles, 1)
	s.Equal("membership-1", s.store.lastAdminFilter.OwnerMembershipID)
	enrollments, err := s.service.ListAdminEnrollments(
		context.Background(), admin, "governed-agent", onprem.AgentArtifactFilter{},
	)
	s.Require().NoError(err)
	s.Require().Len(enrollments, 1)
	revokedEnrollment, err := s.service.RevokeAdminEnrollment(
		context.Background(), admin, "governed-agent", enrollments[0].EnrollmentID, "admin-revoke-enrollment-1",
	)
	s.Require().NoError(err)
	s.Equal("revoked", revokedEnrollment.Status)

	credentials, err := s.service.ListAdminCredentials(
		context.Background(), admin, "governed-agent", onprem.AgentArtifactFilter{},
	)
	s.Require().NoError(err)
	s.Empty(credentials)
	revokedCredential, err := s.service.RevokeAdminCredential(
		context.Background(), admin, "governed-agent", "credential-1", "admin-revoke-credential-1",
	)
	s.Require().NoError(err)
	s.Equal("credential-1", revokedCredential.CredentialID)
}

func (s *registrySuite) TestAgentIdentityValidationMatrix() {
	principal := activeMember()
	tests := []struct {
		name    string
		agentID string
		display string
	}{
		{name: "agent id required", display: "Reviewer"},
		{name: "display required", agentID: "reviewer"},
		{name: "slash rejected", agentID: "team/reviewer", display: "Reviewer"},
		{name: "control rejected", agentID: "review\ner", display: "Reviewer"},
		{name: "long name rejected", agentID: "reviewer", display: string(make([]byte, 201))},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			_, err := s.service.CreateAgent(context.Background(), principal, onprem.CreateAgentRequest{
				AgentID: test.agentID, DisplayName: test.display,
			})
			s.Require().Error(err)
		})
	}
}

func (s *registrySuite) TestEnrollmentPermissionValidationAndDeduplication() {
	principal := activeMember()
	_, err := s.service.CreateAgent(context.Background(), principal, onprem.CreateAgentRequest{
		AgentID: "reviewer", DisplayName: "Reviewer",
	})
	s.Require().NoError(err)
	_, err = s.service.CreateEnrollment(context.Background(), principal, "reviewer", onprem.OwnerEnrollmentRequest{
		Permissions: []onprem.Permission{"admin"},
	})
	s.Require().Error(err)
	enrollment, err := s.service.CreateEnrollment(context.Background(), principal, "reviewer", onprem.OwnerEnrollmentRequest{
		Permissions: []onprem.Permission{onprem.PermissionSearch, onprem.PermissionSearch},
	})
	s.Require().NoError(err)
	s.NotEmpty(enrollment.Token)
	s.Equal([]onprem.Permission{onprem.PermissionSearch}, s.store.enrollments[0].Permissions)
}

func (s *registrySuite) TestMemberGrantablePermissionAllowlist() {
	service, err := onprem.NewRegistryService(s.store, onprem.RegistryConfig{
		SecretPepper:               "0123456789abcdef0123456789abcdef",
		MemberGrantablePermissions: []onprem.Permission{onprem.PermissionSearch},
	})
	s.Require().NoError(err)
	principal := activeMember()
	_, err = service.CreateAgent(context.Background(), principal, onprem.CreateAgentRequest{
		AgentID: "allowlist-agent", DisplayName: "Allowlist Agent",
	})
	s.Require().NoError(err)
	_, err = service.CreateEnrollment(context.Background(), principal, "allowlist-agent", onprem.OwnerEnrollmentRequest{
		Permissions: []onprem.Permission{onprem.PermissionChannelReceive},
	})
	s.Require().ErrorIs(err, onprem.ErrInvalidIdentityInput)
}

func activeMember() onprem.HumanPrincipal {
	return onprem.HumanPrincipal{
		UserID: "user-1", MembershipID: "membership-1", Role: onprem.RoleMember,
		MembershipStatus: onprem.MembershipStatusActive,
	}
}

type registryStore struct {
	agents          []onprem.AgentProfile
	enrollments     []onprem.EnrollmentRecord
	lastAdminFilter onprem.AgentFilter
}

func (s *registryStore) CreateAgent(_ context.Context, profile onprem.AgentProfile) (onprem.AgentProfile, error) {
	s.agents = append(s.agents, profile)
	return profile, nil
}

func (s *registryStore) ListOwnedAgents(
	_ context.Context,
	membershipID string,
	_ onprem.AgentFilter,
) ([]onprem.AgentProfile, error) {
	result := make([]onprem.AgentProfile, 0, len(s.agents))
	for _, profile := range s.agents {
		if profile.OwnerMembershipID == membershipID {
			result = append(result, profile)
		}
	}
	return result, nil
}

func (s *registryStore) GetOwnedAgent(
	_ context.Context,
	membershipID string,
	agentID string,
) (onprem.AgentProfile, error) {
	for _, profile := range s.agents {
		if profile.OwnerMembershipID == membershipID && profile.AgentID == agentID {
			return profile, nil
		}
	}
	return onprem.AgentProfile{}, onprem.ErrAgentNotFound
}

func (s *registryStore) UpdateOwnedAgent(
	_ context.Context,
	membershipID string,
	_ onprem.HumanPrincipal,
	profile onprem.AgentProfile,
) (onprem.AgentProfile, error) {
	for index, current := range s.agents {
		if current.OwnerMembershipID == membershipID && current.AgentID == profile.AgentID {
			s.agents[index] = profile
			return profile, nil
		}
	}
	return onprem.AgentProfile{}, onprem.ErrAgentNotFound
}

func (s *registryStore) RetireOwnedAgent(
	_ context.Context,
	membershipID string,
	_ onprem.HumanPrincipal,
	agentID string,
	resourceVersion int64,
	_ string,
	now time.Time,
) (onprem.AgentProfile, error) {
	for index, profile := range s.agents {
		if profile.OwnerMembershipID == membershipID && profile.AgentID == agentID &&
			profile.ResourceVersion == resourceVersion {
			profile.Status = onprem.AgentStatusRetired
			profile.UpdatedAt = now
			profile.RetiredAt = &now
			profile.ResourceVersion++
			s.agents[index] = profile
			return profile, nil
		}
	}
	return onprem.AgentProfile{}, onprem.ErrAgentConflict
}

func (s *registryStore) TransferAgent(
	_ context.Context,
	_ onprem.HumanPrincipal,
	agentID string,
	targetMembershipID string,
	resourceVersion int64,
	_ time.Time,
) (onprem.AgentProfile, error) {
	for index, profile := range s.agents {
		if profile.AgentID == agentID && profile.ResourceVersion == resourceVersion {
			profile.OwnerMembershipID = targetMembershipID
			profile.ResourceVersion++
			s.agents[index] = profile
			return profile, nil
		}
	}
	return onprem.AgentProfile{}, onprem.ErrAgentConflict
}

func (s *registryStore) CreateOwnedEnrollment(
	_ context.Context,
	_ string,
	record onprem.EnrollmentRecord,
) error {
	s.enrollments = append(s.enrollments, record)
	return nil
}

func (s *registryStore) ListOwnedEnrollments(
	_ context.Context,
	_ string,
	agentID string,
	_ onprem.AgentArtifactFilter,
	_ time.Time,
) ([]onprem.AgentEnrollmentMetadata, error) {
	result := make([]onprem.AgentEnrollmentMetadata, 0, len(s.enrollments))
	for _, enrollment := range s.enrollments {
		if enrollment.AgentID == agentID {
			result = append(result, onprem.AgentEnrollmentMetadata{
				EnrollmentID: enrollment.ID, AgentID: enrollment.AgentID,
				CredentialLabel: enrollment.CredentialLabel, Permissions: enrollment.Permissions,
				Status: "pending", CreatedAt: enrollment.CreatedAt, ExpiresAt: enrollment.ExpiresAt,
			})
		}
	}
	return result, nil
}

func (s *registryStore) RevokeOwnedEnrollment(
	_ context.Context,
	_ string,
	_ onprem.HumanPrincipal,
	agentID string,
	enrollmentID string,
	_ string,
	_ time.Time,
) (onprem.AgentEnrollmentMetadata, error) {
	return onprem.AgentEnrollmentMetadata{
		EnrollmentID: enrollmentID, AgentID: agentID, Status: "revoked",
	}, nil
}

func (s *registryStore) ListOwnedCredentials(
	context.Context,
	string,
	string,
	onprem.AgentArtifactFilter,
	time.Time,
) ([]onprem.AgentCredentialMetadata, error) {
	return nil, nil
}

func (s *registryStore) RevokeOwnedCredential(
	_ context.Context,
	_ string,
	_ onprem.HumanPrincipal,
	agentID string,
	credentialID string,
	_ string,
	_ time.Time,
) (onprem.AgentCredentialMetadata, error) {
	return onprem.AgentCredentialMetadata{CredentialID: credentialID, AgentID: agentID}, nil
}

func (s *registryStore) ListDirectoryAgents(
	_ context.Context,
	_ onprem.AgentFilter,
	_ time.Time,
) ([]onprem.AgentProfile, error) {
	return append([]onprem.AgentProfile(nil), s.agents...), nil
}

func (s *registryStore) GetDirectoryAgent(
	_ context.Context,
	agentID string,
	_ time.Time,
) (onprem.AgentProfile, error) {
	return s.GetOwnedAgent(context.Background(), "membership-1", agentID)
}

func (s *registryStore) ListAdminAgents(
	_ context.Context,
	filter onprem.AgentFilter,
) ([]onprem.AgentProfile, error) {
	s.lastAdminFilter = filter
	return append([]onprem.AgentProfile(nil), s.agents...), nil
}

func (s *registryStore) GetAdminAgent(_ context.Context, agentID string) (onprem.AgentProfile, error) {
	for _, profile := range s.agents {
		if profile.AgentID == agentID {
			return profile, nil
		}
	}
	return onprem.AgentProfile{}, onprem.ErrAgentNotFound
}
