package onprem

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type MembershipStatus string

const (
	MembershipStatusActive    MembershipStatus = "active"
	MembershipStatusSuspended MembershipStatus = "suspended"
	MembershipStatusRemoved   MembershipStatus = "removed"
)

type ExternalIdentity struct {
	Issuer        string
	Subject       string
	Email         string
	EmailVerified bool
	DisplayName   string
}

type HumanSessionRecord struct {
	SessionID        string
	SecretDigest     Digest
	DigestKeyVersion int16
	CreatedAt        time.Time
	ExpiresAt        time.Time
}

type HumanSession struct {
	Token     string
	ExpiresAt time.Time
	Principal HumanPrincipal
}

type InvitationStatus string

const (
	InvitationStatusPending  InvitationStatus = "pending"
	InvitationStatusAccepted InvitationStatus = "accepted"
	InvitationStatusRevoked  InvitationStatus = "revoked"
	InvitationStatusExpired  InvitationStatus = "expired"
)

type InvitationRequest struct {
	TargetEmail string
	Role        Role
	ExpiresIn   time.Duration
}

type InvitationRecord struct {
	InvitationID          string
	TokenDigest           Digest
	DigestKeyVersion      int16
	TargetIssuer          string
	TargetSubject         string
	TargetEmail           string
	Role                  Role
	CreatedByMembershipID string
	CreatedAt             time.Time
	ExpiresAt             time.Time
	AcceptedAt            *time.Time
	AcceptedByUserID      string
	CreatedMembershipID   string
	RevokedAt             *time.Time
}

