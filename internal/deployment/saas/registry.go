package saas

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
)

const defaultEnrollmentTTL = 15 * time.Minute

type RegistryConfig struct {
	SecretPepper               string
	MemberGrantablePermissions []onprem.Permission
	PortalURL                  string
}

type registryOptions struct {
	clock       func() time.Time
	idSource    func() (string, error)
	tokenSource func() (string, error)
}

type RegistryOption func(*registryOptions)

func WithRegistryClock(clock func() time.Time) RegistryOption {
	return func(options *registryOptions) { options.clock = clock }
}

func WithRegistryIDSource(source func() (string, error)) RegistryOption {
	return func(options *registryOptions) { options.idSource = source }
}

func WithRegistryTokenSource(source func() (string, error)) RegistryOption {
	return func(options *registryOptions) { options.tokenSource = source }
}

// Registry is the per-team agent registry service. Its method set
// structurally matches the handler-facing AgentRegistryLifecycle; the
// owned-agent surface mirrors the on-prem RegistryService team-scoped via
// principal.ScopeID, while the admin directory and device methods are
// unsupported in the SaaS profile.
type Registry struct {
	agents      TeamRegistryStore
	credentials TeamCredentialStore
	clock       func() time.Time
	idSource    func() (string, error)
	tokenSource func() (string, error)
	digester    secretDigester
	grantable   map[onprem.Permission]struct{}
	portalURL   string
}

func NewRegistry(
	agents TeamRegistryStore,
	credentials TeamCredentialStore,
	config RegistryConfig,
	options ...RegistryOption,
) (*Registry, error) {
	if agents == nil || credentials == nil {
		return nil, fmt.Errorf("create saas agent registry: agent and credential stores are required")
	}
	digester, err := newSecretDigester(config.SecretPepper)
	if err != nil {
		return nil, fmt.Errorf("create saas agent registry: %w", err)
	}
	grantableList, err := validateExplicitPermissions(config.MemberGrantablePermissions)
	if err != nil {
		return nil, fmt.Errorf("create saas agent registry: %w", err)
	}
	grantable := make(map[onprem.Permission]struct{}, len(grantableList))
	for _, permission := range grantableList {
		grantable[permission] = struct{}{}
	}
	configured := registryOptions{clock: time.Now, idSource: randomToken, tokenSource: randomToken}
	for _, option := range options {
		option(&configured)
	}
	if configured.clock == nil || configured.idSource == nil || configured.tokenSource == nil {
		return nil, fmt.Errorf("create saas agent registry: clock and token sources are required")
	}
	return &Registry{
		agents: agents, credentials: credentials, clock: configured.clock,
		idSource: configured.idSource, tokenSource: configured.tokenSource,
		digester: digester, grantable: grantable, portalURL: strings.TrimSpace(config.PortalURL),
	}, nil
}

func (s *Registry) CreateAgent(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	request onprem.CreateAgentRequest,
) (onprem.AgentProfile, error) {
	if err := validateHumanPrincipal(principal); err != nil {
		return onprem.AgentProfile{}, err
	}
	agentID := strings.TrimSpace(request.AgentID)
	displayName := strings.TrimSpace(request.DisplayName)
	if err := validateAgentIdentity(agentID, displayName); err != nil {
		return onprem.AgentProfile{}, err
	}
	now := s.clock().UTC()
	profile, err := s.agents.CreateAgent(ctx, principal.ScopeID, onprem.AgentProfile{
		AgentID: agentID, OwnerMembershipID: principal.MembershipID, OwnerUserID: principal.UserID,
		DisplayName: displayName, Description: strings.TrimSpace(request.Description),
		AgentType: strings.TrimSpace(request.AgentType), Status: onprem.AgentStatusActive,
		DirectoryVisible: request.DirectoryVisible, CreatedAt: now, UpdatedAt: now, ResourceVersion: 1,
		CreationIdempotencyKey: strings.TrimSpace(request.IdempotencyKey),
	})
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("create team agent: %w", err)
	}
	return profile, nil
}

