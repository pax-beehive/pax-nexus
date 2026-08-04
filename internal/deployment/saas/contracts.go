// Package saas owns the multi-team SaaS control plane: teams, team
// memberships and invitations, per-user team sessions, and per-team agent
// registries and credentials.
//
// The package reuses the shared on-prem identity types (users, principals,
// roles, member/invitation/enrollment/credential records) so the existing
// HTTP handlers can serve both deployment profiles; only team-scoped
// concepts are defined here. team_id IS the scope_id: every store query and
// write in this package filters by team, which is what isolates one team
// from another. Human users stay global (onprem_users keyed by
// (issuer, subject)) so one login can hold memberships in many teams.
package saas

//go:generate mockgen -source=contracts.go -destination=mocks/stores.go -package=mocks TeamStore TeamInvitationStore TeamRegistryStore TeamCredentialStore

import (
	"context"
	"errors"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
)

// SaaS control plane errors. Sentinels whose semantics match the on-prem
// identity domain (unauthorized, invitation invalid, membership conflict,
// idempotency conflict, resource version conflict, agent/credential not
// found, enrollment invalid) are deliberately NOT redefined here: services
// and stores return the onprem.* sentinels so shared error mapping keeps
// working. Callers match errors with errors.Is.
var (
	// ErrTeamNotFound means no team exists for the requested team ID.
	ErrTeamNotFound = errors.New("team not found")
	// ErrTeamSlugConflict means the requested slug is already taken by
	// another team (slugs are globally unique).
	ErrTeamSlugConflict = errors.New("team slug is already taken")
	// ErrNotTeamMember means the user holds no active membership in the
	// team the operation targets.
	ErrNotTeamMember = errors.New("user is not an active member of the team")
	// ErrUnsupportedInSaaS marks interface methods that have no meaning in
	// the SaaS profile (bootstrap, devices, channels, legacy admin
	// enrollments). Handlers map it to a 501-style response.
	ErrUnsupportedInSaaS = errors.New("operation is not supported in the saas profile")
)

// Team is one SaaS tenant. TeamID is the scope_id every data-plane row
// carries; it is generated with the same random-ID mechanism as the other
// postgres IDs, with a "team_" prefix.
type Team struct {
	TeamID          string
	Name            string
	Slug            string
	CreatedByUserID string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ResourceVersion int64
}

// TeamSummary feeds the team switcher: one row per active membership,
// pairing the team with the caller's role and membership in it.
type TeamSummary struct {
	Team         Team
	Role         onprem.Role
	MembershipID string
}

// TeamSessionRecord persists one human login session. CurrentTeamID is the
// team the user is currently acting in (empty means "no current team":
// signed up but not a member anywhere yet); switching teams updates that
// column. SecretDigest is the HMAC-peppered token digest, computed by the
// service layer with the same mechanism on-prem sessions use.
type TeamSessionRecord struct {
	SessionID        string
	UserID           string
	SecretDigest     onprem.Digest
	DigestKeyVersion int16
	CreatedAt        time.Time
	ExpiresAt        time.Time
	CurrentTeamID    string
}

// ScopedCredential is an authenticated team agent credential plus the team
// (scope) it belongs to. Authentication resolves the scope from the
// credential row instead of pinning a constant.
type ScopedCredential struct {
	onprem.CredentialRecord
	TeamID string
}

// TeamStore owns teams, team memberships, and team human sessions. All
// member reads and writes are scoped to one team.
type TeamStore interface {
	// CreateTeam inserts the team and its owner membership atomically.
	// Replaying with the same (CreatedByUserID, idempotencyKey) returns the
	// existing team and owner membership; the same key with a different
	// name or slug fails with onprem.ErrIdempotencyConflict, and a slug
	// taken by another team fails with ErrTeamSlugConflict.
	CreateTeam(ctx context.Context, team Team, ownerMembershipID string, idempotencyKey string, now time.Time) (Team, onprem.Member, error)
	GetTeam(ctx context.Context, teamID string) (Team, error)
	// ListTeamSummaries returns one summary per active membership of the
	// user, most recently joined first (the login default current team is
	// therefore the first entry).
	ListTeamSummaries(ctx context.Context, userID string) ([]TeamSummary, error)
	// CreateUserSession upserts the global (issuer, subject) user and
	// inserts the session row, returning the principal scoped to the
	// session's current team (empty scope when the user has none).
	CreateUserSession(ctx context.Context, identity onprem.ExternalIdentity, record TeamSessionRecord) (onprem.HumanPrincipal, error)
	// ResolveSession authenticates a session by token digest and returns
	// the principal scoped to the session's current team.
	ResolveSession(ctx context.Context, sessionID string, digest onprem.Digest, now time.Time) (onprem.HumanPrincipal, error)
	RevokeSession(ctx context.Context, sessionID string, digest onprem.Digest, now time.Time) error
	// SwitchTeam points the session at another team the session's user is
	// an active member of, failing with ErrNotTeamMember otherwise.
	SwitchTeam(ctx context.Context, sessionID string, userID string, teamID string, now time.Time) error
	ListMembers(ctx context.Context, teamID string, filter onprem.MemberFilter) ([]onprem.Member, error)
	GetMember(ctx context.Context, teamID string, membershipID string) (onprem.Member, error)
	// UpdateMember persists a role/status change with resource-version
	// optimistic concurrency and enforces the per-team last-active-owner
	// invariant, mirroring the on-prem member update.
	UpdateMember(ctx context.Context, teamID string, actorMembershipID string, member onprem.Member, now time.Time) (onprem.Member, error)
	// ListAuditEvents and GetAuditEvent read the shared audit trail scoped
	// to one team (rows written with scope_id = teamID).
	ListAuditEvents(ctx context.Context, teamID string, filter onprem.AuditFilter) ([]onprem.AuditEvent, error)
	GetAuditEvent(ctx context.Context, teamID string, auditEventID int64) (onprem.AuditEvent, error)
}

