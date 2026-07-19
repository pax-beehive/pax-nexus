# Recall Eval v1 Results

This file is the running record for deterministic `recall-eval-v1` replay
results. Each entry must pin the fixture, policy, code revision, and whether
the cohort contains independent cases.

## Baseline: 2026-07-18

Command:

```bash
go run ./cmd/team-memory-recall-replay \
  -fixtures runs/recall-eval-v1/baseline-50/fixture.json \
  -semantic-threshold 0.50 \
  -candidate-limit 16 \
  -dedup -degrade-related \
  -output-dir runs/recall-eval-v1/baseline-50/results
```

The tracked development fixture contains 10 independent cases. This baseline
repeated each case five times with unique IDs, producing 50 deterministic
replays. It is useful for pipeline and latency baselines, but must not be
reported as a 50-case independent benchmark.

| Metric | Result |
| --- | ---: |
| Cases | 50 replays / 10 independent cases |
| Available atoms | 40 |
| Eligible atoms | 40 |
| Candidate recall@limit | 1.000 |
| Delivered conditional recall | 1.000 |
| Delivered eligible recall | 1.000 |
| Relation-expanded recall | 1.000 |
| Selected-set recall | 1.000 |
| Mean context precision | 0.134 |
| Planner mean | 1.47 ms |
| Planner p95 | 2.56 ms |
| Superseded leakage | 0 |
| Budget drops | 0 |

Artifacts:

- `runs/recall-eval-v1/baseline-50/fixture.json`
- `runs/recall-eval-v1/baseline-50/results/replay-summary.json`
- `runs/recall-eval-v1/baseline-50/results/replay-results.jsonl`
- `runs/recall-eval-v1/baseline-50/results/recall-loss-ledger.jsonl`

## Comparison protocol

Future strategies should be replayed against the same independent fixture and
compared on candidate recall, delivered eligible recall, context precision,
budget drops, superseded leakage, identity/temporal safety, and planner p95.
Do not accept a strategy because Token F1 or one aggregate recall number rises
alone. A strategy that improves recall while increasing unauthorized,
future-dated, superseded, or budget leakage is a regression.

## Candidate strategy backlog

The current baseline has perfect candidate and delivery recall on this small
cohort, while context precision is only `0.134`. The first optimization target
should therefore be precision and ranking quality, not wider retrieval.

1. **Evidence-aware reranking** — use extraction evidence strength,
   Knowledge Origin, source recency, and query/atom support to reorder the
   existing candidate set. Keep candidate limits fixed and measure whether
   precision improves without reducing eligible recall.
2. **Temporal intent lane** — classify explicit `before`, `after`, `as of`, and
   latest/current queries, then apply the existing temporal hard gate before
   ranking. This is the highest-value safety strategy once temporal fixtures
   are expanded.
3. **Relation expansion with marginal utility** — retain one-hop relations,
   but expand only when the related note adds an uncovered eligible atom per
   token cost. Compare relation recall, context precision, and token drops.
4. **Consumer-aware lane weighting** — tune exact-scope, lexical, semantic,
   relation, and coordination lane weights using fixed replay cohorts. Do not
   change the global semantic threshold per case.
5. **Adaptive budget packing** — replace the current greedy packing with a
   value-per-token selector while preserving safety and relation constraints.
   This is worthwhile only after ranking gains are measured.
6. **Hint-recall arm** — add production hint disposition, focused query,
   score, lead, and agent activation telemetry before claiming an agent-side
   strategy win. The captured Hint Opportunity scorer is ready to consume that
   evidence but cannot substitute for it.

The next recommended implementation is evidence-aware reranking, evaluated on
an expanded independent cohort with the same identity and temporal controls.
