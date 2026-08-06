package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/pax-beehive/pax-nexus/internal/audit"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/deployment/saas"
	"github.com/pax-beehive/pax-nexus/internal/explorer"
	"github.com/pax-beehive/pax-nexus/internal/operations"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/sessionconsumer"
	platformllm "github.com/pax-beehive/pax-nexus/internal/platform/llm"
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
	teams        TeamLifecycle
	operations   OperationsLifecycle
	explorer     ExplorerLifecycle
	wikiControl  WikiControl
	wikiSettings WikiSettings
	llmUsage     LLMUsage
	sessionAudit SessionAuditQuery
	recorder     operations.Recorder
	readiness    ReadinessCheck
	portalURL    string
	cookieSecure bool
	logger       *slog.Logger
}

// ReadinessCheck reports whether the process can serve traffic. It is
// expected to be cheap (a store round-trip, not a query) because load
// balancers call it continuously; a nil error means ready.
type ReadinessCheck func(context.Context) error

type CredentialLifecycle interface {
	Authenticate(context.Context, string) (onprem.Principal, error)
	CreateEnrollment(context.Context, onprem.Principal, onprem.EnrollmentRequest) (onprem.Enrollment, error)
	ExchangeEnrollment(context.Context, string) (onprem.IssuedCredential, error)
	RotateCredential(context.Context, onprem.Principal) (onprem.IssuedCredential, error)
	RevokeCredential(context.Context, onprem.Principal, string) error
	ProvisionDeviceAgent(context.Context, onprem.Principal, onprem.DeviceProvisionRequest) (onprem.ProvisionedAgentCredential, error)
	ListDeviceProvisionedAgents(context.Context, onprem.Principal) ([]onprem.DeviceProvisionedAgent, error)
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

// TeamLifecycle is the SaaS multi-team control plane surface behind
// /v1/teams and /v1/me/current-team. Only the SaaS profile wires it;
// saas.ControlPlane satisfies it structurally. /v1/me also consults it to
// populate the teams payload, so a wired TeamLifecycle doubles as the
// profile marker for team-aware responses.
type TeamLifecycle interface {
	CreateTeam(context.Context, onprem.HumanPrincipal, string, string) (saas.Team, error)
	ListTeams(context.Context, onprem.HumanPrincipal) ([]saas.TeamSummary, error)
	SwitchTeam(context.Context, onprem.HumanPrincipal, string) (onprem.HumanPrincipal, error)
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
	CreateDeviceEnrollment(context.Context, onprem.HumanPrincipal, onprem.DeviceEnrollmentRequest) (onprem.Enrollment, []onprem.Permission, error)
	RevokeDevice(context.Context, onprem.HumanPrincipal, string, string) (onprem.DeviceSummary, error)
	ListDevices(context.Context, onprem.HumanPrincipal, onprem.DeviceFilter) ([]onprem.DeviceSummary, error)
	GetDevice(context.Context, onprem.HumanPrincipal, string) (onprem.DeviceDetail, error)
	// ListExpiringEnrollments is the Overview endpoint's team-wide read of
	// pending/expired one-time enrollment tokens; owner/admin only, like the
	// device listing above — not the per-agent enrollment listing.
	ListExpiringEnrollments(context.Context, onprem.HumanPrincipal, time.Time, int) ([]onprem.AgentEnrollmentMetadata, error)
}

type OperationsLifecycle interface {
	Summary(context.Context, onprem.HumanPrincipal, operations.TimeFilter) (operations.Summary, error)
	ListEvents(context.Context, onprem.HumanPrincipal, operations.EventFilter) ([]operations.Event, error)
	GetRecallDiagnostic(context.Context, onprem.HumanPrincipal, int64) (operations.RecallDiagnostic, error)
	LatestStorage(context.Context, onprem.HumanPrincipal) (operations.StorageSnapshot, error)
	ListStorage(context.Context, onprem.HumanPrincipal, operations.StorageFilter) ([]operations.StorageSnapshot, error)
	AgentStats(context.Context, onprem.HumanPrincipal, operations.TimeFilter) (operations.AgentStatsReport, error)
	// Series is the Overview endpoint's bucketed throughput read. The bucket
	// duration is always server-derived from the requested window, never
	// client-supplied.
	Series(context.Context, onprem.HumanPrincipal, operations.TimeFilter, time.Duration) ([]operations.SeriesBucket, error)
}

type ExplorerLifecycle interface {
	ListTeamNotes(context.Context, onprem.HumanPrincipal, explorer.TeamNoteFilter) ([]explorer.TeamNoteSummary, error)
	GetTeamNote(context.Context, onprem.HumanPrincipal, string) (explorer.TeamNoteDetail, error)
	GetExtractionDiagnostic(context.Context, onprem.HumanPrincipal, string) (explorer.ExtractionDiagnostic, error)
	GetChannelDiagnostic(context.Context, onprem.HumanPrincipal, string) (explorer.ChannelDiagnostic, error)
	// NoteMix is the Overview endpoint's live-note breakdown by kind.
	NoteMix(context.Context, onprem.HumanPrincipal, time.Time) ([]explorer.NoteKindCount, error)
}

type WikiControl interface {
	Status(context.Context, string) (sessionconsumer.Status, error)
	SetAutoInject(context.Context, string, bool) (sessionconsumer.Status, error)
	InjectSession(context.Context, string, string) (sessionconsumer.InjectResult, error)
	Rebuild(context.Context, string, time.Time) (sessionconsumer.Status, error)
}

type WikiSettings interface {
	GenerationSettings(context.Context, string) (pagewiki.GenerationDirectives, error)
	SetGenerationSettings(context.Context, string, pagewiki.GenerationDirectives) (pagewiki.GenerationDirectives, error)
}

type LLMUsage interface {
	UsageSummary(ctx context.Context, scopeID string, window time.Duration) ([]platformllm.LLMUsageRow, error)
}

// SessionAuditQuery is the read-only capability over the session audit
// projections; it is the handler-facing subset of audit.Query.
type SessionAuditQuery interface {
	ListToolCalls(context.Context, audit.ToolCallFilter) ([]audit.ToolCall, error)
	ListFindings(context.Context, audit.FindingFilter) ([]audit.Finding, error)
	GetActivityDaily(context.Context, audit.ActivityFilter) ([]audit.ActivityDaily, error)
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

// WithTeams wires the SaaS team control plane surface. Without it the team
// endpoints answer 501 and /v1/me omits the team fields, which is exactly
// the on-prem profile's behavior.
func WithTeams(teams TeamLifecycle) OnPremOption {
	return func(configured *Handler) error {
		if teams == nil {
			return fmt.Errorf("configure teams: team lifecycle is required")
		}
		configured.teams = teams
		return nil
	}
}

func WithOperations(service OperationsLifecycle, recorder operations.Recorder) OnPremOption {
	return func(configured *Handler) error {
		if service == nil || recorder == nil {
			return fmt.Errorf("configure operations: service and recorder are required")
		}
		configured.operations = service
		configured.recorder = recorder
		return nil
	}
}

func WithExplorer(service ExplorerLifecycle) OnPremOption {
	return func(configured *Handler) error {
		if service == nil {
			return fmt.Errorf("configure explorer: service is required")
		}
		configured.explorer = service
		return nil
	}
}

func WithWikiControl(control WikiControl) OnPremOption {
	return func(configured *Handler) error {
		if control == nil {
			return fmt.Errorf("configure Wiki control: control is required")
		}
		configured.wikiControl = control
		return nil
	}
}

func WithWikiSettings(settings WikiSettings) OnPremOption {
	return func(configured *Handler) error {
		configured.wikiSettings = settings
		return nil
	}
}

func WithLLMUsage(usage LLMUsage) OnPremOption {
	return func(configured *Handler) error {
		configured.llmUsage = usage
		return nil
	}
}

func WithSessionAudit(query SessionAuditQuery) OnPremOption {
	return func(configured *Handler) error {
		if query == nil {
			return fmt.Errorf("configure session audit: query is required")
		}
		configured.sessionAudit = query
		return nil
	}
}

// WithReadinessCheck wires the probe behind GET /readyz. Without it the
// endpoint reports ready unconditionally, matching /healthz — a deployment
// with no store to check has nothing to wait for.
func WithReadinessCheck(check ReadinessCheck) OnPremOption {
	return func(configured *Handler) error {
		if check == nil {
			return fmt.Errorf("configure readiness check: check is required")
		}
		configured.readiness = check
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
	// channel is deliberately optional: profiles without a channel surface
	// (the SaaS profile) wire nil and the channel endpoints answer 501,
	// matching every other unwired option.
	if runtime == nil || credentials == nil || memory == nil || logger == nil {
		return nil, fmt.Errorf("create on-prem HTTP handler: runtime, credentials, memory, and logger are required")
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
