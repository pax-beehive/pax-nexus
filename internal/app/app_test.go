package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/audit"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/evidencelake"
	"github.com/pax-beehive/pax-nexus/internal/operations"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	pagewikimemory "github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/sessionconsumer"
	platformllm "github.com/pax-beehive/pax-nexus/internal/platform/llm"
	"github.com/pax-beehive/pax-nexus/internal/session"
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
	// deleteDelay simulates a slow DeleteBefore; deleteDeadline and
	// recordDeadline capture each call's context deadline so tests can
	// assert the success-event Record runs on its own fresh timeout instead
	// of the mostly-consumed retention context.
	deleteDelay    time.Duration
	deleteDeadline time.Time
	recordDeadline time.Time
}

func (s *operationsMaintenanceStoreFake) CaptureStorage(
	_ context.Context,
	capturedAt time.Time,
) (operations.StorageSnapshot, error) {
	s.capturedAt = capturedAt
	return operations.StorageSnapshot{CapturedAt: capturedAt}, nil
}

func (s *operationsMaintenanceStoreFake) DeleteBefore(
	ctx context.Context,
	eventCutoff time.Time,
	storageCutoff time.Time,
) (int64, int64, error) {
	s.eventCutoff = eventCutoff
	s.storageCutoff = storageCutoff
	if deadline, ok := ctx.Deadline(); ok {
		s.deleteDeadline = deadline
	}
	if s.deleteDelay > 0 {
		time.Sleep(s.deleteDelay)
	}
	return s.deletedEvents, s.deletedSnapshots, nil
}

func (s *operationsMaintenanceStoreFake) Record(
	ctx context.Context,
	event operations.Event,
) (operations.Event, error) {
	if deadline, ok := ctx.Deadline(); ok {
		s.recordDeadline = deadline
	}
	s.recorded = &event
	return event, nil
}

func TestConfigSuite(t *testing.T) {
	suite.Run(t, new(configSuite))
}

// repoRootFromPackageDir anchors the "checked-in defaults" tests below at the
// repository root: package app lives at internal/app, two directories below
// root, whereas these tests read root-relative paths such as ".env.example".
const repoRootFromPackageDir = "../.."

