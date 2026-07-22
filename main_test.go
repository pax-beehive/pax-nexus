package main

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/operations"
	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
	teamruntime "github.com/pax-beehive/pax-nexus/internal/teamnote/runtime"
	"github.com/stretchr/testify/suite"
)

type configSuite struct {
	suite.Suite
}

type operationsMaintenanceStoreFake struct {
	capturedAt       time.Time
	eventCutoff      time.Time
	storageCutoff    time.Time
	deletedEvents    int64
	deletedSnapshots int64
	recorded         *operations.Event
}

func (s *operationsMaintenanceStoreFake) CaptureStorage(
	_ context.Context,
	capturedAt time.Time,
) (operations.StorageSnapshot, error) {
	s.capturedAt = capturedAt
	return operations.StorageSnapshot{CapturedAt: capturedAt}, nil
}

func (s *operationsMaintenanceStoreFake) DeleteBefore(
	_ context.Context,
	eventCutoff time.Time,
	storageCutoff time.Time,
) (int64, int64, error) {
	s.eventCutoff = eventCutoff
	s.storageCutoff = storageCutoff
	return s.deletedEvents, s.deletedSnapshots, nil
}

func (s *operationsMaintenanceStoreFake) Record(
	_ context.Context,
	event operations.Event,
) (operations.Event, error) {
	s.recorded = &event
	return event, nil
}

func TestConfigSuite(t *testing.T) {
	suite.Run(t, new(configSuite))
}

func (s *configSuite) SetupTest() {
	for _, name := range []string{
		"TEAM_MEMORY_DATABASE_URL", "TEAM_MEMORY_API_KEYS", "TEAM_MEMORY_LISTEN_ADDRESS",
		"TEAM_MEMORY_ADMIN_API_KEY", "TEAM_MEMORY_CREDENTIAL_ROTATION_OVERLAP", "TEAM_MEMORY_WIKI_HINT_ENABLED",
		"TEAM_MEMORY_OPERATIONS_EVENT_RETENTION", "TEAM_MEMORY_OPERATIONS_STORAGE_RETENTION",
		"TEAM_MEMORY_OPERATIONS_SNAPSHOT_INTERVAL", "TEAM_MEMORY_OPERATIONS_MAINTENANCE_TIMEOUT",
		"TEAM_MEMORY_BOOTSTRAP_SECRET", "TEAM_MEMORY_OIDC_ISSUER", "TEAM_MEMORY_OIDC_CLIENT_ID",
		"TEAM_MEMORY_OIDC_CLIENT_SECRET", "TEAM_MEMORY_OIDC_REDIRECT_URL", "TEAM_MEMORY_OIDC_FLOW_SECRET",
		"TEAM_MEMORY_PORTAL_URL", "TEAM_MEMORY_HUMAN_COOKIE_SECURE",
		"TEAM_MEMORY_SECRET_PEPPER", "TEAM_MEMORY_MEMBER_GRANTABLE_PERMISSIONS",
		"TEAM_MEMORY_EXTRACTOR_MODE", "TEAM_MEMORY_EXTRACTOR_BASE_URL",
		"TEAM_MEMORY_EXTRACTOR_API_KEY", "TEAM_MEMORY_EXTRACTOR_MODEL", "TEAM_MEMORY_PROMPT_VERSION",
		"TEAM_MEMORY_EXTRACTION_CONTEXT_MODE", "TEAM_MEMORY_EXTRACTION_VERSION",
		"TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY",
		"TEAM_MEMORY_EXTRACTION_COMPACT_START_TOKENS",
		"TEAM_MEMORY_EXTRACTION_COMPACT_TOKENS", "TEAM_MEMORY_EXTRACTION_COMPACTION_ENABLED",
		"TEAM_MEMORY_EXTRACTION_SUMMARY_ENABLED", "TEAM_MEMORY_EXTRACTION_SUMMARY_TRIGGER_TOKENS",
		"TEAM_MEMORY_EXTRACTION_SUMMARY_TAIL_TOKENS",
		"TEAM_MEMORY_EXTRACTION_PROVIDER_TIMEOUT", "TEAM_MEMORY_EXTRACTION_PROVIDER_MAX_ATTEMPTS",
		"TEAM_MEMORY_EXTRACTION_PROVIDER_RETRY_BACKOFF", "TEAM_MEMORY_EXTRACTION_PROVIDER_MAX_RESPONSE_BYTES",
		"TEAM_MEMORY_EXTRACTION_PRIMARY_MAX_OUTPUT_TOKENS", "TEAM_MEMORY_EXTRACTION_SUMMARY_MAX_OUTPUT_TOKENS",
		"TEAM_MEMORY_EXTRACTION_COMPACTION_MAX_OUTPUT_TOKENS",
		"TEAM_MEMORY_WORKER_SHARDS", "TEAM_MEMORY_WORKER_MAX_ATTEMPTS",
		"TEAM_MEMORY_WORKER_DEBOUNCE", "TEAM_MEMORY_BATCH_TIMEOUT",
		"TEAM_MEMORY_WORKER_JOB_TIMEOUT", "TEAM_MEMORY_WORKER_STOP_TIMEOUT",
		"TEAM_MEMORY_SLICE_EVENT_LIMIT", "TEAM_MEMORY_SLICE_TOKEN_LIMIT",
		"TEAM_MEMORY_SLICE_OVERLAP", "TEAM_MEMORY_MAX_SLICES_PER_JOB",
		"TEAM_MEMORY_EMBEDDING_BASE_URL", "TEAM_MEMORY_EMBEDDING_MODEL",
		"TEAM_MEMORY_EMBEDDING_TIMEOUT", "TEAM_MEMORY_SEMANTIC_THRESHOLD",
		"TEAM_MEMORY_RETRIEVAL_CANDIDATE_LIMIT", "TEAM_MEMORY_HINT_RECALL_ENABLED", "TEAM_MEMORY_HINT_SEMANTIC_THRESHOLD", "TEAM_MEMORY_HINT_THRESHOLD", "TEAM_MEMORY_HINT_MIN_QUERY_RELEVANCE", "TEAM_MEMORY_HINT_MIN_MARGINAL_UTILITY",
	} {
		s.T().Setenv(name, "")
	}
}

