# Evidence Fidelity v1 micro3 canary r1

Date: 2026-07-19 (America/Los_Angeles)

Run ID:
`groupmembench-finance-micro3-evidence-fidelity-v1-20260719-r1`

Baseline:
`groupmembench-finance-micro3-v2-canary-20260718-r1-interaction-slim`

Both runs use the `finance-micro3-quick` profile and source run
`groupmembench-finance-v3-micro6-general-recall-v3-20260717-r3`. The new run
selected 65 of 1,605 source Events across four streams and five extraction
slices. Its zero-cost preflight passed before any provider call.

## Result

| Metric | interaction-slim baseline | evidence-fidelity-v1 | Change |
| --- | ---: | ---: | ---: |
| Matched atoms | 2/3 | 2/3 | 0 |
| Fact recall | 0.667 | 0.667 | 0 |
| Leakage items | 1 | 2 | +1 |
| Primary provider calls | 5 | 5 | 0 |
| Additional summary calls | 0 | 1 | +1 |
| Primary input tokens | 79,525 | 85,596 | +7.6% |
| Primary output tokens | 35,170 | 34,751 | -1.2% |
| Primary mean duration | 71.6 s | 57.4 s | -19.8% |
| Primary P95 duration | 102.9 s | 70.4 s | -31.6% |
| Cache hit rate | 0.681 | 0.688 | +0.7 points |
| State decisions | 21 | 23 | +2 |
| Decision rejections | 8 | 16 | +8 |
| Unreviewed Events | 6 | 13 | +7 |

The additional summary call consumed 28,999 input and 1,734 output tokens.
Across all physical provider calls, the new run consumed 114,595 input and
36,485 output tokens, versus 79,525 and 35,170 in the older baseline. The
baseline predates some current background-summary behavior, so total physical
call cost is diagnostic rather than a strictly paired strategy comparison.

## Case findings

- `knowledge_update_20` retained both required atoms, matching the baseline.
- `multi_hop_1` still missed `reporting_validation_deadline`; the source remains
  non-committal leaning language under the current admission contract.
- `abstention_4` produced two leakage matches. One is the known negation-aware
  suppressed item: `Compliance is not the owner.` The new unsuppressed leak is
  `Compliance owns the exception log for weaker providers.`

The unsuppressed ownership Candidate cites a broad set of adjacent Events. The
fidelity prompt preserved a concrete owner and object, but the Candidate did
not remain atomic to one source clause. Mixing proposal and committed-looking
evidence let the aggregate Candidate bypass the guard that rejects evidence
sets consisting only of non-committal source language.

## Decision

Do not retain or promote `evidence-fidelity-v1` from this run. It did not improve
fact recall, increased leakage, doubled deterministic decision rejections, and
more than doubled unreviewed Events. The primary latency signal improved, but
quality gates take precedence and the additional summary call makes total-call
cost non-paired with the older artifact.

Do not spend two more seeds on the unchanged prompt. The next fidelity revision
must make each Candidate source-clause atomic and prevent a broad evidence set
from laundering proposal-only ownership into committed state. It must keep the
semantic Candidate schema and pass the same zero-cost preflight and micro3 gate
before returning to the planned three-seed micro6 comparison.
