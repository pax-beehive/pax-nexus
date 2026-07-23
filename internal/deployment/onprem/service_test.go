package onprem_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/stretchr/testify/suite"
)

type serviceSuite struct {
	suite.Suite
	store   *memoryCredentialStore
	service *onprem.CredentialService
	now     time.Time
}

func TestServiceSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(serviceSuite))
}

func (s *serviceSuite) SetupTest() {
	s.now = time.Date(2026, time.July, 21, 8, 0, 0, 0, time.UTC)
	s.store = newMemoryCredentialStore()
	tokens := &tokenSequence{values: []string{
		"enrollment-id", "enrollment-secret", "credential-id", "credential-secret",
		"discarded-id", "discarded-secret", "rotated-id", "rotated-secret",
	}}
	service, err := onprem.NewCredentialService(s.store, onprem.CredentialConfig{
		AdminAPIKey: "bootstrap-admin", RotationOverlap: 5 * time.Minute,
		SecretPepper: "0123456789abcdef0123456789abcdef", AllowLegacyAgentCreation: true,
		PortalURL: "https://memory.example.internal/",
	}, onprem.WithClock(func() time.Time { return s.now }), onprem.WithTokenSource(tokens.next))
	s.Require().NoError(err)
	s.service = service
}

func (s *serviceSuite) TestEnrollmentExchangeAuthenticationRotationAndRevocation() {
	ctx := context.Background()
	admin, err := s.service.Authenticate(ctx, "bootstrap-admin")
	s.Require().NoError(err)
	s.True(admin.HasPermission(onprem.PermissionAdmin))
	s.Equal(onprem.LocalScopeID, admin.ScopeID)

	enrollment, err := s.service.CreateEnrollment(ctx, admin, onprem.EnrollmentRequest{
		UserID: "owner", AgentID: "agent-1", ExpiresIn: time.Hour,
	})
	s.Require().NoError(err)
	s.Equal(
		"tm_enroll_enrollment-id.enrollment-secret."+
			"aHR0cHM6Ly9tZW1vcnkuZXhhbXBsZS5pbnRlcm5hbC8",
		enrollment.Token,
	)

	issued, err := s.service.ExchangeEnrollment(ctx, enrollment.Token)
	s.Require().NoError(err)
	s.Equal("tm_key_credential-id.credential-secret", issued.APIKey)
	s.Equal("credential-id", issued.CredentialID)
	_, err = s.service.ExchangeEnrollment(ctx, enrollment.Token)
	s.Require().ErrorIs(err, onprem.ErrEnrollmentInvalid)

	principal, err := s.service.Authenticate(ctx, issued.APIKey)
	s.Require().NoError(err)
	s.Equal("owner", principal.UserID)
	s.Equal("agent-1", principal.AgentID)
	s.Equal("credential-id", principal.CredentialID)
	s.Equal(onprem.LocalScopeID, principal.ScopeID)
	s.True(principal.HasPermission(onprem.PermissionObserve))
	s.True(principal.HasPermission(onprem.PermissionSearch))

	rotated, err := s.service.RotateCredential(ctx, principal)
	s.Require().NoError(err)
	s.Equal("tm_key_rotated-id.rotated-secret", rotated.APIKey)
	_, err = s.service.Authenticate(ctx, issued.APIKey)
	s.Require().NoError(err, "old key remains valid during overlap")
	s.now = s.now.Add(6 * time.Minute)
	_, err = s.service.Authenticate(ctx, issued.APIKey)
	s.Require().ErrorIs(err, onprem.ErrUnauthorized)
	rotatedPrincipal, err := s.service.Authenticate(ctx, rotated.APIKey)
	s.Require().NoError(err)

	s.Require().NoError(s.service.RevokeCredential(ctx, admin, rotatedPrincipal.CredentialID))
	_, err = s.service.Authenticate(ctx, rotated.APIKey)
	s.Require().ErrorIs(err, onprem.ErrUnauthorized)
}

