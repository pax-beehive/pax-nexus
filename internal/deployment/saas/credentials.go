package saas

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
)

type CredentialConfig struct {
	RotationOverlap time.Duration
	SecretPepper    string
	// DeviceAgentLimit caps how many distinct agents a single device
	// credential may keep actively provisioned at once. Values <= 0 default
	// to 16 in NewCredentials, mirroring the on-prem default.
	DeviceAgentLimit int
}

type credentialOptions struct {
	clock       func() time.Time
	tokenSource func() (string, error)
}

type CredentialOption func(*credentialOptions)

func WithCredentialClock(clock func() time.Time) CredentialOption {
	return func(options *credentialOptions) { options.clock = clock }
}

func WithCredentialTokenSource(source func() (string, error)) CredentialOption {
	return func(options *credentialOptions) { options.tokenSource = source }
}

// Credentials is the team agent credential service. Its method set
// structurally matches the handler-facing CredentialLifecycle: Authenticate
// resolves the principal's scope from the credential row's team instead of
// stamping the on-prem constant, enrollment exchange issues credentials for
// owner-created enrollments, and the legacy-admin and device surfaces are
// unsupported in the SaaS profile.
type Credentials struct {
	store       TeamCredentialStore
	config      CredentialConfig
	clock       func() time.Time
	tokenSource func() (string, error)
	digester    secretDigester
}

func NewCredentials(
	store TeamCredentialStore,
	config CredentialConfig,
	options ...CredentialOption,
) (*Credentials, error) {
	if store == nil {
		return nil, fmt.Errorf("create saas credential service: store is required")
	}
	if config.RotationOverlap <= 0 {
		return nil, fmt.Errorf("create saas credential service: rotation overlap must be positive")
	}
	digester, err := newSecretDigester(config.SecretPepper)
	if err != nil {
		return nil, fmt.Errorf("create saas credential service: %w", err)
	}
	configured := credentialOptions{clock: time.Now, tokenSource: randomToken}
	for _, option := range options {
		option(&configured)
	}
	if configured.clock == nil || configured.tokenSource == nil {
		return nil, fmt.Errorf("create saas credential service: clock and token source are required")
	}
	if config.DeviceAgentLimit <= 0 {
		config.DeviceAgentLimit = 16
	}
	return &Credentials{
		store: store, config: config, clock: configured.clock, tokenSource: configured.tokenSource,
		digester: digester,
	}, nil
}

// Authenticate resolves an agent API key to a principal scoped to the
// credential's team. There is no legacy admin key in the SaaS profile.
func (s *Credentials) Authenticate(ctx context.Context, apiKey string) (onprem.Principal, error) {
	apiKey = strings.TrimSpace(apiKey)
	if !strings.HasPrefix(apiKey, "tm_key_") {
		return onprem.Principal{}, onprem.ErrUnauthorized
	}
	credentialID, hasPublicID := secretPublicID(apiKey, "tm_key_")
	var scoped ScopedCredential
	var err error
	if hasPublicID {
		scoped, err = s.store.ResolveCredential(
			ctx, credentialID, s.digester.Digest(credentialDigestDomain, apiKey), s.clock().UTC(),
		)
	}
	if !hasPublicID || errors.Is(err, onprem.ErrUnauthorized) || errors.Is(err, onprem.ErrCredentialNotFound) {
		// Legacy fallback: keys issued before the peppered digest scheme
		// are stored under the plain SHA-256 digest.
		scoped, err = s.store.ResolveCredential(ctx, credentialID, digest(apiKey), s.clock().UTC())
	}
	if err != nil {
		if errors.Is(err, onprem.ErrUnauthorized) || errors.Is(err, onprem.ErrCredentialNotFound) {
			return onprem.Principal{}, fmt.Errorf("authenticate agent credential: %w", onprem.ErrUnauthorized)
		}
		return onprem.Principal{}, fmt.Errorf("authenticate agent credential: %w", err)
	}
	record := scoped.CredentialRecord
	return onprem.Principal{
		UserID: record.UserID, MembershipID: record.MembershipID, AgentID: record.AgentID,
		ScopeID: scoped.TeamID, CredentialID: record.ID, CredentialLabel: record.Label,
		Permissions:          append([]onprem.Permission(nil), record.Permissions...),
		Kind:                 record.Kind,
		GrantablePermissions: append([]onprem.Permission(nil), record.GrantablePermissions...),
	}, nil
}

// CreateEnrollment is the legacy admin enrollment surface; SaaS agent
// enrollments are owner-scoped through Registry.CreateEnrollment.
func (s *Credentials) CreateEnrollment(
	context.Context,
	onprem.Principal,
	onprem.EnrollmentRequest,
) (onprem.Enrollment, error) {
	return onprem.Enrollment{}, ErrUnsupportedInSaaS
}

