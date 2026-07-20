# Source Span v2 plus passive-v1: no-go

Date: 2026-07-19

## Compared runs

Both runs use the same curated ten-case GroupMemBench manifest, DeepSeek v4
Flash consumer/extractor, DeepSeek v4 Pro judge, and the build-time
`passive-v1` recall candidate. They differ only in the extraction candidate.

| Metric | source-span-v1 | source-span-v2 | Change |
| --- | ---: | ---: | ---: |
| Team Note judge correctness | 2/10 | 1/10 | -1 |
| Team Note token F1 | 0.147778 | 0.183501 | +0.035723 |
| Recall context items | 13 | 29 | +16 |
| Consumer input tokens | 4,069 | 6,084 | +49.5% |
| Mean total duration | 66.9 s | 63.1 s | -5.6% |
| Source notes | 81 | 395 | +314 |
| Source-body median / P95 characters | 1,540 / 5,111 | 578 / 718 | smaller shards |
| Recall token-budget rejections | 54 | 130 | +76 |
| Recall fusion-limit rejections | 8 | 235 | +227 |

Artifacts:

- v1: `runs/eval-v2/groupmembench-finance-v2-source-span-v1-passive10-20260719-r4`
- v2: `runs/eval-v2/groupmembench-finance-v2-source-span-v2-passive10-20260719-r1`
- v2 resolved configuration records `source-span-v2` for both the runtime and
  build extraction strategy, and `passive-v1` for the build recall strategy.

## Findings

`source-span-v2` achieved its mechanical extraction target: raw source is
retained in bounded deterministic shards, and the long-span body distribution
is no longer individually larger than the recall budget.

It did not achieve the product target. The sharded representation expanded the
candidate pool enough that generic retrieval rejected 235 fragments at fusion
and 130 more at the shared token budget. The selected context often contained
question-like or adjacent fragments rather than the fragment carrying the
final state. For `knowledge_update_23`, the recall plan selected a late
unresolved-discussion fragment and rejected the concrete baseline-update
fragment at budget, producing a generic governance answer rather than the gold
requirement update.

The single remaining correct Team Note answer is an abstention case. No
positive knowledge-update, multi-hop, temporal, term-ambiguity, or
user-implicit case became judge-correct.

## Decision

**No-go: stop investing in the source-span extraction line.** Do not promote
`source-span-v2` as a default. The observed failure is not missing raw source
retention or a source-span body-size problem; it is generic retrieval and
answer grounding over many adjacent source fragments. Address that only as a
separately scoped recall/answer-policy hypothesis with a fixed replay fixture,
not by adding more source-span extraction variants.