func (s *configSuite) TestLoadsNoopConfiguration() {
	s.T().Setenv("TEAM_MEMORY_DATABASE_URL", "postgres://database")
	s.T().Setenv("TEAM_MEMORY_API_KEYS", `{"key":"scope"}`)
	s.T().Setenv("TEAM_MEMORY_EXTRACTOR_MODE", "noop")

	config, err := loadConfig()
	s.Require().NoError(err)
	s.Equal(":8080", config.listenAddress)
	s.Equal("scope", config.apiKeys["key"])
	s.Equal("v1", config.promptVersion)
	s.Equal("rolling", config.extractionContextMode)
	s.Equal("v1", config.extractionVersion)
	s.Equal(extractor.DefaultCandidateStrategy(), config.extractionCandidateStrategy)
	s.False(config.extractionCompactionEnabled)
	s.True(config.extractionSummaryEnabled)
	s.Equal(12*1024, config.extractionCompactStartTokens)
	s.Equal(16*1024, config.extractionCompactTokens)
	s.Equal(8*1024, config.extractionSummaryTriggerTokens)
	s.Equal(16*1024, config.extractionSummaryTailTokens)
	s.Equal(120*time.Second, config.extractionExecutionPolicy.AttemptTimeout)
	s.Equal(1, config.extractionExecutionPolicy.MaxAttempts)
	s.Equal(250*time.Millisecond, config.extractionExecutionPolicy.RetryBackoff)
	s.Equal(int64(1<<20), config.extractionExecutionPolicy.MaxResponseBytes)
	s.Equal(16*1024, config.extractionExecutionPolicy.PrimaryMaxOutputTokens)
	s.Equal(16, config.workerShards)
	s.Equal(5, config.workerMaxAttempts)
	s.Equal(750*time.Millisecond, config.workerDebounce)
	s.Equal(30*time.Second, config.batchTimeout)
	s.Equal(3*time.Minute, config.workerJobTimeout)
	s.Equal(5*time.Minute, config.credentialRotationOverlap)
	s.False(config.wikiHintEnabled)
	s.Equal(25, config.sliceEventLimit)
	s.Equal(8192, config.sliceTokenLimit)
	s.Equal(3, config.sliceOverlap)
	s.Equal(1, config.maxSlicesPerJob)
	s.Equal(10*time.Second, config.embeddingTimeout)
	strategy, err := teamnote.ResolveRecallCandidateStrategy("")
	s.Require().NoError(err)
	s.Equal(strategy, config.recallCandidateStrategy)
	adapter, err := buildExtractor(config)
	s.Require().NoError(err)
	s.IsType(extractor.Noop{}, adapter)
}