func (s *Credentials) ExchangeEnrollment(ctx context.Context, token string) (onprem.IssuedCredential, error) {
	verifiableToken, enrollmentID, ok := parseEnrollmentToken(token)
	if !ok {
		return onprem.IssuedCredential{}, onprem.ErrEnrollmentInvalid
	}
	id, apiKey, record, err := s.newCredential(onprem.CredentialRecord{})
	if err != nil {
		return onprem.IssuedCredential{}, err
	}
	exchanged, err := s.store.ExchangeEnrollment(
		ctx, enrollmentID, s.digester.Digest(enrollmentDigestDomain, verifiableToken), record, s.clock().UTC(),
	)
	if errors.Is(err, onprem.ErrEnrollmentInvalid) {
		exchanged, err = s.store.ExchangeEnrollment(
			ctx, enrollmentID, digest(verifiableToken), record, s.clock().UTC(),
		)
	}
	if err != nil {
		return onprem.IssuedCredential{}, fmt.Errorf("exchange agent enrollment: %w", err)
	}
	return onprem.IssuedCredential{
		CredentialID: id, APIKey: apiKey, UserID: exchanged.UserID,
		Permissions: append([]onprem.Permission(nil), exchanged.Permissions...),
		Kind:        exchanged.Kind,
		ExpiresAt:   exchanged.CredentialExpiresAt,
	}, nil
}

// RotateCredential replaces the caller's credential with a fresh key inside
// the caller's team. The on-prem scope gate (ScopeID != LocalScopeID) is
// replicated team-scoped: a principal with no team scope is unauthorized,
// and the store only rotates a credential that lives in principal.ScopeID,
// so a key from another team cannot be rotated through this path.
func (s *Credentials) RotateCredential(ctx context.Context, principal onprem.Principal) (onprem.IssuedCredential, error) {
	if principal.CredentialID == "" || strings.TrimSpace(principal.ScopeID) == "" {
		return onprem.IssuedCredential{}, onprem.ErrUnauthorized
	}
	if principal.Kind == onprem.CredentialKindDevice {
		return onprem.IssuedCredential{}, onprem.ErrForbidden
	}
	id, apiKey, replacement, err := s.newCredential(onprem.CredentialRecord{
		UserID: principal.UserID, MembershipID: principal.MembershipID, AgentID: principal.AgentID,
		Label: principal.CredentialLabel, Permissions: append([]onprem.Permission(nil), principal.Permissions...),
		RotatedFromCredentialID: principal.CredentialID,
	})
	if err != nil {
		return onprem.IssuedCredential{}, err
	}
	overlapUntil := s.clock().UTC().Add(s.config.RotationOverlap)
	if err := s.store.RotateCredential(ctx, principal.ScopeID, principal.CredentialID, replacement, overlapUntil); err != nil {
		return onprem.IssuedCredential{}, fmt.Errorf("rotate agent credential: %w", err)
	}
	return onprem.IssuedCredential{
		CredentialID: id, APIKey: apiKey, UserID: principal.UserID,
		Permissions: append([]onprem.Permission(nil), principal.Permissions...),
		Kind:        onprem.CredentialKindAgent,
	}, nil
}

// RevokeCredential is the legacy admin revocation surface; SaaS credential
// revocation is owner-scoped through Registry.RevokeOwnedCredential.
func (s *Credentials) RevokeCredential(context.Context, onprem.Principal, string) error {
	return ErrUnsupportedInSaaS
}

