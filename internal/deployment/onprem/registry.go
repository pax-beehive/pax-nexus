package onprem

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

type HumanPrincipal struct {
	UserID           string
	MembershipID     string
	Role             Role
	MembershipStatus MembershipStatus
	Email            string
	EmailVerified    bool
	SessionID        string
}

type AgentStatus string

const (
	AgentStatusActive    AgentStatus = "active"
	AgentStatusSuspended AgentStatus = "suspended"
	AgentStatusRetired   AgentStatus = "retired"
)

type AgentProfile struct {
	AgentID                string
	OwnerMembershipID      string
	OwnerUserID            string
	DisplayName            string
	Description            string
	AgentType              string
	Status                 AgentStatus
	DirectoryVisible       bool
	CreatedAt              time.Time
	UpdatedAt              time.Time
	RetiredAt              *time.Time
	ResourceVersion        int64
	CreationIdempotencyKey string
}

type CreateAgentRequest struct {
	AgentID          string
	DisplayName      string
	Description      string
	AgentType        string
	DirectoryVisible bool
	IdempotencyKey   string
}

type UpdateAgentRequest struct {
	DisplayName      *string
	Description      *string
	AgentType        *string
	DirectoryVisible *bool
	Status           *AgentStatus
	ResourceVersion  int64
}

type TransferAgentRequest struct {
	TargetMembershipID string
	ResourceVersion    int64
}

type AgentFilter struct {
	OwnerMembershipID string
	Status            AgentStatus
	Query             string
	Limit             int
	Cursor            string
}

type OwnerEnrollmentRequest struct {
	CredentialLabel     string
	Permissions         []Permission
	ExpiresIn           time.Duration
	CredentialExpiresAt *time.Time
}

type AgentEnrollmentMetadata struct {
	EnrollmentID        string
	AgentID             string
	CredentialLabel     string
	Permissions         []Permission
	Status              string
	CreatedAt           time.Time
	ExpiresAt           time.Time
	CredentialExpiresAt *time.Time
}

type AgentCredentialMetadata struct {
	CredentialID string
	AgentID      string
	Label        string
	Permissions  []Permission
	CreatedAt    time.Time
	ExpiresAt    *time.Time
	RevokedAt    *time.Time
	LastUsedAt   *time.Time
}

type AgentArtifactFilter struct {
	Status string
	Limit  int
	Cursor string
}

type RegistryConfig struct {
	SecretPepper               string
	MemberGrantablePermissions []Permission
}

