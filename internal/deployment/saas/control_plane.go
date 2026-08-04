package saas

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
)

// ControlPlaneConfig configures the human-facing control plane. There is no
// bootstrap secret: SaaS sign-up creates a team, so ClaimBootstrap is
// unsupported in this profile.
type ControlPlaneConfig struct {
	SecretPepper  string
	SessionTTL    time.Duration
	InvitationTTL time.Duration
}

type controlPlaneOptions struct {
	clock       func() time.Time
	idSource    func() (string, error)
	tokenSource func() (string, error)
}

type ControlPlaneOption func(*controlPlaneOptions)

func WithControlPlaneClock(clock func() time.Time) ControlPlaneOption {
	return func(options *controlPlaneOptions) { options.clock = clock }
}

func WithControlPlaneIDSource(source func() (string, error)) ControlPlaneOption {
	return func(options *controlPlaneOptions) { options.idSource = source }
}

func WithControlPlaneTokenSource(source func() (string, error)) ControlPlaneOption {
	return func(options *controlPlaneOptions) { options.tokenSource = source }
}

// ControlPlane is the human-facing SaaS control plane service. Its method
// set structurally matches the handler-facing HumanIdentityLifecycle (plus
// team methods) without importing the handler package, mirroring the
// on-prem IdentityService semantics team-scoped: the scope of every
// principal is the session's current team instead of the on-prem constant.
type ControlPlane struct {
	teams       TeamStore
	invitations TeamInvitationStore
	config      ControlPlaneConfig
	clock       func() time.Time
	idSource    func() (string, error)
	tokenSource func() (string, error)
	digester    secretDigester
}

func NewControlPlane(
	teams TeamStore,
	invitations TeamInvitationStore,
	config ControlPlaneConfig,
	options ...ControlPlaneOption,
) (*ControlPlane, error) {
	if teams == nil || invitations == nil {
		return nil, fmt.Errorf("create saas control plane: team and invitation stores are required")
	}
	if config.SessionTTL <= 0 || config.InvitationTTL <= 0 {
		return nil, fmt.Errorf("create saas control plane: session and invitation TTL must be positive")
	}
	digester, err := newSecretDigester(config.SecretPepper)
	if err != nil {
		return nil, fmt.Errorf("create saas control plane: %w", err)
	}
	configured := controlPlaneOptions{clock: time.Now, idSource: randomToken, tokenSource: randomToken}
	for _, option := range options {
		option(&configured)
	}
	if configured.clock == nil || configured.idSource == nil || configured.tokenSource == nil {
		return nil, fmt.Errorf("create saas control plane: clock and token sources are required")
	}
	return &ControlPlane{
		teams: teams, invitations: invitations, config: config, clock: configured.clock,
		idSource: configured.idSource, tokenSource: configured.tokenSource, digester: digester,
	}, nil
}

// Login upserts the global user and opens a session. The session's current
// team is the user's only active membership's team, the most recently
// joined team when there are several, or none (empty scope) when the user
// holds no membership yet.
func (s *ControlPlane) Login(ctx context.Context, identity onprem.ExternalIdentity) (onprem.HumanSession, error) {
	identity.Issuer = strings.TrimSpace(identity.Issuer)
	identity.Subject = strings.TrimSpace(identity.Subject)
	identity.Email = normalizeEmail(identity.Email)
	identity.DisplayName = strings.TrimSpace(identity.DisplayName)
	if identity.Issuer == "" || identity.Subject == "" {
		return onprem.HumanSession{}, fmt.Errorf("login human identity: issuer and subject are required")
	}
	summaries, err := s.teams.ListTeamSummaries(ctx, externalUserID(identity.Issuer, identity.Subject))
	if err != nil {
		return onprem.HumanSession{}, fmt.Errorf("resolve login team memberships: %w", err)
	}
	currentTeamID := ""
	if len(summaries) > 0 {
		currentTeamID = summaries[0].Team.TeamID
	}
	id, err := s.idSource()
	if err != nil {
		return onprem.HumanSession{}, fmt.Errorf("create human session ID: %w", err)
	}
	secret, err := s.tokenSource()
	if err != nil {
		return onprem.HumanSession{}, fmt.Errorf("create human session secret: %w", err)
	}
	token := "tm_session_" + id + "." + secret
	now := s.clock().UTC()
	record := TeamSessionRecord{
		SessionID: id, SecretDigest: s.digester.Digest(sessionDigestDomain, token),
		DigestKeyVersion: currentDigestKeyVersion, CreatedAt: now, ExpiresAt: now.Add(s.config.SessionTTL),
		CurrentTeamID: currentTeamID,
	}
	principal, err := s.teams.CreateUserSession(ctx, identity, record)
	if err != nil {
		return onprem.HumanSession{}, fmt.Errorf("persist human login: %w", err)
	}
	principal.SessionID = id
	return onprem.HumanSession{Token: token, ExpiresAt: record.ExpiresAt, Principal: principal}, nil
}

