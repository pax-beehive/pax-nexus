# Eval v2

Eval v2 is a resumable, configuration-driven experiment runner for long-lived
workstation evaluation. It executes the same cases through multiple arms and
persists every trial in PostgreSQL before exporting renderer-neutral artifacts.

The included configuration compares:

- `control`: OpenCode without passive recall or writes
- `team_note`: OpenCode using the PAX Team Note provider
- `mem0`: OpenCode using a self-hosted Mem0 OSS REST server

Every arm receives the same `asking_user_id`. Mem0 additionally receives a
case-specific `run_id`, which prevents cross-case contamination without changing
the user identity under comparison. Producer and consumer agent IDs remain
distinct so provenance is observable.

## Prepare a run

Copy `.env.example` to `.env` and replace every required placeholder. Mem0's
default self-hosted image needs an embedding-capable provider key in addition to
the DeepSeek key used by OpenCode and Team Note extraction.

Prepare a deterministic 30-case GroupMemBench selection:

```bash
make eval-v2-prepare
cp evals/v2/config.example.yaml evals/v2/config.yaml
```

Set a unique `run.id`, and make `run.manifest` point at the generated manifest.
The run ID is durable: rerunning the same config resumes completed work, while
reusing the ID with a changed config is rejected.

Start the evaluation stack and run the matrix:

```bash
make eval-v2-up \
  MANIFEST=runs/groupmembench-v2-selection/manifest.json \
  RUN_ID=groupmembench-finance-v2-example
make eval-v2 CONFIG=evals/v2/config.yaml
make eval-v2-down
```

`eval-v2-down` keeps PostgreSQL volumes. Use `make eval-v2-reset` only when the
stored Team Note, Mem0, and eval state should all be destroyed.
`RUN_ID` must exactly match `run.id` in the YAML configuration. Both Team Note
and Mem0 scopes include this value, so persistent volumes cannot leak memories
from an older run into a new comparison.

The Mem0 container is configured with `AUTH_DISABLED=true` only on the private
evaluation network. Do not reuse this compose file as a production deployment.
Pin `MEM0_IMAGE` to a tested release or digest for publishable benchmark runs.

## Resume behavior

The runner pre-creates the complete case-by-arm matrix. A trial moves through
`pending`, `running`, and either `completed` or `failed`. On restart, interrupted
`running` trials return to `pending`; completed trials are skipped. When
`retry_failed` is enabled, failed trials are attempted again.

Each trial has one total timeout covering producer, memory readiness, and
consumer stages. Command stdout and stderr are retained under
`<output_dir>/trials/<case>/<arm>/`.

## Output contract

Eval v2 deliberately does not generate an HTML dashboard. `output.formats`
selects `csv`, `jsonl`, or both. Every completed run can export:

- `trials.csv`: one row per case and arm, including identity, quality, tokens,
  producer/consumer cost, total observed cost, stage latency, status, and error
- `trials.jsonl`: lossless trial records for downstream processing
- `summary.csv`: overall and per-category arm aggregates
- `pairwise.csv`: candidate versus control wins, losses, ties, F1 delta, exact
  lift, safe-success lift, and incremental cost over paired completed trials
- `artifacts.json`: schema version, dataset revision, config hash, and artifact
  paths, plus the non-secret runtime versions named by `runtime_env`

The stable artifact schema is `pax-eval-v2.2`. A workstation renderer should
read `artifacts.json`, then generate PNG, SVG, or PDF charts from the declared
CSV files. Suggested views are category heatmaps, paired F1 deltas, latency-cost
scatter plots, failure rates, and cumulative progress over time.

Cost fields use the `opencode_reported` scope. They include all producer and
consumer costs reported by OpenCode, including producer cost incurred before a
later trial failure. `artifacts.json.cost_summary` exposes total run cost,
completed versus failed cost, token totals, and per-arm totals. Team Note
extraction calls and Mem0's internal model/embedder calls are not yet included
because those backends do not expose one common billing contract; the output
names its scope explicitly so this subtotal is not mistaken for full-stack
provider spend.

Exact match and token F1 remain diagnostic metrics rather than an official
GroupMemBench score. A semantic-equivalence judge and evidence-coverage labels
are the next scoring extensions; the raw answer and expected answer are retained
so those metrics can be populated without rerunning agent trials.
