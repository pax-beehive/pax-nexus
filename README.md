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
internal/llmwiki/                       Reserved durable LLM Wiki module
internal/platform/                      Shared PostgreSQL and observability adapters
evals/opencode/                         Two-OpenCode scenarios and Docker orchestration
evals/v2/                               Team Note versus self-hosted Mem0 configuration
idl/                                    Thrift source of truth for HTTP interfaces
cmd/paxm-team-memory-provider paxm provider process used by OpenCode
cmd/team-memory-eval/        Deterministic OpenCode output scorer
cmd/team-memory-eval-v2/     Durable configuration-driven evaluation runner
```

Team Note and LLM Wiki are independent product modules. Both consume Session
Lake evidence, but neither may import the other. Evaluation may depend on either
product module; product code may not depend on Evaluation. These rules are
enforced by `internal/architecture/dependencies_test.go`.

The core module owns admission, canonical note state, lifecycle, and delivery.
Adapters may vary storage, extraction model, and transport without exposing
those choices to callers.

## Local runtime

The runtime uses exactly two containers: PostgreSQL and the Team Memory service.
River runs inside the Team Memory process and stores its durable jobs in the
same PostgreSQL database. Start and stop both containers with:

```bash
make up
make down
```

The HTTP API listens on `http://localhost:58080` by default. `make logs` follows
both services. River defaults to sixteen single-worker queue shards, which keeps a
session on one serial shard while allowing different sessions to be extracted
in parallel. Configure the pool with `TEAM_MEMORY_WORKER_SHARDS`, retry count
with `TEAM_MEMORY_WORKER_MAX_ATTEMPTS`, and lifecycle timeouts with
`TEAM_MEMORY_WORKER_JOB_TIMEOUT` and `TEAM_MEMORY_WORKER_STOP_TIMEOUT`.

Incomplete session batches are sealed after 30 seconds of inactivity by
default. Every new batch for the same session resets the effective timeout;
stale scheduled jobs verify their captured cursor and exit without extracting.
Set `TEAM_MEMORY_BATCH_TIMEOUT` to change this interval. Explicitly complete
batches use the shorter `TEAM_MEMORY_WORKER_DEBOUNCE`, which defaults to 750ms.

Each model call receives at most 25 new events and approximately 8,192 input
tokens, with up to three prior events as overlap. A River job handles at most
four slices before yielding a continuation, and each slice may return at most
10 candidates. Configure these bounds with `TEAM_MEMORY_SLICE_EVENT_LIMIT`,
`TEAM_MEMORY_SLICE_TOKEN_LIMIT`, `TEAM_MEMORY_SLICE_OVERLAP`, and
`TEAM_MEMORY_MAX_SLICES_PER_JOB`.

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