type Invitation struct {
	InvitationID string
	Token        string
	TargetEmail  string
	Role         Role
	Status       InvitationStatus
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

type InvitationFilter struct {
	Status InvitationStatus
	Limit  int
	Cursor string
}

type Member struct {
	MembershipID    string
	UserID          string
	Email           string
	EmailVerified   bool
	DisplayName     string
	Role            Role
	Status          MembershipStatus
	JoinedAt        time.Time
	UpdatedAt       time.Time
	ResourceVersion int64
}

type MemberFilter struct {
	Role   Role
	Status MembershipStatus
	Limit  int
	Cursor string
}

type UpdateMemberRequest struct {
	Role            *Role
	Status          *MembershipStatus
	ResourceVersion int64
}

type AuditEvent struct {
	AuditEventID      int64
	ActorKind         string
	ActorUserID       string
	ActorMembershipID string
	ActorAgentID      string
	ActorCredentialID string
	Action            string
	TargetKind        string
	TargetID          string
	OccurredAt        time.Time
}

type AuditFilter struct {
	ActorKind  string
	Action     string
	TargetKind string
	TargetID   string
	Limit      int
	Cursor     string
}

type IdentityStore interface {
	UpsertUserSession(context.Context, ExternalIdentity, HumanSessionRecord) (HumanPrincipal, error)
	ResolveHumanSession(context.Context, string, Digest, time.Time) (HumanPrincipal, error)
	RevokeHumanSession(context.Context, string, Digest, time.Time) error
	ClaimBootstrap(context.Context, string, time.Time) (HumanPrincipal, error)
	CreateInvitation(context.Context, InvitationRecord) error
	AcceptInvitation(context.Context, string, Digest, string, string, bool, string, time.Time) (HumanPrincipal, error)
	ListInvitations(context.Context, InvitationFilter, time.Time) ([]Invitation, error)
	RevokeInvitation(context.Context, string, string, bool, time.Time) (Invitation, error)
	ListMembers(context.Context, MemberFilter) ([]Member, error)
	GetMember(context.Context, string) (Member, error)
	UpdateMember(context.Context, string, Member, time.Time) (Member, error)
	ListAuditEvents(context.Context, AuditFilter) ([]AuditEvent, error)
	GetAuditEvent(context.Context, int64) (AuditEvent, error)
}

type IdentityConfig struct {
	BootstrapSecret string
	SecretPepper    string
	SessionTTL      time.Duration
	InvitationTTL   time.Duration
}

type identityOptions struct {
	clock       func() time.Time
	idSource    func() (string, error)
	tokenSource func() (string, error)
}

type IdentityOption func(*identityOptions)

func WithIdentityClock(clock func() time.Time) IdentityOption {
	return func(options *identityOptions) { options.clock = clock }
}

func WithIdentityIDSource(source func() (string, error)) IdentityOption {
	return func(options *identityOptions) { options.idSource = source }
}

func WithIdentityTokenSource(source func() (string, error)) IdentityOption {
	return func(options *identityOptions) { options.tokenSource = source }
}

type IdentityService struct {
	store       IdentityStore
	config      IdentityConfig
	clock       func() time.Time
	idSource    func() (string, error)
	tokenSource func() (string, error)
	digester    secretDigester
}

func NewIdentityService(
	store IdentityStore,
	config IdentityConfig,
	options ...IdentityOption,
) (*IdentityService, error) {
	if store == nil || strings.TrimSpace(config.BootstrapSecret) == "" {
		return nil, fmt.Errorf("create on-prem identity service: store and bootstrap secret are required")
	}
	if config.SessionTTL <= 0 || config.InvitationTTL <= 0 {
		return nil, fmt.Errorf("create on-prem identity service: session and invitation TTL must be positive")
	}
	digester, err := newSecretDigester(config.SecretPepper)
	if err != nil {
		return nil, fmt.Errorf("create on-prem identity service: %w", err)
	}
	configured := identityOptions{clock: time.Now, idSource: randomToken, tokenSource: randomToken}
	for _, option := range options {
		option(&configured)
	}
	if configured.clock == nil || configured.idSource == nil || configured.tokenSource == nil {
		return nil, fmt.Errorf("create on-prem identity service: clock and token sources are required")
	}
	return &IdentityService{
		store: store, config: config, clock: configured.clock,
		idSource: configured.idSource, tokenSource: configured.tokenSource, digester: digester,
	}, nil
}

func (s *IdentityService) Login(ctx context.Context, identity ExternalIdentity) (HumanSession, error) {
	identity.Issuer = strings.TrimSpace(identity.Issuer)
	identity.Subject = strings.TrimSpace(identity.Subject)
	identity.Email = normalizeEmail(identity.Email)
	identity.DisplayName = strings.TrimSpace(identity.DisplayName)
	if identity.Issuer == "" || identity.Subject == "" {
		return HumanSession{}, fmt.Errorf("login human identity: issuer and subject are required")
	}
	id, err := s.idSource()
	if err != nil {
		return HumanSession{}, fmt.Errorf("create human session ID: %w", err)
	}
	secret, err := s.tokenSource()
	if err != nil {
		return HumanSession{}, fmt.Errorf("create human session secret: %w", err)
	}
	token := "tm_session_" + id + "." + secret
	now := s.clock().UTC()
	record := HumanSessionRecord{
		SessionID: id, SecretDigest: s.digester.Digest(sessionDigestDomain, token),
		DigestKeyVersion: currentDigestKeyVersion, CreatedAt: now, ExpiresAt: now.Add(s.config.SessionTTL),
	}
	principal, err := s.store.UpsertUserSession(ctx, identity, record)
	if err != nil {
		return HumanSession{}, fmt.Errorf("persist human login: %w", err)
	}
	principal.SessionID = id
	return HumanSession{Token: token, ExpiresAt: record.ExpiresAt, Principal: principal}, nil
}

func (s *IdentityService) AuthenticateSession(ctx context.Context, token string) (HumanPrincipal, error) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "tm_session_") {
		return HumanPrincipal{}, ErrUnauthorized
	}
	sessionID, ok := secretPublicID(token, "tm_session_")
	if !ok {
		return HumanPrincipal{}, ErrUnauthorized
	}
	principal, err := s.store.ResolveHumanSession(
		ctx, sessionID, s.digester.Digest(sessionDigestDomain, token), s.clock().UTC(),
	)
	if errors.Is(err, ErrUnauthorized) {
		principal, err = s.store.ResolveHumanSession(ctx, sessionID, digest(token), s.clock().UTC())
	}
	if err != nil {
		return HumanPrincipal{}, fmt.Errorf("authenticate human session: %w", err)
	}
	return principal, nil
}

func (s *IdentityService) Logout(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "tm_session_") {
		return ErrUnauthorized
	}
	sessionID, ok := secretPublicID(token, "tm_session_")
	if !ok {
		return ErrUnauthorized
	}
	err := s.store.RevokeHumanSession(
		ctx, sessionID, s.digester.Digest(sessionDigestDomain, token), s.clock().UTC(),
	)
	if errors.Is(err, ErrUnauthorized) {
		err = s.store.RevokeHumanSession(ctx, sessionID, digest(token), s.clock().UTC())
	}
	if err != nil {
		return fmt.Errorf("revoke human session: %w", err)
	}
	return nil
}

