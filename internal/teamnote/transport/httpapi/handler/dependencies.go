package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/recall"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

var ErrUnauthorized = errors.New("unauthorized")

const handlerContextKey = "team-memory.http-handler"

type ScopeResolver interface {
	ResolveScope(*app.RequestContext) (string, error)
}

// Handler adapts HTTP requests to one Team Note runtime instance.
type Handler struct {
	runtime      teamnote.Runtime
	resolver     ScopeResolver
	credentials  CredentialLifecycle
	memory       recall.Service
	channel      ChannelLifecycle
	identity     HumanIdentityLifecycle
	oidc         OIDCLifecycle
	registry     AgentRegistryLifecycle
	portalURL    string
	cookieSecure bool
	logger       *slog.Logger
}

type CredentialLifecycle interface {
	Authenticate(context.Context, string) (onprem.Principal, error)
	CreateEnrollment(context.Context, onprem.Principal, onprem.EnrollmentRequest) (onprem.Enrollment, error)
	ExchangeEnrollment(context.Context, string) (onprem.IssuedCredential, error)
	RotateCredential(context.Context, onprem.Principal) (onprem.IssuedCredential, error)
	RevokeCredential(context.Context, onprem.Principal, string) error
}

type ChannelLifecycle interface {
	Send(context.Context, onprem.Principal, onprem.SendEnvelopeRequest) (onprem.ChannelEnvelope, error)
	List(context.Context, onprem.Principal, onprem.ListEnvelopesFilter) ([]onprem.ChannelEnvelope, error)
	Get(context.Context, onprem.Principal, string) (onprem.ChannelEnvelope, error)
	Accept(context.Context, onprem.Principal, string) (onprem.ChannelEnvelope, error)
	Archive(context.Context, onprem.Principal, string) (onprem.ChannelEnvelope, error)
}

type HumanIdentityLifecycle interface {
	Login(context.Context, onprem.ExternalIdentity) (onprem.HumanSession, error)
	AuthenticateSession(context.Context, string) (onprem.HumanPrincipal, error)
	Logout(context.Context, string) error
	ClaimBootstrap(context.Context, onprem.HumanPrincipal, string) (onprem.HumanPrincipal, error)
	CreateInvitation(context.Context, onprem.HumanPrincipal, onprem.InvitationRequest) (onprem.Invitation, error)
	AcceptInvitation(context.Context, onprem.HumanPrincipal, string, string) (onprem.HumanPrincipal, error)
	ListInvitations(context.Context, onprem.HumanPrincipal, onprem.InvitationFilter) ([]onprem.Invitation, error)
	RevokeInvitation(context.Context, onprem.HumanPrincipal, string) (onprem.Invitation, error)
	ListMembers(context.Context, onprem.HumanPrincipal, onprem.MemberFilter) ([]onprem.Member, error)
	GetMember(context.Context, onprem.HumanPrincipal, string) (onprem.Member, error)
	UpdateMember(context.Context, onprem.HumanPrincipal, string, onprem.UpdateMemberRequest) (onprem.Member, error)
	ListAuditEvents(context.Context, onprem.HumanPrincipal, onprem.AuditFilter) ([]onprem.AuditEvent, error)
	GetAuditEvent(context.Context, onprem.HumanPrincipal, int64) (onprem.AuditEvent, error)
}

type OIDCLifecycle interface {
	BeginLogin() (onprem.OIDCFlow, error)
	CompleteLogin(context.Context, string, string, string) (onprem.ExternalIdentity, error)
}

