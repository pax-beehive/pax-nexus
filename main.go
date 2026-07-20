package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/pax-beehive/pax-nexus/internal/platform/textembedding"
	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractionqueue"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
	teamruntime "github.com/pax-beehive/pax-nexus/internal/teamnote/runtime"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/handler"
)

const embeddingBackfillBatchSize = 32

func main() {
	logger := observability.NewLogger(os.Stdout)
	if err := run(context.Background(), logger); err != nil {
		logger.Error("team-memory failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	config, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load service config: %w", err)
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
	noteStore, err := postgres.NewNoteStore(store, teamnote.DefaultTTLPolicy(), teamnote.SystemClock{}, postgres.RetrievalConfig{
		Embedder: embedder, EmbeddingModel: config.embeddingModel,
		Policy: config.recallCandidateStrategy.Policy,
	})
	if err != nil {
		return fmt.Errorf("initialize note store: %w", err)
	}
	backfilled, err := noteStore.BackfillEmbeddings(ctx, embeddingBackfillBatchSize)
	if err != nil {
		return fmt.Errorf("backfill note embeddings: %w", err)
	}
	if backfilled > 0 {
		logger.Info("team note embeddings backfilled", "notes", backfilled, "model", config.embeddingModel)
	}
	runtime, err := teamruntime.New(sessionlake.New(sessions), candidateExtractor, teamruntime.Config{
		NoteStore: noteStore, Logger: logger, SliceEventLimit: config.sliceEventLimit, SliceTokenLimit: config.sliceTokenLimit,
		SliceOverlap: config.sliceOverlap, MaxSlicesPerJob: config.maxSlicesPerJob,
	})
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
	httpHandler, err := handler.New(runtime, handler.StaticAPIKeys(config.apiKeys), logger)
	if err != nil {
		return fmt.Errorf("configure HTTP transport: %w", err)
	}
	if err := queue.Start(ctx); err != nil {
		return fmt.Errorf("start extraction queue: %w", err)
	}
	go continueEmbeddingBackfill(ctx, noteStore, logger)

	h := server.Default(server.WithHostPorts(config.listenAddress))
	h.Use(handler.InstanceMiddleware(httpHandler))
	register(h)
	logger.Info("team-memory started", "listen_address", config.listenAddress, "worker_shards", config.workerShards,
		"extraction_candidate_strategy", config.extractionCandidateStrategy,
		"recall_candidate_strategy", config.recallCandidateStrategy.Name)
	h.Spin()
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

type applicationConfig struct {
	databaseURL                    string
	listenAddress                  string
	apiKeys                        map[string]string
	extractorMode                  string
	extractorBaseURL               string
	extractorAPIKey                string
	extractorModel                 string
	promptVersion                  string
	extractionContextMode          string
	extractionVersion              string
	extractionCandidateStrategy    string
	extractionCompactionEnabled    bool
	extractionCompactStartTokens   int
	extractionCompactTokens        int
	extractionSummaryEnabled       bool
	extractionSummaryTriggerTokens int
	extractionSummaryTailTokens    int
	workerShards                   int
	workerMaxAttempts              int
	workerDebounce                 time.Duration
	batchTimeout                   time.Duration
	workerJobTimeout               time.Duration
	workerStopTimeout              time.Duration
	sliceEventLimit                int
	sliceTokenLimit                int
	sliceOverlap                   int
	maxSlicesPerJob                int
	embeddingBaseURL               string
	embeddingModel                 string
	embeddingTimeout               time.Duration
	recallCandidateStrategy        teamnote.RecallCandidateStrategy
}

func loadConfig() (applicationConfig, error) {
	config := applicationConfig{
		databaseURL: os.Getenv("TEAM_MEMORY_DATABASE_URL"), listenAddress: os.Getenv("TEAM_MEMORY_LISTEN_ADDRESS"),
		extractorMode: os.Getenv("TEAM_MEMORY_EXTRACTOR_MODE"), extractorBaseURL: os.Getenv("TEAM_MEMORY_EXTRACTOR_BASE_URL"),
		extractorAPIKey: os.Getenv("TEAM_MEMORY_EXTRACTOR_API_KEY"), extractorModel: os.Getenv("TEAM_MEMORY_EXTRACTOR_MODEL"),
		promptVersion:               os.Getenv("TEAM_MEMORY_PROMPT_VERSION"),
		extractionContextMode:       os.Getenv("TEAM_MEMORY_EXTRACTION_CONTEXT_MODE"),
		extractionVersion:           os.Getenv("TEAM_MEMORY_EXTRACTION_VERSION"),
		extractionCandidateStrategy: os.Getenv("TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY"),
		embeddingBaseURL:            os.Getenv("TEAM_MEMORY_EMBEDDING_BASE_URL"),
		embeddingModel:              os.Getenv("TEAM_MEMORY_EMBEDDING_MODEL"),
	}
	var err error
	if config.workerShards, err = intEnvironment("TEAM_MEMORY_WORKER_SHARDS", 16); err != nil {
		return applicationConfig{}, err
	}
	if config.workerMaxAttempts, err = intEnvironment("TEAM_MEMORY_WORKER_MAX_ATTEMPTS", 5); err != nil {
		return applicationConfig{}, err
	}
	if config.workerDebounce, err = durationEnvironment("TEAM_MEMORY_WORKER_DEBOUNCE", 750*time.Millisecond); err != nil {
		return applicationConfig{}, err
	}
	if config.batchTimeout, err = durationEnvironment("TEAM_MEMORY_BATCH_TIMEOUT", 30*time.Second); err != nil {
		return applicationConfig{}, err
	}
	if config.workerJobTimeout, err = durationEnvironment("TEAM_MEMORY_WORKER_JOB_TIMEOUT", 2*time.Minute); err != nil {
		return applicationConfig{}, err
	}
	if config.workerStopTimeout, err = durationEnvironment("TEAM_MEMORY_WORKER_STOP_TIMEOUT", 30*time.Second); err != nil {
		return applicationConfig{}, err
	}
	if err = loadExtractionConfig(&config); err != nil {
		return applicationConfig{}, err
	}
	if err = loadRetrievalConfig(&config); err != nil {
		return applicationConfig{}, err
	}
	if config.listenAddress == "" {
		config.listenAddress = ":8080"
	}
	if config.extractorMode == "" {
		config.extractorMode = "openai"
	}
	if config.promptVersion == "" {
		config.promptVersion = "v1"
	}
	if config.extractionContextMode == "" {
		config.extractionContextMode = string(extractor.ContextModeRolling)
	}
	if config.extractionVersion == "" {
		config.extractionVersion = extractor.ExtractionVersionV2
	}
	if config.extractionCandidateStrategy == "" {
		config.extractionCandidateStrategy = extractor.DefaultCandidateStrategy()
	}
	if config.embeddingModel == "" && strings.TrimSpace(config.embeddingBaseURL) != "" {
		config.embeddingModel = "Qwen/Qwen3-Embedding-0.6B"
	}
	if strings.TrimSpace(config.databaseURL) == "" {
		return applicationConfig{}, fmt.Errorf("TEAM_MEMORY_DATABASE_URL is required")
	}
	if err := json.Unmarshal([]byte(os.Getenv("TEAM_MEMORY_API_KEYS")), &config.apiKeys); err != nil {
		return applicationConfig{}, fmt.Errorf("decode TEAM_MEMORY_API_KEYS: %w", err)
	}
	if len(config.apiKeys) == 0 {
		return applicationConfig{}, fmt.Errorf("TEAM_MEMORY_API_KEYS must contain at least one key")
	}
	return config, nil
}

func loadExtractionConfig(config *applicationConfig) error {
	var err error
	if config.extractionCompactionEnabled, err = boolEnvironment("TEAM_MEMORY_EXTRACTION_COMPACTION_ENABLED", false); err != nil {
		return err
	}
	if config.extractionSummaryEnabled, err = boolEnvironment("TEAM_MEMORY_EXTRACTION_SUMMARY_ENABLED", true); err != nil {
		return err
	}
	if config.extractionCompactionEnabled && config.extractionSummaryEnabled {
		return fmt.Errorf("TEAM_MEMORY_EXTRACTION_COMPACTION_ENABLED and TEAM_MEMORY_EXTRACTION_SUMMARY_ENABLED cannot both be true")
	}
	if config.sliceEventLimit, err = intEnvironment("TEAM_MEMORY_SLICE_EVENT_LIMIT", 25); err != nil {
		return err
	}
	if config.sliceTokenLimit, err = intEnvironment("TEAM_MEMORY_SLICE_TOKEN_LIMIT", 8192); err != nil {
		return err
	}
	if config.sliceOverlap, err = intEnvironment("TEAM_MEMORY_SLICE_OVERLAP", 3); err != nil {
		return err
	}
	if config.maxSlicesPerJob, err = intEnvironment("TEAM_MEMORY_MAX_SLICES_PER_JOB", 4); err != nil {
		return err
	}
	if config.extractionCompactStartTokens, err = intEnvironment("TEAM_MEMORY_EXTRACTION_COMPACT_START_TOKENS", 12*1024); err != nil {
		return err
	}
	config.extractionCompactTokens, err = intEnvironment("TEAM_MEMORY_EXTRACTION_COMPACT_TOKENS", 16*1024)
	if err != nil {
		return err
	}
	if config.extractionCompactStartTokens > config.extractionCompactTokens {
		return fmt.Errorf("TEAM_MEMORY_EXTRACTION_COMPACT_START_TOKENS cannot exceed TEAM_MEMORY_EXTRACTION_COMPACT_TOKENS")
	}
	if config.extractionSummaryTriggerTokens, err = intEnvironment("TEAM_MEMORY_EXTRACTION_SUMMARY_TRIGGER_TOKENS", 8*1024); err != nil {
		return err
	}
	config.extractionSummaryTailTokens, err = intEnvironment("TEAM_MEMORY_EXTRACTION_SUMMARY_TAIL_TOKENS", 16*1024)
	return err
}

func boolEnvironment(name string, fallback bool) (bool, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", name, err)
	}
	return parsed, nil
}

func loadRetrievalConfig(config *applicationConfig) error {
	var err error
	if config.embeddingTimeout, err = durationEnvironment("TEAM_MEMORY_EMBEDDING_TIMEOUT", 10*time.Second); err != nil {
		return err
	}
	config.recallCandidateStrategy, err = teamnote.ResolveRecallCandidateStrategy("")
	if err != nil {
		return fmt.Errorf("resolve build recall candidate strategy: %w", err)
	}
	policy := &config.recallCandidateStrategy.Policy
	if policy.CandidateLimit, err = intEnvironment("TEAM_MEMORY_RETRIEVAL_CANDIDATE_LIMIT", policy.CandidateLimit); err != nil {
		return err
	}
	if policy.SemanticThreshold, err = floatEnvironment("TEAM_MEMORY_SEMANTIC_THRESHOLD", policy.SemanticThreshold); err != nil {
		return err
	}
	if policy.HintSemanticThreshold, err = floatEnvironment("TEAM_MEMORY_HINT_SEMANTIC_THRESHOLD", policy.HintSemanticThreshold); err != nil {
		return err
	}
	if policy.EnableHintRecall, err = boolEnvironment("TEAM_MEMORY_HINT_RECALL_ENABLED", policy.EnableHintRecall); err != nil {
		return err
	}
	if policy.HintThreshold, err = floatEnvironment("TEAM_MEMORY_HINT_THRESHOLD", policy.HintThreshold); err != nil {
		return err
	}
	if policy.HintMinQueryRelevance, err = floatEnvironment("TEAM_MEMORY_HINT_MIN_QUERY_RELEVANCE", policy.HintMinQueryRelevance); err != nil {
		return err
	}
	policy.HintMinMarginalUtility, err = floatEnvironment("TEAM_MEMORY_HINT_MIN_MARGINAL_UTILITY", policy.HintMinMarginalUtility)
	return err
}

func floatEnvironment(name string, fallback float64) (float64, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number: %w", name, err)
	}
	if parsed < 0 || parsed > 1 {
		return 0, fmt.Errorf("%s must be between zero and one", name)
	}
	return parsed, nil
}

func intEnvironment(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a positive integer: %w", name, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func durationEnvironment(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a positive duration: %w", name, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return parsed, nil
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
			PromptVersion: config.promptVersion, Client: &http.Client{},
			ContextMode: extractor.ContextMode(config.extractionContextMode), EpisodeStore: episodes,
			ExtractionVersion:  config.extractionVersion,
			V2Variant:          config.extractionCandidateStrategy,
			CompactionEnabled:  config.extractionCompactionEnabled,
			CompactStartTokens: config.extractionCompactStartTokens, CompactTokens: config.extractionCompactTokens,
			SummaryEnabled:       config.extractionSummaryEnabled,
			SummaryTriggerTokens: config.extractionSummaryTriggerTokens,
			SummaryTailTokens:    config.extractionSummaryTailTokens,
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