func (s *ControlPlane) AuthenticateSession(ctx context.Context, token string) (onprem.HumanPrincipal, error) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "tm_session_") {
		return onprem.HumanPrincipal{}, onprem.ErrUnauthorized
	}
	sessionID, ok := secretPublicID(token, "tm_session_")
	if !ok {
		return onprem.HumanPrincipal{}, onprem.ErrUnauthorized
	}
	principal, err := s.teams.ResolveSession(
		ctx, sessionID, s.digester.Digest(sessionDigestDomain, token), s.clock().UTC(),
	)
	if errors.Is(err, onprem.ErrUnauthorized) {
		principal, err = s.teams.ResolveSession(ctx, sessionID, digest(token), s.clock().UTC())
	}
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("authenticate human session: %w", err)
	}
	return principal, nil
}

func (s *ControlPlane) Logout(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "tm_session_") {
		return onprem.ErrUnauthorized
	}
	sessionID, ok := secretPublicID(token, "tm_session_")
	if !ok {
		return onprem.ErrUnauthorized
	}
	err := s.teams.RevokeSession(
		ctx, sessionID, s.digester.Digest(sessionDigestDomain, token), s.clock().UTC(),
	)
	if errors.Is(err, onprem.ErrUnauthorized) {
		err = s.teams.RevokeSession(ctx, sessionID, digest(token), s.clock().UTC())
	}
	if err != nil {
		return fmt.Errorf("revoke human session: %w", err)
	}
	return nil
}

// ClaimBootstrap is unsupported in the SaaS profile: there is no
// single-installation bootstrap, sign-up creates a team.
func (s *ControlPlane) ClaimBootstrap(
	context.Context,
	onprem.HumanPrincipal,
	string,
) (onprem.HumanPrincipal, error) {
	return onprem.HumanPrincipal{}, ErrUnsupportedInSaaS
}

// CreateTeam creates a team owned by the principal's user. The slug is
// derived from the name; on a slug collision the service retries with a
// numeric suffix before surfacing ErrTeamSlugConflict.
func (s *ControlPlane) CreateTeam(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	name string,
	idempotencyKey string,
) (Team, error) {
	if strings.TrimSpace(principal.UserID) == "" {
		return Team{}, onprem.ErrUnauthorized
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return Team{}, fmt.Errorf("%w: team name is required and must be at most 200 characters", onprem.ErrInvalidIdentityInput)
	}
	teamID, err := newPrefixedID("team")
	if err != nil {
		return Team{}, err
	}
	membershipID, err := newPrefixedID("mbr")
	if err != nil {
		return Team{}, err
	}
	now := s.clock().UTC()
	baseSlug := deriveSlug(name)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		slug := baseSlug
		if attempt > 0 {
			slug = fmt.Sprintf("%s-%d", baseSlug, attempt+1)
		}
		team := Team{
			TeamID: teamID, Name: name, Slug: slug, CreatedByUserID: principal.UserID,
			CreatedAt: now, UpdatedAt: now,
		}
		created, _, err := s.teams.CreateTeam(ctx, team, membershipID, idempotencyKey, now)
		if errors.Is(err, ErrTeamSlugConflict) {
			// A slug conflict rolls the transaction back without writing
			// the idempotency key, so retrying with a suffixed slug under
			// the same key is safe.
			lastErr = err
			continue
		}
		if err != nil {
			return Team{}, fmt.Errorf("create team: %w", err)
		}
		return created, nil
	}
	return Team{}, fmt.Errorf("create team: %w", lastErr)
}

func (s *ControlPlane) ListTeams(
	ctx context.Context,
	principal onprem.HumanPrincipal,
) ([]TeamSummary, error) {
	if strings.TrimSpace(principal.UserID) == "" {
		return nil, onprem.ErrUnauthorized
	}
	summaries, err := s.teams.ListTeamSummaries(ctx, principal.UserID)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	return summaries, nil
}

