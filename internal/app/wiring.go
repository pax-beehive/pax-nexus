package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/audit"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/evidencelake"
	"github.com/pax-beehive/pax-nexus/internal/operations"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	pagewikipostgres "github.com/pax-beehive/pax-nexus/internal/pagewiki/postgres"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/sessionconsumer"
	pagewikihttp "github.com/pax-beehive/pax-nexus/internal/pagewiki/transport/httpapi"
	platformllm "github.com/pax-beehive/pax-nexus/internal/platform/llm"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/pax-beehive/pax-nexus/internal/platform/textembedding"
	"github.com/pax-beehive/pax-nexus/internal/recall"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/handler"
	"github.com/pax-beehive/pax-nexus/internal/todoapp"
	todoapppostgres "github.com/pax-beehive/pax-nexus/internal/todoapp/postgres"
	todoapphttp "github.com/pax-beehive/pax-nexus/internal/todoapp/transport/httpapi"
)

func buildPageWikiHTTPHandler(
	ctx context.Context,
	store *postgres.Store,
	embedder textembedding.Embedder,
	usageStore *postgres.LLMUsageStore,
	config applicationConfig,
	logger *slog.Logger,
) (*pagewikihttp.Handler, *pagewiki.ServiceManager, *sessionconsumer.Controller, handler.WikiSettings, error) {
	treeMaxDepth, err := parseTreeMaxDepth(config.llmwikiTreeMaxDepth)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf(
			"initialize Page Wiki repository: %w", err,
		)
	}
	repositoryOptions := make([]memory.Option, 0, 1)
	if treeMaxDepth > 0 {
		repositoryOptions = append(repositoryOptions, memory.WithTopicTreeMaxDepth(treeMaxDepth))
	}
	repositoryManager, err := pagewikipostgres.NewRepositoryManager(store.Pool(), repositoryOptions...)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("initialize Page Wiki repository: %w", err)
	}
	// Eagerly hydrate the on-prem scope at boot, preserving today's
	// hydrate-at-boot fail-fast behavior. The session consumer, and the Page
	// Wiki HTTP transport below, both resolve every request's scope through
	// the managers; this eager call only exists to fail fast at startup and
	// pre-warm the manager cache so the transport's per-request resolution
	// below is a cache hit.
	if _, err := repositoryManager.ForScope(ctx, onprem.LocalScopeID); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("initialize Page Wiki repository: %w", err)
	}
	planner, editor, navigator, curator, err := buildPageWikiMaintainers(usageStore, config, logger)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	options := make([]pagewiki.ServiceOption, 0, 2)
	if navigator != nil {
		options = append(options, pagewiki.WithTreeNavigator(pagewiki.TreeMaintenanceConfig{
			Navigator: navigator, MaxDepth: treeMaxDepth, Logger: logger,
		}))
	}
	if curator != nil {
		curationConfig, err := buildPageWikiCurationConfig(config)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		options = append(options, pagewiki.WithCurator(curator, embedder, curationConfig, logger))
	}
	serviceManager, err := pagewiki.NewServiceManager(
		func(ctx context.Context, scopeID string) (pagewiki.Repository, error) {
			return repositoryManager.ForScope(ctx, scopeID)
		},
		pagewiki.ServiceManagerConfig{Planner: planner, Editor: editor, Options: options},
	)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("initialize Page Wiki service manager: %w", err)
	}
	// Same eager-hydrate/fail-fast/cache-warm rationale as the repository
	// above.
	if _, err := serviceManager.ForScope(ctx, onprem.LocalScopeID); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("initialize Page Wiki service: %w", err)
	}
	consumerStore, err := postgres.NewPageWikiConsumerStore(store.Pool())
	if err != nil {
		return nil, nil, nil, nil, err
	}
	// The consumer sweeps every scope from one loop, resolving each stream's
	// service and each rebuild's repository through the managers above.
	controller, err := sessionconsumer.New(
		consumerStore,
		func(ctx context.Context, scopeID string) (sessionconsumer.Injector, error) {
			return serviceManager.ForScope(ctx, scopeID)
		},
		func(ctx context.Context, scopeID string) (sessionconsumer.Rebuilder, error) {
			return repositoryManager.ForScope(ctx, scopeID)
		},
		logger, 2*time.Second,
	)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	// The transport resolves its injector/reader pair fresh on every
	// request through the managers above, pinned to the on-prem scope; this
	// is the deliberate Phase 2 profile pin. Per-request tenant resolution
	// on this transport arrives with Phase 3 auth.
	configured, err := pagewikihttp.New(
		func(ctx context.Context) (pagewikihttp.Injector, pagewikihttp.Reader, error) {
			service, err := serviceManager.ForScope(ctx, onprem.LocalScopeID)
			if err != nil {
				return nil, nil, err
			}
			repository, err := repositoryManager.ForScope(ctx, onprem.LocalScopeID)
			if err != nil {
				return nil, nil, err
			}
			return service, repository, nil
		},
	)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("initialize Page Wiki HTTP handler: %w", err)
	}
	// Neither the service manager's maintenance loops nor the consumer are
	// started here: Run starts them once every build step has succeeded, so
	// a later build failure never leaves background goroutines running.
	return configured, serviceManager, controller, wikiSettingsAdapter{services: serviceManager}, nil
}

