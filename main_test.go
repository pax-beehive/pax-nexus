package main

import (
	"testing"
	"time"

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
		"TEAM_MEMORY_RETRIEVAL_CANDIDATE_LIMIT",
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
	s.False(config.extractionCompactionEnabled)
	s.True(config.extractionSummaryEnabled)
	s.Equal(12*1024, config.extractionCompactStartTokens)
	s.Equal(16*1024, config.extractionCompactTokens)
	s.Equal(8*1024, config.extractionSummaryTriggerTokens)
	s.Equal(16*1024, config.extractionSummaryTailTokens)
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
	s.InDelta(0.50, config.semanticThreshold, 0.0001)
	s.Equal(16, config.retrievalCandidateLimit)
	adapter, err := buildExtractor(config)
	s.Require().NoError(err)
	s.IsType(extractor.Noop{}, adapter)
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
