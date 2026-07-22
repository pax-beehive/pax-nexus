package onprem

import (
	"context"
	"crypto/hmac"
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

const currentDigestKeyVersion int16 = 1

const (
	credentialDigestDomain = "agent-credential"
	enrollmentDigestDomain = "agent-enrollment"
	sessionDigestDomain    = "human-session"
	invitationDigestDomain = "membership-invitation"
)

type CredentialConfig struct {
	AdminAPIKey              string
	RotationOverlap          time.Duration
	SecretPepper             string
	AllowLegacyAgentCreation bool
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
	digester    secretDigester
}

func NewCredentialService(
	store CredentialStore,
	config CredentialConfig,
	options ...ServiceOption,
) (*CredentialService, error) {
	if store == nil {
		return nil, fmt.Errorf("create on-prem credential service: store is required")
	}
	if config.RotationOverlap <= 0 {
		return nil, fmt.Errorf("create on-prem credential service: rotation overlap must be positive")
	}
	digester, err := newSecretDigester(config.SecretPepper)
	if err != nil {
		return nil, fmt.Errorf("create on-prem credential service: %w", err)
	}
	configured := serviceOptions{clock: time.Now, tokenSource: randomToken}
	for _, option := range options {
		option(&configured)
	}
	if configured.clock == nil || configured.tokenSource == nil {
		return nil, fmt.Errorf("create on-prem credential service: clock and token source are required")
	}
	return &CredentialService{
		store: store, config: config, clock: configured.clock, tokenSource: configured.tokenSource, digester: digester,
	}, nil
}

func (s *CredentialService) Authenticate(ctx context.Context, apiKey string) (Principal, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return Principal{}, ErrUnauthorized
	}
	if s.config.AdminAPIKey != "" && constantTimeEqual(apiKey, s.config.AdminAPIKey) {
		enabled, err := s.store.LegacyAdminEnabled(ctx)
		if err != nil {
			return Principal{}, fmt.Errorf("check legacy admin credential: %w", err)
		}
		if !enabled {
			return Principal{}, ErrUnauthorized
		}
		return Principal{ScopeID: LocalScopeID, Permissions: []Permission{PermissionAdmin}}, nil
	}
	if !strings.HasPrefix(apiKey, "tm_key_") {
		return Principal{}, ErrUnauthorized
	}
	credentialID, ok := secretPublicID(apiKey, "tm_key_")
	var record CredentialRecord
	var err error
	if ok {
		record, err = s.store.ResolveCredential(
			ctx, credentialID, s.digester.Digest(credentialDigestDomain, apiKey), s.clock().UTC(),
		)
	}
	if !ok || errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrCredentialNotFound) {
		record, err = s.store.ResolveCredential(ctx, credentialID, digest(apiKey), s.clock().UTC())
	}
	if err != nil {
		if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrCredentialNotFound) {
			return Principal{}, fmt.Errorf("authenticate agent credential: %w", ErrUnauthorized)
		}
		return Principal{}, fmt.Errorf("authenticate agent credential: %w", err)
	}
	return Principal{
		UserID: record.UserID, MembershipID: record.MembershipID, AgentID: record.AgentID, ScopeID: LocalScopeID,
		CredentialID: record.ID, CredentialLabel: record.Label,
		Permissions: append([]Permission(nil), record.Permissions...),
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
	token := "tm_enroll_" + id + "." + secret
	record := EnrollmentRecord{
		ID: id, TokenDigest: s.digester.Digest(enrollmentDigestDomain, token),
		DigestKeyVersion: currentDigestKeyVersion, UserID: strings.TrimSpace(request.UserID),
		AgentID: strings.TrimSpace(request.AgentID), Permissions: permissions,
		CreatedAt: now, ExpiresAt: now.Add(expiresIn),
		AllowLegacyAgentCreation: s.config.AllowLegacyAgentCreation,
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
	enrollmentID, _ := secretPublicID(token, "tm_enroll_")
	id, apiKey, record, err := s.newCredential(CredentialRecord{})
	if err != nil {
		return IssuedCredential{}, err
	}
	exchanged, err := s.store.ExchangeEnrollment(
		ctx, enrollmentID, s.digester.Digest(enrollmentDigestDomain, token), record, s.clock().UTC(),
	)
	if errors.Is(err, ErrEnrollmentInvalid) {
		exchanged, err = s.store.ExchangeEnrollment(ctx, enrollmentID, digest(token), record, s.clock().UTC())
	}
	if err != nil {
		return IssuedCredential{}, fmt.Errorf("exchange agent enrollment: %w", err)
	}
	return IssuedCredential{CredentialID: id, APIKey: apiKey, ExpiresAt: exchanged.CredentialExpiresAt}, nil
}

func (s *CredentialService) RotateCredential(ctx context.Context, principal Principal) (IssuedCredential, error) {
	if principal.CredentialID == "" || principal.ScopeID != LocalScopeID {
		return IssuedCredential{}, ErrUnauthorized
	}
	id, apiKey, replacement, err := s.newCredential(CredentialRecord{
		UserID: principal.UserID, MembershipID: principal.MembershipID, AgentID: principal.AgentID,
		Label: principal.CredentialLabel, Permissions: append([]Permission(nil), principal.Permissions...),
		RotatedFromCredentialID: principal.CredentialID,
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
	apiKey := "tm_key_" + id + "." + secret
	base.ID = id
	base.KeyDigest = s.digester.Digest(credentialDigestDomain, apiKey)
	base.DigestKeyVersion = currentDigestKeyVersion
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
		permissions = []Permission{
			PermissionObserve,
			PermissionSearch,
			PermissionGet,
		}
	}
	seen := make(map[Permission]struct{}, len(permissions))
	result := make([]Permission, 0, len(permissions))
	for _, permission := range permissions {
		if permission != PermissionObserve && permission != PermissionSearch && permission != PermissionGet &&
			permission != PermissionChannelSend && permission != PermissionChannelReceive {
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

func secretPublicID(value string, prefix string) (string, bool) {
	remainder, found := strings.CutPrefix(value, prefix)
	if !found {
		return "", false
	}
	publicID, secret, found := strings.Cut(remainder, ".")
	if !found || strings.TrimSpace(publicID) == "" || strings.TrimSpace(secret) == "" {
		return "", false
	}
	return publicID, true
}

type secretDigester struct {
	key []byte
}

func newSecretDigester(secret string) (secretDigester, error) {
	secret = strings.TrimSpace(secret)
	if len(secret) < 32 {
		return secretDigester{}, fmt.Errorf("secret pepper must contain at least 32 characters")
	}
	return secretDigester{key: []byte(secret)}, nil
}

func (d secretDigester) Digest(domain string, value string) Digest {
	mac := hmac.New(sha256.New, d.key)
	payload := []byte(domain + "\x00" + value)
	written, err := mac.Write(payload)
	if err != nil || written != len(payload) {
		panic("HMAC digest write failed")
	}
	var result Digest
	copy(result[:], mac.Sum(nil))
	return result
}

func constantTimeEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("read random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
