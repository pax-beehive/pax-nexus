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

## Strategy trial: evidence-aware reranking — 2026-07-18

Implementation: `general-recall-v3` scoring version
`evidence-scorecard-v2-rerank-uncalibrated`. Candidates with stronger
observable evidence confidence are now preferred before lexical-fit tie-breaks;
hard gates, candidate limits, relation expansion, and budget packing are
unchanged.

The same 50-replay baseline was rerun. No quality metric changed:

| Metric | Baseline | Evidence rerank |
| --- | ---: | ---: |
| Candidate recall@limit | 1.000 | 1.000 |
| Delivered eligible recall | 1.000 | 1.000 |
| Relation-expanded recall | 1.000 | 1.000 |
| Selected-set recall | 1.000 | 1.000 |
| Mean context precision | 0.134 | 0.134 |
| Planner p95 | 2.56 ms | 2.92 ms |
| Superseded leakage | 0 | 0 |
| Budget drops | 0 | 0 |

Interpretation: the change is behavior-preserving on this repeated cohort and
does not yet justify adopting evidence confidence as a stronger global ranking
feature. The next useful experiment is an independent cohort with deliberately
conflicting lexical similarity, evidence strength, source recency, and
supersession so the reranker has a measurable decision boundary.

## Strategy trial: evidence-aware recency tie-break — 2026-07-18

The reranker now uses `SourceOccurredAt` as a deterministic tie-break after
evidence confidence and before lexical fallback. Temporal hard-gates remain
unchanged, so a newer note cannot bypass an `as_of` or `current` validity rule.

The same 50-replay baseline was rerun:

| Metric | Evidence rerank | Evidence + recency |
| --- | ---: | ---: |
| Candidate recall@limit | 1.000 | 1.000 |
| Delivered eligible recall | 1.000 | 1.000 |
| Relation-expanded recall | 1.000 | 1.000 |
| Selected-set recall | 1.000 | 1.000 |
| Mean context precision | 0.134 | 0.134 |
| Planner p95 | 2.92 ms | 2.45 ms |
| Superseded leakage | 0 | 0 |
| Budget drops | 0 | 0 |

This remains behavior-preserving for quality on the repeated cohort. It is a
safe tie-break and slightly faster in this run, but should not be treated as a
quality improvement until an independent cohort contains conflicting source
recency and lexical/evidence scores.

## Strategy trial: relation expansion marginal utility — 2026-07-18

Fixture: `evals/stage/replay/relation-marginal-utility-v1.json`. This is a
curated four-case contrast cohort, not repeated cases. It covers status across
services, filing schedules across regions, decisions across components, and
blockers across workstreams. Every case contains one relation that adds an
uncovered query term or fact and one adjacent/repetitive relation.

The policy incrementally retains query-relevant one-hop relations only when
their still-uncovered fact/query/entity-slot gain per token is at least `0.02`.
Coverage is updated after every retained relation, so later relations cannot
claim the same gain. Authorization, temporal,
provenance, and content-safety gates remain mandatory. Queries without an
explicit fact slot keep the legacy exploratory relation behavior.

| Metric | Legacy relation packing | Marginal utility |
| --- | ---: | ---: |
| Independent cases | 4 | 4 |
| Candidate lane limit | 16 | 16 |
| Candidate recall@limit | 0.500 | 0.500 |
| Relation-expanded recall | 1.000 | 1.000 |
| Delivered conditional recall | 1.000 | 1.000 |
| Delivered eligible recall | 1.000 | 1.000 |
| Mean context precision | 1.000 | 1.000 |
| Planned tokens | 269 | 162 |
| Marginal-utility drops | 0 | 4 |
| Superseded leakage | 0 | 0 |
| Budget drops | 0 | 0 |
| End-to-end judge accuracy | N/A | N/A |

The strategy preserved all eight Eligible Atoms while reducing planned tokens
by 39.8%. Every curated case kept its useful relation and rejected its
repetitive relation. End-to-end judge accuracy is not scored by deterministic
replay and requires the paid consumer-Agent cohort. The tracked ten-case
regression also retained candidate, relation-expanded, and delivered
conditional recall at `1.000`, with zero missed Available Atoms.

Per-case decisions were exact: `status-useful`, `deadline-useful`,
`decision-useful`, and `blocker-useful` were selected; their corresponding
`*-noise` relations were rejected with `relation_marginal_utility`. The trace
records each rejection by both primary and related Note ID.