type RegistryStore interface {
	CreateAgent(context.Context, AgentProfile) (AgentProfile, error)
	ListOwnedAgents(context.Context, string, AgentFilter) ([]AgentProfile, error)
	GetOwnedAgent(context.Context, string, string) (AgentProfile, error)
	UpdateOwnedAgent(context.Context, string, HumanPrincipal, AgentProfile) (AgentProfile, error)
	RetireOwnedAgent(context.Context, string, HumanPrincipal, string, int64, string, time.Time) (AgentProfile, error)
	TransferAgent(context.Context, HumanPrincipal, string, string, int64, time.Time) (AgentProfile, error)
	CreateOwnedEnrollment(context.Context, string, EnrollmentRecord) error
	ListOwnedEnrollments(context.Context, string, string, AgentArtifactFilter, time.Time) ([]AgentEnrollmentMetadata, error)
	RevokeOwnedEnrollment(context.Context, string, HumanPrincipal, string, string, string, time.Time) (AgentEnrollmentMetadata, error)
	ListOwnedCredentials(context.Context, string, string, AgentArtifactFilter, time.Time) ([]AgentCredentialMetadata, error)
	RevokeOwnedCredential(context.Context, string, HumanPrincipal, string, string, string, time.Time) (AgentCredentialMetadata, error)
	ListDirectoryAgents(context.Context, AgentFilter, time.Time) ([]AgentProfile, error)
	GetDirectoryAgent(context.Context, string, time.Time) (AgentProfile, error)
	ListAdminAgents(context.Context, AgentFilter) ([]AgentProfile, error)
	GetAdminAgent(context.Context, string) (AgentProfile, error)
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

type RegistryService struct {
	store       RegistryStore
	clock       func() time.Time
	idSource    func() (string, error)
	tokenSource func() (string, error)
	digester    secretDigester
	grantable   map[Permission]struct{}
}

func NewRegistryService(store RegistryStore, config RegistryConfig, options ...RegistryOption) (*RegistryService, error) {
	if store == nil {
		return nil, fmt.Errorf("create agent registry: store is required")
	}
	digester, err := newSecretDigester(config.SecretPepper)
	if err != nil {
		return nil, fmt.Errorf("create agent registry: %w", err)
	}
	grantable, err := grantablePermissionSet(config.MemberGrantablePermissions)
	if err != nil {
		return nil, fmt.Errorf("create agent registry: %w", err)
	}
	configured := registryOptions{clock: time.Now, idSource: randomToken, tokenSource: randomToken}
	for _, option := range options {
		option(&configured)
	}
	if configured.clock == nil || configured.idSource == nil || configured.tokenSource == nil {
		return nil, fmt.Errorf("create agent registry: clock and token sources are required")
	}
	return &RegistryService{
		store: store, clock: configured.clock, idSource: configured.idSource, tokenSource: configured.tokenSource,
		digester: digester, grantable: grantable,
	}, nil
}

func (s *RegistryService) CreateAgent(
	ctx context.Context,
	principal HumanPrincipal,
	request CreateAgentRequest,
) (AgentProfile, error) {
	if err := validateHumanPrincipal(principal); err != nil {
		return AgentProfile{}, err
	}
	agentID := strings.TrimSpace(request.AgentID)
	displayName := strings.TrimSpace(request.DisplayName)
	if err := validateAgentIdentity(agentID, displayName); err != nil {
		return AgentProfile{}, err
	}
	now := s.clock().UTC()
	return s.store.CreateAgent(ctx, AgentProfile{
		AgentID: agentID, OwnerMembershipID: principal.MembershipID, OwnerUserID: principal.UserID,
		DisplayName: displayName, Description: strings.TrimSpace(request.Description),
		AgentType: strings.TrimSpace(request.AgentType), Status: AgentStatusActive,
		DirectoryVisible: request.DirectoryVisible, CreatedAt: now, UpdatedAt: now, ResourceVersion: 1,
		CreationIdempotencyKey: strings.TrimSpace(request.IdempotencyKey),
	})
}

func (s *RegistryService) ListOwnedAgents(
	ctx context.Context,
	principal HumanPrincipal,
	filter AgentFilter,
) ([]AgentProfile, error) {
	if err := validateHumanPrincipal(principal); err != nil {
		return nil, err
	}
	filter = normalizeAgentFilter(filter)
	return s.store.ListOwnedAgents(ctx, principal.MembershipID, filter)
}

func (s *RegistryService) GetOwnedAgent(
	ctx context.Context,
	principal HumanPrincipal,
	agentID string,
) (AgentProfile, error) {
	if err := validateHumanPrincipal(principal); err != nil {
		return AgentProfile{}, err
	}
	if strings.TrimSpace(agentID) == "" {
		return AgentProfile{}, ErrAgentNotFound
	}
	return s.store.GetOwnedAgent(ctx, principal.MembershipID, strings.TrimSpace(agentID))
}

func (s *RegistryService) UpdateOwnedAgent(
	ctx context.Context,
	principal HumanPrincipal,
	agentID string,
	request UpdateAgentRequest,
) (AgentProfile, error) {
	profile, err := s.GetOwnedAgent(ctx, principal, agentID)
	if err != nil {
		return AgentProfile{}, err
	}
	if request.ResourceVersion <= 0 || request.ResourceVersion != profile.ResourceVersion {
		return AgentProfile{}, ErrResourceVersionConflict
	}
	if err := applyAgentUpdate(&profile, request, s.clock().UTC()); err != nil {
		return AgentProfile{}, err
	}
	return s.store.UpdateOwnedAgent(ctx, principal.MembershipID, principal, profile)
}

func (s *RegistryService) RetireOwnedAgent(
	ctx context.Context,
	principal HumanPrincipal,
	agentID string,
	resourceVersion int64,
	idempotencyKey string,
) (AgentProfile, error) {
	if err := validateHumanPrincipal(principal); err != nil {
		return AgentProfile{}, err
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return AgentProfile{}, ErrAgentNotFound
	}
	if resourceVersion <= 0 {
		return AgentProfile{}, ErrResourceVersionConflict
	}
	return s.store.RetireOwnedAgent(
		ctx, principal.MembershipID, principal, agentID, resourceVersion,
		strings.TrimSpace(idempotencyKey), s.clock().UTC(),
	)
}

func (s *RegistryService) CreateEnrollment(
	ctx context.Context,
	principal HumanPrincipal,
	agentID string,
	request OwnerEnrollmentRequest,
) (Enrollment, error) {
	profile, err := s.GetOwnedAgent(ctx, principal, agentID)
	if err != nil {
		return Enrollment{}, err
	}
	if profile.Status != AgentStatusActive {
		return Enrollment{}, ErrForbidden
	}
	permissions, err := validateExplicitPermissions(request.Permissions)
	if err != nil {
		return Enrollment{}, err
	}
	for _, permission := range permissions {
		if _, allowed := s.grantable[permission]; !allowed {
			return Enrollment{}, fmt.Errorf("%w: enrollment permission %q is not grantable", ErrInvalidIdentityInput, permission)
		}
	}
	expiresIn := request.ExpiresIn
	if expiresIn == 0 {
		expiresIn = defaultEnrollmentTTL
	}
	if expiresIn < 0 {
		return Enrollment{}, fmt.Errorf("%w: enrollment expiry must be positive", ErrInvalidIdentityInput)
	}
	id, err := s.idSource()
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
		DigestKeyVersion: currentDigestKeyVersion, UserID: principal.UserID, MembershipID: principal.MembershipID,
		AgentID: profile.AgentID, CredentialLabel: strings.TrimSpace(request.CredentialLabel),
		Permissions: permissions, CreatedAt: now, ExpiresAt: now.Add(expiresIn),
		CredentialExpiresAt: request.CredentialExpiresAt,
	}
	if err := s.store.CreateOwnedEnrollment(ctx, principal.MembershipID, record); err != nil {
		return Enrollment{}, fmt.Errorf("save owned agent enrollment: %w", err)
	}
	return Enrollment{ID: id, Token: token, ExpiresAt: record.ExpiresAt}, nil
}

