package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
	"github.com/stretchr/testify/suite"
)

type configSuite struct {
	suite.Suite
}

func TestConfigSuite(t *testing.T) {
	suite.Run(t, new(configSuite))
}

func (s *configSuite) SetupTest() {
	for _, name := range []string{
		"TEAM_MEMORY_DATABASE_URL", "TEAM_MEMORY_API_KEYS", "TEAM_MEMORY_LISTEN_ADDRESS",
		"TEAM_MEMORY_EXTRACTOR_MODE", "TEAM_MEMORY_EXTRACTOR_BASE_URL",
		"TEAM_MEMORY_EXTRACTOR_API_KEY", "TEAM_MEMORY_EXTRACTOR_MODEL", "TEAM_MEMORY_PROMPT_VERSION",
		"TEAM_MEMORY_EXTRACTION_CONTEXT_MODE", "TEAM_MEMORY_EXTRACTION_VERSION",
		"TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY",
		"TEAM_MEMORY_EXTRACTION_COMPACT_START_TOKENS",
		"TEAM_MEMORY_EXTRACTION_COMPACT_TOKENS", "TEAM_MEMORY_EXTRACTION_COMPACTION_ENABLED",
		"TEAM_MEMORY_EXTRACTION_SUMMARY_ENABLED", "TEAM_MEMORY_EXTRACTION_SUMMARY_TRIGGER_TOKENS",
		"TEAM_MEMORY_EXTRACTION_SUMMARY_TAIL_TOKENS",
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
	s.Equal("v2", config.extractionVersion)
	s.Equal(extractor.DefaultCandidateStrategy(), config.extractionCandidateStrategy)
	s.False(config.extractionCompactionEnabled)
	s.True(config.extractionSummaryEnabled)
	s.Equal(12*1024, config.extractionCompactStartTokens)
	s.Equal(16*1024, config.extractionCompactTokens)
	s.Equal(8*1024, config.extractionSummaryTriggerTokens)
	s.Equal(16*1024, config.extractionSummaryTailTokens)
	s.Equal(90*time.Second, config.extractionExecutionPolicy.AttemptTimeout)
	s.Equal(1, config.extractionExecutionPolicy.MaxAttempts)
	s.Equal(250*time.Millisecond, config.extractionExecutionPolicy.RetryBackoff)
	s.Equal(int64(1<<20), config.extractionExecutionPolicy.MaxResponseBytes)
	s.Equal(16*1024, config.extractionExecutionPolicy.PrimaryMaxOutputTokens)
	s.Equal(16, config.workerShards)
	s.Equal(5, config.workerMaxAttempts)
	s.Equal(750*time.Millisecond, config.workerDebounce)
	s.Equal(30*time.Second, config.batchTimeout)
	s.Equal(2*time.Minute, config.workerJobTimeout)
	s.Equal(25, config.sliceEventLimit)
	s.Equal(8192, config.sliceTokenLimit)
	s.Equal(3, config.sliceOverlap)
	s.Equal(4, config.maxSlicesPerJob)
	s.Equal(10*time.Second, config.embeddingTimeout)
	strategy, err := teamnote.ResolveRecallCandidateStrategy("")
	s.Require().NoError(err)
	s.Equal(strategy, config.recallCandidateStrategy)
	adapter, err := buildExtractor(config)
	s.Require().NoError(err)
	s.IsType(extractor.Noop{}, adapter)
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

func (s *configSuite) TestAllowsExtractionV1Rollback() {
	s.T().Setenv("TEAM_MEMORY_DATABASE_URL", "postgres://database")
	s.T().Setenv("TEAM_MEMORY_API_KEYS", `{"key":"scope"}`)
	s.T().Setenv("TEAM_MEMORY_EXTRACTOR_MODE", "noop")
	s.T().Setenv("TEAM_MEMORY_EXTRACTION_VERSION", "v1")

	config, err := loadConfig()
	s.Require().NoError(err)
	s.Equal("v1", config.extractionVersion)
}

func (s *configSuite) TestCheckedInExtractionDefaultsUseV2() {
	tests := []struct {
		path string
		want string
	}{
		{path: ".env.example", want: "TEAM_MEMORY_EXTRACTION_VERSION=v2"},
		{path: ".env.eval-v2.example", want: "TEAM_MEMORY_EXTRACTION_VERSION=v2"},
		{path: "compose.yaml", want: "TEAM_MEMORY_EXTRACTION_VERSION: ${TEAM_MEMORY_EXTRACTION_VERSION:-v2}"},
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
		{path: ".env.example", want: "evidence-fidelity-v1, source-clause-v1, typed-2, source-span-v1"},
		{path: ".env.example", want: "TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY="},
		{path: ".env.eval-v2.example", want: "TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY=current"},
		{path: "compose.yaml", want: "EXTRACTION_CANDIDATE_STRATEGY: ${TEAM_MEMORY_BUILD_EXTRACTION_CANDIDATE_STRATEGY:-current}"},
		{path: "evals/v2/compose.yaml", want: "TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY: ${TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY:-}"},
		{path: "evals/opencode/compose.yaml", want: "TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY: ${TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY:-}"},
		{path: "evals/v2/config.example.yaml", want: "  - TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY"},
		{path: "evals/v2/config.smoke.example.yaml", want: "  - TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY"},
		{path: "evals/v3/config.example.yaml", want: "  - TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY"},
		{path: "scripts/load-eval-v2-env.sh", want: `: "${TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY:=current}"`},
		{path: "scripts/load-eval-v2-env.sh", want: "TEAM_MEMORY_EXTRACTION_VERSION TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY"},
		{path: "Dockerfile", want: "ARG EXTRACTION_CANDIDATE_STRATEGY=current"},
		{path: "Makefile", want: "EXTRACTION_CANDIDATE_STRATEGY ?= current"},
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
		{name: "empty API keys", database: "postgres://database", apiKeys: `{}`},
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
