package onprem

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

const defaultEnrollmentTTL = 15 * time.Minute

type CredentialConfig struct {
	AdminAPIKey     string
	RotationOverlap time.Duration
}

type serviceOptions struct {
	clock       func() time.Time
	tokenSource func() (string, error)
}

type ServiceOption func(*serviceOptions)

func WithClock(clock func() time.Time) ServiceOption {
	return func(options *serviceOptions) { options.clock = clock }
}

func WithTokenSource(source func() (string, error)) ServiceOption {
	return func(options *serviceOptions) { options.tokenSource = source }
}

type CredentialService struct {
	store       CredentialStore
	config      CredentialConfig
	clock       func() time.Time
	tokenSource func() (string, error)
}

func NewCredentialService(
	store CredentialStore,
	config CredentialConfig,
	options ...ServiceOption,
) (*CredentialService, error) {
	if store == nil || strings.TrimSpace(config.AdminAPIKey) == "" {
		return nil, fmt.Errorf("create on-prem credential service: store and admin API key are required")
	}
	if config.RotationOverlap <= 0 {
		return nil, fmt.Errorf("create on-prem credential service: rotation overlap must be positive")
	}
	configured := serviceOptions{clock: time.Now, tokenSource: randomToken}
	for _, option := range options {
		option(&configured)
	}
	if configured.clock == nil || configured.tokenSource == nil {
		return nil, fmt.Errorf("create on-prem credential service: clock and token source are required")
	}
	return &CredentialService{
		store: store, config: config, clock: configured.clock, tokenSource: configured.tokenSource,
	}, nil
}

func (s *CredentialService) Authenticate(ctx context.Context, apiKey string) (Principal, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return Principal{}, ErrUnauthorized
	}
	if constantTimeEqual(apiKey, s.config.AdminAPIKey) {
		return Principal{ScopeID: LocalScopeID, Permissions: []Permission{PermissionAdmin}}, nil
	}
	if !strings.HasPrefix(apiKey, "tm_key_") {
		return Principal{}, ErrUnauthorized
	}
	record, err := s.store.ResolveCredential(ctx, digest(apiKey), s.clock().UTC())
	if err != nil {
		if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrCredentialNotFound) {
			return Principal{}, fmt.Errorf("authenticate agent credential: %w", ErrUnauthorized)
		}
		return Principal{}, fmt.Errorf("authenticate agent credential: %w", err)
	}
	return Principal{
		UserID: record.UserID, AgentID: record.AgentID, ScopeID: LocalScopeID,
		CredentialID: record.ID, Permissions: append([]Permission(nil), record.Permissions...),
	}, nil
}

func (s *CredentialService) CreateEnrollment(
	ctx context.Context,
	principal Principal,
	request EnrollmentRequest,
) (Enrollment, error) {
	if !principal.HasPermission(PermissionAdmin) {
		return Enrollment{}, ErrForbidden
	}
	permissions, expiresIn, err := validateEnrollmentRequest(request)
	if err != nil {
		return Enrollment{}, err
	}
	id, err := s.tokenSource()
	if err != nil {
		return Enrollment{}, fmt.Errorf("create enrollment ID: %w", err)
	}
	secret, err := s.tokenSource()
	if err != nil {
		return Enrollment{}, fmt.Errorf("create enrollment secret: %w", err)
	}
	now := s.clock().UTC()
	token := "tm_enroll_" + secret
	record := EnrollmentRecord{
		ID: id, TokenDigest: digest(token), UserID: strings.TrimSpace(request.UserID),
		AgentID: strings.TrimSpace(request.AgentID), Permissions: permissions,
		CreatedAt: now, ExpiresAt: now.Add(expiresIn),
	}
	if err := s.store.SaveEnrollment(ctx, record); err != nil {
		return Enrollment{}, fmt.Errorf("save agent enrollment: %w", err)
	}
	return Enrollment{ID: id, Token: token, ExpiresAt: record.ExpiresAt}, nil
}

