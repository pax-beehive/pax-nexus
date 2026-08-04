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

type credentialsSuite struct {
	suite.Suite
	controller *gomock.Controller
	store      *mocks.MockTeamCredentialStore
	service    *saas.Credentials
	now        time.Time
}

func TestCredentialsSuite(t *testing.T) {
	suite.Run(t, new(credentialsSuite))
}

func (s *credentialsSuite) SetupTest() {
	s.controller = gomock.NewController(s.T())
	s.store = mocks.NewMockTeamCredentialStore(s.controller)
	s.now = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	service, err := saas.NewCredentials(s.store, saas.CredentialConfig{
		SecretPepper: testPepper, RotationOverlap: 5 * time.Minute,
	},
		saas.WithCredentialClock(func() time.Time { return s.now }),
		saas.WithCredentialTokenSource(fixedSource("generated-secret")),
	)
	s.Require().NoError(err)
	s.service = service
}

func (s *credentialsSuite) TestConstructorValidation() {
	_, err := saas.NewCredentials(nil, saas.CredentialConfig{SecretPepper: testPepper, RotationOverlap: time.Minute})
	s.Require().Error(err)
	_, err = saas.NewCredentials(s.store, saas.CredentialConfig{SecretPepper: testPepper})
	s.Require().Error(err)
	_, err = saas.NewCredentials(s.store, saas.CredentialConfig{SecretPepper: "short", RotationOverlap: time.Minute})
	s.Require().Error(err)
}

func (s *credentialsSuite) TestAuthenticateResolvesScopeFromCredential() {
	apiKey := "tm_key_cred-1.secret"
	scoped := saas.ScopedCredential{
		CredentialRecord: onprem.CredentialRecord{
			ID: "cred-1", UserID: "usr_owner", MembershipID: "mbr_owner", AgentID: "agent-1",
			Label: "laptop", Permissions: []onprem.Permission{onprem.PermissionGet},
		},
		TeamID: "team_alpha",
	}
	s.store.EXPECT().ResolveCredential(gomock.Any(), "cred-1", gomock.Any(), s.now).
		DoAndReturn(func(_ context.Context, _ string, digest onprem.Digest, _ time.Time) (saas.ScopedCredential, error) {
			s.Equal(testDigest(testPepper, "agent-credential", apiKey), digest)
			return scoped, nil
		})
	principal, err := s.service.Authenticate(context.Background(), apiKey)
	s.Require().NoError(err)
	s.Equal("team_alpha", principal.ScopeID, "the scope comes from the credential row, not a constant")
	s.Equal("agent-1", principal.AgentID)
	s.Equal(onprem.CredentialKindAgent, principal.Kind)
	s.Equal([]onprem.Permission{onprem.PermissionGet}, principal.Permissions)
}

func (s *credentialsSuite) TestAuthenticateRejections() {
	cases := []struct {
		name string
		key  string
	}{
		{"wrong prefix", "tm_session_cred-1.secret"},
		{"empty", "   "},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			_, err := s.service.Authenticate(context.Background(), tc.key)
			s.Require().ErrorIs(err, onprem.ErrUnauthorized)
		})
	}

	s.Run("unknown credential is unauthorized", func() {
		s.store.EXPECT().ResolveCredential(gomock.Any(), "cred-1", gomock.Any(), s.now).
			Return(saas.ScopedCredential{}, onprem.ErrUnauthorized).Times(2)
		_, err := s.service.Authenticate(context.Background(), "tm_key_cred-1.secret")
		s.Require().ErrorIs(err, onprem.ErrUnauthorized)
	})

	s.Run("store failures propagate", func() {
		s.store.EXPECT().ResolveCredential(gomock.Any(), "cred-1", gomock.Any(), s.now).
			Return(saas.ScopedCredential{}, errors.New("store down"))
		_, err := s.service.Authenticate(context.Background(), "tm_key_cred-1.secret")
		s.Require().ErrorContains(err, "store down")
		s.Require().NotErrorIs(err, onprem.ErrUnauthorized)
	})
}

