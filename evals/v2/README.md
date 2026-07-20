# Eval v2

Eval v2 is a resumable, configuration-driven experiment runner for long-lived
workstation evaluation. It executes the same cases through multiple arms and
persists every trial in PostgreSQL before exporting renderer-neutral artifacts.

The included configuration compares:

- `control`: OpenCode without passive recall or writes
- `team_note`: OpenCode using the PAX Team Note provider
- `team_note_hybrid`: Team Note passive recall plus an `active_recall` tool with
  a runtime-enforced maximum of two calls per consumer trial; it uses an
  arm-specific Team Note scope so ingest and readiness metrics stay independent
- `mem0`: OpenCode using a self-hosted Mem0 OSS REST server

The Team Note arms use local CPU `Qwen/Qwen3-Embedding-0.6B` embeddings in
addition to lexical retrieval. Each lane contributes up to 16 candidates;
reciprocal rank fusion and deterministic Team Note rules produce the final
delivery order. This retrieval fusion is separate from the
`team_note_hybrid` arm name, which means passive recall plus at most two active
recall tool calls.

Every trial records the same benchmark `asking_user_id`. Team Note uses it as
the consumer principal so actor-aware recall can resolve first-person queries.
Mem0 instead uses one shared synthetic user and agent identity across all cases,
configured by `MEM0_EVAL_USER_ID` and `MEM0_EVAL_AGENT_ID`. A case-specific
`run_id` prevents cross-case contamination without turning source actors into
separate Mem0 namespaces.

The memory arms receive the exact same selected GroupMemBench messages,
without a producer-model rewrite. Case preparation writes native session batches
partitioned by source author, channel, and phase. Team Note ingests those batches
with their original user, agent, session, timestamp, and reply provenance. Mem0
receives a deterministic transcript rendered from the same events because its
API accepts conversational messages rather than PAX session batches.
Channel and phase remain provenance metadata rather than recall filters; the
case-specific scope already provides isolation, and consumers do not declare a
benchmark task or thread reference.

## Prepare a run

Keep the existing `.env` for shared DeepSeek and paxm credentials. Create the
ignored Eval v2 environment and local run config from the tracked templates:

```bash
cp .env.eval-v2.example .env.eval-v2
cp evals/v2/config.example.yaml evals/v2/config.local.yaml
```

Eval v2 loads `.env.eval-v2` after `.env`, so eval-specific values override the
base environment without entering Git. Replace `MEM0_OPENAI_API_KEY=replace-me`
before starting the stack. Mem0 uses DeepSeek V4 Flash for memory extraction,
while retaining OpenAI `text-embedding-3-small` embeddings, so both provider
keys are required.

The upstream Mem0 API image does not apply `MEM0_DEFAULT_LLM_MODEL` by itself.
After Mem0 becomes healthy, `eval-v2-stack.sh` runs a one-shot configurator that
posts the complete provider configuration to Mem0's `/configure` endpoint. The
stack startup fails if that configuration is rejected, preventing the recorded
model name from drifting away from the model Mem0 actually uses.
The configurator uses Mem0's OpenAI-compatible LLM adapter with
`MEM0_DEEPSEEK_BASE_URL` and the DeepSeek API key. Mem0 0.1.117's dedicated
DeepSeek adapter accepts a `response_format` argument but does not forward it;
the OpenAI-compatible adapter forwards JSON mode while still using
`deepseek-v4-flash`. The embedder remains OpenAI `text-embedding-3-small`.
The local Eval v2 Mem0 image also installs the PostgreSQL driver omitted by the
upstream API image so its configured pgvector store is actually available. It
removes the upstream image's unusable Neo4j default because this vector-only
benchmark does not run a graph store.

Prepare both deterministic GroupMemBench profiles:

```bash
make eval-v2-prepare
```

This writes:

- `manifest.smoke.json`: 3 cases covering multi-hop, knowledge-update, and
  abstention behavior
- `manifest.json`: the 30-case acceptance matrix, with 5 cases in each of the
  6 benchmark categories
- `cases/<case>/producer/session-batches.json`: the native multi-agent source
  events used by both memory arms

Run the smoke profile first:

```bash
make eval-v2-smoke-up
make eval-v2-smoke
```

Only after smoke is green, run the 30-case acceptance profile:

```bash
make eval-v2-acceptance-up
make eval-v2-acceptance
```

