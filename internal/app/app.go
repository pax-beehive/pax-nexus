package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/evidencelake"
	"github.com/pax-beehive/pax-nexus/internal/operations"
	pagewikihttp "github.com/pax-beehive/pax-nexus/internal/pagewiki/transport/httpapi"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/pax-beehive/pax-nexus/internal/platform/textembedding"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractionqueue"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
	teamruntime "github.com/pax-beehive/pax-nexus/internal/teamnote/runtime"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/handler"
	router "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/router"
	todoapphttp "github.com/pax-beehive/pax-nexus/internal/todoapp/transport/httpapi"
)

const (
	embeddingBackfillBatchSize = 32
	// embeddingBackfillRetryDelay paces retries of the background backfill
	// after a transient failure (e.g. the embedding service briefly down).
	embeddingBackfillRetryDelay = 30 * time.Second
	// maxRequestBodySize raises Hertz's ~4 MiB default so session backfill
	// of large CLI session files fits (they reach tens of MiB). Cloud Run's
	// hard request cap is 32 MiB, so stay just under it; the on-prem
	// compose stack has no smaller proxy cap in front.
	maxRequestBodySize = 30 * 1024 * 1024
)

// deploymentProfile selects which control plane the process serves: the
// single-installation on-prem profile or the multi-team SaaS profile. The
// runtime, extraction, and storage wiring is shared; the profile decides
// the HTTP handler construction and the authentication config rules.
type deploymentProfile int

const (
	profileOnPrem deploymentProfile = iota
	profileSaaS
)

// Run assembles and serves the on-prem team-memory application, blocking
// until the HTTP server stops.
func Run(ctx context.Context, logger *slog.Logger) error {
	return runProfile(ctx, logger, profileOnPrem)
}

// RunSaaS assembles and serves the multi-team SaaS team-memory application,
// blocking until the HTTP server stops. It shares the on-prem runtime and
// storage wiring; the delta is the SaaS control plane behind the HTTP
// transport and per-request team scope resolution.
func RunSaaS(ctx context.Context, logger *slog.Logger) error {
	return runProfile(ctx, logger, profileSaaS)
}

func runProfile(ctx context.Context, logger *slog.Logger, profile deploymentProfile) error {
	config, err := loadProfileConfig(profile)
	if err != nil {
		return fmt.Errorf("load service config: %w", err)
	}
	config.providerCallObserver = func(call extractor.ProviderCall) {
		logger.Info("extraction provider attempt",
			"type", call.Type, "scope_id", call.ScopeID, "attempt", call.Attempt,
			"max_attempts", call.MaxAttempts, "duration_ms", call.DurationMS,
			"http_status", call.HTTPStatus, "failure_class", call.FailureClass,
			"retryable", call.Retryable, "input_tokens", call.Usage.InputTokens,
			"output_tokens", call.Usage.OutputTokens)
	}
	store, err := postgres.Open(ctx, config.databaseURL)
	if err != nil {
		return fmt.Errorf("initialize storage: %w", err)
	}
	defer store.Close()
	// Single-binary on-prem installs keep migrating on boot. Managed
	// deployments should instead run cmd/team-memory-migrate as a job before
	// rolling out instances; both paths share migrateStores so they cannot
	// drift.
	if err := migrateStores(ctx, store, config.embeddingDimensions); err != nil {
		return err
	}
	sessions := store.Sessions()
	candidateExtractor, err := buildExtractor(config, store.Episodes())
	if err != nil {
		return fmt.Errorf("initialize extractor: %w", err)
	}
	embedder, err := buildEmbedder(config)
	if err != nil {
		return fmt.Errorf("initialize embedding adapter: %w", err)
	}
	noteStore, usageStore, err := initializeStores(ctx, store, embedder, config, logger)
	if err != nil {
		return err
	}
	var operationRecorder operations.Recorder
	runtimeConfig := teamruntime.Config{
		NoteStore: noteStore, Logger: logger, SliceEventLimit: config.sliceEventLimit, SliceTokenLimit: config.sliceTokenLimit,
		SliceOverlap: config.sliceOverlap, MaxSlicesPerJob: config.maxSlicesPerJob,
		UsageRecorder: extractionUsageRecorder(usageStore, logger),
	}
	if len(config.apiKeys) == 0 {
		dropCountingRecorder, recorderErr := operations.NewDropCountingRecorder(store.Operations(onprem.LocalScopeID))
		if recorderErr != nil {
			return fmt.Errorf("initialize operations recorder: %w", recorderErr)
		}
		operationRecorder = dropCountingRecorder
		runtimeConfig.ExtractionObserver = onprem.NewExtractionObserver(operationRecorder, logger)
	}
	lake := evidencelake.New(sessions)
	runtime, err := teamruntime.New(lake, candidateExtractor, runtimeConfig)
	if err != nil {
		return fmt.Errorf("initialize runtime: %w", err)
	}
	queue, err := extractionqueue.New(store.Pool(), runtime, extractionqueue.Config{
		Shards: config.workerShards, MaxAttempts: config.workerMaxAttempts, Debounce: config.workerDebounce,
		BatchTimeout: config.batchTimeout, Logger: logger,
		JobTimeout: config.workerJobTimeout, SoftStopTimeout: config.workerStopTimeout,
	})
	if err != nil {
		return fmt.Errorf("initialize extraction queue: %w", err)
	}
	if err := sessions.ConfigureExtractionEnqueuer(queue); err != nil {
		return fmt.Errorf("connect extraction queue: %w", err)
	}
	services, err := buildApplicationHTTPHandlers(
		ctx,
		runtime,
		store,
		operationRecorder,
		lake,
		embedder,
		usageStore,
		config,
		logger,
		profile,
	)
	if err != nil {
		return err
	}
	// Every build step succeeded: only now do the background consumers and
	// maintenance loops start, so a failed build never leaves goroutines
	// running while Run unwinds.
	services.start(ctx)
	if err := queue.Start(ctx); err != nil {
		services.stop(config.workerStopTimeout, logger)
		return fmt.Errorf("start extraction queue: %w", err)
	}
	stopOperations := func() {}
	if len(config.apiKeys) == 0 {
		stopOperations = startOperationsMaintenance(ctx, store.Operations(onprem.LocalScopeID), operationRecorder, config, logger)
	}
	go continueEmbeddingBackfill(ctx, noteStore, embeddingBackfillRetryDelay, logger)

	h := server.Default(
		server.WithHostPorts(config.listenAddress),
		server.WithMaxRequestBodySize(maxRequestBodySize),
	)
	h.Use(handler.InstanceMiddleware(services.team))
	h.Use(pagewikihttp.InstanceMiddleware(services.pageWiki))
	h.Use(todoapphttp.InstanceMiddleware(services.todo))
	router.GeneratedRegister(h)
	logger.Info("team-memory started", "listen_address", config.listenAddress, "worker_shards", config.workerShards,
		"extraction_version", config.extractionVersion,
		"extraction_candidate_strategy", config.extractionCandidateStrategy,
		"recall_candidate_strategy", config.recallCandidateStrategy.Name)
	h.Spin()
	stopOperations()
	queueStopContext, cancelQueueStop := context.WithTimeout(context.Background(), config.workerStopTimeout)
	queueErr := queue.Stop(queueStopContext)
	cancelQueueStop()
	services.stop(config.workerStopTimeout, logger)
	extractorStopContext, cancelExtractorStop := context.WithTimeout(context.Background(), config.workerStopTimeout)
	extractorErr := closeExtractor(extractorStopContext, candidateExtractor)
	cancelExtractorStop()
	if queueErr != nil || extractorErr != nil {
		return errors.Join(
			wrapOptionalError("stop extraction queue", queueErr),
			wrapOptionalError("stop extractor", extractorErr),
		)
	}
	logger.Info("team-memory stopped")
	return nil
}