// TeamInvitationStore owns team membership invitations. Accepting an
// invitation creates a membership in the invitation's team; a user may hold
// memberships in many teams (the core difference from on-prem).
type TeamInvitationStore interface {
	CreateInvitation(ctx context.Context, teamID string, record onprem.InvitationRecord) error
	// AcceptInvitation locks the invitation FOR UPDATE, requires a verified
	// matching email, and inserts the membership in the invitation's team.
	// Replays via (accepted_by_user_id, accept_idempotency_key) return the
	// already-created membership principal.
	AcceptInvitation(ctx context.Context, invitationPublicID string, digest onprem.Digest, userID string, email string, emailVerified bool, idempotencyKey string, now time.Time) (onprem.HumanPrincipal, error)
	ListInvitations(ctx context.Context, teamID string, filter onprem.InvitationFilter, now time.Time) ([]onprem.Invitation, error)
	RevokeInvitation(ctx context.Context, teamID string, invitationID string, actorMembershipID string, canRevokeAdmin bool, now time.Time) (onprem.Invitation, error)
}

// TeamRegistryStore owns the per-team agent registry (team_agents). Every
// query filters by team_id.
type TeamRegistryStore interface {
	// CreateAgent inserts an agent owned by a membership of the given team,
	// with on-prem-style creation idempotency replay.
	CreateAgent(ctx context.Context, teamID string, profile onprem.AgentProfile) (onprem.AgentProfile, error)
	ListOwnedAgents(ctx context.Context, teamID string, membershipID string, filter onprem.AgentFilter) ([]onprem.AgentProfile, error)
	GetOwnedAgent(ctx context.Context, teamID string, membershipID string, agentID string) (onprem.AgentProfile, error)
	UpdateOwnedAgent(ctx context.Context, teamID string, membershipID string, actor onprem.HumanPrincipal, profile onprem.AgentProfile) (onprem.AgentProfile, error)
	RetireOwnedAgent(ctx context.Context, teamID string, membershipID string, actor onprem.HumanPrincipal, agentID string, resourceVersion int64, idempotencyKey string, now time.Time) (onprem.AgentProfile, error)
}

// TeamCredentialStore owns team agent enrollments and credentials. Token
// and key digests are HMAC-peppered by the service layer, same as on-prem;
// the store only ever sees digests.
type TeamCredentialStore interface {
	CreateOwnedEnrollment(ctx context.Context, teamID string, membershipID string, record onprem.EnrollmentRecord) error
	// ExchangeEnrollment consumes an enrollment and issues the credential
	// in the enrollment's team, mirroring the on-prem exchange.
	ExchangeEnrollment(ctx context.Context, enrollmentID string, digest onprem.Digest, credential onprem.CredentialRecord, now time.Time) (onprem.EnrollmentRecord, error)
	// ResolveCredential authenticates an API key by digest and returns the
	// credential plus its team as the scope.
	ResolveCredential(ctx context.Context, credentialID string, digest onprem.Digest, now time.Time) (ScopedCredential, error)
	// RotateCredential expires the current credential and inserts its
	// successor inside the same team.
	RotateCredential(ctx context.Context, teamID string, currentID string, replacement onprem.CredentialRecord, overlapUntil time.Time) error
	ListOwnedEnrollments(ctx context.Context, teamID string, membershipID string, agentID string, filter onprem.AgentArtifactFilter, now time.Time) ([]onprem.AgentEnrollmentMetadata, error)
	// RevokeOwnedEnrollment revokes with owner idempotency: replaying the
	// same (actor membership, idempotency key) returns the stored result.
	RevokeOwnedEnrollment(ctx context.Context, teamID string, membershipID string, actor onprem.HumanPrincipal, agentID string, enrollmentID string, idempotencyKey string, now time.Time) (onprem.AgentEnrollmentMetadata, error)
	ListOwnedCredentials(ctx context.Context, teamID string, membershipID string, agentID string, filter onprem.AgentArtifactFilter, now time.Time) ([]onprem.AgentCredentialMetadata, error)
	// RevokeOwnedCredential revokes with owner idempotency, mirroring
	// RevokeOwnedEnrollment.
	RevokeOwnedCredential(ctx context.Context, teamID string, membershipID string, actor onprem.HumanPrincipal, agentID string, credentialID string, idempotencyKey string, now time.Time) (onprem.AgentCredentialMetadata, error)
}