// buildPageWikiCurationConfig parses the LLMWIKI_CURATION_* environment into
// a pagewiki.CurationConfig, mirroring the LLMWIKI_TREE_MAX_DEPTH validation
// style: LLMWIKI_CURATION_INTERVAL is a duration (empty defaults to 24h;
// "0" explicitly disables the background loop; negative values are a
// startup error). The two limits are non-negative integers (empty or an
// explicit "0" both fall through to the package defaults inside
// pagewiki.WithCurator; only a parse error or a negative value is rejected).
func buildPageWikiCurationConfig(config applicationConfig) (pagewiki.CurationConfig, error) {
	interval := 24 * time.Hour
	if raw := strings.TrimSpace(config.llmwikiCurationInterval); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed < 0 {
			return pagewiki.CurationConfig{}, fmt.Errorf(
				"initialize Page Wiki curation: LLMWIKI_CURATION_INTERVAL must be a non-negative duration, got %q",
				raw,
			)
		}
		interval = parsed
	}
	pairLimit, err := parseNonNegativeEnvironment(config.llmwikiCurationPairLimit, "LLMWIKI_CURATION_PAIR_LIMIT")
	if err != nil {
		return pagewiki.CurationConfig{}, err
	}
	pageLimit, err := parseNonNegativeEnvironment(config.llmwikiCurationPageLimit, "LLMWIKI_CURATION_PAGE_LIMIT")
	if err != nil {
		return pagewiki.CurationConfig{}, err
	}
	return pagewiki.CurationConfig{Interval: interval, PairLimit: pairLimit, PageLimit: pageLimit}, nil
}

// parseNonNegativeEnvironment parses raw (already read from the named
// environment variable) as a non-negative integer, treating an empty string
// as 0 (the caller's "use the package default" sentinel). A parse error or a
// negative value is a startup error naming the offending variable.
func parseNonNegativeEnvironment(raw, name string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(trimmed)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf(
			"initialize Page Wiki curation: %s must be a non-negative integer, got %q",
			name, trimmed,
		)
	}
	return parsed, nil
}

// parseTreeMaxDepth returns 0 when unset (callers apply the default).
func parseTreeMaxDepth(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 1 {
		return 0, fmt.Errorf("LLMWIKI_TREE_MAX_DEPTH must be a positive integer, got %q", raw)
	}
	return parsed, nil
}