func (s *RegistryService) ListEnrollments(
	ctx context.Context,
	principal HumanPrincipal,
	agentID string,
	filter AgentArtifactFilter,
) ([]AgentEnrollmentMetadata, error) {
	if _, err := s.GetOwnedAgent(ctx, principal, agentID); err != nil {
		return nil, err
	}
	filter = normalizeArtifactFilter(filter)
	return s.store.ListOwnedEnrollments(
		ctx, principal.MembershipID, strings.TrimSpace(agentID), filter, s.clock().UTC(),
	)
}

func (s *RegistryService) RevokeEnrollment(
	ctx context.Context,
	principal HumanPrincipal,
	agentID string,
	enrollmentID string,
	idempotencyKey string,
) (AgentEnrollmentMetadata, error) {
	if _, err := s.GetOwnedAgent(ctx, principal, agentID); err != nil {
		return AgentEnrollmentMetadata{}, err
	}
	if strings.TrimSpace(enrollmentID) == "" {
		return AgentEnrollmentMetadata{}, ErrEnrollmentInvalid
	}
	return s.store.RevokeOwnedEnrollment(
		ctx, principal.MembershipID, principal, strings.TrimSpace(agentID), strings.TrimSpace(enrollmentID),
		strings.TrimSpace(idempotencyKey), s.clock().UTC(),
	)
}

func (s *RegistryService) ListCredentials(
	ctx context.Context,
	principal HumanPrincipal,
	agentID string,
	filter AgentArtifactFilter,
) ([]AgentCredentialMetadata, error) {
	if _, err := s.GetOwnedAgent(ctx, principal, agentID); err != nil {
		return nil, err
	}
	filter = normalizeArtifactFilter(filter)
	return s.store.ListOwnedCredentials(
		ctx, principal.MembershipID, strings.TrimSpace(agentID), filter, s.clock().UTC(),
	)
}

