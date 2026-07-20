# Recall Eval v2

Recall Eval v2 first runs the fixed hard replay and the curated 12-case Hint
Recall intervention gate, then launches isolated external Agents for
`no_memory`, production `team_note`, and `hint_recall_v0`. A Team Note Trial without a unique Agent session, observed
successful recall hook, provider call, and judge session is unscored and fails
the run. A Hint Trial additionally requires active-recall instrumentation from
the same Agent container.

Prepare the curated 30-case GroupMemBench manifest, create local config, and
run the stack:

```bash
make eval-v3-prepare
cp evals/recall-v2/config.example.yaml evals/recall-v2/config.local.yaml
make recall-eval-v2-up
make recall-eval-v2
make recall-eval-v2-down
```

The checked-in config runs the Hint activation cohort against fixed persisted
Team Notes. Its before-run seeder records provenance through the normal
PostgreSQL NoteStore and keeps extraction disabled, so the comparison isolates
recall. It pins observation time, uses Eval-only durable leases, and records
the canonical observation, manifest, and annotation digests in the linked
seed receipt. The fixed notes are derived from gold answers; judge accuracy from this
run validates Agent activation and answer effect, not raw GroupMemBench quality.
Use a separate full-domain configuration for extraction-plus-recall quality.

The deterministic fixture is a synthetic planner stress control derived from
30 independent GroupMemBench questions. It is not an end-to-end benchmark
result. Regenerate it only when intentionally revising the fixed cohort:

```bash
go run ./cmd/recall-eval-v2-fixtures \
  -manifest runs/groupmembench-v2-selection/manifest.json \
  -output evals/recall-v2/groupmembench-finance-hard30-stage-v1.json \
  -replay-output evals/recall-v2/groupmembench-finance-hard34-replay-v1.json \
  -hint-replay-output evals/recall-v2/groupmembench-finance-hint12-replay-v1.json \
  -relation-fixtures evals/stage/replay/relation-marginal-utility-v1.json
```

The run writes the deterministic summary, cohort identity/source coverage,
linked `before-run.log` seed receipt, normal Eval artifacts, and
`recall-agent-report.json` under the configured
output directory. The latter records a scored or unscored disposition for each
Agent Trial rather than converting missing recall evidence into answer failure.

The Hint candidate is evaluation-only and defaults off in production. The
dedicated Eval service builds `hint-v1-selective`; the ordinary `team_note` Arm
builds `passive-v1`. `hint-v1-selective` contains the same passive evidence
path and allows at most one focused active recall after its selectivity gates
pass.