func buildPageWikiMaintainers(
	usageStore *postgres.LLMUsageStore,
	config applicationConfig,
	logger *slog.Logger,
) (pagewiki.Planner, pagewiki.Editor, pagewiki.TreeNavigator, pagewiki.Curator, error) {
	switch strings.TrimSpace(config.llmwikiMode) {
	case "", "local":
		return pagewiki.SessionDocumentPlanner{}, pagewiki.SessionDocumentEditor{}, nil, nil, nil
	case "openai", "harness":
		if strings.TrimSpace(config.llmwikiBaseURL) == "" ||
			strings.TrimSpace(config.llmwikiAPIKey) == "" ||
			strings.TrimSpace(config.llmwikiModel) == "" {
			return nil, nil, nil, nil, errors.New(
				"initialize Page Wiki LLM maintainers: LLMWIKI_LLM_BASE_URL, " +
					"LLMWIKI_LLM_API_KEY, and LLMWIKI_LLM_MODEL are required",
			)
		}
		client := platformllm.NewDeepSeekClient(platformllm.DeepSeekConfig{
			BaseURL: config.llmwikiBaseURL,
			APIKey:  config.llmwikiAPIKey,
		})
		metered := meteredClientFactory(client, usageStore, logger)
		plannerClient, err := metered("wiki-planner")
		if err != nil {
			return nil, nil, nil, nil, err
		}
		planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
			Client: plannerClient, Model: config.llmwikiModel, Logger: logger,
		})
		if err != nil {
			return nil, nil, nil, nil, err
		}
		editorClient, err := metered("wiki-editor")
		if err != nil {
			return nil, nil, nil, nil, err
		}
		editor, err := pagewiki.NewLLMSessionEditor(pagewiki.LLMEditorConfig{
			Client: editorClient, Model: config.llmwikiModel,
		})
		if err != nil {
			return nil, nil, nil, nil, err
		}
		indexerClient, err := metered("wiki-indexer")
		if err != nil {
			return nil, nil, nil, nil, err
		}
		navigator, err := pagewiki.NewLLMTreeNavigator(pagewiki.LLMTreeNavigatorConfig{
			Client: indexerClient, Model: config.llmwikiModel, Logger: logger,
		})
		if err != nil {
			return nil, nil, nil, nil, err
		}
		curatorClient, err := metered("wiki-curator")
		if err != nil {
			return nil, nil, nil, nil, err
		}
		curator, err := pagewiki.NewLLMCurator(pagewiki.LLMCuratorConfig{
			Client: curatorClient, Model: config.llmwikiModel, Logger: logger,
		})
		if err != nil {
			return nil, nil, nil, nil, err
		}
		return planner, editor, navigator, curator, nil
	default:
		return nil, nil, nil, nil, fmt.Errorf(
			"initialize Page Wiki LLM maintainers: unsupported LLMWIKI_ORGANIZER_MODE %q",
			config.llmwikiMode,
		)
	}
}

// buildAuditConsumer wires the session audit projection consumer and its
// read seam on the shared Postgres pool, mirroring the Page Wiki consumer
// lifecycle. The returned store also serves the admin session-audit queries.
// The controller is returned unstarted; Run starts it after every build step
// has succeeded.
func buildAuditConsumer(
	store *postgres.Store,
	logger *slog.Logger,
) (*audit.Controller, *postgres.AuditStore, error) {
	auditStore, err := postgres.NewAuditStore(store.Pool())
	if err != nil {
		return nil, nil, err
	}
	controller, err := audit.New(auditStore, logger, 0)
	if err != nil {
		return nil, nil, err
	}
	return controller, auditStore, nil
}

// applicationServices bundles the HTTP handlers with the background
// consumers and maintenance loops the build functions assemble but do not
// start. Run starts them only after every build step has succeeded and stops
// them in reverse start order on shutdown, so a failed later build (or a
// failed queue start) never leaves goroutines running while Run unwinds.
type applicationServices struct {
	team             *handler.Handler
	pageWiki         *pagewikihttp.Handler
	todo             *todoapphttp.Handler
	wikiServices     *pagewiki.ServiceManager
	wikiConsumer     *sessionconsumer.Controller
	auditConsumer    *audit.Controller
	startTodoRefresh func(context.Context) func()
	stopTodoRefresh  func()
}

// start launches every background loop the build phase assembled.
func (s *applicationServices) start(ctx context.Context) {
	s.wikiServices.Start(ctx)
	s.wikiConsumer.Start(ctx)
	s.auditConsumer.Start(ctx)
	s.stopTodoRefresh = s.startTodoRefresh(ctx)
}

