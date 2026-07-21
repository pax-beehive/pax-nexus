package postgres_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/stretchr/testify/suite"
)

type credentialStoreSuite struct {
	suite.Suite
	store       *postgres.Store
	credentials *postgres.CredentialStore
}

func TestCredentialStoreSuite(t *testing.T) {
	suite.Run(t, new(credentialStoreSuite))
}

func (s *credentialStoreSuite) SetupSuite() {
	store, err := postgres.Open(context.Background(), testDSN(s.T()))
	s.Require().NoError(err)
	s.Require().NoError(store.Migrate(context.Background()))
	s.store = store
	s.credentials = store.Credentials()
}

func (s *credentialStoreSuite) TearDownSuite() {
	if s.store != nil {
		s.store.Close()
	}
}

func (s *credentialStoreSuite) TestCredentialLifecycleIncludesRotationOverlapAndRevocation() {
	now := time.Now().UTC()
	service, admin := s.newService(&now)
	issued := s.issueCredential(service, admin, time.Minute)

	principal, err := service.Authenticate(context.Background(), issued.APIKey)
	s.Require().NoError(err)
	s.Equal("postgres-agent", principal.AgentID)
	rotated, err := service.RotateCredential(context.Background(), principal)
	s.Require().NoError(err)
	_, err = service.Authenticate(context.Background(), issued.APIKey)
	s.Require().NoError(err, "old key must remain valid during overlap")
	now = now.Add(2 * time.Minute)
	_, err = service.Authenticate(context.Background(), issued.APIKey)
	s.Require().ErrorIs(err, onprem.ErrUnauthorized)
	rotatedPrincipal, err := service.Authenticate(context.Background(), rotated.APIKey)
	s.Require().NoError(err)
	s.Require().NoError(service.RevokeCredential(context.Background(), admin, rotatedPrincipal.CredentialID))
	_, err = service.Authenticate(context.Background(), rotated.APIKey)
	s.Require().ErrorIs(err, onprem.ErrUnauthorized)
}

func (s *credentialStoreSuite) TestEnrollmentStateMatrix() {
	tests := []struct {
		name    string
		expired bool
	}{
		{name: "one-time exchange"},
		{name: "expired enrollment", expired: true},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			now := time.Now().UTC()
			service, admin := s.newService(&now)
			enrollment, err := service.CreateEnrollment(context.Background(), admin, onprem.EnrollmentRequest{
				UserID: "postgres-user", AgentID: "postgres-agent", ExpiresIn: time.Second,
			})
			s.Require().NoError(err)
			if test.expired {
				now = now.Add(2 * time.Second)
			}
			_, err = service.ExchangeEnrollment(context.Background(), enrollment.Token)
			if test.expired {
				s.Require().ErrorIs(err, onprem.ErrEnrollmentInvalid)
				return
			}
			s.Require().NoError(err)
			_, err = service.ExchangeEnrollment(context.Background(), enrollment.Token)
			s.Require().ErrorIs(err, onprem.ErrEnrollmentInvalid)
		})
	}
}

func (s *credentialStoreSuite) TestAgentIdentityCannotBeClaimedByAnotherUser() {
	now := time.Now().UTC()
	service, admin := s.newService(&now)
	agentID := uniqueCredentialValue("claimed-agent")

	first, err := service.CreateEnrollment(context.Background(), admin, onprem.EnrollmentRequest{
		UserID: uniqueCredentialValue("first-user"), AgentID: agentID,
	})
	s.Require().NoError(err)
	_, err = service.ExchangeEnrollment(context.Background(), first.Token)
	s.Require().NoError(err)

	conflicting, err := service.CreateEnrollment(context.Background(), admin, onprem.EnrollmentRequest{
		UserID: uniqueCredentialValue("second-user"), AgentID: agentID,
	})
	s.Require().NoError(err)
	_, err = service.ExchangeEnrollment(context.Background(), conflicting.Token)
	s.ErrorIs(err, onprem.ErrAgentIdentityConflict)
}

