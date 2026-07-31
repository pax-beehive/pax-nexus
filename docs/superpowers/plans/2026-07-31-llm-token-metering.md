# LLM Token Metering Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist every LLM call's token usage (input, cache-hit, cache-miss, output) per scope and component, and show the aggregates on the wiki status page.

**Architecture:** Per ADR `docs/decisions/2026-07-31-llm-token-metering.md`: extend `platform/llm.TokenUsage` with the cache split; add a `MeteredChatClient` decorator + `UsageSink` interface in `platform/llm`; one scoped `llm_usage_events` table (migration 025) with a postgres sink + summary query; wire the decorator around the wiki (planner/editor/indexer) and todo-rewriter clients and hook the extraction path via a runtime callback; expose `GET /v1/llm-usage` (thrift) and render an "LLM usage" card on the wiki status page.

**Tech Stack:** Go (pgx, hertz), thrift codegen (`make generate`), React + TypeScript, vitest, testify.

## Global Constraints

- Four counters are never collapsed: `input_tokens` (provider total), `cache_hit_tokens`, `cache_miss_tokens`, `output_tokens`. Providers omitting the cache split yield zeros there while `input_tokens` stays populated.
- Metering must never fail or block the LLM call: sink errors are logged (`WarnContext`) and swallowed.
- Component labels are exactly: `wiki-planner`, `wiki-editor`, `wiki-indexer`, `todo-rewriter`, `extractor`.
- Scope resolution in the decorator: context first, constructor default second (today `onprem.LocalScopeID`).
- No hand-edits to generated files; thrift model changes go through `idl/team_memory.thrift` + `make generate`.
- Lint gate strict (errcheck `check-blank: true` — never `_ =` an error); coverage `make coverage` ≥ 80%; DB tests need `TEAM_MEMORY_TEST_POSTGRES_DSN` (a local Postgres usually listens on 127.0.0.1:55432, credentials in Makefile line 12).
- Branch feat/llm-token-metering is stacked on feat/wiki-rebuild-lookback (PR #46); do not rebase or touch its commits.

---

### Task 1: `platform/llm` — cache-split usage + metered decorator

**Files:**
- Modify: `internal/platform/llm/chat.go` (TokenUsage, ~line 43)
- Modify: `internal/platform/llm/deepseek.go` (usage decode, ~lines 98-120)
- Create: `internal/platform/llm/metered.go`
- Test (modify): `internal/platform/llm/deepseek_test.go` (or the package's existing client test file — find it)
- Test (create): `internal/platform/llm/metered_test.go`

**Interfaces:**
- Consumes: existing `ChatClient` interface `Complete(context.Context, ChatRequest) (ChatResponse, error)`; scope-in-context helper — check `internal/session/scope.go` for the exact exported names (survey notes `WithScope`/`ScopeFromContext` around lines 11-23) and use those; if `platform/llm` importing `internal/session` would create an import cycle (it won't — session has no llm dependency — but verify with `go build`), fall back to a small `ScopeFromContext func(context.Context) (string, bool)` field on the decorator config wired by callers.
- Produces (Tasks 2-3 rely on these exact shapes):

```go
type TokenUsage struct {
	InputTokens           int
	OutputTokens          int
	PromptCacheHitTokens  int
	PromptCacheMissTokens int
}

type UsageEvent struct {
	ScopeID   string
	Component string
	Model     string
	Usage     TokenUsage
}

type UsageSink interface {
	RecordLLMUsage(ctx context.Context, event UsageEvent) error
}

type MeteredConfig struct {
	Client       ChatClient // required
	Sink         UsageSink  // required
	Component    string     // required, e.g. "wiki-planner"
	DefaultScope string     // required fallback when ctx carries no scope
	Logger       *slog.Logger // optional; nil → discard
}

func NewMeteredChatClient(config MeteredConfig) (*MeteredChatClient, error)
```

- [ ] **Step 1: Write the failing tests**

DeepSeek decode test (add to the existing DeepSeek client test file, following its stub-server pattern): a response body whose `usage` object is `{"prompt_tokens": 100, "completion_tokens": 40, "prompt_cache_hit_tokens": 70, "prompt_cache_miss_tokens": 30}` must decode to `TokenUsage{InputTokens: 100, OutputTokens: 40, PromptCacheHitTokens: 70, PromptCacheMissTokens: 30}`; a body without the cache fields must yield zeros for them.

`metered_test.go` (package `llm`, table-free explicit tests):

```go
type recordingSink struct {
	events []UsageEvent
	err    error
}

func (s *recordingSink) RecordLLMUsage(_ context.Context, event UsageEvent) error {
	s.events = append(s.events, event)
	return s.err
}

type stubChatClient struct {
	response ChatResponse
	err      error
}

func (c stubChatClient) Complete(context.Context, ChatRequest) (ChatResponse, error) {
	return c.response, c.err
}
```

Tests:
1. `TestMeteredClientRecordsUsageWithComponentAndDefaultScope` — inner returns `Usage: TokenUsage{100, 40, 70, 30}` and `ChatRequest{Model: "deepseek-chat"}`; assert one recorded event `{ScopeID: "local-team", Component: "wiki-editor", Model: "deepseek-chat", Usage: …}` and the response passes through unchanged.
2. `TestMeteredClientPrefersContextScope` — put a scope in ctx via the session helper; assert the event carries it instead of the default.
3. `TestMeteredClientSwallowsSinkErrors` — sink err non-nil; Complete still returns the inner response and nil error.
4. `TestMeteredClientSkipsRecordingOnClientError` — inner err non-nil; assert zero events and the error propagates.
5. `TestNewMeteredChatClientValidatesConfig` — nil client / nil sink / empty component / empty default scope each error.

Run: `go test ./internal/platform/llm/ -run 'Metered|Usage' -v` — expect FAIL (types undefined).

- [ ] **Step 2: Implement**

`chat.go`: extend `TokenUsage` with the two cache fields (order as in the interface block above).

`deepseek.go`: extend the inline `Usage` decode struct with `PromptCacheHitTokens int \`json:"prompt_cache_hit_tokens"\`` and `PromptCacheMissTokens int \`json:"prompt_cache_miss_tokens"\``, and map them into the returned `TokenUsage`.

`metered.go`:

```go
package llm

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/session"
)

// UsageEvent / UsageSink / MeteredConfig as in the Interfaces block.

// MeteredChatClient decorates a ChatClient, reporting each successful
// call's token usage to a UsageSink. Metering is best-effort: sink
// failures are logged and never propagated to the caller.
type MeteredChatClient struct {
	client       ChatClient
	sink         UsageSink
	component    string
	defaultScope string
	logger       *slog.Logger
}

func NewMeteredChatClient(config MeteredConfig) (*MeteredChatClient, error) {
	if config.Client == nil || config.Sink == nil ||
		strings.TrimSpace(config.Component) == "" ||
		strings.TrimSpace(config.DefaultScope) == "" {
		return nil, errors.New(
			"create metered chat client: client, sink, component, and default scope are required",
		)
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &MeteredChatClient{
		client: config.Client, sink: config.Sink,
		component: config.Component, defaultScope: config.DefaultScope,
		logger: logger,
	}, nil
}

func (m *MeteredChatClient) Complete(
	ctx context.Context,
	request ChatRequest,
) (ChatResponse, error) {
	response, err := m.client.Complete(ctx, request)
	if err != nil {
		return response, err
	}
	event := UsageEvent{
		ScopeID: m.defaultScope, Component: m.component,
		Model: request.Model, Usage: response.Usage,
	}
	if scoped, scopeErr := session.ScopeFromContext(ctx); scopeErr == nil && scoped != "" {
		event.ScopeID = scoped
	}
	if recordErr := m.sink.RecordLLMUsage(ctx, event); recordErr != nil {
		m.logger.WarnContext(ctx, "record LLM usage failed",
			"component", m.component, "scope_id", event.ScopeID, "error", recordErr)
	}
	return response, nil
}
```

Adapt the `session.ScopeFromContext` call to the helper's real signature (it may return `(string, bool)` or `(string, error)` — read `internal/session/scope.go` first; use whatever it exports, e.g. `teamnote`'s wrapper is NOT importable here, use the session package directly).

- [ ] **Step 3: Run tests**

Run: `go test ./internal/platform/llm/ && go build ./... && make lint`
Expected: PASS, build clean, 0 lint issues (existing `TokenUsage` literal initializers elsewhere keep compiling because the new fields are appended, not reordered).

- [ ] **Step 4: Commit**

```bash
git add internal/platform/llm
git commit -m "feat(llm): cache-split token usage and metered chat client decorator"
```

---

### Task 2: migration 025 + postgres usage sink and summary query

**Files:**
- Create: `internal/platform/postgres/migrations/025_llm_usage_events.sql`
- Create: `internal/platform/postgres/llm_usage.go`
- Test (create): `internal/platform/postgres/llm_usage_test.go` (DSN-gated, follow `TEAM_MEMORY_TEST_POSTGRES_DSN` skip pattern used by sibling suites)

**Interfaces:**
- Consumes: `llm.UsageEvent`, `llm.UsageSink` from Task 1; `pgxpool.Pool`.
- Produces (Task 3 relies on these):

```go
type LLMUsageStore struct{ … }

func NewLLMUsageStore(pool *pgxpool.Pool) (*LLMUsageStore, error)

// implements llm.UsageSink
func (s *LLMUsageStore) RecordLLMUsage(ctx context.Context, event llm.UsageEvent) error

type LLMUsageRow struct {
	Component        string
	Model            string
	Calls            int64
	InputTokens      int64
	CacheHitTokens   int64
	CacheMissTokens  int64
	OutputTokens     int64
}

func (s *LLMUsageStore) UsageSummary(
	ctx context.Context,
	scopeID string,
	window time.Duration,
) ([]LLMUsageRow, error)
```

- [ ] **Step 1: Migration**

`025_llm_usage_events.sql`:

```sql
CREATE TABLE IF NOT EXISTS llm_usage_events (
    ordinal BIGSERIAL PRIMARY KEY,
    scope_id TEXT NOT NULL,
    component TEXT NOT NULL,
    model TEXT NOT NULL DEFAULT '',
    input_tokens BIGINT NOT NULL DEFAULT 0,
    cache_hit_tokens BIGINT NOT NULL DEFAULT 0,
    cache_miss_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS llm_usage_events_scope_time_idx
    ON llm_usage_events (scope_id, occurred_at);
```

Check how migrations are registered (`internal/platform/postgres` — embed directive or directory scan) and follow it; most likely the embed FS picks up the file automatically.

- [ ] **Step 2: Write the failing integration test**

Suite skeleton per sibling suites (skip without DSN, unique scope per test, cleanup in TearDownTest deleting `llm_usage_events` rows for the scope). Tests:
1. `TestRecordAndSummarize` — record 3 events (two `wiki-editor`/`deepseek-chat`, one `extractor`/`deepseek-chat`) with distinct token values; `UsageSummary(ctx, scope, 24*time.Hour)` returns 2 rows aggregated by (component, model) with correct sums and `Calls` counts, ordered by component.
2. `TestSummaryHonorsWindowAndScope` — an event older than the window (insert with explicit `occurred_at` via raw SQL) and an event in another scope are both excluded.
3. `TestValidation` — `NewLLMUsageStore(nil)` errors; `RecordLLMUsage` with empty ScopeID or Component errors; `UsageSummary` with empty scope errors.

Run with DSN → FAIL (files don't exist yet).

- [ ] **Step 3: Implement `llm_usage.go`**

```go
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
)

// LLMUsageStore persists per-call LLM token usage and serves windowed
// aggregates. It implements llm.UsageSink.
type LLMUsageStore struct {
	pool *pgxpool.Pool
}

func NewLLMUsageStore(pool *pgxpool.Pool) (*LLMUsageStore, error) {
	if pool == nil {
		return nil, errors.New("create LLM usage store: pool is required")
	}
	return &LLMUsageStore{pool: pool}, nil
}

func (s *LLMUsageStore) RecordLLMUsage(ctx context.Context, event llm.UsageEvent) error {
	if strings.TrimSpace(event.ScopeID) == "" || strings.TrimSpace(event.Component) == "" {
		return errors.New("record LLM usage: scope and component are required")
	}
	if _, err := s.pool.Exec(ctx, `
INSERT INTO llm_usage_events
    (scope_id, component, model, input_tokens, cache_hit_tokens, cache_miss_tokens, output_tokens)
VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		event.ScopeID, event.Component, event.Model,
		event.Usage.InputTokens, event.Usage.PromptCacheHitTokens,
		event.Usage.PromptCacheMissTokens, event.Usage.OutputTokens,
	); err != nil {
		return fmt.Errorf("record LLM usage: %w", err)
	}
	return nil
}

type LLMUsageRow struct {
	Component       string
	Model           string
	Calls           int64
	InputTokens     int64
	CacheHitTokens  int64
	CacheMissTokens int64
	OutputTokens    int64
}

func (s *LLMUsageStore) UsageSummary(
	ctx context.Context,
	scopeID string,
	window time.Duration,
) ([]LLMUsageRow, error) {
	if strings.TrimSpace(scopeID) == "" {
		return nil, errors.New("summarize LLM usage: scope is required")
	}
	rows, err := s.pool.Query(ctx, `
SELECT component, model, COUNT(*),
       COALESCE(SUM(input_tokens), 0), COALESCE(SUM(cache_hit_tokens), 0),
       COALESCE(SUM(cache_miss_tokens), 0), COALESCE(SUM(output_tokens), 0)
FROM llm_usage_events
WHERE scope_id = $1 AND occurred_at >= NOW() - $2::interval
GROUP BY component, model
ORDER BY component, model`,
		scopeID, window.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("summarize LLM usage: %w", err)
	}
	defer rows.Close()
	var summary []LLMUsageRow
	for rows.Next() {
		var row LLMUsageRow
		if err := rows.Scan(
			&row.Component, &row.Model, &row.Calls, &row.InputTokens,
			&row.CacheHitTokens, &row.CacheMissTokens, &row.OutputTokens,
		); err != nil {
			return nil, fmt.Errorf("summarize LLM usage: scan: %w", err)
		}
		summary = append(summary, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("summarize LLM usage: %w", err)
	}
	return summary, nil
}
```

Note: `window.String()` produces Go duration syntax (`"168h0m0s"`) which Postgres does NOT parse as an interval — instead pass `window` and let pgx encode it, i.e. use `occurred_at >= NOW() - $2` with pgx's `time.Duration`→interval encoding; verify against the live DB in the integration test and adjust (the safe portable form is `occurred_at >= $2` computed in Go: `time.Now().Add(-window)` — prefer that, it also keeps clock authority in one place).

- [ ] **Step 4: Run tests + gates**

Run: `TEAM_MEMORY_TEST_POSTGRES_DSN='postgres://team_memory:team_memory@127.0.0.1:55432/team_memory?sslmode=disable' go test ./internal/platform/postgres/ -run LLMUsage -count=1 -v && make lint`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/platform/postgres
git commit -m "feat(postgres): llm_usage_events store with windowed summary"
```

---

### Task 3: wiring + `GET /v1/llm-usage` endpoint

**Files:**
- Modify: `idl/team_memory.thrift` (structs near line 362, service near line 1150)
- Generated via `make generate`
- Modify: `main.go` (wiki client wiring ~lines 237-258, todo rewriter ~line 361, runtime config, store plumbing)
- Modify: `internal/teamnote/runtime/app.go` (Config + usage hook, ~lines 17-26 and the slice-completion block ~line 166)
- Create: `internal/teamnote/transport/httpapi/handler/llm_usage_endpoints.go`
- Modify: `internal/teamnote/transport/httpapi/handler/dependencies.go` (interface + option)
- Test (create): `internal/teamnote/transport/httpapi/handler/llm_usage_endpoints_test.go`
- Test (modify): `internal/teamnote/runtime/app_test.go` (or wherever App's extraction loop is tested — find the existing test file)

**Interfaces:**
- Consumes: `llm.NewMeteredChatClient`, `postgres.LLMUsageStore` (+ `LLMUsageRow`) from Tasks 1-2.
- Produces: `GET /v1/llm-usage?days=N` → `{"rows": [{component, model, calls, input_tokens, cache_hit_tokens, cache_miss_tokens, output_tokens}]}` (Task 4's frontend contract).

- [ ] **Step 1: Thrift + generate**

```thrift
struct LlmUsageRequest {
  1: optional i32 days (api.query="days")
}

struct LlmUsageRow {
  1: required string component
  2: required string model
  3: required i64 calls
  4: required i64 input_tokens
  5: required i64 cache_hit_tokens
  6: required i64 cache_miss_tokens
  7: required i64 output_tokens
}

struct LlmUsageResponse {
  1: required list<LlmUsageRow> rows
}
```

Service entry next to the wiki ones (line ~1150): `LlmUsageResponse GetLlmUsage(1: LlmUsageRequest request) (api.get="/v1/llm-usage")`. Run `make generate`; confirm blast radius via `git diff --stat`.

- [ ] **Step 2: Handler (TDD — tests first)**

Test file follows `wiki_settings_endpoints_test.go`'s suite shape (fake service struct, `handler.NewOnPrem` with a new `handler.WithLLMUsage(fake)` option, generated route). Tests:
1. member GET `/v1/llm-usage` → 200 with rows serialized, fake received `window = 7 * 24 * time.Hour` (default).
2. `?days=30` → fake receives `30 * 24 * time.Hour`; `?days=0` and `?days=400` → 400 (valid range 1..365).
3. Without `WithLLMUsage` configured → 501 `not_configured` (mirror `authorizeWikiControl`'s nil guard).

Handler interface + endpoint:

```go
// dependencies.go
type LLMUsage interface {
	UsageSummary(ctx context.Context, scopeID string, window time.Duration) ([]platformpostgres.LLMUsageRow, error)
}
```

— check import naming conventions in dependencies.go first; if importing `internal/platform/postgres` into the handler package is inconsistent with existing style (handler currently depends on domain packages, not platform), instead define the row struct in the handler package (or a small shared package) and have main.go adapt. Follow whichever pattern `WikiSettings` (`pagewiki.GenerationDirectives`) uses — domain-type-in-interface — and if a clean domain home is needed, declare `LLMUsageRow` in `internal/platform/llm` next to `UsageEvent` and have the postgres store return that type instead (adjust Task 2's produced signature accordingly at implementation time; the reviewer accepts either home as long as handler doesn't import pgx).

Endpoint (`llm_usage_endpoints.go`): authorize via `h.authorizeHumanMember(ctx, c, false)` (read), parse/validate `days` (absent → 7; outside 1..365 → 400 `invalid_request`), call `h.llmUsage.UsageSummary(ctx, onprem.LocalScopeID, time.Duration(days)*24*time.Hour)`, map to generated response.

- [ ] **Step 3: Wire metering in `main.go` + runtime hook**

- Build `usageStore, err := postgres.NewLLMUsageStore(store.Pool())` once near the other store constructions; pass to `buildHTTPHandler` via `handler.WithLLMUsage(usageStore)`.
- Wiki block (~line 237): after constructing the DeepSeek client, wrap per component before handing to planner/editor/indexer:

```go
metered := func(component string) (platformllm.ChatClient, error) {
	return platformllm.NewMeteredChatClient(platformllm.MeteredConfig{
		Client: client, Sink: usageStore, Component: component,
		DefaultScope: onprem.LocalScopeID, Logger: logger,
	})
}
```

(threading `usageStore` into the builder function's signature; keep errors wrapped in the function's existing style). Same for the todo rewriter (`todo-rewriter`) at ~line 361.
- Extraction: `runtime.Config` gains `UsageRecorder func(ctx context.Context, model string, usage extractor.Usage)` (optional). In the slice-completion block (where the existing slog line lives, app.go ~line 166), after logging call `a.config.UsageRecorder` when non-nil, passing `result.Model` and `result.Usage`. In `main.go`, wire the closure: convert to `llm.UsageEvent{ScopeID: <teamnote.ScopeFromContext(ctx), fallback onprem.LocalScopeID>, Component: "extractor", Model: model, Usage: llm.TokenUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, PromptCacheHitTokens: usage.PromptCacheHitTokens, PromptCacheMissTokens: usage.PromptCacheMissTokens}}` and `RecordLLMUsage` with a `WarnContext` on error (never fail extraction).
- Runtime test: extend the existing App test file with one test asserting the recorder is invoked with the extractor's usage on a successful slice (follow the file's existing fake/lake setup; if no convenient seam exists, a recorder-called boolean + captured usage assertion suffices).

- [ ] **Step 4: Gates**

Run: `go build ./... && go test ./internal/... && make lint`
(DB suites skip without DSN; run the postgres LLMUsage suite with the DSN if reachable.)
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add idl internal main.go
git commit -m "feat(api): metered LLM clients, extraction usage hook, GET /v1/llm-usage"
```

---

### Task 4: wiki status page "LLM usage" card

**Files:**
- Modify: `web/src/api/wiki.ts`
- Modify: `web/src/pages/WikiStatusPage.tsx`
- Modify: `web/src/styles.css` (only if a new rule is needed; follow existing `.wiki-*` blocks)
- Test (modify): `web/tests/wiki-status.dom.test.tsx`, `web/tests/wikiFixtures.ts` (add usage fixture + default route)

**Interfaces:**
- Consumes: `GET /v1/llm-usage?days=N` from Task 3.
- Produces: user-visible card; no downstream consumers.

- [ ] **Step 1: Failing DOM tests**

Read `web/tests/wiki-status.dom.test.tsx` + `wikiFixtures.ts` first; add `/v1/llm-usage` to the default fetch routes returning a fixture like:

```ts
export const llmUsageFixture = {
  rows: [
    { component: "extractor", model: "deepseek-chat", calls: 12, input_tokens: 120000, cache_hit_tokens: 90000, cache_miss_tokens: 30000, output_tokens: 8000 },
    { component: "wiki-editor", model: "deepseek-chat", calls: 30, input_tokens: 400000, cache_hit_tokens: 250000, cache_miss_tokens: 150000, output_tokens: 60000 },
  ],
};
```

Tests:
1. Card renders a table with a row per component showing the four counters (assert a couple of formatted cell values and the component names) plus a totals row summing them.
2. Changing the window `<select>` (options: 24h / 7d / 30d → days=1/7/30, default 7d) refetches with the right query param (assert the fetch mock saw `/v1/llm-usage?days=30`).
3. Endpoint failing → card shows an unavailable note, page otherwise intact (follow how the page handles `statusError`).

- [ ] **Step 2: Implement**

`api/wiki.ts`:

```ts
export interface LLMUsageRow {
  component: string;
  model: string;
  calls: number;
  input_tokens: number;
  cache_hit_tokens: number;
  cache_miss_tokens: number;
  output_tokens: number;
}

export function getLLMUsage(days: number, signal?: AbortSignal): Promise<{ rows: LLMUsageRow[] }> {
  return humanFetch(`/v1/llm-usage?days=${days}`, { signal });
}
```

(match the file's existing fetch helper — check whether reads there use `humanFetch` from actions or a local helper, and mirror the neighboring `getWikiSettings`.)

`WikiStatusPage.tsx`: new state `usageDays` (default 7), `usage` rows, `usageError`; load on mount and on `usageDays` change (follow the settings-load effect pattern with abort handling). New `<section className="card wiki-llm-usage" aria-label="LLM token usage">` after the generation-settings card: header with the window `<select>`; table with columns Component / Model / Calls / Input / Cache hit / Cache miss / Output; totals row; numbers formatted with `toLocaleString()`. Empty rows → "No LLM calls recorded in this window." Error → muted unavailable note.

- [ ] **Step 3: Run**

Run: `cd web && npx vitest run tests/wiki-status.dom.test.tsx && npx vitest run && npx tsc --noEmit`
Expected: all green.

- [ ] **Step 4: Commit**

```bash
git add web
git commit -m "feat(web): LLM token usage card on the wiki status page"
```

---

## Final verification

- `make lint && make coverage` at repo root (coverage ≥ 80%; new postgres/handler code is covered by the suites above).
- `cd web && npx vitest run` full suite green.
- Postgres-backed suites ran with a real DSN at least once (Tasks 2 and 3).