type AgentRegistryLifecycle interface {
	CreateAgent(context.Context, onprem.HumanPrincipal, onprem.CreateAgentRequest) (onprem.AgentProfile, error)
	ListOwnedAgents(context.Context, onprem.HumanPrincipal, onprem.AgentFilter) ([]onprem.AgentProfile, error)
	GetOwnedAgent(context.Context, onprem.HumanPrincipal, string) (onprem.AgentProfile, error)
	UpdateOwnedAgent(context.Context, onprem.HumanPrincipal, string, onprem.UpdateAgentRequest) (onprem.AgentProfile, error)
	RetireOwnedAgent(context.Context, onprem.HumanPrincipal, string, int64, string) (onprem.AgentProfile, error)
	CreateEnrollment(context.Context, onprem.HumanPrincipal, string, onprem.OwnerEnrollmentRequest) (onprem.Enrollment, error)
	ListEnrollments(context.Context, onprem.HumanPrincipal, string, onprem.AgentArtifactFilter) ([]onprem.AgentEnrollmentMetadata, error)
	RevokeEnrollment(context.Context, onprem.HumanPrincipal, string, string, string) (onprem.AgentEnrollmentMetadata, error)
	ListCredentials(context.Context, onprem.HumanPrincipal, string, onprem.AgentArtifactFilter) ([]onprem.AgentCredentialMetadata, error)
	RevokeOwnedCredential(context.Context, onprem.HumanPrincipal, string, string, string) (onprem.AgentCredentialMetadata, error)
	ListDirectoryAgents(context.Context, onprem.Principal, onprem.AgentFilter) ([]onprem.AgentProfile, error)
	GetDirectoryAgent(context.Context, onprem.Principal, string) (onprem.AgentProfile, error)
	ListAdminAgents(context.Context, onprem.HumanPrincipal, onprem.AgentFilter) ([]onprem.AgentProfile, error)
	GetAdminAgent(context.Context, onprem.HumanPrincipal, string) (onprem.AgentProfile, error)
	UpdateAdminAgent(context.Context, onprem.HumanPrincipal, string, onprem.UpdateAgentRequest) (onprem.AgentProfile, error)
	RetireAdminAgent(context.Context, onprem.HumanPrincipal, string, int64, string) (onprem.AgentProfile, error)
	TransferAgent(context.Context, onprem.HumanPrincipal, string, onprem.TransferAgentRequest) (onprem.AgentProfile, error)
	ListAdminEnrollments(context.Context, onprem.HumanPrincipal, string, onprem.AgentArtifactFilter) ([]onprem.AgentEnrollmentMetadata, error)
	RevokeAdminEnrollment(context.Context, onprem.HumanPrincipal, string, string, string) (onprem.AgentEnrollmentMetadata, error)
	ListAdminCredentials(context.Context, onprem.HumanPrincipal, string, onprem.AgentArtifactFilter) ([]onprem.AgentCredentialMetadata, error)
	RevokeAdminCredential(context.Context, onprem.HumanPrincipal, string, string, string) (onprem.AgentCredentialMetadata, error)
}

type OnPremOption func(*Handler) error

func WithAgentRegistry(registry AgentRegistryLifecycle) OnPremOption {
	return func(configured *Handler) error {
		if registry == nil {
			return fmt.Errorf("configure agent registry: registry is required")
		}
		configured.registry = registry
		return nil
	}
}

func WithHumanIdentity(
	identity HumanIdentityLifecycle,
	oidc OIDCLifecycle,
	portalURL string,
	cookieSecure bool,
) OnPremOption {
	return func(configured *Handler) error {
		if identity == nil || oidc == nil || strings.TrimSpace(portalURL) == "" {
			return fmt.Errorf("configure human identity: identity, OIDC, and portal URL are required")
		}
		configured.identity = identity
		configured.oidc = oidc
		configured.portalURL = strings.TrimSpace(portalURL)
		configured.cookieSecure = cookieSecure
		return nil
	}
}

func New(runtime teamnote.Runtime, resolver ScopeResolver, logger *slog.Logger) (*Handler, error) {
	if runtime == nil || resolver == nil || logger == nil {
		return nil, fmt.Errorf("create HTTP handler: runtime, scope resolver, and logger are required")
	}
	return &Handler{runtime: runtime, resolver: resolver, logger: logger}, nil
}

func NewOnPrem(
	runtime teamnote.Runtime,
	credentials CredentialLifecycle,
	memory recall.Service,
	channel ChannelLifecycle,
	logger *slog.Logger,
	options ...OnPremOption,
) (*Handler, error) {
	if runtime == nil || credentials == nil || memory == nil || channel == nil || logger == nil {
		return nil, fmt.Errorf("create on-prem HTTP handler: runtime, credentials, memory, channel, and logger are required")
	}
	configured := &Handler{
		runtime: runtime, resolver: StaticAPIKeys{}, credentials: credentials, memory: memory, channel: channel, logger: logger,
	}
	for _, option := range options {
		if err := option(configured); err != nil {
			return nil, fmt.Errorf("create on-prem HTTP handler: %w", err)
		}
	}
	return configured, nil
}

// InstanceMiddleware binds a handler to requests served by one Hertz instance.
func InstanceMiddleware(handler *Handler) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		request.Set(handlerContextKey, handler)
		request.Next(ctx)
	}
}

func handlerFromRequest(request *app.RequestContext) (*Handler, bool) {
	configured, found := request.Get(handlerContextKey)
	if !found {
		return nil, false
	}
	handler, ok := configured.(*Handler)
	return handler, ok && handler != nil
}

type StaticAPIKeys map[string]string

func (keys StaticAPIKeys) ResolveScope(request *app.RequestContext) (string, error) {
	key, err := bearerKey(request)
	if err != nil {
		return "", ErrUnauthorized
	}
	scopeID := strings.TrimSpace(keys[key])
	if scopeID == "" {
		return "", ErrUnauthorized
	}
	return scopeID, nil
}

func bearerKey(request *app.RequestContext) (string, error) {
	authorization := strings.TrimSpace(string(request.GetHeader("Authorization")))
	const prefix = "Bearer "
	if !strings.HasPrefix(authorization, prefix) {
		return "", ErrUnauthorized
	}
	key := strings.TrimSpace(strings.TrimPrefix(authorization, prefix))
	if key == "" {
		return "", ErrUnauthorized
	}
	return key, nil
}
