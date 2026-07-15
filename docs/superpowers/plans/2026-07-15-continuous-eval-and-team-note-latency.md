# Continuous eval and Team Note latency plan

Status: proposed; automation disabled
Date: 2026-07-15

## Goals

1. Make memory-quality regressions visible with a small, repeatable eval run.
2. Attribute latency and cost to the component that caused them.
3. Reduce Team Note write-to-ready latency without weakening answer quality,
   actor ordering, or provenance.

This document records a future operating plan. It does not enable a recurring
job. The previously considered hourly automation is currently deleted and must
only be recreated after explicit approval.

New GroupMemBench runs use native multi-agent session ingestion and therefore
have no shared producer-model stage. The shared-producer measurements below are
historical baseline data from `r2`, not the target measurement contract.

## Proposed continuous eval

Run the same pinned 10-case cohort across `control`, `mem0`, and `team_note`.
That produces 30 arm trials per run. Use a unique run ID, record the repository
revision and dependency versions, and reject overlapping runs with a lock.

The initial cadence may be hourly, but activation is gated on the latency and
cost instrumentation below. Each run should compare itself with the most recent
compatible successful run and report:

- pass count and token F1 by arm, category, and case;
- regressions and recoveries for individual cases;
- producer, write-to-ready, recall, and consumer latency distributions;
- OpenCode, Team Note extraction, embedding, and Mem0 token usage and cost;
- configuration and version provenance sufficient to reproduce the result.

Known direct OpenCode cost for the current 10-case cohort is approximately
`$0.034` per run, `$0.82` per day, or `$24.50` per 30-day month at an hourly
cadence. Team Note extraction/embedding and the automation agent are additional
costs, so the initial monthly budget guardrail should be `$30-$40`, with the
automation agent cost reported separately rather than guessed.

Do not delete raw runs until a retention policy has been reviewed. A later
proposal may retain all recent runs plus daily and weekly checkpoints, but it
must not silently remove evidence.

## Current Team Note latency baseline

The selected 10-case run
`groupmembench-finance-v2-team-note-poor10-20260715-r2` produced these means:

| Stage | Team Note | Mem0 | Team Note delta |
| --- | ---: | ---: | ---: |
| Shared producer, nominal | 46.25 s | 46.25 s | 0 s |
| Write-to-ready | 34.11 s | 7.66 s | +26.45 s |
| Consumer | 27.84 s | 23.28 s | +4.56 s |
| Total arm wall time | 108.21 s | 63.98 s | +44.23 s |

The total-arm comparison overstates the product difference by about 13.2
seconds. Both memory arms share one producer through synchronized execution,
but each arm's total timer starts when its worker claims the trial. Scheduling
therefore assigns different amounts of the shared producer wait to each arm.
Future reports must not interpret that difference as Team Note latency.

Team Note recall itself is not the bottleneck. Across these cases, the recall
hook averaged 35.7 ms, with a 9 ms minimum and a 149 ms maximum. Session
observation also took only tens of milliseconds.

## Root-cause breakdown

The dominant cost occurs before recall, while Team Note converts an observed
session into durable structured notes:

1. The extraction LLM took 18.8-35.6 seconds per case, averaging about 26.9
   seconds. It often generated 10 candidates and roughly 3,000-5,400 output
   tokens.
2. Queue and scheduler delay averaged about 5.6 seconds. Most jobs waited 2-5
   seconds, but one waited 27.9 seconds because two cases hashed to the same
   extraction shard.
3. Each extraction shard has `MaxWorkers: 1` to preserve ordering. The eval used
   four shards with five-way parallelism, so unrelated sessions can collide and
   experience head-of-line blocking.
4. The Team Note consumer took about 4.6 seconds longer than the Mem0 consumer,
   consistent with a larger or richer injected context. This requires token
   telemetry before it is treated as confirmed causality.
5. Database admission, session observation, and recall are millisecond-scale
   and are not meaningful contributors to the current gap.

## Implementation order

### 1. Correct the measurement contract

- Report a shared producer only for legacy runs; native runs report source-event
  count and ingest time instead.
- Split Team Note readiness into enqueue delay, scheduler delay, queue wait,
  extraction LLM time, persistence/admission time, and cursor visibility.
- Report recall-hook latency separately from consumer-model latency.
- Add per-layer input/output tokens and attributed cost; preserve `unknown`
  when a provider does not expose a value.

### 2. Reduce extraction generation

- Tighten the extraction response schema and prompt.
- Cap candidate count and output tokens for the eval/MVP profile.
- Avoid verbose candidate rationale that is not stored or scored.
- Compare the change on the pinned cohort and reject it if answer quality,
  provenance, or absence behavior regresses.

### 3. Reduce safe queue delay

- Preserve same-actor serialization; do not raise per-shard concurrency without
  proving that extraction order remains correct.
- Evaluate more shards for eval workloads to reduce collision probability.
- Record collision and queue-depth metrics so the benefit is measurable.
- Replace one-second readiness polling with explicit job/cursor completion
  signaling only if it materially improves end-to-end latency.

### 4. Bound recall context

- Measure injected Team Note tokens per case.
- Rank and cap recalled notes before constructing the consumer prompt.
- Verify that latency improves without losing multi-hop or temporal evidence.

### 5. Validate before enabling automation

Run the pinned 10 cases before and after each change, generate the HTML report,
and compare both quality and stage-level latency. Enable an hourly job only
after the measurement contract and cost guardrails are present.

## Acceptance criteria

- Team Note recall p95 remains below 250 ms.
- Mean write-to-ready latency falls below 20 seconds and p95 below 30 seconds
  on the pinned 10-case cohort.
- No regression in Team Note pass count, absence handling, token F1, provenance,
  or actor-local ordering.
- Reports distinguish legacy shared producer time from arm-specific latency and
  report native source event/actor/session counts.
- Reports include Team Note extraction and embedding usage/cost, and clearly
  mark unavailable Mem0 usage instead of estimating it.
- Recurring runs cannot overlap, stop when the monthly budget guardrail is hit,
  retain reproducible artifacts, and remain disabled until explicitly approved.