func (s *Registry) ListOwnedAgents(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	filter onprem.AgentFilter,
) ([]onprem.AgentProfile, error) {
	if err := validateHumanPrincipal(principal); err != nil {
		return nil, err
	}
	filter = normalizeAgentFilter(filter)
	profiles, err := s.agents.ListOwnedAgents(ctx, principal.ScopeID, principal.MembershipID, filter)
	if err != nil {
		return nil, fmt.Errorf("list team owned agents: %w", err)
	}
	return profiles, nil
}

func (s *Registry) GetOwnedAgent(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	agentID string,
) (onprem.AgentProfile, error) {
	if err := validateHumanPrincipal(principal); err != nil {
		return onprem.AgentProfile{}, err
	}
	if strings.TrimSpace(agentID) == "" {
		return onprem.AgentProfile{}, onprem.ErrAgentNotFound
	}
	profile, err := s.agents.GetOwnedAgent(ctx, principal.ScopeID, principal.MembershipID, strings.TrimSpace(agentID))
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("get team owned agent: %w", err)
	}
	return profile, nil
}

func (s *Registry) UpdateOwnedAgent(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	agentID string,
	request onprem.UpdateAgentRequest,
) (onprem.AgentProfile, error) {
	profile, err := s.GetOwnedAgent(ctx, principal, agentID)
	if err != nil {
		return onprem.AgentProfile{}, err
	}
	if request.ResourceVersion <= 0 || request.ResourceVersion != profile.ResourceVersion {
		return onprem.AgentProfile{}, onprem.ErrResourceVersionConflict
	}
	if err := applyAgentUpdate(&profile, request, s.clock().UTC()); err != nil {
		return onprem.AgentProfile{}, err
	}
	updated, err := s.agents.UpdateOwnedAgent(ctx, principal.ScopeID, principal.MembershipID, principal, profile)
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("update team owned agent: %w", err)
	}
	return updated, nil
}

func (s *Registry) RetireOwnedAgent(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	agentID string,
	resourceVersion int64,
	idempotencyKey string,
) (onprem.AgentProfile, error) {
	if err := validateHumanPrincipal(principal); err != nil {
		return onprem.AgentProfile{}, err
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return onprem.AgentProfile{}, onprem.ErrAgentNotFound
	}
	if resourceVersion <= 0 {
		return onprem.AgentProfile{}, onprem.ErrResourceVersionConflict
	}
	profile, err := s.agents.RetireOwnedAgent(
		ctx, principal.ScopeID, principal.MembershipID, principal, agentID, resourceVersion,
		strings.TrimSpace(idempotencyKey), s.clock().UTC(),
	)
	if err != nil {
		return onprem.AgentProfile{}, fmt.Errorf("retire team owned agent: %w", err)
	}
	return profile, nil
}

func (s *Registry) CreateEnrollment(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	agentID string,
	request onprem.OwnerEnrollmentRequest,
) (onprem.Enrollment, error) {
	profile, err := s.GetOwnedAgent(ctx, principal, agentID)
	if err != nil {
		return onprem.Enrollment{}, err
	}
	if profile.Status != onprem.AgentStatusActive {
		return onprem.Enrollment{}, onprem.ErrForbidden
	}
	permissions, err := validateExplicitPermissions(request.Permissions)
	if err != nil {
		return onprem.Enrollment{}, err
	}
	for _, permission := range permissions {
		if _, allowed := s.grantable[permission]; !allowed {
			return onprem.Enrollment{}, fmt.Errorf("%w: enrollment permission %q is not grantable", onprem.ErrInvalidIdentityInput, permission)
		}
	}
	expiresIn := request.ExpiresIn
	if expiresIn == 0 {
		expiresIn = defaultEnrollmentTTL
	}
	if expiresIn < 0 {
		return onprem.Enrollment{}, fmt.Errorf("%w: enrollment expiry must be positive", onprem.ErrInvalidIdentityInput)
	}
	id, err := s.idSource()
	if err != nil {
		return onprem.Enrollment{}, fmt.Errorf("create enrollment ID: %w", err)
	}
	secret, err := s.tokenSource()
	if err != nil {
		return onprem.Enrollment{}, fmt.Errorf("create enrollment secret: %w", err)
	}
	now := s.clock().UTC()
	token, verifiableToken := enrollmentToken(id, secret, s.portalURL)
	record := onprem.EnrollmentRecord{
		ID: id, TokenDigest: s.digester.Digest(enrollmentDigestDomain, verifiableToken),
		DigestKeyVersion: currentDigestKeyVersion, UserID: principal.UserID, MembershipID: principal.MembershipID,
		AgentID: profile.AgentID, CredentialLabel: strings.TrimSpace(request.CredentialLabel),
		Permissions: permissions, CreatedAt: now, ExpiresAt: now.Add(expiresIn),
		CredentialExpiresAt: request.CredentialExpiresAt,
	}
	if err := s.credentials.CreateOwnedEnrollment(ctx, principal.ScopeID, principal.MembershipID, record); err != nil {
		return onprem.Enrollment{}, fmt.Errorf("save owned agent enrollment: %w", err)
	}
	return onprem.Enrollment{ID: id, Token: token, ExpiresAt: record.ExpiresAt}, nil
}

