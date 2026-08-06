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
	agents        TeamRegistryStore
	credentials   TeamCredentialStore
	clock         func() time.Time
	idSource      func() (string, error)
	tokenSource   func() (string, error)
	digester      secretDigester
	grantable     map[onprem.Permission]struct{}
	grantableList []onprem.Permission
	portalURL     string
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
		digester: digester, grantable: grantable, grantableList: grantableList,
		portalURL: strings.TrimSpace(config.PortalURL),
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

// The admin directory methods below have no SaaS meaning: the agent
// directory is an on-prem channel concept, and cross-member admin agent
// management is not part of the team model. They exist so Registry
// satisfies the handler-facing interface set; the device methods above are
// fully implemented (team-scoped workstation enrollment).

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

// CreateDeviceEnrollment creates a one-time device enrollment token in the
// principal's team, mirroring the on-prem service. Its second return value
// is the effective grantable-permissions set actually persisted on the
// enrollment record: the caller-supplied set when GrantablePermissions is
// non-empty, or the registry's configured default otherwise.
func (s *Registry) CreateDeviceEnrollment(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	request onprem.DeviceEnrollmentRequest,
) (onprem.Enrollment, []onprem.Permission, error) {
	if err := authorizeTeamAdmin(principal); err != nil {
		return onprem.Enrollment{}, nil, err
	}
	deviceName := strings.TrimSpace(request.DeviceName)
	if err := validateDeviceName(deviceName); err != nil {
		return onprem.Enrollment{}, nil, err
	}
	grantable := request.GrantablePermissions
	if len(grantable) == 0 {
		grantable = append([]onprem.Permission(nil), s.grantableList...)
	} else {
		validated, err := validateExplicitPermissions(grantable)
		if err != nil {
			return onprem.Enrollment{}, nil, err
		}
		for _, permission := range validated {
			if _, allowed := s.grantable[permission]; !allowed {
				return onprem.Enrollment{}, nil, fmt.Errorf("%w: enrollment permission %q is not grantable", onprem.ErrInvalidIdentityInput, permission)
			}
		}
		grantable = validated
	}
	expiresIn := request.ExpiresIn
	if expiresIn == 0 {
		expiresIn = defaultEnrollmentTTL
	}
	if expiresIn < 0 {
		return onprem.Enrollment{}, nil, fmt.Errorf("%w: enrollment expiry must be positive", onprem.ErrInvalidIdentityInput)
	}
	id, err := s.idSource()
	if err != nil {
		return onprem.Enrollment{}, nil, fmt.Errorf("create device enrollment ID: %w", err)
	}
	secret, err := s.tokenSource()
	if err != nil {
		return onprem.Enrollment{}, nil, fmt.Errorf("create device enrollment secret: %w", err)
	}
	now := s.clock().UTC()
	token, verifiableToken := enrollmentToken(id, secret, s.portalURL)
	record := onprem.EnrollmentRecord{
		ID: id, TokenDigest: s.digester.Digest(enrollmentDigestDomain, verifiableToken),
		DigestKeyVersion: currentDigestKeyVersion, UserID: principal.UserID, MembershipID: principal.MembershipID,
		Kind: onprem.CredentialKindDevice, AgentID: "", CredentialLabel: deviceName,
		Permissions: []onprem.Permission{onprem.PermissionAgentProvision}, GrantablePermissions: grantable,
		CreatedAt: now, ExpiresAt: now.Add(expiresIn),
	}
	if err := s.credentials.CreateDeviceEnrollment(ctx, principal.ScopeID, principal.MembershipID, record); err != nil {
		return onprem.Enrollment{}, nil, fmt.Errorf("save device enrollment: %w", err)
	}
	return onprem.Enrollment{ID: id, Token: token, ExpiresAt: record.ExpiresAt}, grantable, nil
}

// RevokeDevice revokes a device credential in the principal's team and,
// transactionally with the revoke, cascades to every agent credential the
// device provisioned. Only an Owner or Admin may revoke a device.
func (s *Registry) RevokeDevice(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	credentialID string,
	idempotencyKey string,
) (onprem.DeviceSummary, error) {
	if err := authorizeTeamAdmin(principal); err != nil {
		return onprem.DeviceSummary{}, err
	}
	credentialID = strings.TrimSpace(credentialID)
	if credentialID == "" {
		return onprem.DeviceSummary{}, onprem.ErrCredentialNotFound
	}
	summary, err := s.credentials.RevokeDevice(
		ctx, principal.ScopeID, principal, credentialID, strings.TrimSpace(idempotencyKey), s.clock().UTC(),
	)
	if err != nil {
		return onprem.DeviceSummary{}, fmt.Errorf("revoke team device: %w", err)
	}
	return summary, nil
}

// ListDevices returns the device-kind credentials of the principal's team,
// newest first. Only an Owner or Admin may list devices.
func (s *Registry) ListDevices(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	filter onprem.DeviceFilter,
) ([]onprem.DeviceSummary, error) {
	if err := authorizeTeamAdmin(principal); err != nil {
		return nil, err
	}
	devices, err := s.credentials.ListDevices(ctx, principal.ScopeID, normalizeDeviceFilter(filter))
	if err != nil {
		return nil, fmt.Errorf("list team devices: %w", err)
	}
	return devices, nil
}

// GetDevice returns a device credential's summary plus every credential row
// it has provisioned (including revoked history) inside the principal's
// team. Only an Owner or Admin may view a device's detail.
func (s *Registry) GetDevice(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	credentialID string,
) (onprem.DeviceDetail, error) {
	if err := authorizeTeamAdmin(principal); err != nil {
		return onprem.DeviceDetail{}, err
	}
	credentialID = strings.TrimSpace(credentialID)
	if credentialID == "" {
		return onprem.DeviceDetail{}, onprem.ErrCredentialNotFound
	}
	detail, err := s.credentials.GetDevice(ctx, principal.ScopeID, credentialID)
	if err != nil {
		return onprem.DeviceDetail{}, fmt.Errorf("get team device: %w", err)
	}
	return detail, nil
}

// ListExpiringEnrollments returns pending enrollments across the whole
// principal's team whose one-time token expires before `before`, soonest
// first. Unlike ListEnrollments this is not owner-scoped, so it is a new
// read surface over an otherwise owner-private artifact — authorization
// matches the device listing (owner/admin only), not the per-agent
// enrollment listing. Only an Owner or Admin may call it.
func (s *Registry) ListExpiringEnrollments(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	before time.Time,
	limit int,
) ([]onprem.AgentEnrollmentMetadata, error) {
	if err := authorizeTeamAdmin(principal); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	result, err := s.credentials.ListExpiringEnrollments(ctx, principal.ScopeID, before, s.clock().UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("list team expiring enrollments: %w", err)
	}
	return result, nil
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

// validateDeviceName mirrors the on-prem device name rules.
func validateDeviceName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: device_name is required", onprem.ErrInvalidIdentityInput)
	}
	if len(name) > 200 {
		return fmt.Errorf("%w: device_name is too long", onprem.ErrInvalidIdentityInput)
	}
	for _, current := range name {
		if current < 0x20 || current == 0x7f {
			return fmt.Errorf("%w: device_name is invalid", onprem.ErrInvalidIdentityInput)
		}
	}
	return nil
}

// normalizeDeviceFilter mirrors the on-prem device listing normalization.
func normalizeDeviceFilter(filter onprem.DeviceFilter) onprem.DeviceFilter {
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