func (s *configSuite) TestLoadsOnPremConfiguration() {
	s.T().Setenv("TEAM_MEMORY_DATABASE_URL", "postgres://database")
	s.T().Setenv("TEAM_MEMORY_EXTRACTOR_MODE", "noop")
	s.T().Setenv("TEAM_MEMORY_ADMIN_API_KEY", "admin-secret")
	s.T().Setenv("TEAM_MEMORY_SECRET_PEPPER", "0123456789abcdef0123456789abcdef")
	s.T().Setenv("TEAM_MEMORY_MEMBER_GRANTABLE_PERMISSIONS", "search,channel_send")
	s.T().Setenv("TEAM_MEMORY_CREDENTIAL_ROTATION_OVERLAP", "2m")
	s.T().Setenv("TEAM_MEMORY_WIKI_HINT_ENABLED", "true")

	config, err := loadConfig()

	s.Require().NoError(err)
	s.Equal("admin-secret", config.adminAPIKey)
	s.Empty(config.apiKeys)
	s.Equal(2*time.Minute, config.credentialRotationOverlap)
	s.Equal(7*24*time.Hour, config.operationsEventRetention)
	s.Equal(90*24*time.Hour, config.operationsStorageRetention)
	s.Equal(time.Hour, config.operationsSnapshotInterval)
	s.Equal(15*time.Second, config.operationsMaintenanceTimeout)
	s.True(config.wikiHintEnabled)
	s.True(config.humanCookieSecure)
	s.Equal([]onprem.Permission{onprem.PermissionSearch, onprem.PermissionChannelSend}, config.memberGrantablePermissions)
}

func (s *configSuite) TestRejectsOperationsConfigurationOutsideSafeBounds() {
	tests := []struct {
		name  string
		env   string
		value string
	}{
		{name: "event retention", env: "TEAM_MEMORY_OPERATIONS_EVENT_RETENTION", value: "23h"},
		{name: "storage retention", env: "TEAM_MEMORY_OPERATIONS_STORAGE_RETENTION", value: "8761h"},
		{name: "snapshot interval", env: "TEAM_MEMORY_OPERATIONS_SNAPSHOT_INTERVAL", value: "4m"},
		{name: "maintenance timeout", env: "TEAM_MEMORY_OPERATIONS_MAINTENANCE_TIMEOUT", value: "500ms"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			s.T().Setenv("TEAM_MEMORY_DATABASE_URL", "postgres://database")
			s.T().Setenv("TEAM_MEMORY_EXTRACTOR_MODE", "noop")
			s.T().Setenv("TEAM_MEMORY_ADMIN_API_KEY", "admin-secret")
			s.T().Setenv("TEAM_MEMORY_SECRET_PEPPER", "0123456789abcdef0123456789abcdef")
			s.T().Setenv(test.env, test.value)

			_, err := loadConfig()

			s.ErrorContains(err, test.env)
		})
	}
}

func (s *configSuite) TestOperationsMaintenanceCapturesCleansAndRecords() {
	now := time.Now().UTC().Add(-time.Second)
	store := &operationsMaintenanceStoreFake{deletedEvents: 2, deletedSnapshots: 3}
	config := applicationConfig{
		operationsEventRetention: 7 * 24 * time.Hour, operationsStorageRetention: 90 * 24 * time.Hour,
		operationsMaintenanceTimeout: time.Second,
	}

	maintainOperations(context.Background(), store, store, config, slog.New(slog.DiscardHandler), now)

	s.Equal(now, store.capturedAt)
	s.Equal(now.Add(-config.operationsEventRetention), store.eventCutoff)
	s.Equal(now.Add(-config.operationsStorageRetention), store.storageCutoff)
	s.Require().NotNil(store.recorded)
	s.Equal(operations.KindSystemRetention, store.recorded.Kind)
	s.Equal(operations.OutcomeSucceeded, store.recorded.Outcome)
	s.Equal(int64(5), store.recorded.AcceptedItems)
	s.NoError(store.recorded.Validate())
}

