# PAX Nexus

PAX Nexus is the repository for shared agent context. Its current MVP contains
the Team Note runtime and its evaluation harness.
Team Note is short-lived, evidence-backed collaboration state shared between
agents. It is not a generic personal-memory provider or a long-term wiki.

The MVP is evaluation-first:

```text
paxm session events
  -> Team Note runtime
  -> deterministic delivery
  -> dockerized OpenCode consumer
  -> eval result
```

## Repository layout

```text
CONTEXT-MAP.md                         Product contexts and dependency direction
doc/                                   Product architecture and evaluation design
internal/session/                      Shared session identity and event contracts
internal/sessionlake/                  Shared immutable ingestion and cursor module
internal/teamnote/                     Team Note domain module
internal/teamnote/extractor/           Team Note extraction adapters
internal/teamnote/extractionqueue/      River-backed Team Note worker pool
internal/teamnote/runtime/              Team Note orchestration
internal/teamnote/transport/httpapi/    Hertz adapter generated from Thrift IDL
internal/teamnote/paxmprovider/         paxm bridge to Team Note HTTP
internal/eval/                          Evaluation harness and benchmark adapters
internal/eval/v2/                       Resumable multi-arm evaluation engine
internal/eval/v3/                       Three-arm multi-agent GroupMemBench protocol
internal/llmwiki/                       Reserved durable LLM Wiki module
internal/platform/                      Shared PostgreSQL and observability adapters
evals/opencode/                         Two-OpenCode scenarios and Docker orchestration
evals/v2/                               Team Note versus self-hosted Mem0 configuration
evals/v3/                               Full-domain multi-agent architecture evaluation
idl/                                    Thrift source of truth for HTTP interfaces
cmd/paxm-team-memory-provider paxm provider process used by OpenCode
cmd/team-memory-eval/        Deterministic OpenCode output scorer
cmd/team-memory-eval-v2/     Durable configuration-driven evaluation runner
cmd/team-memory-eval-v3/     Multi-agent GroupMemBench evaluation runner
```

Team Note and LLM Wiki are independent product modules. Both consume Session
Lake evidence, but neither may import the other. Evaluation may depend on either
product module; product code may not depend on Evaluation. These rules are
enforced by `internal/architecture/dependencies_test.go`.

The core module owns admission, canonical note state, lifecycle, and delivery.
Adapters may vary storage, extraction model, and transport without exposing
those choices to callers.

## Local runtime

The runtime uses PostgreSQL with pgvector, the Team Memory service, and a local
CPU Qwen3 embedding runtime.
River runs inside the Team Memory process and stores its durable jobs in the
same PostgreSQL database. Start and stop the stack with:

```bash
make up
make down
```

The HTTP API listens on `http://localhost:58080` by default. `make logs` follows
the database and Team Memory process. River defaults to sixteen single-worker
queue shards, which keeps a
session on one serial shard while allowing different sessions to be extracted
in parallel. Configure the pool with `TEAM_MEMORY_WORKER_SHARDS`, retry count
with `TEAM_MEMORY_WORKER_MAX_ATTEMPTS`, and lifecycle timeouts with
`TEAM_MEMORY_WORKER_JOB_TIMEOUT` and `TEAM_MEMORY_WORKER_STOP_TIMEOUT`.

Incomplete session batches are sealed after 30 seconds of inactivity by
default. Every new batch for the same session resets the effective timeout;
stale scheduled jobs verify their captured cursor and exit without extracting.
Set `TEAM_MEMORY_BATCH_TIMEOUT` to change this interval. Explicitly complete
batches use the shorter `TEAM_MEMORY_WORKER_DEBOUNCE`, which defaults to 750ms.

Each queue slice still contributes at most 25 new events and approximately
8,192 input tokens, with up to three prior events as overlap. In the default
`rolling` extraction mode, slices from different agent sessions that share a
scope, task, and thread advance one durable LLM episode. The request history is
append-only so provider prefix caching can reuse earlier context. The model
returns only candidate deltas. At 12,288 estimated tokens, one asynchronous
singleflight starts producing a structured, evidence-backed checkpoint. New
session slices may continue appending while it runs. Before an append would
cross 16,384 tokens, the worker waits for that flight, installs the checkpoint,
retains messages appended after the compaction snapshot as a tail, and continues
from the compacted prefix. Compaction is disabled by default while its retention
and schema behavior are evaluated; set
`TEAM_MEMORY_EXTRACTION_COMPACTION_ENABLED=true` to activate these thresholds.