func (s *configSuite) SetupTest() {
	for _, name := range []string{
		"TEAM_MEMORY_DATABASE_URL", "TEAM_MEMORY_API_KEYS", "TEAM_MEMORY_LISTEN_ADDRESS", "PORT",
		"TEAM_MEMORY_ADMIN_API_KEY", "TEAM_MEMORY_CREDENTIAL_ROTATION_OVERLAP", "TEAM_MEMORY_DEVICE_AGENT_LIMIT",
		"TEAM_MEMORY_WIKI_HINT_ENABLED",
		"TEAM_MEMORY_OPERATIONS_EVENT_RETENTION", "TEAM_MEMORY_OPERATIONS_STORAGE_RETENTION",
		"TEAM_MEMORY_OPERATIONS_SNAPSHOT_INTERVAL", "TEAM_MEMORY_OPERATIONS_MAINTENANCE_TIMEOUT",
		"TEAM_MEMORY_BOOTSTRAP_SECRET", "TEAM_MEMORY_OIDC_ISSUER", "TEAM_MEMORY_OIDC_CLIENT_ID",
		"TEAM_MEMORY_OIDC_CLIENT_SECRET", "TEAM_MEMORY_OIDC_REDIRECT_URL", "TEAM_MEMORY_OIDC_FLOW_SECRET",
		"TEAM_MEMORY_PORTAL_URL", "TEAM_MEMORY_HUMAN_COOKIE_SECURE",
		"TEAM_MEMORY_SECRET_PEPPER", "TEAM_MEMORY_MEMBER_GRANTABLE_PERMISSIONS",
		"TEAM_MEMORY_EXTRACTOR_MODE", "TEAM_MEMORY_EXTRACTOR_BASE_URL",
		"TEAM_MEMORY_EXTRACTOR_API_KEY", "TEAM_MEMORY_EXTRACTOR_MODEL", "TEAM_MEMORY_EXTRACTOR_THINKING_MODE",
		"TEAM_MEMORY_PROMPT_VERSION",
		"TEAM_MEMORY_EXTRACTION_CONTEXT_MODE", "TEAM_MEMORY_EXTRACTION_VERSION",
		"TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY",
		"TEAM_MEMORY_EXTRACTION_COMPACT_START_TOKENS",
		"TEAM_MEMORY_EXTRACTION_COMPACT_TOKENS", "TEAM_MEMORY_EXTRACTION_COMPACTION_ENABLED",
		"TEAM_MEMORY_EXTRACTION_SUMMARY_ENABLED", "TEAM_MEMORY_EXTRACTION_SUMMARY_TRIGGER_TOKENS",
		"TEAM_MEMORY_EXTRACTION_SUMMARY_TAIL_TOKENS", "TEAM_MEMORY_EXTRACTION_MAX_PROMPT_TOKENS",
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
		"LLMWIKI_ORGANIZER_MODE", "LLMWIKI_LLM_BASE_URL", "LLMWIKI_LLM_API_KEY",
		"LLMWIKI_LLM_MODEL",
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
	s.Empty(config.extractorThinkingMode)
	s.False(config.extractionCompactionEnabled)
	s.True(config.extractionSummaryEnabled)
	s.Equal(12*1024, config.extractionCompactStartTokens)
	s.Equal(16*1024, config.extractionCompactTokens)
	s.Equal(8*1024, config.extractionSummaryTriggerTokens)
	s.Equal(16*1024, config.extractionSummaryTailTokens)
	s.Equal(128*1024, config.extractionMaxPromptTokens)
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
	s.Equal(16, config.deviceAgentLimit)
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

// TestListenAddressFallsBackToPortEnvironment covers the managed-runtime
// contract: Cloud Run (and Heroku-style platforms) inject PORT and expect the
// process to bind it. TEAM_MEMORY_LISTEN_ADDRESS stays authoritative so an
// on-prem operator can still bind a specific interface.
func (s *configSuite) TestListenAddressFallsBackToPortEnvironment() {
	tests := []struct {
		name          string
		listenAddress string
		port          string
		want          string
	}{
		{name: "neither set keeps the default", want: ":8080"},
		{name: "PORT alone is honored", port: "9090", want: ":9090"},
		{name: "padded PORT is trimmed", port: " 9090 ", want: ":9090"},
		{
			name: "explicit listen address wins over PORT", listenAddress: "127.0.0.1:7000",
			port: "9090", want: "127.0.0.1:7000",
		},
		{name: "non-numeric PORT is ignored", port: "http", want: ":8080"},
		{name: "out-of-range PORT is ignored", port: "70000", want: ":8080"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			s.T().Setenv("TEAM_MEMORY_DATABASE_URL", "postgres://database")
			s.T().Setenv("TEAM_MEMORY_API_KEYS", `{"key":"scope"}`)
			s.T().Setenv("TEAM_MEMORY_EXTRACTOR_MODE", "noop")
			if test.listenAddress != "" {
				s.T().Setenv("TEAM_MEMORY_LISTEN_ADDRESS", test.listenAddress)
			}
			if test.port != "" {
				s.T().Setenv("PORT", test.port)
			}

			config, err := loadConfig()

			s.Require().NoError(err)
			s.Equal(test.want, config.listenAddress)
		})
	}
}

func (s *configSuite) TestLoadsExtractorThinkingMode() {
	s.T().Setenv("TEAM_MEMORY_DATABASE_URL", "postgres://database")
	s.T().Setenv("TEAM_MEMORY_API_KEYS", `{"key":"scope"}`)
	s.T().Setenv("TEAM_MEMORY_EXTRACTOR_MODE", "noop")
	s.T().Setenv("TEAM_MEMORY_EXTRACTOR_THINKING_MODE", "disabled")

	config, err := loadConfig()

	s.Require().NoError(err)
	s.Equal(extractor.ThinkingModeDisabled, config.extractorThinkingMode)
}

func (s *configSuite) TestBuildsConfiguredPageWikiMaintainers() {
	logger := slog.New(slog.DiscardHandler)
	localPlanner, localEditor, localNavigator, localCurator, err := buildPageWikiMaintainers(nil, applicationConfig{}, logger)
	s.Require().NoError(err)
	s.IsType(pagewiki.SessionDocumentPlanner{}, localPlanner)
	s.IsType(pagewiki.SessionDocumentEditor{}, localEditor)
	s.Nil(localNavigator)
	s.Nil(localCurator)

	config := applicationConfig{
		llmwikiMode: "harness", llmwikiBaseURL: "https://api.deepseek.com",
		llmwikiAPIKey: "secret", llmwikiModel: "deepseek-v4-pro",
	}
	planner, editor, navigator, curator, err := buildPageWikiMaintainers(nil, config, logger)
	s.Require().NoError(err)
	s.IsType(&pagewiki.LLMSessionPlanner{}, planner)
	s.IsType(&pagewiki.LLMSessionEditor{}, editor)
	s.IsType(&pagewiki.LLMTreeNavigator{}, navigator)
	s.IsType(&pagewiki.LLMCurator{}, curator)

	config.llmwikiAPIKey = ""
	_, _, _, _, err = buildPageWikiMaintainers(nil, config, logger)
	s.Require().ErrorContains(err, "LLMWIKI_LLM_API_KEY")
	config.llmwikiMode = "unsupported"
	_, _, _, _, err = buildPageWikiMaintainers(nil, config, logger)
	s.Require().ErrorContains(err, "unsupported LLMWIKI_ORGANIZER_MODE")
}

func (s *configSuite) TestBuildsPageWikiCurationConfig() {
	config, err := buildPageWikiCurationConfig(applicationConfig{})
	s.Require().NoError(err)
	s.Equal(24*time.Hour, config.Interval)
	s.Equal(0, config.PairLimit)
	s.Equal(0, config.PageLimit)

	config, err = buildPageWikiCurationConfig(applicationConfig{
		llmwikiCurationInterval: "0", llmwikiCurationPairLimit: "0", llmwikiCurationPageLimit: "0",
	})
	s.Require().NoError(err)
	s.Equal(time.Duration(0), config.Interval)
	s.Equal(0, config.PairLimit)
	s.Equal(0, config.PageLimit)

	config, err = buildPageWikiCurationConfig(applicationConfig{
		llmwikiCurationInterval: "6h", llmwikiCurationPairLimit: "4", llmwikiCurationPageLimit: "5",
	})
	s.Require().NoError(err)
	s.Equal(6*time.Hour, config.Interval)
	s.Equal(4, config.PairLimit)
	s.Equal(5, config.PageLimit)

	_, err = buildPageWikiCurationConfig(applicationConfig{llmwikiCurationInterval: "not-a-duration"})
	s.Require().ErrorContains(err, "LLMWIKI_CURATION_INTERVAL")
	_, err = buildPageWikiCurationConfig(applicationConfig{llmwikiCurationInterval: "-1h"})
	s.Require().ErrorContains(err, "LLMWIKI_CURATION_INTERVAL")
	_, err = buildPageWikiCurationConfig(applicationConfig{llmwikiCurationPairLimit: "-1"})
	s.Require().ErrorContains(err, "LLMWIKI_CURATION_PAIR_LIMIT")
	_, err = buildPageWikiCurationConfig(applicationConfig{llmwikiCurationPairLimit: "nope"})
	s.Require().ErrorContains(err, "LLMWIKI_CURATION_PAIR_LIMIT")
	_, err = buildPageWikiCurationConfig(applicationConfig{llmwikiCurationPageLimit: "-1"})
	s.Require().ErrorContains(err, "LLMWIKI_CURATION_PAGE_LIMIT")
	_, err = buildPageWikiCurationConfig(applicationConfig{llmwikiCurationPageLimit: "nope"})
	s.Require().ErrorContains(err, "LLMWIKI_CURATION_PAGE_LIMIT")
}

func (s *configSuite) TestLoadsOnPremConfiguration() {
	s.T().Setenv("TEAM_MEMORY_DATABASE_URL", "postgres://database")
	s.T().Setenv("TEAM_MEMORY_EXTRACTOR_MODE", "noop")
	s.T().Setenv("TEAM_MEMORY_ADMIN_API_KEY", "admin-secret")
	s.T().Setenv("TEAM_MEMORY_SECRET_PEPPER", "0123456789abcdef0123456789abcdef")
	s.T().Setenv("TEAM_MEMORY_MEMBER_GRANTABLE_PERMISSIONS", "search,channel_send")
	s.T().Setenv("TEAM_MEMORY_CREDENTIAL_ROTATION_OVERLAP", "2m")
	s.T().Setenv("TEAM_MEMORY_DEVICE_AGENT_LIMIT", "8")

	config, err := loadConfig()

	s.Require().NoError(err)
	s.Equal("admin-secret", config.adminAPIKey)
	s.Empty(config.apiKeys)
	s.Equal(2*time.Minute, config.credentialRotationOverlap)
	s.Equal(8, config.deviceAgentLimit)
	s.Equal(7*24*time.Hour, config.operationsEventRetention)
	s.Equal(90*24*time.Hour, config.operationsStorageRetention)
	s.Equal(time.Hour, config.operationsSnapshotInterval)
	s.Equal(15*time.Second, config.operationsMaintenanceTimeout)
	s.False(config.wikiHintEnabled)
	s.True(config.humanCookieSecure)
	s.Equal([]onprem.Permission{onprem.PermissionSearch, onprem.PermissionChannelSend}, config.memberGrantablePermissions)
}

// TestRejectsWikiHintWithoutWikiPathImplementation locks the startup guard
// for TEAM_MEMORY_WIKI_HINT_ENABLED. No recall.WikiPath implementation is
// wired anywhere, so enabling the hint could only fail deep inside
// recall.NewRouter; config load rejects it up front with a message naming
// the variable.
func (s *configSuite) TestRejectsWikiHintWithoutWikiPathImplementation() {
	s.T().Setenv("TEAM_MEMORY_DATABASE_URL", "postgres://database")
	s.T().Setenv("TEAM_MEMORY_EXTRACTOR_MODE", "noop")
	s.T().Setenv("TEAM_MEMORY_ADMIN_API_KEY", "admin-secret")
	s.T().Setenv("TEAM_MEMORY_SECRET_PEPPER", "0123456789abcdef0123456789abcdef")
	s.T().Setenv("TEAM_MEMORY_WIKI_HINT_ENABLED", "true")

	_, err := loadConfig()

	s.Require().Error(err)
	s.ErrorContains(err, "TEAM_MEMORY_WIKI_HINT_ENABLED")
}

// TestTrimsAdminAPIKeySurroundingWhitespace covers the mounted-secret case:
// CredentialService.Authenticate compares the trimmed presented key against
// the configured one, so an admin key carrying a trailing newline must be
// trimmed at load rather than making admin authentication silently
// impossible while still selecting on-prem mode.
func (s *configSuite) TestTrimsAdminAPIKeySurroundingWhitespace() {
	s.T().Setenv("TEAM_MEMORY_DATABASE_URL", "postgres://database")
	s.T().Setenv("TEAM_MEMORY_EXTRACTOR_MODE", "noop")
	s.T().Setenv("TEAM_MEMORY_ADMIN_API_KEY", "  admin-secret\n")
	s.T().Setenv("TEAM_MEMORY_SECRET_PEPPER", "0123456789abcdef0123456789abcdef")

	config, err := loadConfig()

	s.Require().NoError(err)
	s.Equal("admin-secret", config.adminAPIKey)
	s.Empty(config.apiKeys, "a whitespace-padded admin key must still select on-prem mode")
}

// TestSliceOverlapAcceptsExplicitZero covers the one integer variable whose
// zero is a valid policy: evidencelake supports a zero overlap, so an
// explicit "0" must load instead of being rejected like every other
// strictly-positive integer variable.
func (s *configSuite) TestSliceOverlapAcceptsExplicitZero() {
	tests := []struct {
		name      string
		value     string
		wantValue int
		wantError bool
	}{
		{name: "explicit zero", value: "0", wantValue: 0},
		{name: "positive", value: "5", wantValue: 5},
		{name: "unset keeps default", value: "", wantValue: 3},
		{name: "negative rejected", value: "-1", wantError: true},
		{name: "garbage rejected", value: "many", wantError: true},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			s.T().Setenv("TEAM_MEMORY_DATABASE_URL", "postgres://database")
			s.T().Setenv("TEAM_MEMORY_EXTRACTOR_MODE", "noop")
			s.T().Setenv("TEAM_MEMORY_ADMIN_API_KEY", "admin-secret")
			s.T().Setenv("TEAM_MEMORY_SECRET_PEPPER", "0123456789abcdef0123456789abcdef")
			if test.value != "" {
				s.T().Setenv("TEAM_MEMORY_SLICE_OVERLAP", test.value)
			}

			config, err := loadConfig()

			if test.wantError {
				s.Require().Error(err)
				s.ErrorContains(err, "TEAM_MEMORY_SLICE_OVERLAP")
				return
			}
			s.Require().NoError(err)
			s.Equal(test.wantValue, config.sliceOverlap)
		})
	}
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
		{name: "device agent limit too low", env: "TEAM_MEMORY_DEVICE_AGENT_LIMIT", value: "0"},
		{name: "device agent limit too high", env: "TEAM_MEMORY_DEVICE_AGENT_LIMIT", value: "1001"},
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

