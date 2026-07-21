// Package onprem owns single-Team deployment identity and credential lifecycle.
package onprem

import (
	"context"
	"errors"
	"time"
)

const LocalScopeID = "local-team"

var (
	ErrUnauthorized          = errors.New("unauthorized")
	ErrForbidden             = errors.New("forbidden")
	ErrEnrollmentInvalid     = errors.New("enrollment is invalid or expired")
	ErrCredentialNotFound    = errors.New("credential not found")
	ErrTargetAgentNotFound   = errors.New("target agent not found")
	ErrAgentIdentityConflict = errors.New("agent identity is ambiguous")
	ErrEnvelopeNotFound      = errors.New("channel envelope not found")
	ErrEnvelopeState         = errors.New("channel envelope state does not allow the operation")
	ErrIdempotencyConflict   = errors.New("idempotency key was already used for a different envelope")
	ErrInvalidChannelRequest = errors.New("invalid channel request")
)

type Permission string

const (
	PermissionAdmin          Permission = "admin"
	PermissionObserve        Permission = "observe"
	PermissionSearch         Permission = "search"
	PermissionGet            Permission = "get"
	PermissionChannelSend    Permission = "channel_send"
	PermissionChannelReceive Permission = "channel_receive"
)

type Principal struct {
	UserID       string
	AgentID      string
	ScopeID      string
	CredentialID string
	Permissions  []Permission
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
	ID          string
	TokenDigest Digest
	UserID      string
	AgentID     string
	Permissions []Permission
	CreatedAt   time.Time
	ExpiresAt   time.Time
	ConsumedAt  *time.Time
}

type CredentialRecord struct {
	ID          string
	KeyDigest   Digest
	UserID      string
	AgentID     string
	Permissions []Permission
	CreatedAt   time.Time
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
	LastUsedAt  *time.Time
}

type CredentialStore interface {
	SaveEnrollment(context.Context, EnrollmentRecord) error
	ExchangeEnrollment(context.Context, Digest, CredentialRecord, time.Time) (EnrollmentRecord, error)
	ResolveCredential(context.Context, Digest, time.Time) (CredentialRecord, error)
	RotateCredential(context.Context, string, CredentialRecord, time.Time) error
	RevokeCredential(context.Context, string, time.Time) error
}
