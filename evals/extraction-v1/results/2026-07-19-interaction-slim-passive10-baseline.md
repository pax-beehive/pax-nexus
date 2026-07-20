# Interaction Slim plus passive-v1: end-to-end baseline

Date: 2026-07-19

## Configuration

This is the missing end-to-end baseline for the curated GroupMemBench finance
ten-case cohort used by the source-span experiments. It uses DeepSeek v4 Flash
for the consumer and extractor, DeepSeek v4 Pro with high thinking for judging,
rolling extraction v2, the build-time `passive-v1` recall candidate, and
`interaction-slim` for both the build and runtime extraction candidate.

The resolved configuration is persisted in:

`runs/eval-v2/groupmembench-finance-v2-interaction-slim-passive10-20260719-r1/config.resolved.json`

## Result

| Extraction candidate | Team Note judge correctness | Control judge correctness | Pairwise wins/losses/ties | Mean token F1 | Consumer input tokens | Mean total duration |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| interaction-slim | 3/10 | 1/10 | 2 / 0 / 8 | 0.131618 | 1,544 | 228.0 s |
| source-span-v1 | 2/10 | 2/10 | 0 / 0 / 10 | 0.147778 | 4,069 | 66.9 s |
| source-span-v2 | 1/10 | 2/10 | n/a | 0.183501 | 6,084 | 63.1 s |

Artifacts:

- interaction-slim:
  `runs/eval-v2/groupmembench-finance-v2-interaction-slim-passive10-20260719-r1`
- source-span-v1:
  `runs/eval-v2/groupmembench-finance-v2-source-span-v1-passive10-20260719-r4`
- source-span-v2:
  `runs/eval-v2/groupmembench-finance-v2-source-span-v2-passive10-20260719-r1`

`interaction-slim` was judge-correct for both abstention cases and
`multi_hop_15`. It did not produce a correct knowledge-update, temporal,
term-ambiguity, or user-implicit answer.

## Decision

`interaction-slim + passive-v1` is the observed best extraction/recall
combination on this ten-case cohort by Team Note judge correctness. This fills
the previously missing baseline, so the source-span no-go now has the required
product-level comparison: source-span-v1 and source-span-v2 both underperform
the baseline.

The result is a single externally evaluated replay cohort, not a promotion
decision. Its 228-second mean total duration includes extraction retries after
extractor response deadlines and is materially worse than both source-span
runs. Treat latency/retry behavior as a separate extraction reliability issue
before changing the default strategy.