// TestOperationsMaintenanceRecordsOnItsOwnTimeoutBudget pins the fix for the
// success-event write starving behind a slow retention delete: the Record
// call must carry a freshly budgeted deadline rather than the retention
// context whose budget DeleteBefore already consumed.
func (s *configSuite) TestOperationsMaintenanceRecordsOnItsOwnTimeoutBudget() {
	now := time.Now().UTC().Add(-time.Second)
	store := &operationsMaintenanceStoreFake{
		deletedEvents: 2, deletedSnapshots: 3, deleteDelay: 50 * time.Millisecond,
	}
	config := applicationConfig{
		operationsEventRetention: 7 * 24 * time.Hour, operationsStorageRetention: 90 * 24 * time.Hour,
		operationsMaintenanceTimeout: 200 * time.Millisecond,
	}

	maintainOperations(context.Background(), store, store, config, slog.New(slog.DiscardHandler), now)

	s.Require().NotNil(store.recorded)
	s.Require().False(store.deleteDeadline.IsZero())
	s.Require().False(store.recordDeadline.IsZero())
	s.True(store.recordDeadline.After(store.deleteDeadline),
		"the success-event Record must start a fresh timeout, not inherit the delete's spent budget")
}

// TestStartOperationsMaintenanceStopsAndDrains is the leak guard for the
// maintenance loop: the returned stop function must cancel the loop and
// block until its goroutine has actually exited.
func (s *configSuite) TestStartOperationsMaintenanceStopsAndDrains() {
	store := &operationsMaintenanceStoreFake{}
	config := applicationConfig{
		operationsEventRetention: 7 * 24 * time.Hour, operationsStorageRetention: 90 * 24 * time.Hour,
		operationsMaintenanceTimeout: time.Second, operationsSnapshotInterval: time.Hour,
	}

	stop := startOperationsMaintenance(
		context.Background(), store, store, config, slog.New(slog.DiscardHandler),
	)
	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		s.Fail("stop did not return; the maintenance goroutine never exited")
	}
	s.False(store.capturedAt.IsZero(), "the immediate first maintenance pass must have run")
}