// SwitchTeam points the principal's session at another team the user
// actively belongs to and returns the principal re-scoped to that team.
func (s *ControlPlane) SwitchTeam(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	teamID string,
) (onprem.HumanPrincipal, error) {
	if strings.TrimSpace(principal.UserID) == "" || strings.TrimSpace(principal.SessionID) == "" {
		return onprem.HumanPrincipal{}, onprem.ErrUnauthorized
	}
	teamID = strings.TrimSpace(teamID)
	if teamID == "" {
		return onprem.HumanPrincipal{}, fmt.Errorf("%w: team_id is required", onprem.ErrInvalidIdentityInput)
	}
	summaries, err := s.teams.ListTeamSummaries(ctx, principal.UserID)
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("resolve team memberships: %w", err)
	}
	var target *TeamSummary
	for index := range summaries {
		if summaries[index].Team.TeamID == teamID {
			target = &summaries[index]
			break
		}
	}
	if target == nil {
		return onprem.HumanPrincipal{}, ErrNotTeamMember
	}
	if err := s.teams.SwitchTeam(ctx, principal.SessionID, principal.UserID, teamID, s.clock().UTC()); err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("switch team: %w", err)
	}
	principal.ScopeID = target.Team.TeamID
	principal.MembershipID = target.MembershipID
	principal.Role = target.Role
	principal.MembershipStatus = onprem.MembershipStatusActive
	return principal, nil
}

func (s *ControlPlane) CreateInvitation(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	request onprem.InvitationRequest,
) (onprem.Invitation, error) {
	if err := authorizeInvitation(principal, request.Role); err != nil {
		return onprem.Invitation{}, err
	}
	targetEmail := normalizeEmail(request.TargetEmail)
	if targetEmail == "" {
		return onprem.Invitation{}, fmt.Errorf("%w: target email is required", onprem.ErrInvalidIdentityInput)
	}
	expiresIn := request.ExpiresIn
	if expiresIn == 0 {
		expiresIn = s.config.InvitationTTL
	}
	if expiresIn <= 0 || expiresIn > 7*24*time.Hour {
		return onprem.Invitation{}, fmt.Errorf("%w: invitation expiry is invalid", onprem.ErrInvalidIdentityInput)
	}
	id, err := s.idSource()
	if err != nil {
		return onprem.Invitation{}, fmt.Errorf("create invitation ID: %w", err)
	}
	secret, err := s.tokenSource()
	if err != nil {
		return onprem.Invitation{}, fmt.Errorf("create invitation secret: %w", err)
	}
	token := "tm_invite_" + id + "." + secret
	now := s.clock().UTC()
	record := onprem.InvitationRecord{
		InvitationID: id, TokenDigest: s.digester.Digest(invitationDigestDomain, token),
		DigestKeyVersion: currentDigestKeyVersion, TargetEmail: targetEmail, Role: request.Role,
		CreatedByMembershipID: principal.MembershipID, CreatedAt: now, ExpiresAt: now.Add(expiresIn),
	}
	if err := s.invitations.CreateInvitation(ctx, principal.ScopeID, record); err != nil {
		return onprem.Invitation{}, fmt.Errorf("persist membership invitation: %w", err)
	}
	return onprem.Invitation{
		InvitationID: id, Token: token, TargetEmail: targetEmail, Role: request.Role,
		Status: onprem.InvitationStatusPending, CreatedAt: now, ExpiresAt: record.ExpiresAt,
	}, nil
}

// AcceptInvitation creates a membership in the invitation's team, which may
// differ from the principal's current team: a user may belong to many
// teams. Afterwards the session's current team is switched to the new team
// so the accept lands the user where they were invited.
func (s *ControlPlane) AcceptInvitation(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	token string,
	idempotencyKey string,
) (onprem.HumanPrincipal, error) {
	if strings.TrimSpace(principal.UserID) == "" {
		return onprem.HumanPrincipal{}, onprem.ErrMembershipConflict
	}
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "tm_invite_") {
		return onprem.HumanPrincipal{}, onprem.ErrInvitationInvalid
	}
	invitationID, ok := secretPublicID(token, "tm_invite_")
	if !ok {
		return onprem.HumanPrincipal{}, onprem.ErrInvitationInvalid
	}
	now := s.clock().UTC()
	accepted, err := s.invitations.AcceptInvitation(
		ctx, invitationID, s.digester.Digest(invitationDigestDomain, token), principal.UserID,
		normalizeEmail(principal.Email), principal.EmailVerified, strings.TrimSpace(idempotencyKey), now,
	)
	if errors.Is(err, onprem.ErrInvitationInvalid) {
		accepted, err = s.invitations.AcceptInvitation(
			ctx, invitationID, digest(token), principal.UserID,
			normalizeEmail(principal.Email), principal.EmailVerified, strings.TrimSpace(idempotencyKey), now,
		)
	}
	if err != nil {
		return onprem.HumanPrincipal{}, fmt.Errorf("accept membership invitation: %w", err)
	}
	if principal.SessionID != "" && accepted.ScopeID != principal.ScopeID {
		if err := s.teams.SwitchTeam(ctx, principal.SessionID, principal.UserID, accepted.ScopeID, now); err != nil {
			return onprem.HumanPrincipal{}, fmt.Errorf("switch session to the invited team: %w", err)
		}
	}
	accepted.SessionID = principal.SessionID
	return accepted, nil
}

