package main

import (
	"context"
	"encoding/json"
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
	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractionqueue"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
	teamruntime "github.com/pax-beehive/pax-nexus/internal/teamnote/runtime"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/handler"
)

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
	candidateExtractor, err := buildExtractor(config)
	if err != nil {
		return fmt.Errorf("initialize extractor: %w", err)
	}
	noteStore, err := postgres.NewNoteStore(store, teamnote.DefaultTTLPolicy(), teamnote.SystemClock{})
	if err != nil {
		return fmt.Errorf("initialize note store: %w", err)
	}
	runtime, err := teamruntime.New(sessionlake.New(store), candidateExtractor, teamruntime.Config{
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
	if err := store.ConfigureExtractionEnqueuer(queue); err != nil {
		return fmt.Errorf("connect extraction queue: %w", err)
	}
	if err := handler.ConfigureWithLogger(runtime, handler.StaticAPIKeys(config.apiKeys), logger); err != nil {
		return fmt.Errorf("configure HTTP transport: %w", err)
	}
	if err := queue.Start(ctx); err != nil {
		return fmt.Errorf("start extraction queue: %w", err)
	}

	h := server.Default(server.WithHostPorts(config.listenAddress))
	register(h)
	logger.Info("team-memory started", "listen_address", config.listenAddress, "worker_shards", config.workerShards)
	h.Spin()
	stopContext, cancel := context.WithTimeout(context.Background(), config.workerStopTimeout)
	defer cancel()
	if err := queue.Stop(stopContext); err != nil {
		return fmt.Errorf("stop extraction queue: %w", err)
	}
	logger.Info("team-memory stopped")
	return nil
}

type applicationConfig struct {
	databaseURL       string
	listenAddress     string
	apiKeys           map[string]string
	extractorMode     string
	extractorBaseURL  string
	extractorAPIKey   string
	extractorModel    string
	promptVersion     string
	workerShards      int
	workerMaxAttempts int
	workerDebounce    time.Duration
	batchTimeout      time.Duration
	workerJobTimeout  time.Duration
	workerStopTimeout time.Duration
	sliceEventLimit   int
	sliceTokenLimit   int
	sliceOverlap      int
	maxSlicesPerJob   int
}

func loadConfig() (applicationConfig, error) {
	config := applicationConfig{
		databaseURL: os.Getenv("TEAM_MEMORY_DATABASE_URL"), listenAddress: os.Getenv("TEAM_MEMORY_LISTEN_ADDRESS"),
		extractorMode: os.Getenv("TEAM_MEMORY_EXTRACTOR_MODE"), extractorBaseURL: os.Getenv("TEAM_MEMORY_EXTRACTOR_BASE_URL"),
		extractorAPIKey: os.Getenv("TEAM_MEMORY_EXTRACTOR_API_KEY"), extractorModel: os.Getenv("TEAM_MEMORY_EXTRACTOR_MODEL"),
		promptVersion: os.Getenv("TEAM_MEMORY_PROMPT_VERSION"),
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
	if config.sliceEventLimit, err = intEnvironment("TEAM_MEMORY_SLICE_EVENT_LIMIT", 25); err != nil {
		return applicationConfig{}, err
	}
	if config.sliceTokenLimit, err = intEnvironment("TEAM_MEMORY_SLICE_TOKEN_LIMIT", 8192); err != nil {
		return applicationConfig{}, err
	}
	if config.sliceOverlap, err = intEnvironment("TEAM_MEMORY_SLICE_OVERLAP", 3); err != nil {
		return applicationConfig{}, err
	}
	if config.maxSlicesPerJob, err = intEnvironment("TEAM_MEMORY_MAX_SLICES_PER_JOB", 4); err != nil {
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

func buildExtractor(config applicationConfig) (extractor.Extractor, error) {
	switch config.extractorMode {
	case "noop":
		return extractor.Noop{}, nil
	case "openai":
		return extractor.NewOpenAI(extractor.OpenAIConfig{
			BaseURL: config.extractorBaseURL, APIKey: config.extractorAPIKey, Model: config.extractorModel,
			PromptVersion: config.promptVersion, Client: &http.Client{},
		})
	default:
		return nil, fmt.Errorf("unsupported extractor mode %q", config.extractorMode)
	}
}
