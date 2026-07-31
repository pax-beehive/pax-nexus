package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type llmUsageSuite struct {
	suite.Suite
	store *postgres.Store
	usage *postgres.LLMUsageStore

	// scopeID is set by each test via newScope and cleaned up in
	// TearDownTest.
	scopeID string
}

func (s *llmUsageSuite) SetupSuite() {
	store, err := postgres.Open(context.Background(), testDSN(s.T()))
	s.Require().NoError(err)
	s.Require().NoError(store.Migrate(context.Background()))
	s.store = store

	usage, err := postgres.NewLLMUsageStore(store.Pool())
	s.Require().NoError(err)
	s.usage = usage
}

func (s *llmUsageSuite) TearDownSuite() {
	if s.store != nil {
		s.store.Close()
	}
}

func (s *llmUsageSuite) TearDownTest() {
	if s.scopeID == "" {
		return
	}
	_, err := s.store.Pool().Exec(context.Background(),
		`DELETE FROM llm_usage_events WHERE scope_id = $1`, s.scopeID)
	s.NoError(err)
	s.scopeID = ""
}

func (s *llmUsageSuite) newScope(label string) string {
	s.scopeID = uniqueScope(label)
	return s.scopeID
}

func TestLLMUsageSuite(t *testing.T) {
	suite.Run(t, new(llmUsageSuite))
}

// TestNilStoreDegradesInsteadOfPanicking exercises a typed-nil
// *postgres.LLMUsageStore, as flows through llm.UsageSink when the store is
// wired but never opened (or its constructor is bypassed via a zero value).
// It does not need a DSN: the guard must fire before the pool is touched.
func TestNilStoreDegradesInsteadOfPanicking(t *testing.T) {
	var store *postgres.LLMUsageStore

	err := store.RecordLLMUsage(context.Background(), llm.UsageEvent{
		ScopeID: "scope", Component: "component",
	})
	require.Error(t, err)

	_, err = store.UsageSummary(context.Background(), "scope", time.Hour)
	require.Error(t, err)
}

func (s *llmUsageSuite) TestRecordAndSummarize() {
	ctx := context.Background()
	scope := s.newScope("llm-usage-summary")

	s.Require().NoError(s.usage.RecordLLMUsage(ctx, llm.UsageEvent{
		ScopeID: scope, Component: "wiki-editor", Model: "deepseek-chat",
		Usage: llm.TokenUsage{
			InputTokens: 100, OutputTokens: 20,
			PromptCacheHitTokens: 10, PromptCacheMissTokens: 90,
		},
	}))
	s.Require().NoError(s.usage.RecordLLMUsage(ctx, llm.UsageEvent{
		ScopeID: scope, Component: "wiki-editor", Model: "deepseek-chat",
		Usage: llm.TokenUsage{
			InputTokens: 200, OutputTokens: 30,
			PromptCacheHitTokens: 50, PromptCacheMissTokens: 150,
		},
	}))
	s.Require().NoError(s.usage.RecordLLMUsage(ctx, llm.UsageEvent{
		ScopeID: scope, Component: "extractor", Model: "deepseek-chat",
		Usage: llm.TokenUsage{
			InputTokens: 40, OutputTokens: 5,
			PromptCacheHitTokens: 4, PromptCacheMissTokens: 36,
		},
	}))

	summary, err := s.usage.UsageSummary(ctx, scope, 24*time.Hour)
	s.Require().NoError(err)
	s.Require().Len(summary, 2)

	s.Equal("extractor", summary[0].Component)
	s.Equal("deepseek-chat", summary[0].Model)
	s.Equal(int64(1), summary[0].Calls)
	s.Equal(int64(40), summary[0].InputTokens)
	s.Equal(int64(4), summary[0].CacheHitTokens)
	s.Equal(int64(36), summary[0].CacheMissTokens)
	s.Equal(int64(5), summary[0].OutputTokens)

	s.Equal("wiki-editor", summary[1].Component)
	s.Equal("deepseek-chat", summary[1].Model)
	s.Equal(int64(2), summary[1].Calls)
	s.Equal(int64(300), summary[1].InputTokens)
	s.Equal(int64(60), summary[1].CacheHitTokens)
	s.Equal(int64(240), summary[1].CacheMissTokens)
	s.Equal(int64(50), summary[1].OutputTokens)
}

func (s *llmUsageSuite) TestSummaryHonorsWindowAndScope() {
	ctx := context.Background()
	scope := s.newScope("llm-usage-window")
	otherScope := uniqueScope("llm-usage-other")
	s.T().Cleanup(func() {
		_, err := s.store.Pool().Exec(context.Background(),
			`DELETE FROM llm_usage_events WHERE scope_id = $1`, otherScope)
		s.NoError(err)
	})

	s.Require().NoError(s.usage.RecordLLMUsage(ctx, llm.UsageEvent{
		ScopeID: scope, Component: "wiki-editor", Model: "deepseek-chat",
		Usage: llm.TokenUsage{InputTokens: 10, OutputTokens: 1},
	}))

	// Older than the window: excluded via an explicit occurred_at.
	_, err := s.store.Pool().Exec(ctx, `
INSERT INTO llm_usage_events
    (scope_id, component, model, input_tokens, cache_hit_tokens, cache_miss_tokens, output_tokens, occurred_at)
VALUES ($1, 'wiki-editor', 'deepseek-chat', 999, 0, 0, 0, $2)`,
		scope, time.Now().Add(-48*time.Hour))
	s.Require().NoError(err)

	// Different scope: excluded regardless of window.
	s.Require().NoError(s.usage.RecordLLMUsage(ctx, llm.UsageEvent{
		ScopeID: otherScope, Component: "wiki-editor", Model: "deepseek-chat",
		Usage: llm.TokenUsage{InputTokens: 777, OutputTokens: 1},
	}))

	summary, err := s.usage.UsageSummary(ctx, scope, 24*time.Hour)
	s.Require().NoError(err)
	s.Require().Len(summary, 1)
	s.Equal(int64(1), summary[0].Calls)
	s.Equal(int64(10), summary[0].InputTokens)
}

func (s *llmUsageSuite) TestValidation() {
	_, err := postgres.NewLLMUsageStore(nil)
	s.Require().Error(err)

	ctx := context.Background()
	err = s.usage.RecordLLMUsage(ctx, llm.UsageEvent{ScopeID: "", Component: "wiki-editor"})
	s.Require().Error(err)
	err = s.usage.RecordLLMUsage(ctx, llm.UsageEvent{ScopeID: "scope", Component: "  "})
	s.Require().Error(err)

	_, err = s.usage.UsageSummary(ctx, "  ", time.Hour)
	s.Require().Error(err)
}