func (s *ControlPlane) ListInvitations(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	filter onprem.InvitationFilter,
) ([]onprem.Invitation, error) {
	if err := authorizeTeamAdmin(principal); err != nil {
		return nil, err
	}
	filter = normalizeInvitationFilter(filter)
	invitations, err := s.invitations.ListInvitations(ctx, principal.ScopeID, filter, s.clock().UTC())
	if err != nil {
		return nil, fmt.Errorf("list membership invitations: %w", err)
	}
	return invitations, nil
}

func (s *ControlPlane) RevokeInvitation(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	invitationID string,
) (onprem.Invitation, error) {
	if err := authorizeTeamAdmin(principal); err != nil {
		return onprem.Invitation{}, err
	}
	if strings.TrimSpace(invitationID) == "" {
		return onprem.Invitation{}, onprem.ErrInvitationInvalid
	}
	invitation, err := s.invitations.RevokeInvitation(
		ctx, principal.ScopeID, strings.TrimSpace(invitationID), principal.MembershipID,
		principal.Role == onprem.RoleOwner, s.clock().UTC(),
	)
	if err != nil {
		return onprem.Invitation{}, fmt.Errorf("revoke membership invitation: %w", err)
	}
	return invitation, nil
}

func (s *ControlPlane) ListMembers(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	filter onprem.MemberFilter,
) ([]onprem.Member, error) {
	if err := authorizeTeamAdmin(principal); err != nil {
		return nil, err
	}
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.Limit > 100 {
		filter.Limit = 100
	}
	filter.Cursor = strings.TrimSpace(filter.Cursor)
	members, err := s.teams.ListMembers(ctx, principal.ScopeID, filter)
	if err != nil {
		return nil, fmt.Errorf("list team members: %w", err)
	}
	return members, nil
}

func (s *ControlPlane) GetMember(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	membershipID string,
) (onprem.Member, error) {
	if err := authorizeTeamAdmin(principal); err != nil {
		return onprem.Member{}, err
	}
	if strings.TrimSpace(membershipID) == "" {
		return onprem.Member{}, onprem.ErrMembershipConflict
	}
	member, err := s.teams.GetMember(ctx, principal.ScopeID, strings.TrimSpace(membershipID))
	if err != nil {
		return onprem.Member{}, fmt.Errorf("get team member: %w", err)
	}
	return member, nil
}

// UpdateMember applies a role/status change with resource-version
// optimistic concurrency, mirroring the on-prem role matrix (admins may
// only touch members, role changes are owner-only) scoped to the
// principal's team.
func (s *ControlPlane) UpdateMember(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	membershipID string,
	request onprem.UpdateMemberRequest,
) (onprem.Member, error) {
	target, err := s.GetMember(ctx, principal, membershipID)
	if err != nil {
		return onprem.Member{}, err
	}
	if principal.Role == onprem.RoleAdmin && target.Role != onprem.RoleMember {
		return onprem.Member{}, onprem.ErrForbidden
	}
	if err := applyMemberUpdate(&target, principal, request); err != nil {
		return onprem.Member{}, err
	}
	target.UpdatedAt = s.clock().UTC()
	updated, err := s.teams.UpdateMember(ctx, principal.ScopeID, principal.MembershipID, target, target.UpdatedAt)
	if err != nil {
		return onprem.Member{}, fmt.Errorf("update team member: %w", err)
	}
	return updated, nil
}

