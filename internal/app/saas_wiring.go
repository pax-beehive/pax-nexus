package app

// SaaS profile wiring: the identity services behind the SaaS HTTP transport,
// the per-request scope adapters that un-pin operations/explorer from the
// on-prem scope, and the Page Wiki request scope resolver. Everything here
// is constructed only when the process runs the SaaS profile; the on-prem
// wiring paths in wiring.go are unchanged.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/deployment/saas"
	"github.com/pax-beehive/pax-nexus/internal/explorer"
	"github.com/pax-beehive/pax-nexus/internal/operations"
	pagewikihttp "github.com/pax-beehive/pax-nexus/internal/pagewiki/transport/httpapi"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/pax-beehive/pax-nexus/internal/recall"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/handler"
)

// SaaS session/invitation TTLs mirror the on-prem identity configuration;
// they are constants, not environment settings, in both profiles.
const (
	saasSessionTTL    = 12 * time.Hour
	saasInvitationTTL = 24 * time.Hour
)

// humanSessionCookieName duplicates the team-memory handler's session cookie
// name (unexported there) so the Page Wiki scope resolver can authenticate
// browser requests against the same session the team endpoints issued.
const humanSessionCookieName = "tm_human_session"

// saasIdentityServices bundles the SaaS control plane services shared by the
// team-memory transport, the Page Wiki scope resolver, and the Todo App
// authenticator.
type saasIdentityServices struct {
	controlPlane *saas.ControlPlane
	registry     *saas.Registry
	credentials  *saas.Credentials
	oidc         handler.OIDCLifecycle
}

