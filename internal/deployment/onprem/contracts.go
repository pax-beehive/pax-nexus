// Package onprem owns single-Team deployment identity and credential lifecycle.
package onprem

import (
	"context"
	"errors"
	"time"
)

const LocalScopeID = "local-team"

var (
	ErrUnauthorized             = errors.New("unauthorized")
	ErrForbidden                = errors.New("forbidden")
	ErrEnrollmentInvalid        = errors.New("enrollment is invalid or expired")
	ErrCredentialNotFound       = errors.New("credential not found")
	ErrAgentNotFound            = errors.New("agent not found")
	ErrAgentConflict            = errors.New("agent already exists or version conflicts")
	ErrAgentIDConflict          = errors.New("agent ID already exists")
	ErrResourceVersionConflict  = errors.New("resource version conflicts with current state")
	ErrInvalidStateTransition   = errors.New("state transition is not allowed")
	ErrBootstrapClosed          = errors.New("bootstrap is closed")
	ErrInvitationInvalid        = errors.New("invitation is invalid or expired")
	ErrMembershipConflict       = errors.New("membership state conflicts with the operation")
	ErrLastActiveOwner          = errors.New("at least one active owner is required")
	ErrTargetAgentNotFound      = errors.New("target agent not found")
	ErrAgentIdentityConflict    = errors.New("agent identity is ambiguous")
	ErrEnvelopeNotFound         = errors.New("channel envelope not found")
	ErrEnvelopeState            = errors.New("channel envelope state does not allow the operation")
	ErrIdempotencyConflict      = errors.New("idempotency key was already used for a different request")
	ErrInvalidChannelRequest    = errors.New("invalid channel request")
	ErrInvalidIdentityInput     = errors.New("invalid identity or agent input")
	ErrAuditEventNotFound       = errors.New("audit event not found")
	ErrAgentProvisionConflict   = errors.New("agent is provisioned by another owner")
	ErrDeviceAgentLimitExceeded = errors.New("device active agent limit exceeded")
)

type Permission string

const (
	PermissionAdmin          Permission = "admin"
	PermissionObserve        Permission = "observe"
	PermissionSearch         Permission = "search"
	PermissionGet            Permission = "get"
	PermissionChannelSend    Permission = "channel_send"
	PermissionChannelReceive Permission = "channel_receive"
	PermissionAgentProvision Permission = "agent_provision"
)

// CredentialKind distinguishes agent credentials from device credentials.
// The zero value ("") must be treated as CredentialKindAgent: records built
// on paths that never set a kind (legacy admin principal, pre-device tests)
// carry the zero value. Only kind checks against CredentialKindDevice gate
// device-only behavior.
type CredentialKind string

const (
	CredentialKindAgent  CredentialKind = "agent"
	CredentialKindDevice CredentialKind = "device"
)

type Principal struct {
	UserID               string
	MembershipID         string
	AgentID              string
	ScopeID              string
	CredentialID         string
	CredentialLabel      string
	Permissions          []Permission
	Kind                 CredentialKind
	GrantablePermissions []Permission
}

func (p Principal) HasPermission(permission Permission) bool {
	for _, current := range p.Permissions {
		if current == permission {
			return true
		}
	}
	return false
}

type EnrollmentRequest struct {
	UserID      string
	AgentID     string
	ExpiresIn   time.Duration
	Permissions []Permission
}

type Enrollment struct {
	ID        string
	Token     string
	ExpiresAt time.Time
}

type IssuedCredential struct {
	CredentialID string
	APIKey       string
	ExpiresAt    *time.Time
}

type Digest [32]byte

type EnrollmentRecord struct {
	ID                       string
	TokenDigest              Digest
	DigestKeyVersion         int16
	UserID                   string
	MembershipID             string
	AgentID                  string
	CredentialLabel          string
	Permissions              []Permission
	CreatedAt                time.Time
	ExpiresAt                time.Time
	CredentialExpiresAt      *time.Time
	ConsumedAt               *time.Time
	AllowLegacyAgentCreation bool
	Kind                     CredentialKind
	GrantablePermissions     []Permission
}

type CredentialRecord struct {
	ID                      string
	KeyDigest               Digest
	DigestKeyVersion        int16
	UserID                  string
	MembershipID            string
	AgentID                 string
	Label                   string
	Permissions             []Permission
	CreatedAt               time.Time
	ExpiresAt               *time.Time
	RevokedAt               *time.Time
	LastUsedAt              *time.Time
	RotatedFromCredentialID string
	Kind                    CredentialKind
	ProvisionedBy           string
	GrantablePermissions    []Permission
}

// ProvisionOutcome reports the store-side effects of a device-scoped agent
// provisioning transaction that the service cannot otherwise observe.
type ProvisionOutcome struct {
	RotatedFromCredentialID string
	AgentCreated            bool
}

// DeviceProvisionedAgent is one credential row issued for an agent a device
// credential provisions. A device that has rotated an agent's credential
// appears once per credential row (including revoked history), not once
// per agent, so callers can reconstruct the full provisioning timeline.
type DeviceProvisionedAgent struct {
	AgentID      string
	DisplayName  string
	AgentType    string
	AgentStatus  AgentStatus
	CredentialID string
	CreatedAt    time.Time
	RevokedAt    *time.Time
	LastUsedAt   *time.Time
}

type CredentialStore interface {
	LegacyAdminEnabled(context.Context) (bool, error)
	SaveEnrollment(context.Context, EnrollmentRecord) error
	ExchangeEnrollment(context.Context, string, Digest, CredentialRecord, time.Time) (EnrollmentRecord, error)
	ResolveCredential(context.Context, string, Digest, time.Time) (CredentialRecord, error)
	RotateCredential(context.Context, string, CredentialRecord, time.Time) error
	RevokeCredential(context.Context, string, time.Time) error
	// ProvisionAgentCredential creates or rotates an agent identity and
	// credential on behalf of a device credential (deviceCredentialID),
	// enforcing the device's active-agent cap (activeAgentLimit).
	ProvisionAgentCredential(context.Context, string, AgentProfile, CredentialRecord, int, time.Time) (ProvisionOutcome, error)
	// ListDeviceProvisionedAgents returns every credential row (including
	// revoked history) a device credential (identified by its credential ID)
	// has provisioned, ordered by agent ID then created_at DESC.
	ListDeviceProvisionedAgents(context.Context, string) ([]DeviceProvisionedAgent, error)
}