func (s *Registry) ListEnrollments(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	agentID string,
	filter onprem.AgentArtifactFilter,
) ([]onprem.AgentEnrollmentMetadata, error) {
	if _, err := s.GetOwnedAgent(ctx, principal, agentID); err != nil {
		return nil, err
	}
	result, err := s.credentials.ListOwnedEnrollments(
		ctx, principal.ScopeID, principal.MembershipID, strings.TrimSpace(agentID),
		normalizeArtifactFilter(filter), s.clock().UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("list team owned enrollments: %w", err)
	}
	return result, nil
}

func (s *Registry) RevokeEnrollment(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	agentID string,
	enrollmentID string,
	idempotencyKey string,
) (onprem.AgentEnrollmentMetadata, error) {
	if _, err := s.GetOwnedAgent(ctx, principal, agentID); err != nil {
		return onprem.AgentEnrollmentMetadata{}, err
	}
	if strings.TrimSpace(enrollmentID) == "" {
		return onprem.AgentEnrollmentMetadata{}, onprem.ErrEnrollmentInvalid
	}
	result, err := s.credentials.RevokeOwnedEnrollment(
		ctx, principal.ScopeID, principal.MembershipID, principal, strings.TrimSpace(agentID),
		strings.TrimSpace(enrollmentID), strings.TrimSpace(idempotencyKey), s.clock().UTC(),
	)
	if err != nil {
		return onprem.AgentEnrollmentMetadata{}, fmt.Errorf("revoke team owned enrollment: %w", err)
	}
	return result, nil
}

func (s *Registry) ListCredentials(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	agentID string,
	filter onprem.AgentArtifactFilter,
) ([]onprem.AgentCredentialMetadata, error) {
	if _, err := s.GetOwnedAgent(ctx, principal, agentID); err != nil {
		return nil, err
	}
	result, err := s.credentials.ListOwnedCredentials(
		ctx, principal.ScopeID, principal.MembershipID, strings.TrimSpace(agentID),
		normalizeArtifactFilter(filter), s.clock().UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("list team owned credentials: %w", err)
	}
	return result, nil
}

func (s *Registry) RevokeOwnedCredential(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	agentID string,
	credentialID string,
	idempotencyKey string,
) (onprem.AgentCredentialMetadata, error) {
	if _, err := s.GetOwnedAgent(ctx, principal, agentID); err != nil {
		return onprem.AgentCredentialMetadata{}, err
	}
	if strings.TrimSpace(credentialID) == "" {
		return onprem.AgentCredentialMetadata{}, onprem.ErrCredentialNotFound
	}
	result, err := s.credentials.RevokeOwnedCredential(
		ctx, principal.ScopeID, principal.MembershipID, principal, strings.TrimSpace(agentID),
		strings.TrimSpace(credentialID), strings.TrimSpace(idempotencyKey), s.clock().UTC(),
	)
	if err != nil {
		return onprem.AgentCredentialMetadata{}, fmt.Errorf("revoke team owned credential: %w", err)
	}
	return result, nil
}