// stop shuts the background loops down in reverse start order, giving each
// consumer a bounded wait mirroring the extraction queue stop in Run. A stop
// timeout is logged rather than returned so shutdown keeps making progress
// past a wedged consumer.
func (s *applicationServices) stop(timeout time.Duration, logger *slog.Logger) {
	s.stopTodoRefresh()
	consumers := []struct {
		name string
		stop func(context.Context) error
	}{
		{name: "session audit consumer", stop: s.auditConsumer.Stop},
		{name: "Page Wiki session consumer", stop: s.wikiConsumer.Stop},
	}
	for _, consumer := range consumers {
		stopContext, cancelStop := context.WithTimeout(context.Background(), timeout)
		if err := consumer.stop(stopContext); err != nil {
			logger.Warn("stop background consumer failed", "consumer", consumer.name, "error", err)
		}
		cancelStop()
	}
	s.wikiServices.Stop()
}

func buildApplicationHTTPHandlers(
	ctx context.Context,
	runtime teamnote.Runtime,
	store *postgres.Store,
	operationRecorder operations.Recorder,
	lake *evidencelake.Lake,
	embedder textembedding.Embedder,
	usageStore *postgres.LLMUsageStore,
	config applicationConfig,
	logger *slog.Logger,
) (*applicationServices, error) {
	pageHandler, wikiServices, wikiControl, wikiSettings, err := buildPageWikiHTTPHandler(
		ctx, store, embedder, usageStore, config, logger,
	)
	if err != nil {
		return nil, err
	}
	auditConsumer, auditStore, err := buildAuditConsumer(store, logger)
	if err != nil {
		return nil, err
	}
	teamHandler, identity, err := buildHTTPHandler(
		ctx,
		runtime,
		store,
		operationRecorder,
		usageStore,
		config,
		logger,
		wikiControl,
		wikiSettings,
		auditStore,
	)
	if err != nil {
		return nil, err
	}
	todoHandler, startTodoRefresh, err := buildTodoApp(ctx, store, lake, usageStore, identity, config, logger)
	if err != nil {
		return nil, err
	}
	return &applicationServices{
		team: teamHandler, pageWiki: pageHandler, todo: todoHandler,
		wikiServices: wikiServices, wikiConsumer: wikiControl, auditConsumer: auditConsumer,
		startTodoRefresh: startTodoRefresh,
	}, nil
}