func (s *IdentityService) ClaimBootstrap(
	ctx context.Context,
	principal HumanPrincipal,
	bootstrapSecret string,
) (HumanPrincipal, error) {
	if strings.TrimSpace(principal.UserID) == "" || principal.MembershipID != "" {
		return HumanPrincipal{}, ErrForbidden
	}
	if !constantTimeEqual(strings.TrimSpace(bootstrapSecret), s.config.BootstrapSecret) {
		return HumanPrincipal{}, ErrForbidden
	}
	claimed, err := s.store.ClaimBootstrap(ctx, principal.UserID, s.clock().UTC())
	if err != nil {
		return HumanPrincipal{}, fmt.Errorf("claim on-prem bootstrap: %w", err)
	}
	return claimed, nil
}

func (s *IdentityService) CreateInvitation(
	ctx context.Context,
	principal HumanPrincipal,
	request InvitationRequest,
) (Invitation, error) {
	if err := authorizeInvitation(principal, request.Role); err != nil {
		return Invitation{}, err
	}
	targetEmail := normalizeEmail(request.TargetEmail)
	if targetEmail == "" {
		return Invitation{}, fmt.Errorf("%w: target email is required", ErrInvalidIdentityInput)
	}
	expiresIn := request.ExpiresIn
	if expiresIn == 0 {
		expiresIn = s.config.InvitationTTL
	}
	if expiresIn <= 0 || expiresIn > 7*24*time.Hour {
		return Invitation{}, fmt.Errorf("%w: invitation expiry is invalid", ErrInvalidIdentityInput)
	}
	id, err := s.idSource()
	if err != nil {
		return Invitation{}, fmt.Errorf("create invitation ID: %w", err)
	}
	secret, err := s.tokenSource()
	if err != nil {
		return Invitation{}, fmt.Errorf("create invitation secret: %w", err)
	}
	token := "tm_invite_" + id + "." + secret
	now := s.clock().UTC()
	record := InvitationRecord{
		InvitationID: id, TokenDigest: s.digester.Digest(invitationDigestDomain, token),
		DigestKeyVersion: currentDigestKeyVersion, TargetEmail: targetEmail, Role: request.Role,
		CreatedByMembershipID: principal.MembershipID, CreatedAt: now, ExpiresAt: now.Add(expiresIn),
	}
	if err := s.store.CreateInvitation(ctx, record); err != nil {
		return Invitation{}, fmt.Errorf("persist membership invitation: %w", err)
	}
	return Invitation{
		InvitationID: id, Token: token, TargetEmail: targetEmail, Role: request.Role,
		Status: InvitationStatusPending, CreatedAt: now, ExpiresAt: record.ExpiresAt,
	}, nil
}

func (s *IdentityService) AcceptInvitation(
	ctx context.Context,
	principal HumanPrincipal,
	token string,
	idempotencyKey string,
) (HumanPrincipal, error) {
	if strings.TrimSpace(principal.UserID) == "" {
		return HumanPrincipal{}, ErrMembershipConflict
	}
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "tm_invite_") {
		return HumanPrincipal{}, ErrInvitationInvalid
	}
	invitationID, ok := secretPublicID(token, "tm_invite_")
	if !ok {
		return HumanPrincipal{}, ErrInvitationInvalid
	}
	accepted, err := s.store.AcceptInvitation(
		ctx, invitationID, s.digester.Digest(invitationDigestDomain, token), principal.UserID,
		normalizeEmail(principal.Email), principal.EmailVerified, strings.TrimSpace(idempotencyKey), s.clock().UTC(),
	)
	if errors.Is(err, ErrInvitationInvalid) {
		accepted, err = s.store.AcceptInvitation(
			ctx, invitationID, digest(token), principal.UserID,
			normalizeEmail(principal.Email), principal.EmailVerified, strings.TrimSpace(idempotencyKey), s.clock().UTC(),
		)
	}
	if err != nil {
		return HumanPrincipal{}, fmt.Errorf("accept membership invitation: %w", err)
	}
	return accepted, nil
}

func (s *IdentityService) ListInvitations(
	ctx context.Context,
	principal HumanPrincipal,
	filter InvitationFilter,
) ([]Invitation, error) {
	if err := authorizeIdentityAdmin(principal); err != nil {
		return nil, err
	}
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.Limit > 100 {
		filter.Limit = 100
	}
	filter.Cursor = strings.TrimSpace(filter.Cursor)
	return s.store.ListInvitations(ctx, filter, s.clock().UTC())
}