// embeddingBackfillerFake replays a scripted sequence of BackfillEmbeddings
// outcomes, then repeats its last one, so tests can drive the retry loop
// deterministically. onCall fires after every call for cancellation tests.
type embeddingBackfillerFake struct {
	mu      sync.Mutex
	results []embeddingBackfillResult
	calls   int
	onCall  func(call int)
}

type embeddingBackfillResult struct {
	backfilled int
	err        error
}

func (f *embeddingBackfillerFake) BackfillEmbeddings(_ context.Context, _ int) (int, error) {
	f.mu.Lock()
	call := f.calls
	f.calls++
	result := f.results[len(f.results)-1]
	if call < len(f.results) {
		result = f.results[call]
	}
	f.mu.Unlock()
	if f.onCall != nil {
		f.onCall(call)
	}
	return result.backfilled, result.err
}

func (f *embeddingBackfillerFake) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// TestEmbeddingBackfillRetriesTransientFailures pins the fix for the backlog
// being abandoned until the next process restart: a transient failure is
// retried, and the drain still finishes on the following batch.
func (s *configSuite) TestEmbeddingBackfillRetriesTransientFailures() {
	backfiller := &embeddingBackfillerFake{results: []embeddingBackfillResult{
		{err: errors.New("embedding service unavailable")},
		{backfilled: embeddingBackfillBatchSize},
		{backfilled: 1},
	}}
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn}))

	done := make(chan struct{})
	go func() {
		continueEmbeddingBackfill(context.Background(), backfiller, time.Millisecond, logger)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.Fail("backfill never finished draining after a transient failure")
	}
	s.Equal(3, backfiller.callCount(), "the loop must retry the failure and then drain to a short batch")
	s.Contains(logs.String(), "retrying", "a retried failure must be logged for operators")
}