func (s *serviceSuite) TestEnrollmentExchangeTokenCompatibility() {
	tests := []struct {
		name  string
		token string
	}{
		{
			name:  "origin hint is ignored",
			token: "tm_enroll_enrollment-id.enrollment-secret.aHR0cHM6Ly9hdHRhY2tlci5leGFtcGxlLw",
		},
		{
			name:  "legacy two segment token is accepted",
			token: "tm_enroll_enrollment-id.enrollment-secret",
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			store := newMemoryCredentialStore()
			tokens := &tokenSequence{values: []string{
				"enrollment-id", "enrollment-secret", "credential-id", "credential-secret",
			}}
			service, err := onprem.NewCredentialService(store, onprem.CredentialConfig{
				AdminAPIKey: "bootstrap-admin", RotationOverlap: 5 * time.Minute,
				SecretPepper: "0123456789abcdef0123456789abcdef", AllowLegacyAgentCreation: true,
				PortalURL: "https://memory.example.internal/",
			}, onprem.WithClock(func() time.Time { return s.now }), onprem.WithTokenSource(tokens.next))
			s.Require().NoError(err)
			ctx := context.Background()
			admin, err := service.Authenticate(ctx, "bootstrap-admin")
			s.Require().NoError(err)
			_, err = service.CreateEnrollment(ctx, admin, onprem.EnrollmentRequest{
				UserID: "owner", AgentID: "agent-1", ExpiresIn: time.Hour,
			})
			s.Require().NoError(err)

			issued, err := service.ExchangeEnrollment(ctx, test.token)

			s.Require().NoError(err)
			s.Equal("tm_key_credential-id.credential-secret", issued.APIKey)
		})
	}
}

func (s *serviceSuite) TestEnrollmentWithoutAuthoritativeOriginUsesLegacyToken() {
	tests := []struct {
		name      string
		portalURL string
	}{
		{name: "relative URL", portalURL: "/"},
		{name: "path", portalURL: "https://memory.example.internal/app"},
		{name: "query", portalURL: "https://memory.example.internal/?tenant=one"},
		{name: "fragment", portalURL: "https://memory.example.internal/#setup"},
		{name: "userinfo", portalURL: "https://operator:secret@memory.example.internal/"},
		{name: "malformed host", portalURL: "https://:443/"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			store := newMemoryCredentialStore()
			tokens := &tokenSequence{values: []string{"enrollment-id", "enrollment-secret"}}
			service, err := onprem.NewCredentialService(store, onprem.CredentialConfig{
				AdminAPIKey: "bootstrap-admin", RotationOverlap: 5 * time.Minute,
				SecretPepper: "0123456789abcdef0123456789abcdef", AllowLegacyAgentCreation: true,
				PortalURL: test.portalURL,
			}, onprem.WithClock(func() time.Time { return s.now }), onprem.WithTokenSource(tokens.next))
			s.Require().NoError(err)
			admin, err := service.Authenticate(context.Background(), "bootstrap-admin")
			s.Require().NoError(err)

			enrollment, err := service.CreateEnrollment(context.Background(), admin, onprem.EnrollmentRequest{
				UserID: "owner", AgentID: "agent-1", ExpiresIn: time.Hour,
			})

			s.Require().NoError(err)
			s.Equal("tm_enroll_enrollment-id.enrollment-secret", enrollment.Token)
		})
	}
}

func (s *serviceSuite) TestAuthorizationAndValidationFailures() {
	ctx := context.Background()
	agent := onprem.Principal{UserID: "owner", AgentID: "agent", ScopeID: onprem.LocalScopeID}
	_, err := s.service.CreateEnrollment(ctx, agent, onprem.EnrollmentRequest{UserID: "owner", AgentID: "agent"})
	s.Require().ErrorIs(err, onprem.ErrForbidden)
	s.Require().ErrorIs(s.service.RevokeCredential(ctx, agent, "credential"), onprem.ErrForbidden)
	_, err = s.service.RotateCredential(ctx, agent)
	s.Require().ErrorIs(err, onprem.ErrUnauthorized)

	admin, err := s.service.Authenticate(ctx, "bootstrap-admin")
	s.Require().NoError(err)
	for _, request := range []onprem.EnrollmentRequest{
		{},
		{UserID: "owner", AgentID: "agent", ExpiresIn: -time.Second},
		{UserID: "owner", AgentID: "agent", Permissions: []onprem.Permission{"unknown"}},
	} {
		_, err = s.service.CreateEnrollment(ctx, admin, request)
		s.Require().Error(err)
	}
	_, err = s.service.Authenticate(ctx, "wrong")
	s.Require().ErrorIs(err, onprem.ErrUnauthorized)
	_, err = s.service.ExchangeEnrollment(ctx, "wrong")
	s.Require().ErrorIs(err, onprem.ErrEnrollmentInvalid)
}