func (s *RegistryService) RevokeOwnedCredential(
	ctx context.Context,
	principal HumanPrincipal,
	agentID string,
	credentialID string,
	idempotencyKey string,
) (AgentCredentialMetadata, error) {
	if _, err := s.GetOwnedAgent(ctx, principal, agentID); err != nil {
		return AgentCredentialMetadata{}, err
	}
	if strings.TrimSpace(credentialID) == "" {
		return AgentCredentialMetadata{}, ErrCredentialNotFound
	}
	return s.store.RevokeOwnedCredential(
		ctx, principal.MembershipID, principal, strings.TrimSpace(agentID), strings.TrimSpace(credentialID),
		strings.TrimSpace(idempotencyKey), s.clock().UTC(),
	)
}

func grantablePermissionSet(permissions []Permission) (map[Permission]struct{}, error) {
	validated, err := validateExplicitPermissions(permissions)
	if err != nil {
		return nil, err
	}
	result := make(map[Permission]struct{}, len(validated))
	for _, permission := range validated {
		result[permission] = struct{}{}
	}
	return result, nil
}

func (s *RegistryService) ListDirectoryAgents(
	ctx context.Context,
	principal Principal,
	filter AgentFilter,
) ([]AgentProfile, error) {
	if principal.ScopeID != LocalScopeID || !principal.HasPermission(PermissionChannelSend) {
		return nil, ErrForbidden
	}
	return s.store.ListDirectoryAgents(ctx, normalizeAgentFilter(filter), s.clock().UTC())
}

func (s *RegistryService) GetDirectoryAgent(
	ctx context.Context,
	principal Principal,
	agentID string,
) (AgentProfile, error) {
	if principal.ScopeID != LocalScopeID || !principal.HasPermission(PermissionChannelSend) {
		return AgentProfile{}, ErrForbidden
	}
	if strings.TrimSpace(agentID) == "" {
		return AgentProfile{}, ErrAgentNotFound
	}
	return s.store.GetDirectoryAgent(ctx, strings.TrimSpace(agentID), s.clock().UTC())
}

func (s *RegistryService) ListAdminAgents(
	ctx context.Context,
	principal HumanPrincipal,
	filter AgentFilter,
) ([]AgentProfile, error) {
	if err := authorizeHumanAdmin(principal); err != nil {
		return nil, err
	}
	return s.store.ListAdminAgents(ctx, normalizeAgentFilter(filter))
}

func (s *RegistryService) GetAdminAgent(
	ctx context.Context,
	principal HumanPrincipal,
	agentID string,
) (AgentProfile, error) {
	if err := authorizeHumanAdmin(principal); err != nil {
		return AgentProfile{}, err
	}
	if strings.TrimSpace(agentID) == "" {
		return AgentProfile{}, ErrAgentNotFound
	}
	return s.store.GetAdminAgent(ctx, strings.TrimSpace(agentID))
}

func (s *RegistryService) UpdateAdminAgent(
	ctx context.Context,
	principal HumanPrincipal,
	agentID string,
	request UpdateAgentRequest,
) (AgentProfile, error) {
	profile, err := s.GetAdminAgent(ctx, principal, agentID)
	if err != nil {
		return AgentProfile{}, err
	}
	if request.ResourceVersion <= 0 || request.ResourceVersion != profile.ResourceVersion {
		return AgentProfile{}, ErrResourceVersionConflict
	}
	if principal.Role == RoleAdmin {
		if request.Status == nil || *request.Status != AgentStatusSuspended ||
			request.DisplayName != nil || request.Description != nil || request.AgentType != nil ||
			request.DirectoryVisible != nil {
			return AgentProfile{}, ErrForbidden
		}
	}
	if err := applyAgentUpdate(&profile, request, s.clock().UTC()); err != nil {
		return AgentProfile{}, err
	}
	return s.store.UpdateOwnedAgent(ctx, profile.OwnerMembershipID, principal, profile)
}

