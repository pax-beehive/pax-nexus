# Evaluation runbook

Use Eval v2 for current Team Note quality, latency, and cost comparisons. It
runs the same deterministic GroupMemBench cases through four arms:

- `control`: OpenCode with memory recall and writes disabled
- `team_note`: OpenCode with the PAX Team Note provider
- `team_note_hybrid`: the same passive Team Note context plus at most two
  agent-initiated focused recalls
- `mem0`: OpenCode with the self-hosted Mem0 provider

Eval v2 persists trial state in PostgreSQL, resumes interrupted runs, and
exports CSV, JSONL, and a self-contained HTML report. The lower-level
[`v2/README.md`](v2/README.md) documents implementation details and the complete
artifact contract.

## Prerequisites

Install Docker, Go, `make`, and `jq`. Keep a local checkout of paxm
(`memory-adaptor`) because the OpenCode evaluation image builds the production
paxm plugin from source.

Create local environment and configuration files from the tracked templates:

```bash
cp .env.eval-v2.example .env.eval-v2
cp evals/v2/config.example.yaml evals/v2/config.local.yaml
```

Eval scripts load `.env` first and then `.env.eval-v2`. The second file is an
ignored local override. Do not commit either file or any credentials.

Set these required values:

| Variable | Purpose |
| --- | --- |
| `DEEPSEEK_API_KEY` | OpenCode and the DeepSeek-backed Mem0 extractor |
| `TEAM_MEMORY_EXTRACTOR_API_KEY` | Team Note extraction; it may use the same DeepSeek key |
| `MEM0_OPENAI_API_KEY` | Mem0 `text-embedding-3-small` embeddings |
| `PAXM_SOURCE_DIR` | Absolute path to the local paxm source checkout |
| `EVAL_V2_POSTGRES_DSN` | Durable Eval v2 state store; the template targets port `55433` |

The tracked profile uses DeepSeek V4 Flash for OpenCode, Team Note extraction,
and Mem0 extraction. Mem0 retains OpenAI `text-embedding-3-small` embeddings.

## Run the smoke profile

Always start with the three-case smoke profile. It covers multi-hop,
knowledge-update, and abstention behavior:

```bash
make eval-v2-smoke-up
make eval-v2-smoke
make eval-v2-down
```

`eval-v2-smoke-up` also downloads the pinned GroupMemBench Finance data and
prepares deterministic case artifacts.

## Run the default acceptance profile

The acceptance profile selects five cases from each of the six GroupMemBench
categories, for 30 cases and 120 arm trials:

```bash
make eval-v2-acceptance-up
make eval-v2-acceptance
make eval-v2-down
```

The convenience targets use fixed example run IDs. Use a unique named config
for a result that will be retained or published.

## Run a custom number of cases

Prepare a deterministic selection in its own directory. For example, prepare
50 cases with:

```bash
GROUPMEMBENCH_TOTAL_CASES=50 \
  ./scripts/eval-v2-prepare-groupmembench.sh \
  runs/groupmembench-v2-selection-50
```

Selection parameters are environment variables:

| Variable | Default | Meaning |
| --- | ---: | --- |
| `GROUPMEMBENCH_DOMAIN` | `Finance` | Dataset domain |
| `GROUPMEMBENCH_SEED` | `pax-eval-v2` | Deterministic selection seed |
| `GROUPMEMBENCH_PER_CATEGORY` | `5` | Cases per category when total cases is unset |
| `GROUPMEMBENCH_TOTAL_CASES` | `0` | Exact total case count when greater than zero |
| `GROUPMEMBENCH_TOP_K` | `8` | Core messages retrieved for each question |
| `GROUPMEMBENCH_NEIGHBOR_RADIUS` | `1` | Neighbor messages added around core messages |
| `GROUPMEMBENCH_MAX_CONTEXT_MESSAGES` | `32` | Maximum source messages per case |

Copy `evals/v2/config.example.yaml` to a new ignored config and change at least
the run ID, manifest, output directory, and desired parallelism:

```yaml
run:
  id: groupmembench-finance-50-20260715-r1
  manifest: runs/groupmembench-v2-selection-50/manifest.json
  output_dir: runs/eval-v2/groupmembench-finance-50-20260715-r1
  parallelism: 8

trial_timeout: 30m
```

The stack run ID must exactly match `run.id` in the YAML file:

```bash
make eval-v2-up \
  MANIFEST=runs/groupmembench-v2-selection-50/manifest.json \
  RUN_ID=groupmembench-finance-50-20260715-r1

make eval-v2 CONFIG=evals/v2/config.local.yaml
make eval-v2-down
```

Use a new run ID and output directory for every publishable experiment. Reusing
an unchanged run ID resumes it; reusing a run ID with a changed trial config is
rejected by the config-hash guard.