func (s *credentialStoreSuite) TestEnrollmentExchangeRollsBackConsumptionWhenCredentialInsertFails() {
	now := time.Now().UTC()
	service, admin := s.newService(&now)
	existing := s.issueCredential(service, admin, time.Minute)
	token := uniqueCredentialValue("rollback-enrollment")
	tokenDigest := credentialDigest(token)
	s.Require().NoError(s.credentials.SaveEnrollment(context.Background(), onprem.EnrollmentRecord{
		ID: uniqueCredentialValue("enrollment"), TokenDigest: tokenDigest,
		UserID: "postgres-user", AgentID: "postgres-agent", Permissions: []onprem.Permission{onprem.PermissionSearch},
		CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}))
	replacement := onprem.CredentialRecord{
		ID: existing.CredentialID, KeyDigest: credentialDigest(uniqueCredentialValue("failed-key")), CreatedAt: now,
	}
	_, err := s.credentials.ExchangeEnrollment(context.Background(), tokenDigest, replacement, now)
	s.Require().Error(err)
	replacement.ID = uniqueCredentialValue("replacement")
	replacement.KeyDigest = credentialDigest(uniqueCredentialValue("replacement-key"))
	_, err = s.credentials.ExchangeEnrollment(context.Background(), tokenDigest, replacement, now)
	s.Require().NoError(err, "failed insert must not consume the enrollment")
}

func (s *credentialStoreSuite) TestRotationRollsBackExpiryWhenReplacementInsertFails() {
	now := time.Now().UTC()
	service, admin := s.newService(&now)
	issued := s.issueCredential(service, admin, time.Minute)
	principal, err := service.Authenticate(context.Background(), issued.APIKey)
	s.Require().NoError(err)
	replacement := onprem.CredentialRecord{
		ID: principal.CredentialID, KeyDigest: credentialDigest(uniqueCredentialValue("collision-key")),
		UserID: principal.UserID, AgentID: principal.AgentID, Permissions: principal.Permissions, CreatedAt: now,
	}
	err = s.credentials.RotateCredential(context.Background(), principal.CredentialID, replacement, now.Add(time.Minute))
	s.Require().Error(err)
	now = now.Add(2 * time.Minute)
	_, err = service.Authenticate(context.Background(), issued.APIKey)
	s.Require().NoError(err, "failed replacement insert must roll back the old-key expiry")
}

func (s *credentialStoreSuite) TestRevokeUnknownCredentialReturnsTypedError() {
	err := s.credentials.RevokeCredential(context.Background(), uniqueCredentialValue("missing"), time.Now().UTC())
	s.Require().ErrorIs(err, onprem.ErrCredentialNotFound)
}

func (s *credentialStoreSuite) newService(now *time.Time) (*onprem.CredentialService, onprem.Principal) {
	adminKey := uniqueCredentialValue("postgres-admin")
	service, err := onprem.NewCredentialService(s.credentials, onprem.CredentialConfig{
		AdminAPIKey: adminKey, RotationOverlap: time.Minute,
	}, onprem.WithClock(func() time.Time { return *now }))
	s.Require().NoError(err)
	admin, err := service.Authenticate(context.Background(), adminKey)
	s.Require().NoError(err)
	return service, admin
}

func (s *credentialStoreSuite) issueCredential(
	service *onprem.CredentialService,
	admin onprem.Principal,
	expiresIn time.Duration,
) onprem.IssuedCredential {
	enrollment, err := service.CreateEnrollment(context.Background(), admin, onprem.EnrollmentRequest{
		UserID: "postgres-user", AgentID: "postgres-agent", ExpiresIn: expiresIn,
	})
	s.Require().NoError(err)
	issued, err := service.ExchangeEnrollment(context.Background(), enrollment.Token)
	s.Require().NoError(err)
	return issued
}

func uniqueCredentialValue(label string) string {
	return fmt.Sprintf("%s-%d", label, time.Now().UnixNano())
}

func credentialDigest(value string) onprem.Digest {
	return sha256.Sum256([]byte(value))
}
