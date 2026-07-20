# Finance micro6 extraction v2 r1

Date: 2026-07-17 (America/Los_Angeles)

Run ID: `groupmembench-finance-micro6-extraction-v2-20260717-r1`

Source run ID:
`groupmembench-finance-v3-micro6-general-recall-v3-20260717-r3`

Artifact:
`runs/extraction-eval-v1/groupmembench-finance-micro6-extraction-v2-20260717-r1/report.json`

Extractor: v2 / `deepseek-v4-flash` / prompt `v2`

## Raw result

| Metric | Result |
|---|---:|
| Cases | 6 |
| Persisted events | 1,605 across 15 streams |
| slices / recorded primary calls | 74 / 74 |
| Call errors | 0 |
| Admitted Team Notes | 61 |
| Required atoms matched | 1 / 6 |
| Raw fact recall | 0.167 |
| Raw leakage matches | 3 |
| Input tokens | 3,576,015 |
| Output tokens | 416,021 |
| Cache hit / miss tokens | 247,040 / 3,328,975 |
| Token-weighted cache hit rate | 6.91% |
| Mean / P95 duration | 42.8 s / 100.9 s |
| Total model-call duration | 3,169.6 s (52.8 min) |
| Estimated model cost | approximately US$0.58 |

Trace totals were 88 admitted State Decisions, 122 Claim rejections, 109
Decision rejections, 1,593 Interaction rejections, 614 `no_state` session events, and
801 unreviewed session events. Almost half of the input session events were therefore not
accounted for by admitted evidence or an explicit `no_state` classification.

## Validity limitation

The raw six-case recall is not an acceptance result. The persisted source used
by this run contains only these three Finance phases:

- Risk: Calculation Discrepancy;
- Risk: ESG Data Provider Reliability;
- Risk: Inconsistent Sustainability Standards.

Those phases support `multi_hop_1`, `knowledge_update_20`, and
`abstention_4`. The supporting source phases and session events for `temporal_7`,
`user_implicit_7`, and `term_ambiguity_11` are absent. Those three misses are
input-cohort construction failures, not observations of extractor recall.

Among the three supported cases, `multi_hop_1` matched. The trace exposed a
deterministic false rejection in `knowledge_update_20` and proposal leakage in
`abstention_4`; the run is therefore still a quality-gate failure even after
excluding the unsupported cases.

## Confirmed defects

1. The model produced the required `knowledge_update_20` decisions, including
   the refreshed currency-feed check and impacted-close-cycle rerun, but set
   `temporal_resolution` to `unresolved` without a source temporal expression
   or validity timestamp. Deterministic admission rejected the otherwise
   grounded decisions as temporal metadata without a source expression.
2. Proposal and request language such as asking Compliance to own the
   exception log was promoted into current ownership state. The model also
   omitted the interaction actor, so interaction validation discarded the
   observation before the speech-act guard could use it.
3. One of the three raw leakage matches was a cross-topic scorer match rather
   than a clean ownership leak. Raw leakage count should therefore be inspected
   item by item until the forbidden-atom matcher is tightened.

The first two defects now have deterministic regression tests. The admission
fix normalizes only an otherwise empty `unresolved` resolution and inspects the
source evidence directly for non-committal proposal or request language. This
artifact predates those fixes and must not be relabeled as a post-fix result.

## Decision

Do not use 0.167 as the Extraction v2 quality baseline and do not rerun the
paid cohort against the same incomplete source. Regenerate a source scope that
contains all six fixture phases, preflight supporting session event coverage before
the first model call, and then run a paired control/treatment comparison.

The valid performance baseline from this run is 74 serial primary calls, 52.8
minutes of recorded extraction-call time, approximately US$0.58, 6.91% cache
reuse, and 416,021 output tokens. Periodic summary calls run inside the
extractor and are not counted independently by the current harness; their
usage is folded into a later primary slice only when the asynchronous result
is consumed. Actual provider-call count is therefore unknown and the reported
cost can slightly undercount a final unconsumed summary. The next harness
revision must classify primary and summary calls separately. The observed
values are sufficiently poor that the next protocol optimization should first
reduce response volume and primary-call count before adding concurrency.
