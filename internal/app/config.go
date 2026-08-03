package app

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractionbudget"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
)

type applicationConfig struct {
	databaseURL                    string
	listenAddress                  string
	apiKeys                        map[string]string
	adminAPIKey                    string
	bootstrapSecret                string
	oidcIssuer                     string
	oidcClientID                   string
	oidcClientSecret               string
	oidcRedirectURL                string
	oidcFlowSecret                 string
	secretPepper                   string
	memberGrantablePermissions     []onprem.Permission
	portalURL                      string
	humanCookieSecure              bool
	credentialRotationOverlap      time.Duration
	deviceAgentLimit               int
	operationsEventRetention       time.Duration
	operationsStorageRetention     time.Duration
	operationsSnapshotInterval     time.Duration
	operationsMaintenanceTimeout   time.Duration
	wikiHintEnabled                bool
	extractorMode                  string
	extractorBaseURL               string
	extractorAPIKey                string
	extractorModel                 string
	extractorThinkingMode          extractor.ThinkingMode
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
	extractionMaxPromptTokens      int
	extractionExecutionPolicy      extractor.ExecutionPolicy
	providerCallObserver           extractor.ProviderCallObserver
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
	llmwikiMode                    string
	llmwikiBaseURL                 string
	llmwikiAPIKey                  string
	llmwikiModel                   string
	llmwikiTreeMaxDepth            string
	llmwikiCurationInterval        string
	llmwikiCurationPairLimit       string
	llmwikiCurationPageLimit       string
	todoRefreshInterval            time.Duration
}