## Important run parameters

| YAML field | Purpose |
| --- | --- |
| `run.id` | Durable experiment identity and provider isolation prefix |
| `run.manifest` | Prepared case manifest |
| `run.output_dir` | Artifact and per-trial log directory |
| `run.parallelism` | Maximum concurrently claimed arm trials |
| `store.dsn_env` | Environment variable containing the PostgreSQL DSN |
| `baseline_arm` | Pairwise comparison baseline, normally `control` |
| `trial_timeout` | Total timeout for ingest, readiness, and consumer stages |
| `retry_failed` | Whether failed trials may be claimed again |
| `retry_max_attempts` | Required and at least `2` when retry is enabled |
| `runtime_env` | Non-secret version and runtime values recorded in artifacts |
| `output.formats` | Any of `csv`, `jsonl`, and `html` |

The supplied arm and preflight commands should normally remain unchanged. The
preflight performs real add and recall operations against Team Note and Mem0
before any paid trial begins.

## Input contract

The preparation script writes this ignored directory layout:

```text
runs/<selection>/
  manifest.json
  manifest.smoke.json
  cases/<case-id>/
    producer/source.md
    producer/session-batches.json
    consumer/README.md
```

The runner requires `dataset_revision` and at least one case. Each case contains
an ID, category, question, expected `answer`, `asking_user_id`, and an isolated
`scope_id`:

```json
{
  "dataset_revision": "<pinned revision>",
  "cases": [
    {
      "id": "case-1",
      "category": "multi_hop",
      "question": "What was decided?",
      "answer": "Expected answer",
      "asking_user_id": "actor-id",
      "scope_id": "groupmembench-case-1"
    }
  ]
}
```

`session-batches.json` contains native session batches. Each event preserves
its source user, agent, session, sequence, timestamp, content, visibility, and
channel/phase provenance. Both memory arms ingest the same selected events;
Mem0 receives a deterministic transcript because its API does not accept PAX
session batches directly.

Do not hand-edit generated GroupMemBench cases for a comparable run. Change the
selection parameters and regenerate them instead.

## Outputs

With four arms, an `N`-case run produces `4N` trial records. Files are written
under `run.output_dir`:

| File | Contents |
| --- | --- |
| `trials.jsonl` | Lossless record for every case and arm |
| `trials.csv` | Flattened trial identity, quality, ingest, token, cost, latency, status, and answer fields |
| `summary.csv` | Overall and per-category aggregates for every arm |
| `pairwise.csv` | Team Note and Mem0 wins, losses, ties, F1 delta, lifts, and incremental cost versus control |
| `artifacts.json` | Schema version, revision, config hash, runtime provenance, artifact paths, and cost summary |
| `stage/artifacts.json` | Optional live Team Note extraction/recall Observations and stage scores |
| `report.html` | Self-contained overall, category, and case-by-arm comparison report |
| `trials/<case>/<arm>/` | Raw command stdout and stderr for ingest, readiness, and consumer stages |

The stable artifact schema is `pax-eval-v2.8`. Current cost fields use the
`opencode_reported` scope: they include reported OpenCode calls but not Team
Note extraction/embedding or Mem0 internal model/embedding billing. Treat them
as arm-level subtotals, not the complete provider bill.

## Resume, shutdown, and cleanup

The runner pre-creates the entire case-by-arm matrix in PostgreSQL:

- completed trials are skipped on resume;
- interrupted `running` trials return to `pending`;
- failed trials are not retried unless bounded retry is enabled;
- the same run ID cannot be reused with a changed trial config.

`make eval-v2-down` stops containers but preserves Team Note, Mem0, and Eval v2
volumes. `make eval-v2-reset` deletes all three stores. Do not reset while a run
may need to be resumed or audited.

## Current 50-case readiness workaround

Team Note readiness currently has a separate 480-poll limit in addition to the
runner timeout. On the observed native 50-case workload, the known-sufficient
temporary settings were:

```text
trial_timeout: 30m
PAX_EVAL_READINESS_ATTEMPTS=1800
```

Put the environment override in `.env.eval-v2`. These values avoid false
readiness failures but do not improve extraction latency and are not proven
minimums. See the
[`continuous eval and Team Note latency plan`](../docs/superpowers/plans/2026-07-15-continuous-eval-and-team-note-latency.md)
for the known readiness and measurement issues.

## Other evaluation harnesses

- [`groupmembench/README.md`](groupmembench/README.md) describes the older
  bounded-context Team Note versus control diagnostic.
- [`opencode/README.md`](opencode/README.md) describes the two-container
  producer-to-consumer integration fixture.

Use those harnesses only for their narrower integration scenarios. Use Eval v2
for new control, Team Note, and Mem0 benchmark comparisons.