// The admin directory and device methods below have no SaaS meaning: the
// agent directory is an on-prem channel concept, cross-member admin agent
// management is not part of M3, and device registration is an on-prem
// workstation story. They exist so Registry satisfies the handler-facing
// interface set; P3/P4 decides which endpoints the SaaS profile wires.

func (s *Registry) ListDirectoryAgents(context.Context, onprem.Principal, onprem.AgentFilter) ([]onprem.AgentProfile, error) {
	return nil, ErrUnsupportedInSaaS
}

func (s *Registry) GetDirectoryAgent(context.Context, onprem.Principal, string) (onprem.AgentProfile, error) {
	return onprem.AgentProfile{}, ErrUnsupportedInSaaS
}

func (s *Registry) ListAdminAgents(context.Context, onprem.HumanPrincipal, onprem.AgentFilter) ([]onprem.AgentProfile, error) {
	return nil, ErrUnsupportedInSaaS
}

func (s *Registry) GetAdminAgent(context.Context, onprem.HumanPrincipal, string) (onprem.AgentProfile, error) {
	return onprem.AgentProfile{}, ErrUnsupportedInSaaS
}

func (s *Registry) UpdateAdminAgent(context.Context, onprem.HumanPrincipal, string, onprem.UpdateAgentRequest) (onprem.AgentProfile, error) {
	return onprem.AgentProfile{}, ErrUnsupportedInSaaS
}

func (s *Registry) RetireAdminAgent(context.Context, onprem.HumanPrincipal, string, int64, string) (onprem.AgentProfile, error) {
	return onprem.AgentProfile{}, ErrUnsupportedInSaaS
}

func (s *Registry) TransferAgent(context.Context, onprem.HumanPrincipal, string, onprem.TransferAgentRequest) (onprem.AgentProfile, error) {
	return onprem.AgentProfile{}, ErrUnsupportedInSaaS
}

func (s *Registry) ListAdminEnrollments(context.Context, onprem.HumanPrincipal, string, onprem.AgentArtifactFilter) ([]onprem.AgentEnrollmentMetadata, error) {
	return nil, ErrUnsupportedInSaaS
}

func (s *Registry) RevokeAdminEnrollment(context.Context, onprem.HumanPrincipal, string, string, string) (onprem.AgentEnrollmentMetadata, error) {
	return onprem.AgentEnrollmentMetadata{}, ErrUnsupportedInSaaS
}

func (s *Registry) ListAdminCredentials(context.Context, onprem.HumanPrincipal, string, onprem.AgentArtifactFilter) ([]onprem.AgentCredentialMetadata, error) {
	return nil, ErrUnsupportedInSaaS
}

func (s *Registry) RevokeAdminCredential(context.Context, onprem.HumanPrincipal, string, string, string) (onprem.AgentCredentialMetadata, error) {
	return onprem.AgentCredentialMetadata{}, ErrUnsupportedInSaaS
}

func (s *Registry) CreateDeviceEnrollment(context.Context, onprem.HumanPrincipal, onprem.DeviceEnrollmentRequest) (onprem.Enrollment, []onprem.Permission, error) {
	return onprem.Enrollment{}, nil, ErrUnsupportedInSaaS
}

func (s *Registry) RevokeDevice(context.Context, onprem.HumanPrincipal, string, string) (onprem.DeviceSummary, error) {
	return onprem.DeviceSummary{}, ErrUnsupportedInSaaS
}

func (s *Registry) ListDevices(context.Context, onprem.HumanPrincipal, onprem.DeviceFilter) ([]onprem.DeviceSummary, error) {
	return nil, ErrUnsupportedInSaaS
}

func (s *Registry) GetDevice(context.Context, onprem.HumanPrincipal, string) (onprem.DeviceDetail, error) {
	return onprem.DeviceDetail{}, ErrUnsupportedInSaaS
}