Extraction v2 is available behind `TEAM_MEMORY_EXTRACTION_VERSION=v2` only in
rolling mode. It emits atomic, evidence-cited State Decisions, exceptional
Claims, explicit per-Event coverage, and a diagnostic Extraction Trace in one
primary model call. The default remains `v1` until the fixed extraction shadow
passes fact-recall, leakage, output-token, cache, and latency gates; changing
the extraction protocol starts a fresh rolling episode instead of replaying an
incompatible response history.

The default rolling path instead keeps an append-only recent raw tail and runs a
non-blocking continuity summary. After the raw message history grows beyond the
16,384-token tail by another 8,192 tokens, one asynchronous summary updates the
derived checkpoint and retains the newest raw tail. Extraction never waits for
this summary; a failed summary leaves the previous checkpoint and raw history in
place. Configure this with `TEAM_MEMORY_EXTRACTION_SUMMARY_ENABLED`,
`TEAM_MEMORY_EXTRACTION_SUMMARY_TRIGGER_TOKENS`, and
`TEAM_MEMORY_EXTRACTION_SUMMARY_TAIL_TOKENS`.

Set `TEAM_MEMORY_EXTRACTION_CONTEXT_MODE=slice` to retain the original isolated
slice behavior for evaluation. Configure the rolling soft trigger with
`TEAM_MEMORY_EXTRACTION_COMPACT_START_TOKENS` and the hard wait threshold with
`TEAM_MEMORY_EXTRACTION_COMPACT_TOKENS`; configure queue slice bounds with
`TEAM_MEMORY_SLICE_EVENT_LIMIT`, `TEAM_MEMORY_SLICE_TOKEN_LIMIT`,
`TEAM_MEMORY_SLICE_OVERLAP`, and `TEAM_MEMORY_MAX_SLICES_PER_JOB`. Extraction
logs expose prompt cache hit and miss tokens when the provider reports them.

Passive recall uses two candidate lanes behind the existing `RecallNotes`
interface: PostgreSQL full-text rank and Qwen3 semantic similarity. The runtime
embeds Team Notes after admission, fuses the top 16 candidates per lane with
reciprocal rank fusion, and then applies the existing authorization, scalar-slot,
kind, recency, budget, and delivery-once rules. The default local model is
`Qwen/Qwen3-Embedding-0.6B`; its Matryoshka output is truncated and normalized
to 384 dimensions. One-hop related facts are composed in either direction, so
a recalled blocker can carry the action that points back to it. Configure it
with `TEAM_MEMORY_EMBEDDING_BASE_URL`,
`TEAM_MEMORY_EMBEDDING_MODEL`, `TEAM_MEMORY_EMBEDDING_TIMEOUT`,
`TEAM_MEMORY_SEMANTIC_THRESHOLD`, and
`TEAM_MEMORY_RETRIEVAL_CANDIDATE_LIMIT`. Leaving the base URL empty disables
semantic recall, and embedding failures fall back to lexical recall.

## Identity assumption

paxm session-event metadata supplies stable `user_id` and `agent_id` values and
the existing `session_id`. Collaboration-space scope is resolved by the
credential or provider configuration and is not accepted from event metadata.

## MVP order

1. Land deterministic Team Note contracts and fixtures.
2. Add PostgreSQL event and note persistence.
3. Add HTTP ingestion and recall.
4. Run extracted notes through two isolated OpenCode containers connected by
   the production paxm plugin and JSON-RPC provider seam.
5. Add gold-note and recent-event arms, then tune extraction against the
   matched control.

See [the design draft](doc/team-note-design.md) and the
[OpenCode eval layout](evals/opencode/README.md). The exact extraction schema
and recall participation rules are documented in
[extraction and recall](doc/extraction-and-recall.md).

For current paired runs, Eval v2 persists control, passive Team Note, hybrid
Team Note, and self-hosted Mem0 trials in PostgreSQL and exports CSV, JSONL, and
a self-contained HTML report. See [the evaluation runbook](evals/README.md).

## Development gates

The Makefile owns IDL generation, mocks, formatting, linting, tests, the
75-percent coverage gate, and the Docker eval. Run `make generate`, `make
mocks`, and `make lint test` after changing interfaces or implementation. Run
`make docker-eval` with the model and extractor environment described in
`evals/opencode/README.md`. Detailed coding rules live in `AGENTS.md`.