func (s *configSuite) TestExtractionObserverRecordsEverySliceOutcome() {
	tests := []struct {
		name        string
		status      teamruntime.ExtractionStatus
		wantOutcome operations.Outcome
		wantCode    string
	}{
		{name: "success", status: teamruntime.ExtractionCompleted, wantOutcome: operations.OutcomeSucceeded},
		{name: "quarantine", status: teamruntime.ExtractionQuarantined, wantOutcome: operations.OutcomeRejected, wantCode: "extraction_quarantined"},
		{name: "deadline", status: teamruntime.ExtractionTimedOut, wantOutcome: operations.OutcomeTimedOut, wantCode: "deadline_exceeded"},
		{name: "failure", status: teamruntime.ExtractionFailed, wantOutcome: operations.OutcomeFailed, wantCode: "operation_failed"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			store := &operationsMaintenanceStoreFake{}
			observer := onprem.NewExtractionObserver(store, slog.New(slog.DiscardHandler))
			startedAt := time.Now().UTC().Add(-time.Second)
			observer(context.Background(), teamruntime.ExtractionObservation{
				Actor: teamnote.Actor{UserID: "owner", AgentID: "agent-1", SessionID: "session-1"},
				RunID: "run-1", StartedAt: startedAt, CompletedAt: startedAt.Add(time.Second),
				Status: test.status, InputEvents: 3, Candidates: 2, InputTokens: 11, OutputTokens: 7,
			})

			s.Require().NotNil(store.recorded)
			s.Equal(operations.KindExtractionRun, store.recorded.Kind)
			s.Equal(test.wantOutcome, store.recorded.Outcome)
			s.Equal(test.wantCode, store.recorded.ErrorCode)
			s.Equal(int64(3), store.recorded.InputItems)
			s.Equal(int64(2), store.recorded.ResultItems)
			s.Equal("run-1", store.recorded.DetailID)
			s.NoError(store.recorded.Validate())
		})
	}
}

func (s *configSuite) TestRejectsPartialOIDCConfiguration() {
	s.T().Setenv("TEAM_MEMORY_DATABASE_URL", "postgres://database")
	s.T().Setenv("TEAM_MEMORY_EXTRACTOR_MODE", "noop")
	s.T().Setenv("TEAM_MEMORY_ADMIN_API_KEY", "admin-secret")
	s.T().Setenv("TEAM_MEMORY_OIDC_ISSUER", "https://identity.example")

	_, err := loadConfig()

	s.Require().ErrorContains(err, "required together")
}

func (s *configSuite) TestRejectsMixedLegacyAndOnPremAuthentication() {
	s.T().Setenv("TEAM_MEMORY_DATABASE_URL", "postgres://database")
	s.T().Setenv("TEAM_MEMORY_EXTRACTOR_MODE", "noop")
	s.T().Setenv("TEAM_MEMORY_API_KEYS", `{"legacy":"other-scope"}`)
	s.T().Setenv("TEAM_MEMORY_ADMIN_API_KEY", "admin-secret")

	_, err := loadConfig()

	s.Require().ErrorContains(err, "mutually exclusive")
}

func (s *configSuite) TestBuildHTTPHandlerKeepsLegacyModeWithoutAdminSecret() {
	runtime := &runtimeStub{}
	configured, err := buildHTTPHandler(context.Background(), runtime, nil, nil,
		applicationConfig{apiKeys: map[string]string{"key": "scope"}}, slog.New(slog.DiscardHandler))
	s.Require().NoError(err)
	s.NotNil(configured)

	_, err = buildHTTPHandler(context.Background(), runtime, nil, nil, applicationConfig{
		apiKeys: map[string]string{"key": "scope"}, adminAPIKey: "admin", credentialRotationOverlap: time.Minute,
	}, slog.New(slog.DiscardHandler))
	s.Error(err)
}