func (s *RegistryService) RetireAdminAgent(
	ctx context.Context,
	principal HumanPrincipal,
	agentID string,
	resourceVersion int64,
	idempotencyKey string,
) (AgentProfile, error) {
	if principal.Role != RoleOwner {
		if err := authorizeHumanAdmin(principal); err != nil {
			return AgentProfile{}, err
		}
		return AgentProfile{}, ErrForbidden
	}
	profile, err := s.GetAdminAgent(ctx, principal, agentID)
	if err != nil {
		return AgentProfile{}, err
	}
	if resourceVersion <= 0 {
		return AgentProfile{}, ErrResourceVersionConflict
	}
	return s.store.RetireOwnedAgent(
		ctx, profile.OwnerMembershipID, principal, profile.AgentID, resourceVersion,
		strings.TrimSpace(idempotencyKey), s.clock().UTC(),
	)
}

func (s *RegistryService) TransferAgent(
	ctx context.Context,
	principal HumanPrincipal,
	agentID string,
	request TransferAgentRequest,
) (AgentProfile, error) {
	if err := authorizeHumanAdmin(principal); err != nil {
		return AgentProfile{}, err
	}
	if principal.Role != RoleOwner {
		return AgentProfile{}, ErrForbidden
	}
	agentID = strings.TrimSpace(agentID)
	targetMembershipID := strings.TrimSpace(request.TargetMembershipID)
	if agentID == "" {
		return AgentProfile{}, ErrAgentNotFound
	}
	if targetMembershipID == "" {
		return AgentProfile{}, ErrInvalidIdentityInput
	}
	profile, err := s.GetAdminAgent(ctx, principal, agentID)
	if err != nil {
		return AgentProfile{}, err
	}
	if request.ResourceVersion <= 0 || request.ResourceVersion != profile.ResourceVersion {
		return AgentProfile{}, ErrResourceVersionConflict
	}
	if profile.Status == AgentStatusRetired {
		return AgentProfile{}, ErrInvalidStateTransition
	}
	return s.store.TransferAgent(
		ctx, principal, agentID, targetMembershipID,
		request.ResourceVersion, s.clock().UTC(),
	)
}

func (s *RegistryService) ListAdminEnrollments(
	ctx context.Context,
	principal HumanPrincipal,
	agentID string,
	filter AgentArtifactFilter,
) ([]AgentEnrollmentMetadata, error) {
	profile, err := s.GetAdminAgent(ctx, principal, agentID)
	if err != nil {
		return nil, err
	}
	return s.store.ListOwnedEnrollments(
		ctx, profile.OwnerMembershipID, profile.AgentID, normalizeArtifactFilter(filter), s.clock().UTC(),
	)
}

func (s *RegistryService) RevokeAdminEnrollment(
	ctx context.Context,
	principal HumanPrincipal,
	agentID string,
	enrollmentID string,
	idempotencyKey string,
) (AgentEnrollmentMetadata, error) {
	profile, err := s.GetAdminAgent(ctx, principal, agentID)
	if err != nil {
		return AgentEnrollmentMetadata{}, err
	}
	if strings.TrimSpace(enrollmentID) == "" {
		return AgentEnrollmentMetadata{}, ErrEnrollmentInvalid
	}
	return s.store.RevokeOwnedEnrollment(
		ctx, profile.OwnerMembershipID, principal, profile.AgentID, strings.TrimSpace(enrollmentID),
		strings.TrimSpace(idempotencyKey), s.clock().UTC(),
	)
}

func (s *RegistryService) ListAdminCredentials(
	ctx context.Context,
	principal HumanPrincipal,
	agentID string,
	filter AgentArtifactFilter,
) ([]AgentCredentialMetadata, error) {
	profile, err := s.GetAdminAgent(ctx, principal, agentID)
	if err != nil {
		return nil, err
	}
	return s.store.ListOwnedCredentials(
		ctx, profile.OwnerMembershipID, profile.AgentID, normalizeArtifactFilter(filter), s.clock().UTC(),
	)
}