// initializeStores builds the note store (backfilling any pending
// embeddings) and the LLM usage store on top of an already-open Postgres
// store, keeping run's own branching within the linter's complexity budget.
func initializeStores(
	ctx context.Context,
	store *postgres.Store,
	embedder textembedding.Embedder,
	config applicationConfig,
	logger *slog.Logger,
) (*postgres.NoteStore, *postgres.LLMUsageStore, error) {
	noteStore, err := postgres.NewNoteStore(store, teamnote.DefaultTTLPolicy(), teamnote.SystemClock{}, postgres.RetrievalConfig{
		Embedder: embedder, EmbeddingModel: config.embeddingModel,
		EmbeddingDimensions: config.embeddingDimensions,
		Policy:              config.recallCandidateStrategy.Policy, Logger: logger,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("initialize note store: %w", err)
	}
	backfilled, err := noteStore.BackfillEmbeddings(ctx, embeddingBackfillBatchSize)
	if err != nil {
		return nil, nil, fmt.Errorf("backfill note embeddings: %w", err)
	}
	if backfilled > 0 {
		logger.Info("team note embeddings backfilled", "notes", backfilled, "model", config.embeddingModel)
	}
	usageStore, err := postgres.NewLLMUsageStore(store.Pool())
	if err != nil {
		return nil, nil, fmt.Errorf("initialize LLM usage store: %w", err)
	}
	return noteStore, usageStore, nil
}

func closeExtractor(ctx context.Context, candidateExtractor extractor.Extractor) error {
	lifecycle, ok := candidateExtractor.(extractor.Lifecycle)
	if !ok {
		return nil
	}
	return lifecycle.Close(ctx)
}

// loadProfileConfig loads the configuration for the selected deployment
// profile; the profiles share every non-authentication setting.
func loadProfileConfig(profile deploymentProfile) (applicationConfig, error) {
	if profile == profileSaaS {
		return loadSaaSConfig()
	}
	return loadConfig()
}

func wrapOptionalError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// embeddingBackfiller is the store seam continueEmbeddingBackfill drains;
// *postgres.NoteStore satisfies it.
type embeddingBackfiller interface {
	BackfillEmbeddings(ctx context.Context, batchSize int) (int, error)
}

// continueEmbeddingBackfill drains the pending-embedding backlog in batches.
// A transient failure (e.g. the embedding service briefly unreachable) is
// retried after retryDelay instead of abandoning the backlog until the next
// restart; only ctx cancellation stops the loop early.
func continueEmbeddingBackfill(
	ctx context.Context,
	noteStore embeddingBackfiller,
	retryDelay time.Duration,
	logger *slog.Logger,
) {
	for {
		backfilled, err := noteStore.BackfillEmbeddings(ctx, embeddingBackfillBatchSize)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.Warn("team note embedding backfill failed; retrying",
				"error", err, "retry_delay", retryDelay)
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay):
			}
			continue
		}
		if backfilled > 0 {
			logger.Info("team note embeddings backfilled", "notes", backfilled)
		}
		if backfilled < embeddingBackfillBatchSize {
			return
		}
	}
}