func (s *configSuite) TestRejectsExtractionExecutionBudgetThatExceedsJobDeadline() {
	tests := []struct {
		name       string
		maxSlices  string
		compaction bool
	}{
		{name: "multiple primary calls", maxSlices: "2"},
		{name: "compaction fallback calls", maxSlices: "1", compaction: true},
	}

	for _, test := range tests {
		s.Run(test.name, func() {
			s.T().Setenv("TEAM_MEMORY_DATABASE_URL", "postgres://database")
			s.T().Setenv("TEAM_MEMORY_API_KEYS", `{"key":"scope"}`)
			s.T().Setenv("TEAM_MEMORY_EXTRACTOR_MODE", "noop")
			s.T().Setenv("TEAM_MEMORY_EXTRACTION_PROVIDER_TIMEOUT", "2m")
			s.T().Setenv("TEAM_MEMORY_MAX_SLICES_PER_JOB", test.maxSlices)
			s.T().Setenv("TEAM_MEMORY_WORKER_JOB_TIMEOUT", "3m")
			if test.compaction {
				s.T().Setenv("TEAM_MEMORY_EXTRACTION_SUMMARY_ENABLED", "false")
				s.T().Setenv("TEAM_MEMORY_EXTRACTION_COMPACTION_ENABLED", "true")
			}

			_, err := loadConfig()

			s.Require().Error(err)
			s.ErrorContains(err, "extraction execution budget")
		})
	}
}

func (s *configSuite) TestRuntimeCandidateStrategyOverridesBuildDefault() {
	s.T().Setenv("TEAM_MEMORY_DATABASE_URL", "postgres://database")
	s.T().Setenv("TEAM_MEMORY_API_KEYS", `{"key":"scope"}`)
	s.T().Setenv("TEAM_MEMORY_EXTRACTOR_MODE", "noop")
	s.T().Setenv("TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY", extractor.CandidateStrategyTyped2)

	config, err := loadConfig()

	s.Require().NoError(err)
	s.Equal(extractor.CandidateStrategyTyped2, config.extractionCandidateStrategy)
}

func (s *configSuite) TestAllowsExtractionV2OptIn() {
	s.T().Setenv("TEAM_MEMORY_DATABASE_URL", "postgres://database")
	s.T().Setenv("TEAM_MEMORY_API_KEYS", `{"key":"scope"}`)
	s.T().Setenv("TEAM_MEMORY_EXTRACTOR_MODE", "noop")
	s.T().Setenv("TEAM_MEMORY_EXTRACTION_VERSION", "v2")

	config, err := loadConfig()
	s.Require().NoError(err)
	s.Equal("v2", config.extractionVersion)
}

func (s *configSuite) TestCheckedInExtractionProtocolDefaults() {
	tests := []struct {
		path string
		want string
	}{
		{path: ".env.example", want: "TEAM_MEMORY_EXTRACTION_VERSION=v1"},
		{path: ".env.eval-v2.example", want: "TEAM_MEMORY_EXTRACTION_VERSION=v2"},
		{path: "compose.yaml", want: "TEAM_MEMORY_EXTRACTION_VERSION: ${TEAM_MEMORY_EXTRACTION_VERSION:-v1}"},
		{path: "evals/opencode/compose.yaml", want: "TEAM_MEMORY_EXTRACTION_VERSION: ${TEAM_MEMORY_EXTRACTION_VERSION:-v2}"},
		{path: "evals/v2/compose.yaml", want: "TEAM_MEMORY_EXTRACTION_VERSION: ${TEAM_MEMORY_EXTRACTION_VERSION:-v2}"},
		{path: "scripts/load-eval-v2-env.sh", want: `: "${TEAM_MEMORY_EXTRACTION_VERSION:=v2}"`},
	}
	for _, test := range tests {
		s.Run(test.path, func() {
			content, err := os.ReadFile(test.path)
			s.Require().NoError(err)
			s.Contains(string(content), test.want)
		})
	}
}

func (s *configSuite) TestCheckedInCandidateStrategyBuildInterface() {
	tests := []struct {
		path string
		want string
	}{
		{path: ".env.example", want: "evidence-fidelity-v1, source-clause-v1, source-clause-implicit-state-v1, typed-2"},
		{path: ".env.example", want: "TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY="},
		{path: ".env.eval-v2.example", want: "TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY=source-clause-v1"},
		{path: "compose.yaml", want: "EXTRACTION_CANDIDATE_STRATEGY: ${TEAM_MEMORY_BUILD_EXTRACTION_CANDIDATE_STRATEGY:-source-clause-v1}"},
		{path: "evals/v2/compose.yaml", want: "TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY: ${TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY:-}"},
		{path: "evals/opencode/compose.yaml", want: "TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY: ${TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY:-}"},
		{path: "evals/v2/config.example.yaml", want: "  - TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY"},
		{path: "evals/v2/config.smoke.example.yaml", want: "  - TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY"},
		{path: "evals/v3/config.example.yaml", want: "  - TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY"},
		{path: "scripts/load-eval-v2-env.sh", want: `: "${TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY:=source-clause-v1}"`},
		{path: "scripts/load-eval-v2-env.sh", want: "TEAM_MEMORY_EXTRACTION_VERSION TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY"},
		{path: "Dockerfile", want: "ARG EXTRACTION_CANDIDATE_STRATEGY=source-clause-v1"},
		{path: "Makefile", want: "EXTRACTION_CANDIDATE_STRATEGY ?= source-clause-v1"},
	}
	for _, test := range tests {
		s.Run(test.path, func() {
			content, err := os.ReadFile(test.path)
			s.Require().NoError(err)
			s.Contains(string(content), test.want)
		})
	}
}