func (s *RegistryService) RevokeAdminCredential(
	ctx context.Context,
	principal HumanPrincipal,
	agentID string,
	credentialID string,
	idempotencyKey string,
) (AgentCredentialMetadata, error) {
	profile, err := s.GetAdminAgent(ctx, principal, agentID)
	if err != nil {
		return AgentCredentialMetadata{}, err
	}
	if strings.TrimSpace(credentialID) == "" {
		return AgentCredentialMetadata{}, ErrCredentialNotFound
	}
	return s.store.RevokeOwnedCredential(
		ctx, profile.OwnerMembershipID, principal, profile.AgentID, strings.TrimSpace(credentialID),
		strings.TrimSpace(idempotencyKey), s.clock().UTC(),
	)
}

func authorizeHumanAdmin(principal HumanPrincipal) error {
	if err := validateHumanPrincipal(principal); err != nil {
		return err
	}
	if principal.Role != RoleOwner && principal.Role != RoleAdmin {
		return ErrForbidden
	}
	return nil
}

func validateHumanPrincipal(principal HumanPrincipal) error {
	if strings.TrimSpace(principal.UserID) == "" || strings.TrimSpace(principal.MembershipID) == "" {
		return ErrUnauthorized
	}
	if principal.MembershipStatus != MembershipStatusActive {
		return ErrForbidden
	}
	if principal.Role != RoleOwner && principal.Role != RoleAdmin && principal.Role != RoleMember {
		return ErrForbidden
	}
	return nil
}

func validateAgentIdentity(agentID, displayName string) error {
	if agentID == "" || displayName == "" {
		return fmt.Errorf("%w: agent_id and display_name are required", ErrInvalidIdentityInput)
	}
	if len(agentID) > 128 || strings.ContainsAny(agentID, "/\\") {
		return fmt.Errorf("%w: agent_id is invalid", ErrInvalidIdentityInput)
	}
	for _, current := range agentID {
		if current < 0x20 || current == 0x7f {
			return fmt.Errorf("%w: agent_id is invalid", ErrInvalidIdentityInput)
		}
	}
	if len(displayName) > 200 {
		return fmt.Errorf("%w: display_name is too long", ErrInvalidIdentityInput)
	}
	return nil
}

func validateExplicitPermissions(permissions []Permission) ([]Permission, error) {
	if len(permissions) == 0 {
		return nil, fmt.Errorf("%w: enrollment permissions are required", ErrInvalidIdentityInput)
	}
	seen := make(map[Permission]struct{}, len(permissions))
	result := make([]Permission, 0, len(permissions))
	for _, permission := range permissions {
		if permission != PermissionObserve && permission != PermissionSearch && permission != PermissionGet &&
			permission != PermissionChannelSend && permission != PermissionChannelReceive {
			return nil, fmt.Errorf("%w: unsupported enrollment permission %q", ErrInvalidIdentityInput, permission)
		}
		if _, exists := seen[permission]; exists {
			continue
		}
		seen[permission] = struct{}{}
		result = append(result, permission)
	}
	return result, nil
}

func applyAgentUpdate(profile *AgentProfile, request UpdateAgentRequest, now time.Time) error {
	if request.DisplayName != nil {
		value := strings.TrimSpace(*request.DisplayName)
		if value == "" || len(value) > 200 {
			return fmt.Errorf("%w: display_name is invalid", ErrInvalidIdentityInput)
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
		if *request.Status == AgentStatusRetired {
			return ErrInvalidStateTransition
		}
		if *request.Status != AgentStatusActive && *request.Status != AgentStatusSuspended {
			return fmt.Errorf("%w: unsupported agent status %q", ErrInvalidIdentityInput, *request.Status)
		}
		if profile.Status == AgentStatusRetired ||
			(profile.Status != AgentStatusSuspended && *request.Status == AgentStatusActive) {
			return ErrInvalidStateTransition
		}
		profile.Status = *request.Status
	}
	profile.UpdatedAt = now
	profile.ResourceVersion++
	return nil
}

func normalizeAgentFilter(filter AgentFilter) AgentFilter {
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

func normalizeArtifactFilter(filter AgentArtifactFilter) AgentArtifactFilter {
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