// TestEmbeddingBackfillStopsOnContextCancellation guards the retry loop
// against outliving shutdown: a permanently failing store must not keep the
// goroutine alive once the context is cancelled.
func (s *configSuite) TestEmbeddingBackfillStopsOnContextCancellation() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backfiller := &embeddingBackfillerFake{
		results: []embeddingBackfillResult{{err: errors.New("embedding service unavailable")}},
		onCall:  func(int) { cancel() },
	}

	done := make(chan struct{})
	go func() {
		continueEmbeddingBackfill(ctx, backfiller, time.Hour, slog.New(slog.DiscardHandler))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.Fail("backfill kept retrying after its context was cancelled")
	}
}

// TestBuildEmbedderWithoutBaseURLReturnsTrueNilInterface guards the typed-nil
// trap: the embedder is handed to the note store and the wiki curator, which
// both branch on a nil interface, so an unconfigured embedder must compare
// equal to nil rather than being a non-nil interface holding a nil pointer.
func (s *configSuite) TestBuildEmbedderWithoutBaseURLReturnsTrueNilInterface() {
	embedder, err := buildEmbedder(applicationConfig{embeddingBaseURL: "   "})

	s.Require().NoError(err)
	isNilInterface := embedder == nil
	s.True(isNilInterface, "an unconfigured embedder must be a true nil interface, not a typed nil")
}

// TestTodoAuthenticatorMapsNilIdentityToNilInterface is the same typed-nil
// guard for the Todo App transport, whose "identity == nil answers 501"
// contract breaks if a nil *onprem.IdentityService is passed straight
// through as a non-nil interface.
func (s *configSuite) TestTodoAuthenticatorMapsNilIdentityToNilInterface() {
	authenticator := todoAuthenticator(nil)

	isNilInterface := authenticator == nil
	s.True(isNilInterface, "legacy API-key mode must produce a true nil authenticator")
}

// usageSinkFake captures the events extractionUsageRecorder emits and can
// fail the write to prove recording stays best-effort.
type usageSinkFake struct {
	events []platformllm.UsageEvent
	err    error
}

func (f *usageSinkFake) RecordLLMUsage(_ context.Context, event platformllm.UsageEvent) error {
	f.events = append(f.events, event)
	return f.err
}

// TestExtractionUsageRecorderAttributesScopeAndSwallowsSinkFailures covers
// both halves of the recorder's contract: usage is attributed to the scope
// carried on the extraction context (falling back to the local scope when
// none is present), and a sink failure is logged rather than propagated,
// because metering must never fail an extraction.
func (s *configSuite) TestExtractionUsageRecorderAttributesScopeAndSwallowsSinkFailures() {
	tests := []struct {
		name      string
		ctx       context.Context
		sinkErr   error
		wantScope string
	}{
		{
			name:      "scope from context",
			ctx:       teamnote.WithScope(context.Background(), "tenant-a"),
			wantScope: "tenant-a",
		},
		{
			name:      "missing scope falls back to the local scope",
			ctx:       context.Background(),
			wantScope: onprem.LocalScopeID,
		},
		{
			name:      "sink failure is logged not propagated",
			ctx:       context.Background(),
			sinkErr:   errors.New("usage store unavailable"),
			wantScope: onprem.LocalScopeID,
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			sink := &usageSinkFake{err: test.sinkErr}
			logs := &bytes.Buffer{}
			logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn}))

			record := extractionUsageRecorder(sink, logger)
			record(test.ctx, "deepseek-chat", extractor.Usage{InputTokens: 11, OutputTokens: 7})

			s.Require().Len(sink.events, 1)
			s.Equal(test.wantScope, sink.events[0].ScopeID)
			s.Equal("extractor", sink.events[0].Component)
			s.Equal("deepseek-chat", sink.events[0].Model)
			s.Equal(11, sink.events[0].Usage.InputTokens)
			s.Equal(7, sink.events[0].Usage.OutputTokens)
			if test.sinkErr != nil {
				s.Contains(logs.String(), "record extraction LLM usage failed")
				return
			}
			s.Empty(logs.String(), "a successful usage write must stay silent")
		})
	}
}