func (s *configSuite) TestCheckedInRecallCandidateStrategyBuildInterface() {
	tests := []struct {
		path string
		want string
	}{
		{path: ".env.example", want: "TEAM_MEMORY_BUILD_RECALL_CANDIDATE_STRATEGY=passive-v1"},
		{path: "compose.yaml", want: "RECALL_CANDIDATE_STRATEGY: ${TEAM_MEMORY_BUILD_RECALL_CANDIDATE_STRATEGY:-passive-v1}"},
		{path: "evals/v2/compose.yaml", want: "RECALL_CANDIDATE_STRATEGY: passive-v1"},
		{path: "evals/v2/compose.yaml", want: "RECALL_CANDIDATE_STRATEGY: hint-v1-selective"},
		{path: "evals/opencode/compose.yaml", want: "RECALL_CANDIDATE_STRATEGY: ${TEAM_MEMORY_BUILD_RECALL_CANDIDATE_STRATEGY:-passive-v1}"},
		{path: "Dockerfile", want: "ARG RECALL_CANDIDATE_STRATEGY=passive-v1"},
		{path: "Makefile", want: "RECALL_CANDIDATE_STRATEGY ?= passive-v1"},
	}
	for _, test := range tests {
		s.Run(test.path+test.want, func() {
			content, err := os.ReadFile(test.path)
			s.Require().NoError(err)
			s.Contains(string(content), test.want)
		})
	}
}

func (s *configSuite) TestRuntimeRecallPolicyOverridesBuildDefaultCompatibly() {
	s.T().Setenv("TEAM_MEMORY_DATABASE_URL", "postgres://database")
	s.T().Setenv("TEAM_MEMORY_API_KEYS", `{"key":"scope"}`)
	s.T().Setenv("TEAM_MEMORY_SEMANTIC_THRESHOLD", "0.20")

	config, err := loadConfig()

	s.Require().NoError(err)
	s.InDelta(0.20, config.recallCandidateStrategy.Policy.SemanticThreshold, 0.001)
}

func (s *configSuite) TestRejectsInvalidWorkerConfiguration() {
	s.T().Setenv("TEAM_MEMORY_DATABASE_URL", "postgres://database")
	s.T().Setenv("TEAM_MEMORY_API_KEYS", `{"key":"scope"}`)
	tests := []struct {
		name  string
		value string
	}{
		{name: "TEAM_MEMORY_WORKER_SHARDS", value: "zero"},
		{name: "TEAM_MEMORY_WORKER_MAX_ATTEMPTS", value: "0"},
		{name: "TEAM_MEMORY_WORKER_DEBOUNCE", value: "later"},
		{name: "TEAM_MEMORY_BATCH_TIMEOUT", value: "0s"},
		{name: "TEAM_MEMORY_WORKER_JOB_TIMEOUT", value: "later"},
		{name: "TEAM_MEMORY_WORKER_STOP_TIMEOUT", value: "-1s"},
		{name: "TEAM_MEMORY_SLICE_EVENT_LIMIT", value: "0"},
		{name: "TEAM_MEMORY_SLICE_TOKEN_LIMIT", value: "many"},
		{name: "TEAM_MEMORY_SLICE_OVERLAP", value: "-1"},
		{name: "TEAM_MEMORY_MAX_SLICES_PER_JOB", value: "0"},
		{name: "TEAM_MEMORY_EXTRACTION_COMPACT_TOKENS", value: "0"},
		{name: "TEAM_MEMORY_EXTRACTION_COMPACT_START_TOKENS", value: "0"},
		{name: "TEAM_MEMORY_EXTRACTION_COMPACTION_ENABLED", value: "sometimes"},
		{name: "TEAM_MEMORY_EXTRACTION_SUMMARY_ENABLED", value: "sometimes"},
		{name: "TEAM_MEMORY_EXTRACTION_SUMMARY_TRIGGER_TOKENS", value: "0"},
		{name: "TEAM_MEMORY_EXTRACTION_SUMMARY_TAIL_TOKENS", value: "0"},
		{name: "TEAM_MEMORY_EMBEDDING_TIMEOUT", value: "0s"},
		{name: "TEAM_MEMORY_SEMANTIC_THRESHOLD", value: "high"},
		{name: "TEAM_MEMORY_RETRIEVAL_CANDIDATE_LIMIT", value: "0"},
		{name: "TEAM_MEMORY_HINT_RECALL_ENABLED", value: "sometimes"},
		{name: "TEAM_MEMORY_HINT_SEMANTIC_THRESHOLD", value: "low"},
		{name: "TEAM_MEMORY_HINT_THRESHOLD", value: "high"},
		{name: "TEAM_MEMORY_HINT_MIN_QUERY_RELEVANCE", value: "high"},
		{name: "TEAM_MEMORY_HINT_MIN_MARGINAL_UTILITY", value: "high"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			s.T().Setenv(test.name, test.value)
			_, err := loadConfig()
			s.Require().Error(err)
			s.T().Setenv(test.name, "")
		})
	}
}

