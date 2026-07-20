# BM25 baseline results

## Scope

`note-bm25-v1` isolates candidate retrieval on the fixed Team Note replay
fixture. It replaces only the adapter-provided lexical scores with deterministic
BM25 over `subject + body`; identity, temporal gates, relation expansion,
selection, and token-budget packing remain behind `PlanRecall`.

`raw-bm25-v1` is a separate end-to-end baseline. It ranks original
`team_note_eligible` session events, applies an optional explicit temporal
cutoff, and provides the packed raw context directly to the answer agent. It
does not ingest or extract Team Notes.

## Fixed replay: note BM25

Fixture: `evals/recall-v2/groupmembench-finance-hard34-replay-v1.json`.
Both arms use candidate limit 16, token budget from the fixture, and semantic
threshold 0.65. Artifacts are retained under:

- `runs/recall-v2/groupmembench-finance-note-adapter-v1-20260719-r1`
- `runs/recall-v2/groupmembench-finance-note-bm25-v1-20260719-r1`

| Metric | Adapter candidate source | note-bm25-v1 |
| --- | ---: | ---: |
| Candidate recall at lane limit | 0.879 | 1.000 |
| Delivered conditional recall | 0.909 (30/33) | 0.909 (30/33) |
| Gold recall | 0.909 | 0.909 |
| Mean context precision | 0.603 | 0.574 |
| Budget-dropped atoms | 1 | 1 |

## Decision

BM25 improves candidate availability but does not improve delivered answer
evidence on this fixed cohort, while adding more selected material and reducing
context precision. Keep the current adapter candidate source as the default.
Use `note-bm25-v1` as a regression baseline for future candidate-source work,
not as a production recall strategy.

## Raw baseline protocol

`evals/v2/config.interaction-slim-passive10-raw-bm25.local.yaml` pairs
`control` with `raw_bm25` on the selected 10-case GroupMemBench manifest.
The raw arm is configured with single-event documents, BM25 top 8, and a
500-token budget. Each trial writes `raw-bm25.json`, including the complete
ranked candidate trace and a rejection reason (`candidate_limit` or
`token_budget`) for every non-delivered raw event chunk.

The raw and Team Note arms must be compared only by end-to-end judge accuracy
on this same manifest. The current static source fixture has no per-query
historical cutoff, so raw BM25's temporal cutoff is available as a protocol
parameter but is intentionally unset for this cohort; it must not be treated
as a temporal-recall validation.

## Raw BM25 agent smoke

Run: `runs/eval-v2/groupmembench-finance-v2-raw-bm25-smoke1-20260719-r1`.
The paired single-case smoke (`knowledge_update_5`) completed with both arms
judged incorrect: control `0/1`, raw BM25 `0/1`.

The raw trace is diagnostic rather than ambiguous: all 32 source events were
eligible, the answer-bearing event `Msg_3687` ranked sixth, and its rejection
was `token_budget`. The arm selected ranks 1--5 and dropped ranks 6--8 under
the 500-token budget. Thus this smoke validates the real agent path and
evidence trace, but it is not evidence that raw BM25 is competitive with Team
Notes. Do not run the 10-case cohort until a pre-registered packing variant is
selected; silently widening the budget after this one case would invalidate
the comparison.