func (s *serviceSuite) TestAuthenticationPreservesStoreFailures() {
	storeFailure := errors.New("credential store unavailable")
	s.store.resolveErr = storeFailure

	_, err := s.service.Authenticate(context.Background(), "tm_key_candidate.secret")

	s.Require().ErrorIs(err, storeFailure)
	s.Require().NotErrorIs(err, onprem.ErrUnauthorized)
}

func (s *serviceSuite) TestAuthenticationAcceptsLegacyKeyWithoutPublicCredentialID() {
	legacyKey := "tm_key_legacy-secret"
	legacyDigest := sha256.Sum256([]byte(legacyKey))
	s.store.credentials[legacyDigest] = onprem.CredentialRecord{
		ID: "legacy-credential", KeyDigest: legacyDigest, UserID: "owner", AgentID: "legacy-agent",
		Permissions: []onprem.Permission{onprem.PermissionSearch}, CreatedAt: s.now,
	}

	principal, err := s.service.Authenticate(context.Background(), legacyKey)

	s.Require().NoError(err)
	s.Equal("legacy-credential", principal.CredentialID)
	s.Equal("legacy-agent", principal.AgentID)
}

type tokenSequence struct {
	mu     sync.Mutex
	values []string
}

func (s *tokenSequence) next() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.values) == 0 {
		return "", errors.New("token sequence exhausted")
	}
	value := s.values[0]
	s.values = s.values[1:]
	return value, nil
}

type memoryCredentialStore struct {
	mu          sync.Mutex
	enrollments map[onprem.Digest]onprem.EnrollmentRecord
	credentials map[onprem.Digest]onprem.CredentialRecord
	byID        map[string]onprem.Digest
	resolveErr  error
}

func (s *memoryCredentialStore) LegacyAdminEnabled(context.Context) (bool, error) {
	return true, nil
}

func newMemoryCredentialStore() *memoryCredentialStore {
	return &memoryCredentialStore{
		enrollments: make(map[onprem.Digest]onprem.EnrollmentRecord),
		credentials: make(map[onprem.Digest]onprem.CredentialRecord),
		byID:        make(map[string]onprem.Digest),
	}
}

func (s *memoryCredentialStore) SaveEnrollment(_ context.Context, record onprem.EnrollmentRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enrollments[record.TokenDigest] = record
	return nil
}

func (s *memoryCredentialStore) ExchangeEnrollment(
	_ context.Context,
	_ string,
	digest onprem.Digest,
	credential onprem.CredentialRecord,
	now time.Time,
) (onprem.EnrollmentRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	enrollment, ok := s.enrollments[digest]
	if !ok || enrollment.ConsumedAt != nil || !enrollment.ExpiresAt.After(now) {
		return onprem.EnrollmentRecord{}, onprem.ErrEnrollmentInvalid
	}
	enrollment.ConsumedAt = &now
	s.enrollments[digest] = enrollment
	credential.UserID = enrollment.UserID
	credential.AgentID = enrollment.AgentID
	credential.Permissions = enrollment.Permissions
	s.credentials[credential.KeyDigest] = credential
	s.byID[credential.ID] = credential.KeyDigest
	return enrollment, nil
}

func (s *memoryCredentialStore) ResolveCredential(
	_ context.Context,
	_ string,
	digest onprem.Digest,
	now time.Time,
) (onprem.CredentialRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resolveErr != nil {
		return onprem.CredentialRecord{}, s.resolveErr
	}
	record, ok := s.credentials[digest]
	if !ok || record.RevokedAt != nil || (record.ExpiresAt != nil && !record.ExpiresAt.After(now)) {
		return onprem.CredentialRecord{}, onprem.ErrUnauthorized
	}
	record.LastUsedAt = &now
	s.credentials[digest] = record
	return record, nil
}

func (s *memoryCredentialStore) RotateCredential(
	_ context.Context,
	currentID string,
	replacement onprem.CredentialRecord,
	overlapUntil time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	digest, ok := s.byID[currentID]
	if !ok {
		return onprem.ErrUnauthorized
	}
	current := s.credentials[digest]
	current.ExpiresAt = &overlapUntil
	s.credentials[digest] = current
	s.credentials[replacement.KeyDigest] = replacement
	s.byID[replacement.ID] = replacement.KeyDigest
	return nil
}

func (s *memoryCredentialStore) RevokeCredential(_ context.Context, credentialID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	digest, ok := s.byID[credentialID]
	if !ok {
		return onprem.ErrCredentialNotFound
	}
	record := s.credentials[digest]
	record.RevokedAt = &now
	s.credentials[digest] = record
	return nil
}