// TestParseTreeMaxDepthValidatesEnvironment covers the LLMWIKI_TREE_MAX_DEPTH
// parser, whose zero return is the caller's "apply the package default"
// sentinel rather than a depth of zero.
func (s *configSuite) TestParseTreeMaxDepthValidatesEnvironment() {
	tests := []struct {
		name      string
		raw       string
		want      int
		wantError bool
	}{
		{name: "unset defers to the package default", raw: "", want: 0},
		{name: "whitespace defers to the package default", raw: "  ", want: 0},
		{name: "positive depth", raw: "7", want: 7},
		{name: "padded positive depth", raw: " 4 ", want: 4},
		{name: "zero rejected", raw: "0", wantError: true},
		{name: "negative rejected", raw: "-2", wantError: true},
		{name: "garbage rejected", raw: "deep", wantError: true},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			depth, err := parseTreeMaxDepth(test.raw)

			if test.wantError {
				s.Require().Error(err)
				s.ErrorContains(err, "LLMWIKI_TREE_MAX_DEPTH")
				return
			}
			s.Require().NoError(err)
			s.Equal(test.want, depth)
		})
	}
}

// The three stubs below only need to satisfy their interfaces: the option
// assembler branches on presence, never on behavior.
type wikiControlStub struct{}

func (wikiControlStub) Status(context.Context, string) (sessionconsumer.Status, error) {
	return sessionconsumer.Status{}, nil
}

func (wikiControlStub) SetAutoInject(context.Context, string, bool) (sessionconsumer.Status, error) {
	return sessionconsumer.Status{}, nil
}

func (wikiControlStub) InjectSession(
	context.Context, string, string,
) (sessionconsumer.InjectResult, error) {
	return sessionconsumer.InjectResult{}, nil
}

func (wikiControlStub) Rebuild(context.Context, string, time.Time) (sessionconsumer.Status, error) {
	return sessionconsumer.Status{}, nil
}

type wikiSettingsStub struct{}

func (wikiSettingsStub) GenerationSettings(
	context.Context, string,
) (pagewiki.GenerationDirectives, error) {
	return pagewiki.GenerationDirectives{}, nil
}

func (wikiSettingsStub) SetGenerationSettings(
	context.Context, string, pagewiki.GenerationDirectives,
) (pagewiki.GenerationDirectives, error) {
	return pagewiki.GenerationDirectives{}, nil
}

type sessionAuditStub struct{}

func (sessionAuditStub) ListToolCalls(context.Context, audit.ToolCallFilter) ([]audit.ToolCall, error) {
	return nil, nil
}

func (sessionAuditStub) ListFindings(context.Context, audit.FindingFilter) ([]audit.Finding, error) {
	return nil, nil
}

func (sessionAuditStub) GetActivityDaily(
	context.Context, audit.ActivityFilter,
) ([]audit.ActivityDaily, error) {
	return nil, nil
}

// TestOnPremHandlerOptionsWiresOnlyConfiguredDependencies pins the optional
// wiring: each absent dependency must drop exactly one option, since a
// silently missing option disables a whole admin surface.
func (s *configSuite) TestOnPremHandlerOptionsWiresOnlyConfiguredDependencies() {
	const requiredOptions = 3

	bare := onPremHandlerOptions(nil, nil, nil, nil, nil, nil, nil, nil)
	s.Len(bare, requiredOptions, "registry, operations, and explorer are always wired")

	withWiki := onPremHandlerOptions(
		nil, nil, nil, nil, nil, wikiControlStub{}, wikiSettingsStub{}, nil,
	)
	s.Len(withWiki, requiredOptions+2, "wiki control and wiki settings each add one option")

	withAudit := onPremHandlerOptions(
		nil, nil, nil, nil, nil, wikiControlStub{}, wikiSettingsStub{}, sessionAuditStub{},
	)
	s.Len(withAudit, requiredOptions+3, "the session audit query adds one more option")
}

// idleWikiConsumerStore is a no-work Page Wiki consumer store: the lifecycle
// test only needs the controller's loop to run and stop, not to inject.
type idleWikiConsumerStore struct{}

func (idleWikiConsumerStore) AutoInjectEnabled(context.Context, string) (bool, error) {
	return false, nil
}
func (idleWikiConsumerStore) SetAutoInjectEnabled(context.Context, string, bool) error {
	return nil
}
func (idleWikiConsumerStore) PendingStreams(context.Context) ([]sessionconsumer.Stream, error) {
	return nil, nil
}

