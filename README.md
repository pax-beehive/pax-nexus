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

See [Code Module Architecture](docs/code-architecture.md) for the current
module seams, runtime flows, dependency direction, concurrency ownership, and
verification contracts. The documents under `docs/decisions/` retain the
decision history for individual extraction, recall, and evaluation policies.

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
The production defaults allow one Slice per three-minute worker job and one
120-second provider attempt. Startup rejects a configured serial provider
budget that reaches the worker deadline; synchronous compaction includes its
initial call, one possible fallback call, and the primary call.

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

Extraction v1 is the service and deployment default in `rolling` mode. Set
`TEAM_MEMORY_EXTRACTION_VERSION=v1.1` to run the candidate-schema-compatible
fidelity experiment. V1.1 blocks the known `I'd keep` preference-tail leakage
and requires model-supplied semantic identities for mutable status and handoff
state; the server deterministically rejects missing identities, while broader
modality classification and exact identity reuse remain extraction-protocol
obligations. Set the version to `v2` to run the structured experiment, which
emits atomic,
evidence-cited State Decisions, exceptional Claims, explicit per-Event
coverage, and a diagnostic Extraction Trace in one primary model call.
Changing the extraction protocol starts a fresh rolling episode instead of
replaying an incompatible response history. The paired evidence and rollout
decisions remain recorded in the extraction ADRs.

V2 extraction candidate strategies are packaged behind the same `Extractor`
interface. `current`, `interaction-slim`, `evidence-fidelity-v1`,
`source-clause-v1`, `source-clause-implicit-state-v1`, `typed-2`,
`source-span-v1`, `source-span-v2`, `claim-card-v1`, and `claim-card-v2` each
bind their prompt, response decoder, and rolling episode protocol revision in
one registry. `source-clause-v1` is the v2 evaluation default. The implicit-state,
Evidence Fidelity, Source Span, and Claim Card candidates are retained only for
evaluation reproducibility. Build
a distribution with a selected default using
`make build EXTRACTION_CANDIDATE_STRATEGY=typed-2`, or pass the same build
argument to Docker as `EXTRACTION_CANDIDATE_STRATEGY`. At runtime,
`TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY` overrides the embedded default;
an empty value uses the build default. The extractor model remains independent,
so a Pro arm also sets `TEAM_MEMORY_EXTRACTOR_MODEL=deepseek-v4-pro`.

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
defaults to one Slice per job. Provider execution defaults to one 120-second
attempt, and the worker job deadline defaults to three minutes. Extraction
logs expose prompt cache hit and miss tokens when the provider reports them.

Passive recall runs General Recall v3 behind the existing `RecallNotes` and
`PlanRecall` seams. It compiles an explicit intent, records exact, lexical,
temporal, relation, coordination, and routing lanes, applies temporal and
adapter-prechecked hard gates plus provenance and stored prompt-injection
checks, then uses a lexicographic evidence selection and shared token budget.
Each candidate records query-safe matched-term counts, reason codes,
scorecard contributions, relation paths, disposition, and rejection reason.
Qwen3 semantic similarity remains a compatibility fallback when inspectable
lanes do not supply evidence; it only generates candidates and cannot assign
passive evidence without inspectable coordination, routing, or intent support.
It is not a core v3 ranking or confidence signal.
The default local model is `Qwen/Qwen3-Embedding-0.6B`; its Matryoshka output is truncated and normalized
to 384 dimensions. One-hop related facts are composed in either direction, so
a recalled blocker can carry the action that points back to it. Configure the
embedding adapter with `TEAM_MEMORY_EMBEDDING_BASE_URL`,
`TEAM_MEMORY_EMBEDDING_MODEL`, and `TEAM_MEMORY_EMBEDDING_TIMEOUT`. Recall
semantics are distributed as a build-time candidate:
`TEAM_MEMORY_BUILD_RECALL_CANDIDATE_STRATEGY=passive-v1` is the production
default, while `hint-v1-selective` retains the same passive evidence path and
permits one focused active recall when the selectivity gates pass. Leaving the
base URL empty disables semantic recall, and embedding failures fall back to
lexical recall.

## Single-Team on-prem API

The default Compose deployment represents one Team and requires
`TEAM_MEMORY_ADMIN_API_KEY`. It does not expose a Team ID: enrolled Agent keys
always resolve to the internal `local-team` scope, and the authenticated key
owns the `user_id` and `agent_id` used by observation and recall requests.

The Hertz HTTP surface is generated from `idl/team_memory.thrift` and provides:

- `POST /v1/admin/agent-enrollments`, one-time Agent enrollment;
- `POST /v1/agent-enrollments/exchange`, API-key exchange;
- `GET /v1/agent-identity`, credential-bound identity;
- `POST /v1/agent-credentials/rotate` and
  `DELETE /v1/admin/agent-credentials/:credential_id`;
- `POST /v1/observations`, asynchronous Session Event ingestion;
- `POST /v1/memory/search` and `POST /v1/memory/get`.

Passive search returns Team Note evidence and can speculatively run one typed
LLM Wiki hint path under the same deadline and token budget. The hint path is
off by default. The concrete LLM Wiki page store, index, and document resolver
remain a separate module decision; until that adapter is installed, active
Wiki search/get is unavailable and `TEAM_MEMORY_WIKI_HINT_ENABLED` must remain
false. The legacy session-batch and note-recall endpoints remain available only
when `TEAM_MEMORY_API_KEYS` selects compatibility mode without an admin secret.
The two authentication environment variables are mutually exclusive.

See [the on-prem deployment decision](docs/decisions/2026-07-21-single-team-on-prem-deployment.md)
for the identity, credential, recall composition, and module boundaries.

Run `make onprem-e2e` for the container-only core-flow test. It builds Team
Memory, a deterministic OpenAI-compatible extractor, a black-box test runner,
and pgvector PostgreSQL in one unique Compose project. The test enrolls two
Agents, exchanges their credentials, writes an Observation, waits for River and
extraction, then verifies passive recall through the public HTTP API. PostgreSQL
uses a project-scoped temporary volume; the script runs `docker compose down -v`
and verifies that the volume was removed on both success and failure. It refuses
to start if an overridden Compose project name already owns any resources.

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
80-percent handwritten-code coverage gate, and the Docker eval. Pure Hertz
generated model and router files are excluded; handler bridges count toward the
gate, and PostgreSQL adapter coverage runs against PostgreSQL. Run `make generate`, `make
mocks`, and `make lint test` after changing interfaces or implementation. Run
`make docker-eval` with the model and extractor environment described in
`evals/opencode/README.md`. Detailed coding rules live in `AGENTS.md`.