// buildTodoApp wires the Todo App domain service, its Postgres-backed
// repository and note directory, its evidence-lake reporter, an optional LLM
// rewriter reusing the LLMWIKI_LLM_* configuration, and the HTTP transport.
// identity may be nil (legacy API-key mode); the transport then answers 501
// for every route. The background suggestion-refresh scheduler is returned
// as a deferred start closure so Run launches it only after every build step
// has succeeded.
func buildTodoApp(
	ctx context.Context,
	store *postgres.Store,
	lake *evidencelake.Lake,
	usageStore *postgres.LLMUsageStore,
	identity *onprem.IdentityService,
	config applicationConfig,
	logger *slog.Logger,
) (*todoapphttp.Handler, func(context.Context) func(), error) {
	todoRepository, err := todoapppostgres.NewRepository(ctx, store.Pool())
	if err != nil {
		return nil, nil, fmt.Errorf("initialize Todo App repository: %w", err)
	}
	noteDirectory, err := postgres.NewTodoNoteDirectory(store.Pool())
	if err != nil {
		return nil, nil, fmt.Errorf("initialize Todo App note directory: %w", err)
	}
	reporter, err := todoapp.NewLakeReporter(lake)
	if err != nil {
		return nil, nil, fmt.Errorf("initialize Todo App reporter: %w", err)
	}
	rewriter, err := buildTodoRewriter(usageStore, config, logger)
	if err != nil {
		return nil, nil, err
	}
	service, err := todoapp.NewService(todoapp.ServiceConfig{
		Repository: todoRepository, Notes: noteDirectory, Rewriter: rewriter, Reporter: reporter, Logger: logger,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("initialize Todo App service: %w", err)
	}
	configured, err := todoapphttp.New(service, todoAuthenticator(identity), todoapphttp.WithLogger(logger))
	if err != nil {
		return nil, nil, fmt.Errorf("initialize Todo App HTTP handler: %w", err)
	}
	startRefresh := func(ctx context.Context) func() {
		return todoapp.StartSuggestionRefresh(ctx, service, noteDirectory, config.todoRefreshInterval, logger)
	}
	return configured, startRefresh, nil
}

// todoAuthenticator converts the typed *onprem.IdentityService into the Todo
// App transport's HumanAuthenticator interface, mapping a nil pointer to a
// true nil interface so the transport's "identity == nil answers 501"
// contract holds instead of tripping the typed-nil trap.
func todoAuthenticator(identity *onprem.IdentityService) todoapphttp.HumanAuthenticator {
	if identity == nil {
		return nil
	}
	return identity
}

// buildTodoRewriter returns an LLM-backed Rewriter when the LLMWIKI_LLM_*
// settings are fully configured, reusing the same environment as Page Wiki's
// LLM maintainers (see buildPageWikiMaintainers). Otherwise it returns a nil
// Rewriter, and the service falls back to verbatim copy.
func buildTodoRewriter(
	usageStore *postgres.LLMUsageStore, config applicationConfig, logger *slog.Logger,
) (todoapp.Rewriter, error) {
	if strings.TrimSpace(config.llmwikiBaseURL) == "" ||
		strings.TrimSpace(config.llmwikiAPIKey) == "" ||
		strings.TrimSpace(config.llmwikiModel) == "" {
		return nil, nil
	}
	client := platformllm.NewDeepSeekClient(platformllm.DeepSeekConfig{
		BaseURL: config.llmwikiBaseURL,
		APIKey:  config.llmwikiAPIKey,
	})
	meteredClient, err := meteredClientFactory(client, usageStore, logger)("todo-rewriter")
	if err != nil {
		return nil, fmt.Errorf("initialize Todo App LLM rewriter: %w", err)
	}
	rewriter, err := todoapp.NewLLMRewriter(todoapp.LLMRewriterConfig{
		Client: meteredClient, Model: config.llmwikiModel, Logger: logger,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize Todo App LLM rewriter: %w", err)
	}
	return rewriter, nil
}

// meteredClientFactory returns a closure that wraps client in a
// MeteredChatClient attributing usage to the given component, reporting to
// usageStore under onprem.LocalScopeID by default (overridden per call by
// any scope carried on the request context).
func meteredClientFactory(
	client platformllm.ChatClient, usageStore *postgres.LLMUsageStore, logger *slog.Logger,
) func(component string) (platformllm.ChatClient, error) {
	return func(component string) (platformllm.ChatClient, error) {
		metered, err := platformllm.NewMeteredChatClient(platformllm.MeteredConfig{
			Client: client, Sink: usageStore, Component: component,
			DefaultScope: onprem.LocalScopeID, Logger: logger,
		})
		if err != nil {
			return nil, fmt.Errorf("initialize metered LLM client for %s: %w", component, err)
		}
		return metered, nil
	}
}

// extractionUsageRecorder adapts extraction's slice-level usage reporting to
// llm.UsageSink, attributing usage to the "extractor" component under the
// scope carried on the extraction context (falling back to
// onprem.LocalScopeID when none is present). Recording is best-effort: a
// sink failure is logged and never fails extraction. The sink is the
// llm.UsageSink seam (satisfied by *postgres.LLMUsageStore) so the fallback
// and error handling stay testable without a database.
func extractionUsageRecorder(
	usageStore platformllm.UsageSink, logger *slog.Logger,
) func(context.Context, string, extractor.Usage) {
	return func(ctx context.Context, model string, usage extractor.Usage) {
		scopeID, err := teamnote.ScopeFromContext(ctx)
		if err != nil || strings.TrimSpace(scopeID) == "" {
			scopeID = onprem.LocalScopeID
		}
		event := platformllm.UsageEvent{
			ScopeID: scopeID, Component: "extractor", Model: model,
			Usage: platformllm.TokenUsage{
				InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
				PromptCacheHitTokens: usage.PromptCacheHitTokens, PromptCacheMissTokens: usage.PromptCacheMissTokens,
			},
		}
		if recordErr := usageStore.RecordLLMUsage(ctx, event); recordErr != nil {
			logger.WarnContext(ctx, "record extraction LLM usage failed",
				"scope_id", scopeID, "model", model, "error", recordErr)
		}
	}
}

// buildHTTPHandler configures the team-memory HTTP transport. It also
// returns the *onprem.IdentityService built for the human portal (nil in
// legacy API-key mode), so callers can share it with other handlers that
// authenticate Human Portal sessions, such as the Todo App transport.
func buildHTTPHandler(
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
) (*handler.Handler, *onprem.IdentityService, error) {
	if len(config.apiKeys) > 0 && (strings.TrimSpace(config.adminAPIKey) != "" || config.humanIdentityConfigured()) {
		return nil, nil, fmt.Errorf("configure HTTP transport: legacy and on-prem authentication are mutually exclusive")
	}
	if len(config.apiKeys) > 0 {
		configured, err := handler.New(runtime, handler.StaticAPIKeys(config.apiKeys), logger)
		if err != nil {
			return nil, nil, fmt.Errorf("configure HTTP transport: %w", err)
		}
		return configured, nil, nil
	}
	if store == nil {
		return nil, nil, fmt.Errorf("configure on-prem HTTP transport: postgres store is required")
	}
	if operationRecorder == nil {
		return nil, nil, fmt.Errorf("configure on-prem HTTP transport: operations recorder is required")
	}
	credentials, err := onprem.NewCredentialService(store.Credentials(), onprem.CredentialConfig{
		AdminAPIKey: config.adminAPIKey, RotationOverlap: config.credentialRotationOverlap,
		SecretPepper: config.secretPepper, AllowLegacyAgentCreation: !config.humanIdentityConfigured(),
		PortalURL: config.portalURL, DeviceAgentLimit: config.deviceAgentLimit,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("configure on-prem credentials: %w", err)
	}
	// recallRouter must not shadow the imported pagewiki memory package.
	recallRouter, err := recall.NewRouter(runtime, nil, recall.Config{EnablePassiveWikiHint: config.wikiHintEnabled})
	if err != nil {
		return nil, nil, fmt.Errorf("configure recall router: %w", err)
	}
	channel, err := onprem.NewChannelService(store.Channel())
	if err != nil {
		return nil, nil, fmt.Errorf("configure on-prem channel: %w", err)
	}
	registry, err := onprem.NewRegistryService(store.Registry(), onprem.RegistryConfig{
		SecretPepper: config.secretPepper, MemberGrantablePermissions: config.memberGrantablePermissions,
		PortalURL: config.portalURL,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("configure on-prem agent registry: %w", err)
	}
	operationsService, err := onprem.NewOperationsService(store.Operations(onprem.LocalScopeID), onprem.OperationsConfig{
		EventRetention: config.operationsEventRetention, StorageRetention: config.operationsStorageRetention,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("configure on-prem operations: %w", err)
	}
	explorerService, err := onprem.NewExplorerService(store.Explorer(onprem.LocalScopeID))
	if err != nil {
		return nil, nil, fmt.Errorf("configure team memory explorer: %w", err)
	}
	options := onPremHandlerOptions(
		registry, operationsService, operationRecorder, explorerService, usageStore, wikiControl, wikiSettings,
		sessionAudit,
	)
	options = append(options, handler.WithReadinessCheck(storeReadinessCheck(store)))
	var identityService *onprem.IdentityService
	if config.humanIdentityConfigured() {
		identity, err := onprem.NewIdentityService(store.Identity(), onprem.IdentityConfig{
			BootstrapSecret: config.bootstrapSecret, SessionTTL: 12 * time.Hour,
			InvitationTTL: 24 * time.Hour, SecretPepper: config.secretPepper,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("configure on-prem human identity: %w", err)
		}
		oidcAuthenticator, err := onprem.NewOIDCAuthenticator(ctx, onprem.OIDCConfig{
			Issuer: config.oidcIssuer, ClientID: config.oidcClientID,
			ClientSecret: config.oidcClientSecret, RedirectURL: config.oidcRedirectURL,
			FlowSecret:              config.oidcFlowSecret,
			AuthorizationParameters: config.oidcAuthorizationParameters,
			IdentitySource:          onprem.OIDCIdentitySource(config.oidcIdentitySource),
		})
		if err != nil {
			return nil, nil, fmt.Errorf("configure on-prem OIDC: %w", err)
		}
		options = append(options, handler.WithHumanIdentity(
			identity, oidcAuthenticator, config.portalURL, config.humanCookieSecure,
		))
		identityService = identity
	}
	configured, err := handler.NewOnPrem(runtime, credentials, recallRouter, channel, logger, options...)
	if err != nil {
		return nil, nil, fmt.Errorf("configure on-prem HTTP transport: %w", err)
	}
	return configured, identityService, nil
}

// readinessProbeTimeout bounds the /readyz store round-trip. Load balancers
// call the probe continuously and have their own deadline, so a wedged pool
// must fail fast rather than pile up probe goroutines.
const readinessProbeTimeout = 2 * time.Second

// storeReadinessCheck reports whether the Postgres pool can still serve
// traffic. Ping is deliberately cheap: readiness answers "is the backing
// store reachable", not "is every query healthy".
func storeReadinessCheck(store *postgres.Store) handler.ReadinessCheck {
	return func(ctx context.Context) error {
		probeContext, cancel := context.WithTimeout(ctx, readinessProbeTimeout)
		defer cancel()
		if err := store.Pool().Ping(probeContext); err != nil {
			return fmt.Errorf("ping postgres: %w", err)
		}
		return nil
	}
}

// onPremHandlerOptions assembles the OnPremOption list for buildHTTPHandler,
// keeping that function's own branching within the linter's complexity
// budget. usageStore, wikiControl, wikiSettings, and sessionAudit are
// optional; each is wired only when its dependency is configured.
func onPremHandlerOptions(
	registry *onprem.RegistryService,
	operationsService *onprem.OperationsService,
	operationRecorder operations.Recorder,
	explorerService *onprem.ExplorerService,
	usageStore *postgres.LLMUsageStore,
	wikiControl handler.WikiControl,
	wikiSettings handler.WikiSettings,
	sessionAudit handler.SessionAuditQuery,
) []handler.OnPremOption {
	options := []handler.OnPremOption{
		handler.WithAgentRegistry(registry), handler.WithOperations(operationsService, operationRecorder),
		handler.WithExplorer(explorerService),
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
	return options
}

func buildExtractor(config applicationConfig, stores ...extractor.EpisodeStore) (extractor.Extractor, error) {
	var episodes extractor.EpisodeStore
	if len(stores) > 0 {
		episodes = stores[0]
	}
	switch config.extractorMode {
	case "noop":
		return extractor.Noop{}, nil
	case "openai":
		return extractor.NewOpenAI(extractor.OpenAIConfig{
			BaseURL: config.extractorBaseURL, APIKey: config.extractorAPIKey, Model: config.extractorModel,
			ThinkingMode:  config.extractorThinkingMode,
			PromptVersion: config.promptVersion, Client: &http.Client{},
			ContextMode: extractor.ContextMode(config.extractionContextMode), EpisodeStore: episodes,
			ExtractionVersion:  config.extractionVersion,
			V2Variant:          config.extractionCandidateStrategy,
			CompactionEnabled:  config.extractionCompactionEnabled,
			CompactStartTokens: config.extractionCompactStartTokens, CompactTokens: config.extractionCompactTokens,
			SummaryEnabled:       config.extractionSummaryEnabled,
			SummaryTriggerTokens: config.extractionSummaryTriggerTokens,
			SummaryTailTokens:    config.extractionSummaryTailTokens,
			MaxPromptTokens:      config.extractionMaxPromptTokens,
			ExecutionPolicy:      config.extractionExecutionPolicy,
			ProviderCallObserver: config.providerCallObserver,
		})
	default:
		return nil, fmt.Errorf("unsupported extractor mode %q", config.extractorMode)
	}
}

func buildEmbedder(config applicationConfig) (textembedding.Embedder, error) {
	if strings.TrimSpace(config.embeddingBaseURL) == "" {
		return nil, nil
	}
	return textembedding.NewOpenAI(textembedding.OpenAIConfig{
		BaseURL: config.embeddingBaseURL, Model: config.embeddingModel,
		Dimensions: config.embeddingDimensions, APIKey: config.embeddingAPIKey,
		Client: &http.Client{Timeout: config.embeddingTimeout},
	})
}