func buildSaaSIdentityServices(
	ctx context.Context,
	store *postgres.Store,
	config applicationConfig,
) (*saasIdentityServices, error) {
	if store == nil {
		return nil, fmt.Errorf("configure saas identity services: postgres store is required")
	}
	controlPlane, err := saas.NewControlPlane(store.SaaSIdentity(), store.SaaSIdentity(), saas.ControlPlaneConfig{
		SecretPepper: config.secretPepper, SessionTTL: saasSessionTTL, InvitationTTL: saasInvitationTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("configure saas control plane: %w", err)
	}
	registry, err := saas.NewRegistry(store.SaaSRegistry(), store.SaaSCredentials(), saas.RegistryConfig{
		SecretPepper: config.secretPepper, MemberGrantablePermissions: config.memberGrantablePermissions,
		PortalURL: config.portalURL,
	})
	if err != nil {
		return nil, fmt.Errorf("configure saas agent registry: %w", err)
	}
	credentials, err := saas.NewCredentials(store.SaaSCredentials(), saas.CredentialConfig{
		RotationOverlap: config.credentialRotationOverlap, SecretPepper: config.secretPepper,
		DeviceAgentLimit: config.deviceAgentLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("configure saas credentials: %w", err)
	}
	oidcAuthenticator, err := onprem.NewOIDCAuthenticator(ctx, onprem.OIDCConfig{
		Issuer: config.oidcIssuer, ClientID: config.oidcClientID,
		ClientSecret: config.oidcClientSecret, RedirectURL: config.oidcRedirectURL,
		FlowSecret:              config.oidcFlowSecret,
		AuthorizationParameters: config.oidcAuthorizationParameters,
		IdentitySource:          onprem.OIDCIdentitySource(config.oidcIdentitySource),
	})
	if err != nil {
		return nil, fmt.Errorf("configure saas OIDC: %w", err)
	}
	return &saasIdentityServices{
		controlPlane: controlPlane, registry: registry, credentials: credentials, oidc: oidcAuthenticator,
	}, nil
}

// buildSaaSHTTPHandler configures the team-memory HTTP transport for the
// SaaS profile: the same routes as on-prem, backed by the saas control
// plane, with operations/explorer resolved per request scope and no channel
// lifecycle (device/channel flows are on-prem only and answer 501).
func buildSaaSHTTPHandler(
	ctx context.Context,
	runtime teamnote.Runtime,
	store *postgres.Store,
	operationRecorder operations.Recorder,
	usageStore *postgres.LLMUsageStore,
	config applicationConfig,
	logger *slog.Logger,
	wikiControl handler.WikiControl,
	wikiSettings handler.WikiSettings,
	sessionAudit handler.SessionAuditQuery,
	services *saasIdentityServices,
) (*handler.Handler, error) {
	if store == nil {
		return nil, fmt.Errorf("configure saas HTTP transport: postgres store is required")
	}
	if operationRecorder == nil {
		return nil, fmt.Errorf("configure saas HTTP transport: operations recorder is required")
	}
	if services == nil {
		return nil, fmt.Errorf("configure saas HTTP transport: saas identity services are required")
	}
	recallRouter, err := recall.NewRouter(runtime, nil, recall.Config{EnablePassiveWikiHint: config.wikiHintEnabled})
	if err != nil {
		return nil, fmt.Errorf("configure recall router: %w", err)
	}
	operationsService, err := newScopedOperationsService(store, onprem.OperationsConfig{
		EventRetention: config.operationsEventRetention, StorageRetention: config.operationsStorageRetention,
	})
	if err != nil {
		return nil, fmt.Errorf("configure saas operations: %w", err)
	}
	options := []handler.OnPremOption{
		handler.WithAgentRegistry(services.registry),
		handler.WithOperations(operationsService, operationRecorder),
		handler.WithExplorer(newScopedExplorerService(store)),
		handler.WithTeams(services.controlPlane),
		handler.WithHumanIdentity(services.controlPlane, services.oidc, config.portalURL, config.humanCookieSecure),
		handler.WithReadinessCheck(storeReadinessCheck(store)),
	}
	if usageStore != nil {
		options = append(options, handler.WithLLMUsage(usageStore))
	}
	if wikiControl != nil {
		options = append(options, handler.WithWikiControl(wikiControl))
	}
	if wikiSettings != nil {
		options = append(options, handler.WithWikiSettings(wikiSettings))
	}
	if sessionAudit != nil {
		options = append(options, handler.WithSessionAudit(sessionAudit))
	}
	configured, err := handler.NewOnPrem(runtime, services.credentials, recallRouter, nil, logger, options...)
	if err != nil {
		return nil, fmt.Errorf("configure saas HTTP transport: %w", err)
	}
	return configured, nil
}

// onPremWikiScope pins the Page Wiki transport to the on-prem scope, the
// single-tenant profile's deliberate behavior.
func onPremWikiScope(context.Context, *app.RequestContext) (string, error) {
	return onprem.LocalScopeID, nil
}

// saasWikiScopeResolver resolves a Page Wiki request's team from its
// authenticated principal: a human session cookie first (browser traffic),
// then a Bearer agent credential (agent traffic, e.g. wiki injection). A
// request carrying neither resolves to ErrScopeUnresolved, which the
// transport maps to 401. A principal with no current team (signed up but in
// no team) also cannot read any wiki and resolves the same way.
func saasWikiScopeResolver(
	services *saasIdentityServices,
) func(ctx context.Context, request *app.RequestContext) (string, error) {
	return func(ctx context.Context, request *app.RequestContext) (string, error) {
		if token := strings.TrimSpace(string(request.Cookie(humanSessionCookieName))); token != "" {
			principal, err := services.controlPlane.AuthenticateSession(ctx, token)
			if err == nil && strings.TrimSpace(principal.ScopeID) != "" {
				return principal.ScopeID, nil
			}
		}
		if key := bearerCredential(request); key != "" {
			principal, err := services.credentials.Authenticate(ctx, key)
			if err == nil && strings.TrimSpace(principal.ScopeID) != "" {
				return principal.ScopeID, nil
			}
		}
		return "", pagewikihttp.ErrScopeUnresolved
	}
}

// bearerCredential extracts a Bearer API key from the Authorization header,
// mirroring the team-memory handler's bearer parsing.
func bearerCredential(request *app.RequestContext) string {
	authorization := strings.TrimSpace(string(request.GetHeader("Authorization")))
	const prefix = "Bearer "
	if !strings.HasPrefix(authorization, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(authorization, prefix))
}

// scopedOperationsService implements handler.OperationsLifecycle for the
// SaaS profile by delegating each call to an on-prem operations service
// bound to the requesting principal's team scope. The authorization matrix
// stays inside the delegated service, identical to on-prem.
type scopedOperationsService struct {
	store  *postgres.Store
	config onprem.OperationsConfig
}

func newScopedOperationsService(
	store *postgres.Store,
	config onprem.OperationsConfig,
) (*scopedOperationsService, error) {
	if store == nil || config.EventRetention <= 0 || config.StorageRetention <= 0 {
		return nil, fmt.Errorf("create scoped operations service: store and positive retention are required")
	}
	return &scopedOperationsService{store: store, config: config}, nil
}

func (s *scopedOperationsService) forPrincipal(
	principal onprem.HumanPrincipal,
) (*onprem.OperationsService, error) {
	if strings.TrimSpace(principal.ScopeID) == "" {
		return nil, onprem.ErrForbidden
	}
	return onprem.NewOperationsService(s.store.Operations(principal.ScopeID), s.config)
}

func (s *scopedOperationsService) Summary(
	ctx context.Context, principal onprem.HumanPrincipal, filter operations.TimeFilter,
) (operations.Summary, error) {
	service, err := s.forPrincipal(principal)
	if err != nil {
		return operations.Summary{}, err
	}
	return service.Summary(ctx, principal, filter)
}

func (s *scopedOperationsService) ListEvents(
	ctx context.Context, principal onprem.HumanPrincipal, filter operations.EventFilter,
) ([]operations.Event, error) {
	service, err := s.forPrincipal(principal)
	if err != nil {
		return nil, err
	}
	return service.ListEvents(ctx, principal, filter)
}

func (s *scopedOperationsService) GetRecallDiagnostic(
	ctx context.Context, principal onprem.HumanPrincipal, observationID int64,
) (operations.RecallDiagnostic, error) {
	service, err := s.forPrincipal(principal)
	if err != nil {
		return operations.RecallDiagnostic{}, err
	}
	return service.GetRecallDiagnostic(ctx, principal, observationID)
}

func (s *scopedOperationsService) LatestStorage(
	ctx context.Context, principal onprem.HumanPrincipal,
) (operations.StorageSnapshot, error) {
	service, err := s.forPrincipal(principal)
	if err != nil {
		return operations.StorageSnapshot{}, err
	}
	return service.LatestStorage(ctx, principal)
}

func (s *scopedOperationsService) ListStorage(
	ctx context.Context, principal onprem.HumanPrincipal, filter operations.StorageFilter,
) ([]operations.StorageSnapshot, error) {
	service, err := s.forPrincipal(principal)
	if err != nil {
		return nil, err
	}
	return service.ListStorage(ctx, principal, filter)
}

func (s *scopedOperationsService) AgentStats(
	ctx context.Context, principal onprem.HumanPrincipal, filter operations.TimeFilter,
) (operations.AgentStatsReport, error) {
	service, err := s.forPrincipal(principal)
	if err != nil {
		return operations.AgentStatsReport{}, err
	}
	return service.AgentStats(ctx, principal, filter)
}

func (s *scopedOperationsService) Series(
	ctx context.Context, principal onprem.HumanPrincipal, filter operations.TimeFilter, bucket time.Duration,
) ([]operations.SeriesBucket, error) {
	service, err := s.forPrincipal(principal)
	if err != nil {
		return nil, err
	}
	return service.Series(ctx, principal, filter, bucket)
}

// scopedExplorerService implements handler.ExplorerLifecycle for the SaaS
// profile with the same per-call scope delegation as
// scopedOperationsService.
type scopedExplorerService struct {
	store *postgres.Store
}

func newScopedExplorerService(store *postgres.Store) *scopedExplorerService {
	return &scopedExplorerService{store: store}
}

func (s *scopedExplorerService) forPrincipal(principal onprem.HumanPrincipal) (*onprem.ExplorerService, error) {
	if strings.TrimSpace(principal.ScopeID) == "" {
		return nil, onprem.ErrForbidden
	}
	return onprem.NewExplorerService(s.store.Explorer(principal.ScopeID))
}

func (s *scopedExplorerService) ListTeamNotes(
	ctx context.Context, principal onprem.HumanPrincipal, filter explorer.TeamNoteFilter,
) ([]explorer.TeamNoteSummary, error) {
	service, err := s.forPrincipal(principal)
	if err != nil {
		return nil, err
	}
	return service.ListTeamNotes(ctx, principal, filter)
}

func (s *scopedExplorerService) GetTeamNote(
	ctx context.Context, principal onprem.HumanPrincipal, noteID string,
) (explorer.TeamNoteDetail, error) {
	service, err := s.forPrincipal(principal)
	if err != nil {
		return explorer.TeamNoteDetail{}, err
	}
	return service.GetTeamNote(ctx, principal, noteID)
}

func (s *scopedExplorerService) GetExtractionDiagnostic(
	ctx context.Context, principal onprem.HumanPrincipal, noteID string,
) (explorer.ExtractionDiagnostic, error) {
	service, err := s.forPrincipal(principal)
	if err != nil {
		return explorer.ExtractionDiagnostic{}, err
	}
	return service.GetExtractionDiagnostic(ctx, principal, noteID)
}

func (s *scopedExplorerService) GetChannelDiagnostic(
	ctx context.Context, principal onprem.HumanPrincipal, envelopeID string,
) (explorer.ChannelDiagnostic, error) {
	service, err := s.forPrincipal(principal)
	if err != nil {
		return explorer.ChannelDiagnostic{}, err
	}
	return service.GetChannelDiagnostic(ctx, principal, envelopeID)
}

func (s *scopedExplorerService) NoteMix(
	ctx context.Context, principal onprem.HumanPrincipal, at time.Time,
) ([]explorer.NoteKindCount, error) {
	service, err := s.forPrincipal(principal)
	if err != nil {
		return nil, err
	}
	return service.NoteMix(ctx, principal, at)
}

func (s *scopedExplorerService) CountExpiringNotes(
	ctx context.Context, principal onprem.HumanPrincipal, at time.Time, within time.Duration,
) (int64, error) {
	service, err := s.forPrincipal(principal)
	if err != nil {
		return 0, err
	}
	return service.CountExpiringNotes(ctx, principal, at, within)
}