func (s *configSuite) TestRejectsInvalidConfiguration() {
	tests := []struct {
		name     string
		database string
		apiKeys  string
	}{
		{name: "missing database", apiKeys: `{"key":"scope"}`},
		{name: "malformed API keys", database: "postgres://database", apiKeys: "not-json"},
		{name: "empty authentication", database: "postgres://database", apiKeys: `{}`},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			s.T().Setenv("TEAM_MEMORY_DATABASE_URL", test.database)
			s.T().Setenv("TEAM_MEMORY_API_KEYS", test.apiKeys)
			_, err := loadConfig()
			s.Require().Error(err)
		})
	}
}

func (s *configSuite) TestRejectsCompactionStartAboveHardLimit() {
	s.T().Setenv("TEAM_MEMORY_DATABASE_URL", "postgres://database")
	s.T().Setenv("TEAM_MEMORY_API_KEYS", `{"key":"scope"}`)
	s.T().Setenv("TEAM_MEMORY_EXTRACTION_COMPACT_START_TOKENS", "200")
	s.T().Setenv("TEAM_MEMORY_EXTRACTION_COMPACT_TOKENS", "100")
	_, err := loadConfig()
	s.Require().Error(err)
}

func (s *configSuite) TestRejectsCompactionAndSummaryTogether() {
	s.T().Setenv("TEAM_MEMORY_DATABASE_URL", "postgres://database")
	s.T().Setenv("TEAM_MEMORY_API_KEYS", `{"key":"scope"}`)
	s.T().Setenv("TEAM_MEMORY_EXTRACTION_COMPACTION_ENABLED", "true")
	s.T().Setenv("TEAM_MEMORY_EXTRACTION_SUMMARY_ENABLED", "true")
	_, err := loadConfig()
	s.Require().Error(err)
}

func (s *configSuite) TestRejectsUnsupportedExtractor() {
	_, err := buildExtractor(applicationConfig{extractorMode: "unknown"})
	s.Require().Error(err)
}

func (s *configSuite) TestCloseExtractorDrainsLifecycleImplementations() {
	adapter := new(lifecycleExtractor)

	err := closeExtractor(context.Background(), adapter)

	s.Require().NoError(err)
	s.True(adapter.closed)
}

type lifecycleExtractor struct {
	closed bool
}

type runtimeStub struct{}

func (*runtimeStub) ObserveSession(context.Context, teamnote.SessionBatch) (teamnote.IngestReceipt, error) {
	return teamnote.IngestReceipt{}, nil
}

func (*runtimeStub) RecallNotes(context.Context, teamnote.RecallRequest) (teamnote.NoteEnvelope, error) {
	return teamnote.NoteEnvelope{}, nil
}

func (*lifecycleExtractor) Extract(context.Context, sessionlake.Slice) (extractor.Result, error) {
	return extractor.Result{}, nil
}

func (*lifecycleExtractor) WaitForBackground(context.Context) error {
	return nil
}

func (e *lifecycleExtractor) Close(context.Context) error {
	e.closed = true
	return nil
}