The convenience targets use the tracked example configs. For a publishable
named run, copy the relevant config, change its run ID and output directory,
and use the generic `make eval-v2-up` / `make eval-v2` targets. Smoke and
acceptance use different run IDs and output directories, so their durable
results cannot collide.

The supplied templates use matching run IDs and manifest paths. Change the run
ID in `.env.eval-v2` and `config.local.yaml`, plus `run.output_dir`, together
when starting a new named run.

The acceptance template also enables live stage capture for the first ten
annotated Finance cases. `stage_capture.fixtures` selects the annotation set;
`stage_capture.arms` selects Team Note arms whose completed consumer sessions
must be exported. Smoke does not enable this ten-case fixture because its
three-case manifest is a different subset.
Stage files are built in a temporary directory and replace the prior complete
set only after every configured arm is ready. Judge-only export reuses the
existing stage artifacts rather than recapturing mutable live state.
Set a unique `run.id`, and make `run.manifest` point at the generated manifest.
The run ID is durable: rerunning the same config resumes completed work, while
reusing the ID with a changed config is rejected.

Start the evaluation stack and run the matrix:

```bash
make eval-v2-up
make eval-v2
make eval-v2-down
```

`eval-v2-down` keeps PostgreSQL volumes. Use `make eval-v2-reset` only when the
stored Team Note, Mem0, and eval state should all be destroyed.
`RUN_ID` must exactly match `run.id` in the YAML configuration. Both Team Note
and Mem0 scopes include this value, so persistent volumes cannot leak memories
from an older run into a new comparison.

The Mem0 container is configured with `AUTH_DISABLED=true` only on the private
evaluation network. Do not reuse this compose file as a production deployment.
The template pins `MEM0_IMAGE` to the tested Mem0 0.1.117 image digest. Change
that digest deliberately and record it in `runtime_env` when validating an
upgrade; do not use `latest` for a publishable run.