func (idleWikiConsumerStore) StreamsBySessionID(
	context.Context, string, string,
) ([]sessionconsumer.Stream, error) {
	return nil, nil
}

func (idleWikiConsumerStore) SessionEvents(
	context.Context, sessionconsumer.Stream,
) ([]session.SessionEvent, error) {
	return nil, nil
}
func (idleWikiConsumerStore) AdvanceCursor(context.Context, sessionconsumer.Stream) error { return nil }

func (idleWikiConsumerStore) Progress(context.Context, string) (sessionconsumer.Progress, error) {
	return sessionconsumer.Progress{}, nil
}

// idleAuditStore is the equivalent no-work store for the session audit
// consumer.
type idleAuditStore struct{}

func (idleAuditStore) PendingStreams(context.Context) ([]audit.Stream, error) { return nil, nil }

func (idleAuditStore) SessionEvents(context.Context, audit.Stream) ([]session.SessionEvent, error) {
	return nil, nil
}
func (idleAuditStore) ApplyBatch(context.Context, audit.Batch) error { return nil }

// TestApplicationServicesStartAndStopEveryBackgroundLoop is the lifecycle
// guard for the shutdown seam: Run starts the background consumers only
// after every build step succeeds, and stop must shut all of them down —
// including the Todo refresh loop and the audit consumer that used to be
// discarded entirely — without hanging.
func (s *configSuite) TestApplicationServicesStartAndStopEveryBackgroundLoop() {
	wikiServices, err := pagewiki.NewServiceManager(
		func(context.Context, string) (pagewiki.Repository, error) {
			return pagewikimemory.NewRepository(), nil
		},
		pagewiki.ServiceManagerConfig{Planner: pagewiki.ScriptedPlanner{}, Editor: pagewiki.ScriptedEditor{}},
	)
	s.Require().NoError(err)
	wikiConsumer, err := sessionconsumer.New(
		idleWikiConsumerStore{},
		func(context.Context, string) (sessionconsumer.Injector, error) { return nil, nil },
		func(context.Context, string) (sessionconsumer.Rebuilder, error) { return nil, nil },
		slog.New(slog.DiscardHandler), 10*time.Millisecond,
	)
	s.Require().NoError(err)
	auditConsumer, err := audit.New(idleAuditStore{}, slog.New(slog.DiscardHandler), 10*time.Millisecond)
	s.Require().NoError(err)

	var todoRefreshStopped bool
	services := &applicationServices{
		wikiServices: wikiServices, wikiConsumer: wikiConsumer, auditConsumer: auditConsumer,
		startTodoRefresh: func(context.Context) func() {
			return func() { todoRefreshStopped = true }
		},
	}

	services.start(context.Background())
	stopped := make(chan struct{})
	go func() {
		services.stop(5*time.Second, slog.New(slog.DiscardHandler))
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		s.Fail("stop hung; a background consumer never exited")
	}
	s.True(todoRefreshStopped, "the Todo refresh loop must be stopped too")
}