func loadConfig() (applicationConfig, error) {
	config := applicationConfig{
		databaseURL: os.Getenv("TEAM_MEMORY_DATABASE_URL"), listenAddress: os.Getenv("TEAM_MEMORY_LISTEN_ADDRESS"),
		extractorMode: os.Getenv("TEAM_MEMORY_EXTRACTOR_MODE"), extractorBaseURL: os.Getenv("TEAM_MEMORY_EXTRACTOR_BASE_URL"),
		extractorAPIKey: os.Getenv("TEAM_MEMORY_EXTRACTOR_API_KEY"), extractorModel: os.Getenv("TEAM_MEMORY_EXTRACTOR_MODEL"),
		llmwikiMode: os.Getenv("LLMWIKI_ORGANIZER_MODE"), llmwikiBaseURL: os.Getenv("LLMWIKI_LLM_BASE_URL"),
		llmwikiAPIKey: os.Getenv("LLMWIKI_LLM_API_KEY"), llmwikiModel: os.Getenv("LLMWIKI_LLM_MODEL"),
		llmwikiTreeMaxDepth:         os.Getenv("LLMWIKI_TREE_MAX_DEPTH"),
		llmwikiCurationInterval:     os.Getenv("LLMWIKI_CURATION_INTERVAL"),
		llmwikiCurationPairLimit:    os.Getenv("LLMWIKI_CURATION_PAIR_LIMIT"),
		llmwikiCurationPageLimit:    os.Getenv("LLMWIKI_CURATION_PAGE_LIMIT"),
		promptVersion:               os.Getenv("TEAM_MEMORY_PROMPT_VERSION"),
		extractionContextMode:       os.Getenv("TEAM_MEMORY_EXTRACTION_CONTEXT_MODE"),
		extractionVersion:           os.Getenv("TEAM_MEMORY_EXTRACTION_VERSION"),
		extractionCandidateStrategy: os.Getenv("TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY"),
		embeddingBaseURL:            os.Getenv("TEAM_MEMORY_EMBEDDING_BASE_URL"),
		embeddingModel:              os.Getenv("TEAM_MEMORY_EMBEDDING_MODEL"),
		adminAPIKey:                 os.Getenv("TEAM_MEMORY_ADMIN_API_KEY"),
		bootstrapSecret:             os.Getenv("TEAM_MEMORY_BOOTSTRAP_SECRET"),
		oidcIssuer:                  os.Getenv("TEAM_MEMORY_OIDC_ISSUER"),
		oidcClientID:                os.Getenv("TEAM_MEMORY_OIDC_CLIENT_ID"),
		oidcClientSecret:            os.Getenv("TEAM_MEMORY_OIDC_CLIENT_SECRET"),
		oidcRedirectURL:             os.Getenv("TEAM_MEMORY_OIDC_REDIRECT_URL"),
		oidcFlowSecret:              os.Getenv("TEAM_MEMORY_OIDC_FLOW_SECRET"),
		secretPepper:                os.Getenv("TEAM_MEMORY_SECRET_PEPPER"),
		portalURL:                   os.Getenv("TEAM_MEMORY_PORTAL_URL"),
	}
	var err error
	if config.extractorThinkingMode, err = extractor.ParseThinkingMode(os.Getenv("TEAM_MEMORY_EXTRACTOR_THINKING_MODE")); err != nil {
		return applicationConfig{}, fmt.Errorf("load extractor thinking mode: %w", err)
	}
	if err = loadWorkerConfig(&config); err != nil {
		return applicationConfig{}, err
	}
	if err = loadOnPremConfig(&config); err != nil {
		return applicationConfig{}, err
	}
	if err = loadAndValidateExtractionConfig(&config); err != nil {
		return applicationConfig{}, err
	}
	if err = loadRetrievalConfig(&config); err != nil {
		return applicationConfig{}, err
	}
	if config.todoRefreshInterval, err = durationEnvironment("TODOAPP_REFRESH_INTERVAL", time.Hour); err != nil {
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
		config.extractionVersion = extractor.ExtractionVersionV1
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
	if err = loadAuthenticationConfig(&config); err != nil {
		return applicationConfig{}, err
	}
	return config, nil
}

func loadWorkerConfig(config *applicationConfig) error {
	var err error
	if config.workerShards, err = intEnvironment("TEAM_MEMORY_WORKER_SHARDS", 16); err != nil {
		return err
	}
	if config.workerMaxAttempts, err = intEnvironment("TEAM_MEMORY_WORKER_MAX_ATTEMPTS", 5); err != nil {
		return err
	}
	if config.workerDebounce, err = durationEnvironment("TEAM_MEMORY_WORKER_DEBOUNCE", 750*time.Millisecond); err != nil {
		return err
	}
	if config.batchTimeout, err = durationEnvironment("TEAM_MEMORY_BATCH_TIMEOUT", 30*time.Second); err != nil {
		return err
	}
	if config.workerJobTimeout, err = durationEnvironment(
		"TEAM_MEMORY_WORKER_JOB_TIMEOUT", extractionbudget.DefaultWorkerJobTimeout,
	); err != nil {
		return err
	}
	config.workerStopTimeout, err = durationEnvironment("TEAM_MEMORY_WORKER_STOP_TIMEOUT", 30*time.Second)
	return err
}

func loadOnPremConfig(config *applicationConfig) error {
	var err error
	if config.credentialRotationOverlap, err = durationEnvironment(
		"TEAM_MEMORY_CREDENTIAL_ROTATION_OVERLAP", 5*time.Minute,
	); err != nil {
		return err
	}
	if config.wikiHintEnabled, err = boolEnvironment("TEAM_MEMORY_WIKI_HINT_ENABLED", false); err != nil {
		return err
	}
	if config.operationsEventRetention, err = durationEnvironment(
		"TEAM_MEMORY_OPERATIONS_EVENT_RETENTION", 7*24*time.Hour,
	); err != nil {
		return err
	}
	if config.operationsStorageRetention, err = durationEnvironment(
		"TEAM_MEMORY_OPERATIONS_STORAGE_RETENTION", 90*24*time.Hour,
	); err != nil {
		return err
	}
	if config.operationsSnapshotInterval, err = durationEnvironment(
		"TEAM_MEMORY_OPERATIONS_SNAPSHOT_INTERVAL", time.Hour,
	); err != nil {
		return err
	}
	if config.operationsMaintenanceTimeout, err = durationEnvironment(
		"TEAM_MEMORY_OPERATIONS_MAINTENANCE_TIMEOUT", 15*time.Second,
	); err != nil {
		return err
	}
	if err := validateOperationsConfig(*config); err != nil {
		return err
	}
	if config.deviceAgentLimit, err = intEnvironment("TEAM_MEMORY_DEVICE_AGENT_LIMIT", 16); err != nil {
		return err
	}
	if config.deviceAgentLimit < 1 || config.deviceAgentLimit > 1000 {
		return fmt.Errorf("TEAM_MEMORY_DEVICE_AGENT_LIMIT must be between 1 and 1000")
	}
	config.humanCookieSecure, err = boolEnvironment("TEAM_MEMORY_HUMAN_COOKIE_SECURE", true)
	if err != nil {
		return err
	}
	config.memberGrantablePermissions, err = permissionListEnvironment(
		"TEAM_MEMORY_MEMBER_GRANTABLE_PERMISSIONS",
		"observe,search,get,channel_send,channel_receive",
	)
	if err != nil {
		return err
	}
	if config.portalURL == "" {
		config.portalURL = "/"
	}
	return err
}

func validateOperationsConfig(config applicationConfig) error {
	tests := []struct {
		name    string
		value   time.Duration
		minimum time.Duration
		maximum time.Duration
	}{
		{name: "TEAM_MEMORY_OPERATIONS_EVENT_RETENTION", value: config.operationsEventRetention, minimum: 24 * time.Hour, maximum: 90 * 24 * time.Hour},
		{name: "TEAM_MEMORY_OPERATIONS_STORAGE_RETENTION", value: config.operationsStorageRetention, minimum: 7 * 24 * time.Hour, maximum: 365 * 24 * time.Hour},
		{name: "TEAM_MEMORY_OPERATIONS_SNAPSHOT_INTERVAL", value: config.operationsSnapshotInterval, minimum: 5 * time.Minute, maximum: 24 * time.Hour},
		{name: "TEAM_MEMORY_OPERATIONS_MAINTENANCE_TIMEOUT", value: config.operationsMaintenanceTimeout, minimum: time.Second, maximum: 5 * time.Minute},
	}
	for _, test := range tests {
		if test.value < test.minimum || test.value > test.maximum {
			return fmt.Errorf("%s must be between %s and %s", test.name, test.minimum, test.maximum)
		}
	}
	return nil
}

func loadAuthenticationConfig(config *applicationConfig) error {
	apiKeys := strings.TrimSpace(os.Getenv("TEAM_MEMORY_API_KEYS"))
	if apiKeys == "" {
		config.apiKeys = make(map[string]string)
	} else if err := json.Unmarshal([]byte(apiKeys), &config.apiKeys); err != nil {
		return fmt.Errorf("decode TEAM_MEMORY_API_KEYS: %w", err)
	}
	identityConfigured := config.humanIdentityConfigured()
	if config.humanIdentitySettingCount() > 0 && !identityConfigured {
		return fmt.Errorf("all TEAM_MEMORY_BOOTSTRAP_SECRET and TEAM_MEMORY_OIDC_* settings are required together")
	}
	if len(config.apiKeys) > 0 && (strings.TrimSpace(config.adminAPIKey) != "" || identityConfigured) {
		return fmt.Errorf("TEAM_MEMORY_API_KEYS and on-prem identity settings select mutually exclusive authentication modes")
	}
	if len(config.apiKeys) == 0 && strings.TrimSpace(config.adminAPIKey) == "" && !identityConfigured {
		return fmt.Errorf("TEAM_MEMORY_API_KEYS, TEAM_MEMORY_ADMIN_API_KEY, or complete OIDC identity settings are required")
	}
	if len(config.apiKeys) == 0 && len(strings.TrimSpace(config.secretPepper)) < 32 {
		return fmt.Errorf("TEAM_MEMORY_SECRET_PEPPER must contain at least 32 characters in on-prem mode")
	}
	return nil
}

func permissionListEnvironment(name string, fallback string) ([]onprem.Permission, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		raw = fallback
	}
	seen := make(map[onprem.Permission]struct{})
	result := make([]onprem.Permission, 0)
	for _, value := range strings.Split(raw, ",") {
		permission := onprem.Permission(strings.TrimSpace(value))
		switch permission {
		case onprem.PermissionObserve, onprem.PermissionSearch, onprem.PermissionGet,
			onprem.PermissionChannelSend, onprem.PermissionChannelReceive:
		default:
			return nil, fmt.Errorf("%s contains unsupported permission %q", name, permission)
		}
		if _, exists := seen[permission]; exists {
			continue
		}
		seen[permission] = struct{}{}
		result = append(result, permission)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("%s must not be empty", name)
	}
	return result, nil
}