func (s *credentialsSuite) TestExchangeEnrollment() {
	token := "tm_enroll_enr-1.secret"
	s.Run("issues a credential for the enrollment", func() {
		s.store.EXPECT().ExchangeEnrollment(gomock.Any(), "enr-1", gomock.Any(), gomock.Any(), s.now).
			DoAndReturn(func(_ context.Context, _ string, digest onprem.Digest, credential onprem.CredentialRecord, _ time.Time) (onprem.EnrollmentRecord, error) {
				s.Equal(testDigest(testPepper, "agent-enrollment", token), digest)
				s.Equal("generated-secret", credential.ID)
				return onprem.EnrollmentRecord{
					ID: "enr-1", UserID: "usr_owner", AgentID: "agent-1",
					Permissions: []onprem.Permission{onprem.PermissionGet},
				}, nil
			})
		issued, err := s.service.ExchangeEnrollment(context.Background(), token)
		s.Require().NoError(err)
		s.Equal("tm_key_generated-secret.generated-secret", issued.APIKey)
		s.Equal("usr_owner", issued.UserID)
		s.Equal(onprem.CredentialKindAgent, issued.Kind)
	})

	s.Run("rejects malformed and invalid tokens", func() {
		_, err := s.service.ExchangeEnrollment(context.Background(), "not-an-enrollment")
		s.Require().ErrorIs(err, onprem.ErrEnrollmentInvalid)

		s.store.EXPECT().ExchangeEnrollment(gomock.Any(), "enr-1", gomock.Any(), gomock.Any(), s.now).
			Return(onprem.EnrollmentRecord{}, onprem.ErrEnrollmentInvalid).Times(2)
		_, err = s.service.ExchangeEnrollment(context.Background(), token)
		s.Require().ErrorIs(err, onprem.ErrEnrollmentInvalid)
	})
}

func (s *credentialsSuite) TestRotateCredential() {
	agentPrincipal := onprem.Principal{
		UserID: "usr_owner", MembershipID: "mbr_owner", AgentID: "agent-1",
		ScopeID: "team_alpha", CredentialID: "cred-1", CredentialLabel: "laptop",
		Permissions: []onprem.Permission{onprem.PermissionGet}, Kind: onprem.CredentialKindAgent,
	}
	s.Run("rotates inside the principal's team", func() {
		s.store.EXPECT().RotateCredential(gomock.Any(), "team_alpha", "cred-1", gomock.Any(), s.now.Add(5*time.Minute)).
			DoAndReturn(func(_ context.Context, _, _ string, replacement onprem.CredentialRecord, _ time.Time) error {
				s.Equal("cred-1", replacement.RotatedFromCredentialID)
				s.Equal("agent-1", replacement.AgentID)
				return nil
			})
		issued, err := s.service.RotateCredential(context.Background(), agentPrincipal)
		s.Require().NoError(err)
		s.Equal("tm_key_generated-secret.generated-secret", issued.APIKey)
	})

	s.Run("wrong scope cannot rotate", func() {
		// The store enforces the team boundary: a credential that does not
		// live in the principal's team resolves to unauthorized.
		s.store.EXPECT().RotateCredential(gomock.Any(), "team_alpha", "cred-1", gomock.Any(), gomock.Any()).
			Return(onprem.ErrUnauthorized)
		_, err := s.service.RotateCredential(context.Background(), agentPrincipal)
		s.Require().ErrorIs(err, onprem.ErrUnauthorized)
	})

	s.Run("validation", func() {
		_, err := s.service.RotateCredential(context.Background(), onprem.Principal{ScopeID: "team_alpha"})
		s.Require().ErrorIs(err, onprem.ErrUnauthorized, "a credential ID is required")

		noScope := agentPrincipal
		noScope.ScopeID = ""
		_, err = s.service.RotateCredential(context.Background(), noScope)
		s.Require().ErrorIs(err, onprem.ErrUnauthorized, "a team scope is required")

		device := agentPrincipal
		device.Kind = onprem.CredentialKindDevice
		_, err = s.service.RotateCredential(context.Background(), device)
		s.Require().ErrorIs(err, onprem.ErrForbidden)
	})
}

func (s *credentialsSuite) TestUnsupportedSurfaces() {
	principal := onprem.Principal{ScopeID: "team_alpha"}
	cases := []struct {
		name string
		call func() error
	}{
		{"CreateEnrollment", func() error {
			_, err := s.service.CreateEnrollment(context.Background(), principal, onprem.EnrollmentRequest{})
			return err
		}},
		{"RevokeCredential", func() error {
			return s.service.RevokeCredential(context.Background(), principal, "cred-1")
		}},
		{"ProvisionDeviceAgent", func() error {
			_, err := s.service.ProvisionDeviceAgent(context.Background(), principal, onprem.DeviceProvisionRequest{})
			return err
		}},
		{"ListDeviceProvisionedAgents", func() error {
			_, err := s.service.ListDeviceProvisionedAgents(context.Background(), principal)
			return err
		}},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.Require().ErrorIs(tc.call(), saas.ErrUnsupportedInSaaS)
		})
	}
}
