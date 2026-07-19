# Recall Eval v2

Recall Eval v2 first runs the fixed 30-case deterministic planner gate, then
launches paired isolated external Agents for `no_memory` and production
`team_note` recall. A Team Note Trial without a unique Agent session, observed
successful recall hook, provider call, and judge session is unscored and fails
the run.

Prepare the full-domain 30-case GroupMemBench manifest, create local config,
and run the stack:

```bash
make eval-v3-prepare
cp evals/recall-v2/config.example.yaml evals/recall-v2/config.local.yaml
make recall-eval-v2-up
make recall-eval-v2
make recall-eval-v2-down
```

The deterministic fixture is a synthetic planner stress control derived from
30 independent GroupMemBench questions. It is not an end-to-end benchmark
result. Regenerate it only when intentionally revising the fixed cohort:

```bash
go run ./cmd/recall-eval-v2-fixtures \
  -manifest runs/groupmembench-v2-selection/manifest.json \
  -output evals/recall-v2/groupmembench-finance-hard30-stage-v1.json \
  -replay-output evals/recall-v2/groupmembench-finance-hard34-replay-v1.json \
  -relation-fixtures evals/stage/replay/relation-marginal-utility-v1.json
```

The run writes the deterministic summary, cohort identity/source coverage,
normal Eval artifacts, and `recall-agent-report.json` under the configured
output directory. The latter records a scored or unscored disposition for each
Agent Trial rather than converting missing recall evidence into answer failure.
