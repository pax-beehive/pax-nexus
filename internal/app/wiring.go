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
) (*pagewikihttp.Handler, *sessionconsumer.Controller, *pagewiki.Service, error) {
	treeMaxDepth, err := parseTreeMaxDepth(config.llmwikiTreeMaxDepth)
	if err != nil {
		return nil, nil, nil, fmt.Errorf(
			"initialize Page Wiki repository: %w", err,
		)
	}
	repositoryOptions := make([]memory.Option, 0, 1)
	if treeMaxDepth > 0 {
		repositoryOptions = append(repositoryOptions, memory.WithTopicTreeMaxDepth(treeMaxDepth))
	}
	repository, err := pagewikipostgres.NewRepository(
		ctx, store.Pool(), onprem.LocalScopeID, repositoryOptions...,
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("initialize Page Wiki repository: %w", err)
	}
	planner, editor, navigator, curator, err := buildPageWikiMaintainers(usageStore, config, logger)
	if err != nil {
		return nil, nil, nil, err
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
			return nil, nil, nil, err
		}
		options = append(options, pagewiki.WithCurator(curator, embedder, curationConfig, logger))
	}
	service := pagewiki.NewService(repository, planner, editor, options...)
	consumerStore, err := postgres.NewPageWikiConsumerStore(store.Pool(), onprem.LocalScopeID)
	if err != nil {
		return nil, nil, nil, err
	}
	controller, err := sessionconsumer.New(consumerStore, service, repository, logger, 2*time.Second)
	if err != nil {
		return nil, nil, nil, err
	}
	configured, err := pagewikihttp.New(service, repository)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("initialize Page Wiki HTTP handler: %w", err)
	}
	service.StartTreeMaintenance(ctx)
	service.StartCurationMaintenance(ctx)
	controller.Start(ctx)
	return configured, controller, service, nil
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
) (*handler.Handler, *pagewikihttp.Handler, *todoapphttp.Handler, func(), error) {
	pageHandler, wikiControl, wikiSettings, err := buildPageWikiHTTPHandler(ctx, store, embedder, usageStore, config, logger)
	if err != nil {
		return nil, nil, nil, nil, err
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
	)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	todoHandler, stopTodoRefresh, err := buildTodoApp(ctx, store, lake, usageStore, identity, config, logger)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return teamHandler, pageHandler, todoHandler, stopTodoRefresh, nil
}

// buildTodoApp wires the Todo App domain service, its Postgres-backed
// repository and note directory, its evidence-lake reporter, an optional LLM
// rewriter reusing the LLMWIKI_LLM_* configuration, the HTTP transport, and
// the background suggestion-refresh scheduler. identity may be nil (legacy
// API-key mode); the transport then answers 501 for every route.
func buildTodoApp(
	ctx context.Context,
	store *postgres.Store,
	lake *evidencelake.Lake,
	usageStore *postgres.LLMUsageStore,
	identity *onprem.IdentityService,
	config applicationConfig,
	logger *slog.Logger,
) (*todoapphttp.Handler, func(), error) {
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
	// identity is a typed *onprem.IdentityService; only pass it through as the
	// HumanAuthenticator interface when it is actually configured, so a nil
	// identity produces a true nil interface (routes then answer 501).
	var authenticator todoapphttp.HumanAuthenticator
	if identity != nil {
		authenticator = identity
	}
	configured, err := todoapphttp.New(service, authenticator)
	if err != nil {
		return nil, nil, fmt.Errorf("initialize Todo App HTTP handler: %w", err)
	}
	stop := todoapp.StartSuggestionRefresh(ctx, service, onprem.LocalScopeID, config.todoRefreshInterval, logger)
	return configured, stop, nil
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
// sink failure is logged and never fails extraction.
func extractionUsageRecorder(
	usageStore *postgres.LLMUsageStore, logger *slog.Logger,
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
	memory, err := recall.NewRouter(runtime, nil, recall.Config{EnablePassiveWikiHint: config.wikiHintEnabled})
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
	operationsService, err := onprem.NewOperationsService(store.Operations(), onprem.OperationsConfig{
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
	)
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
			FlowSecret: config.oidcFlowSecret,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("configure on-prem OIDC: %w", err)
		}
		options = append(options, handler.WithHumanIdentity(
			identity, oidcAuthenticator, config.portalURL, config.humanCookieSecure,
		))
		identityService = identity
	}
	configured, err := handler.NewOnPrem(runtime, credentials, memory, channel, logger, options...)
	if err != nil {
		return nil, nil, fmt.Errorf("configure on-prem HTTP transport: %w", err)
	}
	return configured, identityService, nil
}

// onPremHandlerOptions assembles the OnPremOption list for buildHTTPHandler,
// keeping that function's own branching within the linter's complexity
// budget. usageStore, wikiControl, and wikiSettings are optional; each is
// wired only when its dependency is configured.
func onPremHandlerOptions(
	registry *onprem.RegistryService,
	operationsService *onprem.OperationsService,
	operationRecorder operations.Recorder,
	explorerService *onprem.ExplorerService,
	usageStore *postgres.LLMUsageStore,
	wikiControl handler.WikiControl,
	wikiSettings handler.WikiSettings,
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
		BaseURL: config.embeddingBaseURL, Model: config.embeddingModel, Dimensions: postgres.EmbeddingDimensions,
		Client: &http.Client{Timeout: config.embeddingTimeout},
	})
}