Publishable benchmark templates default passive recall thresholds to `-1` and
the insertion threshold to zero. The negative recall sentinel is intentional:
paxm treats an all-zero threshold profile as unset and restores its `0.25`
defaults. `-1` keeps every non-negative provider result, while insertion zero
disables its positive-score filter. This preserves each provider's returned
top-k/rank instead of comparing uncalibrated absolute scores (for example, Team
Note's fixed `1.0` with a vector store distance). Production-like experiments may override
`PAXM_PASSIVE_MIN_RELEVANCE`, `PAXM_PASSIVE_MIN_SCORE`, and
`PAXM_INSERTION_MIN_SCORE`, but should calibrate and report them per provider.
`PAXM_EVAL_DIAGNOSTICS=1` appends paxm diagnostics to each consumer stderr
artifact.

## Resume behavior

The runner pre-creates the complete case-by-arm matrix. A trial moves through
`pending`, `running`, and either `completed` or `failed`. On restart, interrupted
`running` trials return to `pending`; completed trials are skipped. Failed
trials are not retried by default. Enabling `retry_failed` also requires an
explicit `retry_max_attempts` of at least 2, and the store refuses to claim a
failed trial after that cap. Diagnose and correct a systemic failure before
opting into a bounded retry.

Before any runnable trial starts, the configured preflight performs real
health, add, and recall operations against both providers. It also deletes the
Mem0 probe and verifies it is gone. Team Note does not expose delete, so its
probe uses a run-specific preflight scope that is never visible to benchmark
cases. A preflight failure leaves all paid trials pending. Resuming a run with
no runnable work skips preflight.

Each trial has one total timeout covering native source ingest, memory readiness,
and the consumer. Command stdout and stderr are retained under
`<output_dir>/trials/<case>/<arm>/`. Native session batches are deterministic
case artifacts, so a bounded retry reuses them without another model call.
The runner and memory helper retain legacy `shared_producer`/`text-file`
compatibility for custom commands, but the supplied GroupMemBench script and
templates use native session batches.
Native multi-agent cases can create several source sessions. Readiness requires
every session cursor to complete, not merely the first one; the shell helper
allows up to 480 one-second checks by default within the trial timeout.

## Output contract

`output.formats` selects any of `csv`, `jsonl`, and `html`; all three are on
by default. Adding or removing `html` does not change the trial config hash,
so a completed run can be resumed to generate its report without rerunning
trials. Every completed run can export:

- `trials.csv`: one row per case and arm, including identity, quality, tokens,
  producer/consumer cost, total observed cost, stage latency, memory ingest
  receipt counts/no-op status, trial status, and error
- `trials.jsonl`: lossless trial records for downstream processing
- `attempts.jsonl`: append-only execution history with retry sequence, stage,
  failure class, timing, and artifact references
- `summary.csv`: overall and per-category arm aggregates
- `pairwise.csv`: candidate versus control wins, losses, ties, F1 delta, exact
  lift, safe-success lift, and incremental cost over paired completed trials
- `artifacts.json`: schema version, dataset revision, config hash, and artifact
  paths, plus the non-secret runtime versions named by `runtime_env`
- `stage/artifacts.json`: when `stage_capture` is configured, per-arm live
  extraction/recall Observations, stage results, and summaries
- `report.html`: a self-contained comparison report with overall and
  per-category token-F1 summaries, representative field notes, and an
  expandable breakdown of every case and every arm

The stable artifact schema is `pax-eval-v2.10`. Trial CSV/JSONL rows include
observed recall candidates, hits, injected context items, and recall latency.
`report.html` covers the common comparison views; raw CSV/JSONL files remain
available for other analysis.

Raw command output is retained per execution under
`trials/<case>/<arm>/attempts/<sequence>/`; a retry never overwrites the prior
Attempt's logs.

## Automated workstation job

`make eval-v2-job` builds a dedicated runner image and executes one isolated,
reproducible eval job. The runner image contains the eval and deterministic
GroupMemBench selection binaries plus pinned Docker CLI tooling. It mounts the
workstation Docker socket, so use it only on a dedicated trusted workstation.

The job refuses a dirty checkout, derives a unique run ID and selection seed,
uses a unique Compose project, enforces a process lock and a whole-run timeout,
and always removes its Compose volumes. A successful run updates `latest` and
`latest-success` symlinks under `runs/eval-v2/automated`. A failed model run
still retains `run.json`, command logs, and a self-contained failure
`report.html`. Successful runs also include `config.resolved.json`, containing
the exact non-secret configuration and runtime provenance used by the runner.

Each run records the candidate and framework Git revisions, selection seed and
algorithm, manifest SHA-256, resolved runtime versions, and the exact image IDs
used by the runner and Compose services. The generated manifest and all case
workspaces live under the immutable run directory.

Useful runtime controls are:

- `EVAL_V2_TOTAL_CASES`, default `120`
- `EVAL_V2_SEED`, default the unique run ID
- `EVAL_V2_JOB_RUN_ID`, default `nightly-<UTC>-<Git SHA>`
- `EVAL_V2_JOB_TIMEOUT`, default `24h`
- `EVAL_V2_OUTPUT_ROOT`, default `runs/eval-v2/automated`
- `EVAL_V2_BASE_CONFIG`, default `evals/v2/config.example.yaml`

Configure a dedicated clean checkout with `.env` and `.env.eval-v2`, then let
the cron agent run only:

```sh
git pull --ff-only origin main
make eval-v2-job
```

The agent should publish or retain the resulting `report.html`; it should not
choose cases or modify eval configuration. `make test-scripts` runs a dry-run
cron smoke test without starting providers or calling models.
Token F1 and its paired win/loss/tie counts are lexical diagnostics, not counts
of semantically correct answers. Exact remains a full-string diagnostic.
`safe_success` also accepts conservative semantic abstentions when the reference
answer explicitly says the information is unavailable; token F1 remains lexical
for all answers.

## Stage-local diagnostics

Eval v2 answer accuracy is the outer acceptance loop. When a Team Note arm
fails, use the stage-local scorer in [`evals/stage/README.md`](../stage/README.md)
to separate extraction loss from recall loss. Its Observation contract retains
Note identity and evidence links; it does not infer a cause from the final
answer alone.

Cost fields use the `opencode_reported` scope. Per-arm values now contain the
consumer call plus any legacy arm-local or shared producer. The native
GroupMemBench path has no producer-model cost. Team Note
extraction calls and Mem0's internal model/embedder calls are also not yet
included because those backends do not expose one common billing contract.
Therefore `artifacts.json.cost_summary` remains an arm-level subtotal, not the
full billed cost of the run.

Exact match and token F1 remain diagnostic metrics rather than an official
GroupMemBench score. A semantic-equivalence judge and evidence-coverage labels
are the next scoring extensions; the raw answer and expected answer are retained
so those metrics can be populated without rerunning agent trials.
