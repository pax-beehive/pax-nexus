package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

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

const embeddingBackfillBatchSize = 32

// Run assembles and serves the on-prem team-memory application, blocking
// until the HTTP server stops.
func Run(ctx context.Context, logger *slog.Logger) error {
	config, err := loadConfig()
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
	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("initialize storage schema: %w", err)
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
		dropCountingRecorder, recorderErr := operations.NewDropCountingRecorder(store.Operations())
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
	if err := extractionqueue.Migrate(ctx, store.Pool()); err != nil {
		return fmt.Errorf("initialize extraction queue schema: %w", err)
	}
	if err := sessions.ConfigureExtractionEnqueuer(queue); err != nil {
		return fmt.Errorf("connect extraction queue: %w", err)
	}
	httpHandler, pageWikiHandler, todoHandler, stopTodoRefresh, err := buildApplicationHTTPHandlers(
		ctx,
		runtime,
		store,
		operationRecorder,
		lake,
		embedder,
		usageStore,
		config,
		logger,
	)
	if err != nil {
		return err
	}
	if err := queue.Start(ctx); err != nil {
		return fmt.Errorf("start extraction queue: %w", err)
	}
	stopOperations := func() {}
	if len(config.apiKeys) == 0 {
		stopOperations = startOperationsMaintenance(ctx, store.Operations(), operationRecorder, config, logger)
	}
	go continueEmbeddingBackfill(ctx, noteStore, logger)

	h := server.Default(server.WithHostPorts(config.listenAddress))
	h.Use(handler.InstanceMiddleware(httpHandler))
	h.Use(pagewikihttp.InstanceMiddleware(pageWikiHandler))
	h.Use(todoapphttp.InstanceMiddleware(todoHandler))
	router.GeneratedRegister(h)
	logger.Info("team-memory started", "listen_address", config.listenAddress, "worker_shards", config.workerShards,
		"extraction_version", config.extractionVersion,
		"extraction_candidate_strategy", config.extractionCandidateStrategy,
		"recall_candidate_strategy", config.recallCandidateStrategy.Name)
	h.Spin()
	stopOperations()
	stopTodoRefresh()
	queueStopContext, cancelQueueStop := context.WithTimeout(context.Background(), config.workerStopTimeout)
	queueErr := queue.Stop(queueStopContext)
	cancelQueueStop()
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
		Policy: config.recallCandidateStrategy.Policy,
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

func wrapOptionalError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func continueEmbeddingBackfill(ctx context.Context, noteStore *postgres.NoteStore, logger *slog.Logger) {
	for {
		backfilled, err := noteStore.BackfillEmbeddings(ctx, embeddingBackfillBatchSize)
		if err != nil {
			logger.Error("team note embedding backfill failed", "error", err)
			return
		}
		if backfilled > 0 {
			logger.Info("team note embeddings backfilled", "notes", backfilled)
		}
		if backfilled < embeddingBackfillBatchSize {
			return
		}
	}
}