// TestApplicationServicesStopIsSafeWithoutStart covers the failed-build
// unwind path: Run stops the services it assembled even when a later build
// step (or the queue start) failed before anything was started.
func (s *configSuite) TestApplicationServicesStopIsSafeWithoutStart() {
	wikiServices, err := pagewiki.NewServiceManager(
		func(context.Context, string) (pagewiki.Repository, error) {
			return pagewikimemory.NewRepository(), nil
		},
		pagewiki.ServiceManagerConfig{Planner: pagewiki.ScriptedPlanner{}, Editor: pagewiki.ScriptedEditor{}},
	)
	s.Require().NoError(err)
	wikiConsumer, err := sessionconsumer.New(
		idleWikiConsumerStore{},
		func(context.Context, string) (sessionconsumer.Injector, error) { return nil, nil },
		func(context.Context, string) (sessionconsumer.Rebuilder, error) { return nil, nil },
		slog.New(slog.DiscardHandler), 10*time.Millisecond,
	)
	s.Require().NoError(err)
	auditConsumer, err := audit.New(idleAuditStore{}, slog.New(slog.DiscardHandler), 10*time.Millisecond)
	s.Require().NoError(err)

	services := &applicationServices{
		wikiServices: wikiServices, wikiConsumer: wikiConsumer, auditConsumer: auditConsumer,
		stopTodoRefresh: func() {},
	}

	s.NotPanics(func() { services.stop(time.Second, slog.New(slog.DiscardHandler)) })
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

// TestParsesOIDCAuthorizationParameters covers the escape hatch for
// providers whose authorize endpoint needs a parameter standard OIDC does
// not define. WorkOS AuthKit is the motivating case: without
// provider=authkit it rejects the request as an invalid connection selector.
func (s *configSuite) TestParsesOIDCAuthorizationParameters() {
	tests := []struct {
		name      string
		raw       string
		want      map[string]string
		wantError bool
	}{
		{name: "unset", raw: "", want: nil},
		{name: "single pair", raw: "provider=authkit", want: map[string]string{"provider": "authkit"}},
		{
			name: "several pairs with spacing", raw: " provider=authkit , prompt=login ",
			want: map[string]string{"provider": "authkit", "prompt": "login"},
		},
		{name: "value containing an equals sign", raw: "hint=a=b", want: map[string]string{"hint": "a=b"}},
		{name: "missing separator", raw: "provider", wantError: true},
		{name: "empty name", raw: "=authkit", wantError: true},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			parsed, err := parseAuthorizationParameters(test.raw)

			if test.wantError {
				s.Require().Error(err)
				s.ErrorContains(err, "TEAM_MEMORY_OIDC_AUTH_PARAMS")
				return
			}
			s.Require().NoError(err)
			s.Equal(test.want, parsed)
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
	configured, identity, err := buildHTTPHandler(context.Background(), runtime, nil, nil, nil,
		applicationConfig{apiKeys: map[string]string{"key": "scope"}}, slog.New(slog.DiscardHandler), nil, nil, nil)
	s.Require().NoError(err)
	s.NotNil(configured)
	s.Nil(identity)

	_, _, err = buildHTTPHandler(context.Background(), runtime, nil, nil, nil, applicationConfig{
		apiKeys: map[string]string{"key": "scope"}, adminAPIKey: "admin", credentialRotationOverlap: time.Minute,
	}, slog.New(slog.DiscardHandler), nil, nil, nil)
	s.Error(err)
}

// TestBuildHTTPHandlerRequiresStoreInOnPremMode covers the on-prem branch's
// first guard clause, the only one reachable without a live database: every
// later guard needs a real *postgres.Store to get past this one. The fully
// wired on-prem assembly (identity, OIDC discovery, and the option list) is
// exercised end to end by tests/onprem-e2e against a real Postgres and a
// mock OIDC issuer.
func (s *configSuite) TestBuildHTTPHandlerRequiresStoreInOnPremMode() {
	_, _, err := buildHTTPHandler(
		context.Background(), &runtimeStub{}, nil, nil, nil,
		applicationConfig{adminAPIKey: "admin", credentialRotationOverlap: time.Minute},
		slog.New(slog.DiscardHandler), nil, nil, nil,
	)

	s.Require().Error(err)
	s.ErrorContains(err, "postgres store is required")
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
	s.T().Setenv("TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY", extractor.CandidateStrategySourceSpanV1)

	config, err := loadConfig()

	s.Require().NoError(err)
	s.Equal(extractor.CandidateStrategySourceSpanV1, config.extractionCandidateStrategy)
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

func (s *configSuite) TestOverridesExtractionMaxPromptTokens() {
	s.T().Setenv("TEAM_MEMORY_DATABASE_URL", "postgres://database")
	s.T().Setenv("TEAM_MEMORY_API_KEYS", `{"key":"scope"}`)
	s.T().Setenv("TEAM_MEMORY_EXTRACTOR_MODE", "noop")
	s.T().Setenv("TEAM_MEMORY_EXTRACTION_MAX_PROMPT_TOKENS", "65536")

	config, err := loadConfig()
	s.Require().NoError(err)
	s.Equal(65536, config.extractionMaxPromptTokens)
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
			content, err := os.ReadFile(filepath.Join(repoRootFromPackageDir, test.path))
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
		{path: ".env.example", want: "interaction-slim,\n# source-clause-v1, source-span-v1, source-span-v2, or claim-card-v2."},
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
		{path: "Dockerfile", want: "case \"${EXTRACTION_CANDIDATE_STRATEGY}\" in \\\n      interaction-slim|source-clause-v1|source-span-v1|source-span-v2|claim-card-v2) ;; \\"},
		{path: "Makefile", want: "EXTRACTION_CANDIDATE_STRATEGY ?= source-clause-v1"},
		{path: "README.md", want: "interface. `interaction-slim`, `source-clause-v1`, `source-span-v1`,\n`source-span-v2`, and `claim-card-v2` each"},
	}
	for _, test := range tests {
		s.Run(test.path, func() {
			content, err := os.ReadFile(filepath.Join(repoRootFromPackageDir, test.path))
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
			content, err := os.ReadFile(filepath.Join(repoRootFromPackageDir, test.path))
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
		{name: "TEAM_MEMORY_EXTRACTION_MAX_PROMPT_TOKENS", value: "0"},
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

func (*runtimeStub) ObserveStream(context.Context, teamnote.StreamBatch) (teamnote.IngestReceipt, error) {
	return teamnote.IngestReceipt{}, nil
}

func (*runtimeStub) RecallNotes(context.Context, teamnote.RecallRequest) (teamnote.NoteEnvelope, error) {
	return teamnote.NoteEnvelope{}, nil
}

func (*lifecycleExtractor) Extract(context.Context, evidencelake.Slice) (extractor.Result, error) {
	return extractor.Result{}, nil
}

func (*lifecycleExtractor) WaitForBackground(context.Context) error {
	return nil
}

func (e *lifecycleExtractor) Close(context.Context) error {
	e.closed = true
	return nil
}