func applyMemberUpdate(
	target *onprem.Member,
	principal onprem.HumanPrincipal,
	request onprem.UpdateMemberRequest,
) error {
	if request.Role != nil {
		if *request.Role != onprem.RoleOwner && *request.Role != onprem.RoleAdmin && *request.Role != onprem.RoleMember {
			return onprem.ErrInvalidIdentityInput
		}
		if principal.Role != onprem.RoleOwner {
			return onprem.ErrForbidden
		}
		target.Role = *request.Role
	}
	if request.Status != nil {
		if *request.Status != onprem.MembershipStatusActive && *request.Status != onprem.MembershipStatusSuspended &&
			*request.Status != onprem.MembershipStatusRemoved {
			return onprem.ErrInvalidIdentityInput
		}
		if target.Status == onprem.MembershipStatusRemoved {
			return onprem.ErrInvalidStateTransition
		}
		target.Status = *request.Status
	}
	if request.ResourceVersion <= 0 || request.ResourceVersion != target.ResourceVersion {
		return onprem.ErrResourceVersionConflict
	}
	target.ResourceVersion++
	return nil
}

func (s *ControlPlane) ListAuditEvents(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	filter onprem.AuditFilter,
) ([]onprem.AuditEvent, error) {
	if err := authorizeTeamAdmin(principal); err != nil {
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
	events, err := s.teams.ListAuditEvents(ctx, principal.ScopeID, filter)
	if err != nil {
		return nil, fmt.Errorf("list team audit events: %w", err)
	}
	return events, nil
}

func (s *ControlPlane) GetAuditEvent(
	ctx context.Context,
	principal onprem.HumanPrincipal,
	auditEventID int64,
) (onprem.AuditEvent, error) {
	if err := authorizeTeamAdmin(principal); err != nil {
		return onprem.AuditEvent{}, err
	}
	if auditEventID <= 0 {
		return onprem.AuditEvent{}, onprem.ErrAuditEventNotFound
	}
	event, err := s.teams.GetAuditEvent(ctx, principal.ScopeID, auditEventID)
	if err != nil {
		return onprem.AuditEvent{}, fmt.Errorf("get team audit event: %w", err)
	}
	return event, nil
}

// validateHumanPrincipal mirrors the on-prem principal validation: a signed
// in user with an active membership and a known role.
func validateHumanPrincipal(principal onprem.HumanPrincipal) error {
	if strings.TrimSpace(principal.UserID) == "" || strings.TrimSpace(principal.MembershipID) == "" {
		return onprem.ErrUnauthorized
	}
	if principal.MembershipStatus != onprem.MembershipStatusActive {
		return onprem.ErrForbidden
	}
	if principal.Role != onprem.RoleOwner && principal.Role != onprem.RoleAdmin && principal.Role != onprem.RoleMember {
		return onprem.ErrForbidden
	}
	return nil
}

func authorizeTeamAdmin(principal onprem.HumanPrincipal) error {
	if err := validateHumanPrincipal(principal); err != nil {
		return err
	}
	if principal.Role != onprem.RoleOwner && principal.Role != onprem.RoleAdmin {
		return onprem.ErrForbidden
	}
	return nil
}

// authorizeInvitation mirrors the on-prem invitation role matrix: owners
// invite admins and members, admins invite members only.
func authorizeInvitation(principal onprem.HumanPrincipal, targetRole onprem.Role) error {
	if principal.MembershipStatus != onprem.MembershipStatusActive {
		return onprem.ErrForbidden
	}
	switch principal.Role {
	case onprem.RoleOwner:
		if targetRole == onprem.RoleAdmin || targetRole == onprem.RoleMember {
			return nil
		}
	case onprem.RoleAdmin:
		if targetRole == onprem.RoleMember {
			return nil
		}
	}
	return onprem.ErrForbidden
}

func normalizeInvitationFilter(filter onprem.InvitationFilter) onprem.InvitationFilter {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.Limit > 100 {
		filter.Limit = 100
	}
	filter.Cursor = strings.TrimSpace(filter.Cursor)
	return filter
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// deriveSlug turns a team name into a URL-safe slug: lowercase, digits and
// letters kept, everything else collapsed into single dashes.
func deriveSlug(name string) string {
	var builder strings.Builder
	lastDash := true
	for _, current := range strings.ToLower(name) {
		isAlphaNum := current >= 'a' && current <= 'z' || current >= '0' && current <= '9'
		if isAlphaNum {
			builder.WriteRune(current)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		return "team"
	}
	if len(slug) > 63 {
		slug = strings.Trim(slug[:63], "-")
	}
	return slug
}