// ProvisionDeviceAgent creates or rotates the credential for an agent that
// a device credential provisions. Only a device credential carrying
// PermissionAgentProvision may call this; an agent credential is always
// forbidden, even if it somehow carries the permission. The team scope
// comes from the device credential's own row (principal.ScopeID), so a
// device can only ever provision inside its own team.
func (s *Credentials) ProvisionDeviceAgent(
	ctx context.Context,
	principal onprem.Principal,
	request onprem.DeviceProvisionRequest,
) (onprem.ProvisionedAgentCredential, error) {
	if err := requireDeviceProvisioner(principal); err != nil {
		return onprem.ProvisionedAgentCredential{}, err
	}
	agentID := strings.TrimSpace(request.AgentID)
	displayName := strings.TrimSpace(request.DisplayName)
	if err := validateAgentIdentity(agentID, displayName); err != nil {
		return onprem.ProvisionedAgentCredential{}, err
	}
	agentType := strings.TrimSpace(request.AgentType)
	if agentType == "" {
		return onprem.ProvisionedAgentCredential{}, fmt.Errorf("%w: agent_type is required", onprem.ErrInvalidIdentityInput)
	}
	permissions, err := deviceGrantedPermissions(request.Permissions, principal.GrantablePermissions)
	if err != nil {
		return onprem.ProvisionedAgentCredential{}, err
	}
	id, apiKey, record, err := s.newCredential(onprem.CredentialRecord{
		UserID: principal.UserID, MembershipID: principal.MembershipID, AgentID: agentID,
		Label: displayName, Permissions: permissions,
		Kind: onprem.CredentialKindAgent, ProvisionedBy: principal.CredentialID,
	})
	if err != nil {
		return onprem.ProvisionedAgentCredential{}, err
	}
	now := record.CreatedAt
	profile := onprem.AgentProfile{
		AgentID: agentID, OwnerMembershipID: principal.MembershipID, OwnerUserID: principal.UserID,
		DisplayName: displayName, AgentType: agentType, Status: onprem.AgentStatusActive,
		DirectoryVisible: true, CreatedAt: now, UpdatedAt: now, ResourceVersion: 1,
		ProvisionedBy: principal.CredentialID,
	}
	outcome, err := s.store.ProvisionAgentCredential(
		ctx, principal.ScopeID, principal.CredentialID, profile, record, s.config.DeviceAgentLimit, now,
	)
	if err != nil {
		return onprem.ProvisionedAgentCredential{}, fmt.Errorf("provision device agent: %w", err)
	}
	return onprem.ProvisionedAgentCredential{
		CredentialID: id, APIKey: apiKey, AgentID: agentID, Permissions: permissions,
		CreatedAt: now, RotatedFromCredentialID: outcome.RotatedFromCredentialID,
		AgentCreated: outcome.AgentCreated,
	}, nil
}

// ListDeviceProvisionedAgents returns every credential row (including
// revoked history) the device credential in principal has provisioned in
// its team. Subject to the same guard as ProvisionDeviceAgent.
func (s *Credentials) ListDeviceProvisionedAgents(
	ctx context.Context,
	principal onprem.Principal,
) ([]onprem.DeviceProvisionedAgent, error) {
	if err := requireDeviceProvisioner(principal); err != nil {
		return nil, err
	}
	agents, err := s.store.ListDeviceProvisionedAgents(ctx, principal.ScopeID, principal.CredentialID)
	if err != nil {
		return nil, fmt.Errorf("list device provisioned agents: %w", err)
	}
	return agents, nil
}

// requireDeviceProvisioner is the guard shared by the device-scoped
// provisioning endpoints, mirroring the on-prem guard with the deployment
// pin replaced by a team scope: only a device credential carrying
// PermissionAgentProvision, scoped to a team, may proceed.
func requireDeviceProvisioner(principal onprem.Principal) error {
	if strings.TrimSpace(principal.ScopeID) == "" || principal.CredentialID == "" ||
		principal.Kind != onprem.CredentialKindDevice || !principal.HasPermission(onprem.PermissionAgentProvision) {
		return onprem.ErrForbidden
	}
	return nil
}

// deviceGrantedPermissions mirrors the on-prem narrowing: an empty request
// inherits the device's grantable set, an explicit request must stay inside
// it.
func deviceGrantedPermissions(requested, grantable []onprem.Permission) ([]onprem.Permission, error) {
	if len(requested) == 0 {
		if len(grantable) == 0 {
			return nil, fmt.Errorf("%w: device has no grantable permissions", onprem.ErrForbidden)
		}
		return append([]onprem.Permission(nil), grantable...), nil
	}
	validated, err := validateExplicitPermissions(requested)
	if err != nil {
		return nil, err
	}
	allowed := make(map[onprem.Permission]struct{}, len(grantable))
	for _, permission := range grantable {
		allowed[permission] = struct{}{}
	}
	for _, permission := range validated {
		if _, ok := allowed[permission]; !ok {
			return nil, fmt.Errorf("%w: permission %q exceeds device grantable set", onprem.ErrForbidden, permission)
		}
	}
	return validated, nil
}

func (s *Credentials) newCredential(base onprem.CredentialRecord) (string, string, onprem.CredentialRecord, error) {
	id, err := s.tokenSource()
	if err != nil {
		return "", "", onprem.CredentialRecord{}, fmt.Errorf("create credential ID: %w", err)
	}
	secret, err := s.tokenSource()
	if err != nil {
		return "", "", onprem.CredentialRecord{}, fmt.Errorf("create credential secret: %w", err)
	}
	apiKey := "tm_key_" + id + "." + secret
	base.ID = id
	base.KeyDigest = s.digester.Digest(credentialDigestDomain, apiKey)
	base.DigestKeyVersion = currentDigestKeyVersion
	base.CreatedAt = s.clock().UTC()
	return id, apiKey, base, nil
}
