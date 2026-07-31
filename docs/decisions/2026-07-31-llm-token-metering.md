# LLM Token Metering

Status: Accepted

Date: 2026-07-31

Related:

- [On-Prem Core with Multi-Team SaaS](./2026-07-31-onprem-saas-split.md)
- [Single-Team On-Prem Deployment](./2026-07-21-single-team-on-prem-deployment.md)
- [DeepSeek Context Caching](https://api-docs.deepseek.com/guides/kv_cache/)

## Context

Every LLM call in the system burns money, and the burn is dominated by three
differently-priced buckets: cache-hit input tokens (~1/10 the price of a
miss on DeepSeek), cache-miss input tokens, and output tokens. Today the
telemetry is inconsistent per call path:

- **Extraction** (`internal/teamnote/extractor`): parses all four usage
  fields (input, output, cache hit, cache miss) and persists input/output
  per run in `extraction_runs`, but the cache split only reaches slog.
- **Page Wiki** (planner / editor / tree indexer) and the **Todo rewriter**
  share `platform/llm.ChatClient`; `TokenUsage` carries only
  `InputTokens`/`OutputTokens`, the DeepSeek client drops the cache split,
  and callers discard `Usage` entirely — nothing is persisted.

Nobody can answer "what did this rebuild cost" or "which team burned how
many tokens this week". Per-team quotas and SaaS billing (see the
companion ADR) both depend on exactly this data, keyed by `scope_id`.

## Decision

Record every LLM call in one scoped table, with the full price-relevant
breakdown, via a decorator at the existing `ChatClient` choke point plus
one hook on the extraction path.

1. **`llm.TokenUsage` carries the cache split.** Add
   `PromptCacheHitTokens` / `PromptCacheMissTokens`; the DeepSeek client
   parses `prompt_cache_hit_tokens` / `prompt_cache_miss_tokens`
   (zero when a provider omits them).
2. **One scoped events table** (migration 025):
   `llm_usage_events(ordinal BIGSERIAL PK, scope_id, component, model,
   input_tokens, cache_hit_tokens, cache_miss_tokens, output_tokens,
   occurred_at)`, indexed on `(scope_id, occurred_at)`. Raw per-call rows,
   no pre-aggregation — call volume is low (a handful per session inject)
   and raw rows keep per-run attribution.
3. **`MeteredChatClient` decorator** in `platform/llm` wraps any
   `ChatClient` with a component label and a `UsageSink`. Wiring wraps the
   client once per consumer: `wiki-planner`, `wiki-editor`,
   `wiki-indexer`, `todo-rewriter`. Scope resolution: from the request
   context when present, else the constructor-supplied default scope
   (today `onprem.LocalScopeID`; the context path is what SaaS will use).
   Recording failures are logged, never propagated — metering must not
   fail the call.
4. **Extraction hooks the sink directly** in `teamnote/runtime` where the
   complete `Usage` (with cache split) and the context scope are both in
   hand, with component `extractor`. `extraction_runs` keeps its existing
   columns untouched.
5. **Read API + UI.** `GET /v1/llm-usage?days=N` (thrift-generated,
   human-member read) returns per-component aggregates of the four
   counters for the window. The wiki status page gains an "LLM usage"
   card rendering that table.

## Consequences

- Billing-accurate accounting per team from day one: the three price
  buckets are never collapsed, so a price sheet can be applied directly.
- Cache-hit rate becomes observable per component, which measures how
  well the ingest pipeline exploits provider-side prompt caching.
- The decorator is the natural future enforcement point for per-team
  quotas: check budget before the call, record after it.
- The events table grows unboundedly; at current volumes this is years of
  headroom, and aggregation/retention can be added when a tenant's volume
  demands it (recorded as deferred, not designed now).
- Eval harnesses and other direct LLM users outside the four wired
  components remain unmetered until they opt into the decorator.