func (s *IdentityService) RevokeInvitation(
	ctx context.Context,
	principal HumanPrincipal,
	invitationID string,
) (Invitation, error) {
	if err := authorizeIdentityAdmin(principal); err != nil {
		return Invitation{}, err
	}
	if strings.TrimSpace(invitationID) == "" {
		return Invitation{}, ErrInvitationInvalid
	}
	return s.store.RevokeInvitation(
		ctx, strings.TrimSpace(invitationID), principal.MembershipID,
		principal.Role == RoleOwner, s.clock().UTC(),
	)
}

func (s *IdentityService) ListMembers(
	ctx context.Context,
	principal HumanPrincipal,
	filter MemberFilter,
) ([]Member, error) {
	if err := authorizeIdentityAdmin(principal); err != nil {
		return nil, err
	}
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.Limit > 100 {
		filter.Limit = 100
	}
	filter.Cursor = strings.TrimSpace(filter.Cursor)
	return s.store.ListMembers(ctx, filter)
}

func (s *IdentityService) GetMember(
	ctx context.Context,
	principal HumanPrincipal,
	membershipID string,
) (Member, error) {
	if err := authorizeIdentityAdmin(principal); err != nil {
		return Member{}, err
	}
	if strings.TrimSpace(membershipID) == "" {
		return Member{}, ErrMembershipConflict
	}
	return s.store.GetMember(ctx, strings.TrimSpace(membershipID))
}

func (s *IdentityService) UpdateMember(
	ctx context.Context,
	principal HumanPrincipal,
	membershipID string,
	request UpdateMemberRequest,
) (Member, error) {
	target, err := s.GetMember(ctx, principal, membershipID)
	if err != nil {
		return Member{}, err
	}
	if principal.Role == RoleAdmin && target.Role != RoleMember {
		return Member{}, ErrForbidden
	}
	if request.Role != nil {
		if *request.Role != RoleOwner && *request.Role != RoleAdmin && *request.Role != RoleMember {
			return Member{}, ErrMembershipConflict
		}
		if principal.Role != RoleOwner {
			return Member{}, ErrForbidden
		}
		target.Role = *request.Role
	}
	if request.Status != nil {
		if *request.Status != MembershipStatusActive && *request.Status != MembershipStatusSuspended &&
			*request.Status != MembershipStatusRemoved {
			return Member{}, ErrMembershipConflict
		}
		if target.Status == MembershipStatusRemoved {
			return Member{}, ErrMembershipConflict
		}
		target.Status = *request.Status
	}
	if request.ResourceVersion <= 0 || request.ResourceVersion != target.ResourceVersion {
		return Member{}, ErrMembershipConflict
	}
	target.ResourceVersion++
	target.UpdatedAt = s.clock().UTC()
	return s.store.UpdateMember(ctx, principal.MembershipID, target, target.UpdatedAt)
}

func (s *IdentityService) ListAuditEvents(
	ctx context.Context,
	principal HumanPrincipal,
	filter AuditFilter,
) ([]AuditEvent, error) {
	if err := authorizeIdentityAdmin(principal); err != nil {
		return nil, err
	}
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.Limit > 100 {
		filter.Limit = 100
	}
	filter.ActorKind = strings.TrimSpace(filter.ActorKind)
	filter.Action = strings.TrimSpace(filter.Action)
	filter.TargetKind = strings.TrimSpace(filter.TargetKind)
	filter.TargetID = strings.TrimSpace(filter.TargetID)
	filter.Cursor = strings.TrimSpace(filter.Cursor)
	return s.store.ListAuditEvents(ctx, filter)
}

func (s *IdentityService) GetAuditEvent(
	ctx context.Context,
	principal HumanPrincipal,
	auditEventID int64,
) (AuditEvent, error) {
	if err := authorizeIdentityAdmin(principal); err != nil {
		return AuditEvent{}, err
	}
	if auditEventID <= 0 {
		return AuditEvent{}, ErrAuditEventNotFound
	}
	return s.store.GetAuditEvent(ctx, auditEventID)
}

func authorizeIdentityAdmin(principal HumanPrincipal) error {
	if principal.MembershipStatus != MembershipStatusActive {
		return ErrForbidden
	}
	if principal.Role != RoleOwner && principal.Role != RoleAdmin {
		return ErrForbidden
	}
	return nil
}

func authorizeInvitation(principal HumanPrincipal, targetRole Role) error {
	if principal.MembershipStatus != MembershipStatusActive {
		return ErrForbidden
	}
	switch principal.Role {
	case RoleOwner:
		if targetRole == RoleAdmin || targetRole == RoleMember {
			return nil
		}
	case RoleAdmin:
		if targetRole == RoleMember {
			return nil
		}
	}
	return ErrForbidden
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