// validateAgentIdentity mirrors the on-prem agent ID/display-name rules.
func validateAgentIdentity(agentID, displayName string) error {
	if agentID == "" || displayName == "" {
		return fmt.Errorf("%w: agent_id and display_name are required", onprem.ErrInvalidIdentityInput)
	}
	if len(agentID) > 128 || strings.ContainsAny(agentID, "/\\") {
		return fmt.Errorf("%w: agent_id is invalid", onprem.ErrInvalidIdentityInput)
	}
	for _, current := range agentID {
		if current < 0x20 || current == 0x7f {
			return fmt.Errorf("%w: agent_id is invalid", onprem.ErrInvalidIdentityInput)
		}
	}
	if len(displayName) > 200 {
		return fmt.Errorf("%w: display_name is too long", onprem.ErrInvalidIdentityInput)
	}
	return nil
}

// validateExplicitPermissions mirrors the on-prem enrollment permission
// allow-list and de-duplication.
func validateExplicitPermissions(permissions []onprem.Permission) ([]onprem.Permission, error) {
	if len(permissions) == 0 {
		return nil, fmt.Errorf("%w: enrollment permissions are required", onprem.ErrInvalidIdentityInput)
	}
	seen := make(map[onprem.Permission]struct{}, len(permissions))
	result := make([]onprem.Permission, 0, len(permissions))
	for _, permission := range permissions {
		if permission != onprem.PermissionObserve && permission != onprem.PermissionSearch &&
			permission != onprem.PermissionGet &&
			permission != onprem.PermissionChannelSend && permission != onprem.PermissionChannelReceive {
			return nil, fmt.Errorf("%w: unsupported enrollment permission %q", onprem.ErrInvalidIdentityInput, permission)
		}
		if _, exists := seen[permission]; exists {
			continue
		}
		seen[permission] = struct{}{}
		result = append(result, permission)
	}
	return result, nil
}

// applyAgentUpdate mirrors the on-prem agent update semantics: partial
// field updates, active/suspended transitions only, resource version bump.
func applyAgentUpdate(profile *onprem.AgentProfile, request onprem.UpdateAgentRequest, now time.Time) error {
	if request.DisplayName != nil {
		value := strings.TrimSpace(*request.DisplayName)
		if value == "" || len(value) > 200 {
			return fmt.Errorf("%w: display_name is invalid", onprem.ErrInvalidIdentityInput)
		}
		profile.DisplayName = value
	}
	if request.Description != nil {
		profile.Description = strings.TrimSpace(*request.Description)
	}
	if request.AgentType != nil {
		profile.AgentType = strings.TrimSpace(*request.AgentType)
	}
	if request.DirectoryVisible != nil {
		profile.DirectoryVisible = *request.DirectoryVisible
	}
	if request.Status != nil {
		if *request.Status == onprem.AgentStatusRetired {
			return onprem.ErrInvalidStateTransition
		}
		if *request.Status != onprem.AgentStatusActive && *request.Status != onprem.AgentStatusSuspended {
			return fmt.Errorf("%w: unsupported agent status %q", onprem.ErrInvalidIdentityInput, *request.Status)
		}
		if profile.Status == onprem.AgentStatusRetired {
			return onprem.ErrInvalidStateTransition
		}
		profile.Status = *request.Status
	}
	profile.UpdatedAt = now
	profile.ResourceVersion++
	return nil
}

func normalizeAgentFilter(filter onprem.AgentFilter) onprem.AgentFilter {
	filter.OwnerMembershipID = strings.TrimSpace(filter.OwnerMembershipID)
	filter.Query = strings.TrimSpace(filter.Query)
	filter.Cursor = strings.TrimSpace(filter.Cursor)
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.Limit > 100 {
		filter.Limit = 100
	}
	return filter
}

func normalizeArtifactFilter(filter onprem.AgentArtifactFilter) onprem.AgentArtifactFilter {
	filter.Status = strings.TrimSpace(filter.Status)
	filter.Cursor = strings.TrimSpace(filter.Cursor)
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.Limit > 100 {
		filter.Limit = 100
	}
	return filter
}