func (s *CredentialService) ExchangeEnrollment(ctx context.Context, token string) (IssuedCredential, error) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "tm_enroll_") {
		return IssuedCredential{}, ErrEnrollmentInvalid
	}
	id, apiKey, record, err := s.newCredential(CredentialRecord{})
	if err != nil {
		return IssuedCredential{}, err
	}
	_, err = s.store.ExchangeEnrollment(ctx, digest(token), record, s.clock().UTC())
	if err != nil {
		return IssuedCredential{}, fmt.Errorf("exchange agent enrollment: %w", err)
	}
	return IssuedCredential{CredentialID: id, APIKey: apiKey}, nil
}

func (s *CredentialService) RotateCredential(ctx context.Context, principal Principal) (IssuedCredential, error) {
	if principal.CredentialID == "" || principal.ScopeID != LocalScopeID {
		return IssuedCredential{}, ErrUnauthorized
	}
	id, apiKey, replacement, err := s.newCredential(CredentialRecord{
		UserID: principal.UserID, AgentID: principal.AgentID,
		Permissions: append([]Permission(nil), principal.Permissions...),
	})
	if err != nil {
		return IssuedCredential{}, err
	}
	overlapUntil := s.clock().UTC().Add(s.config.RotationOverlap)
	if err := s.store.RotateCredential(ctx, principal.CredentialID, replacement, overlapUntil); err != nil {
		return IssuedCredential{}, fmt.Errorf("rotate agent credential: %w", err)
	}
	return IssuedCredential{CredentialID: id, APIKey: apiKey}, nil
}

func (s *CredentialService) RevokeCredential(ctx context.Context, principal Principal, credentialID string) error {
	if !principal.HasPermission(PermissionAdmin) {
		return ErrForbidden
	}
	if strings.TrimSpace(credentialID) == "" {
		return fmt.Errorf("revoke agent credential: credential ID is required")
	}
	if err := s.store.RevokeCredential(ctx, credentialID, s.clock().UTC()); err != nil {
		return fmt.Errorf("revoke agent credential: %w", err)
	}
	return nil
}

func (s *CredentialService) newCredential(base CredentialRecord) (string, string, CredentialRecord, error) {
	id, err := s.tokenSource()
	if err != nil {
		return "", "", CredentialRecord{}, fmt.Errorf("create credential ID: %w", err)
	}
	secret, err := s.tokenSource()
	if err != nil {
		return "", "", CredentialRecord{}, fmt.Errorf("create credential secret: %w", err)
	}
	apiKey := "tm_key_" + secret
	base.ID = id
	base.KeyDigest = digest(apiKey)
	base.CreatedAt = s.clock().UTC()
	return id, apiKey, base, nil
}

func validateEnrollmentRequest(request EnrollmentRequest) ([]Permission, time.Duration, error) {
	if strings.TrimSpace(request.UserID) == "" || strings.TrimSpace(request.AgentID) == "" {
		return nil, 0, fmt.Errorf("create agent enrollment: user_id and agent_id are required")
	}
	expiresIn := request.ExpiresIn
	if expiresIn == 0 {
		expiresIn = defaultEnrollmentTTL
	}
	if expiresIn < 0 {
		return nil, 0, fmt.Errorf("create agent enrollment: expiry must be positive")
	}
	permissions := request.Permissions
	if len(permissions) == 0 {
		permissions = []Permission{PermissionObserve, PermissionSearch, PermissionGet}
	}
	seen := make(map[Permission]struct{}, len(permissions))
	result := make([]Permission, 0, len(permissions))
	for _, permission := range permissions {
		if permission != PermissionObserve && permission != PermissionSearch && permission != PermissionGet {
			return nil, 0, fmt.Errorf("create agent enrollment: unsupported permission %q", permission)
		}
		if _, exists := seen[permission]; exists {
			continue
		}
		seen[permission] = struct{}{}
		result = append(result, permission)
	}
	return result, expiresIn, nil
}

func digest(value string) Digest {
	return sha256.Sum256([]byte(value))
}

func constantTimeEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func randomToken() (string, error) {
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("read random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