func (config applicationConfig) humanIdentityConfigured() bool {
	return config.humanIdentitySettingCount() == 6
}

func (config applicationConfig) humanIdentitySettingCount() int {
	values := []string{
		config.bootstrapSecret, config.oidcIssuer, config.oidcClientID,
		config.oidcClientSecret, config.oidcRedirectURL, config.oidcFlowSecret,
	}
	configured := 0
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			configured++
		}
	}
	return configured
}

func loadAndValidateExtractionConfig(config *applicationConfig) error {
	if err := loadExtractionConfig(config); err != nil {
		return err
	}
	return (extractionbudget.Envelope{
		Provider: config.extractionExecutionPolicy, MaxSlicesPerJob: config.maxSlicesPerJob,
		WorkerJobTimeout: config.workerJobTimeout, CompactionEnabled: config.extractionCompactionEnabled,
	}).Validate()
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
	if config.maxSlicesPerJob, err = intEnvironment(
		"TEAM_MEMORY_MAX_SLICES_PER_JOB", extractionbudget.DefaultMaxSlicesPerJob,
	); err != nil {
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
	if err != nil {
		return err
	}
	if config.extractionMaxPromptTokens, err = intEnvironment("TEAM_MEMORY_EXTRACTION_MAX_PROMPT_TOKENS", 128*1024); err != nil {
		return err
	}
	return loadExtractionExecutionPolicy(config)
}

func loadExtractionExecutionPolicy(config *applicationConfig) error {
	var err error
	policy := &config.extractionExecutionPolicy
	providerDefaults := extractionbudget.DefaultProviderPolicy()
	if policy.AttemptTimeout, err = durationEnvironment(
		"TEAM_MEMORY_EXTRACTION_PROVIDER_TIMEOUT", providerDefaults.AttemptTimeout,
	); err != nil {
		return err
	}
	if policy.MaxAttempts, err = intEnvironment(
		"TEAM_MEMORY_EXTRACTION_PROVIDER_MAX_ATTEMPTS", providerDefaults.MaxAttempts,
	); err != nil {
		return err
	}
	if policy.RetryBackoff, err = durationEnvironment(
		"TEAM_MEMORY_EXTRACTION_PROVIDER_RETRY_BACKOFF", providerDefaults.RetryBackoff,
	); err != nil {
		return err
	}
	maxResponseBytes, err := intEnvironment(
		"TEAM_MEMORY_EXTRACTION_PROVIDER_MAX_RESPONSE_BYTES", int(providerDefaults.MaxResponseBytes),
	)
	if err != nil {
		return err
	}
	policy.MaxResponseBytes = int64(maxResponseBytes)
	if policy.PrimaryMaxOutputTokens, err = intEnvironment(
		"TEAM_MEMORY_EXTRACTION_PRIMARY_MAX_OUTPUT_TOKENS", providerDefaults.PrimaryMaxOutputTokens,
	); err != nil {
		return err
	}
	if policy.SummaryMaxOutputTokens, err = intEnvironment(
		"TEAM_MEMORY_EXTRACTION_SUMMARY_MAX_OUTPUT_TOKENS", providerDefaults.SummaryMaxOutputTokens,
	); err != nil {
		return err
	}
	policy.CompactionMaxOutputTokens, err = intEnvironment(
		"TEAM_MEMORY_EXTRACTION_COMPACTION_MAX_OUTPUT_TOKENS", providerDefaults.CompactionMaxOutputTokens,
	)
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
