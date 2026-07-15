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

## Current native 50-case final-record snapshot

The completed run
`groupmembench-finance-v2-shared-mem0-50-20260715-r9` is the current native
multi-agent result snapshot. It completed all 150 arm trials over 50 cases. The
selected inputs contained 1,467 source events, 295 actor appearances, and 391
native sessions.

| Stage | Team Note | Mem0 | Team Note delta |
| --- | ---: | ---: | ---: |
| Write-to-ready | 277.67 s | 14.12 s | +263.55 s |
| Consumer | 17.63 s | 22.13 s | -4.50 s |
| Total arm trial | 295.31 s | 36.26 s | +259.05 s |

These are means over the final successful records, not a clean first-pass
latency baseline. The five retried records replaced their original failed
attempts and completed readiness in about one second after background
extraction had already progressed. The reported 277.67-second mean therefore
understates first-pass readiness, which cannot be reconstructed from the final
artifacts. Even with that bias, Team Note readiness consumed about 94 percent of
its mean trial time. Its consumer was faster than the Mem0 consumer, so recall
and answer generation do not explain the end-to-end gap.

Operator observation recorded about 46 minutes of wall time, eight runner
workers, a 30-minute trial timeout, a temporary 1,800-poll readiness allowance,
and four single-worker extraction shards. The retained artifacts do not capture
these settings, so they are operational evidence rather than independently
reproducible run provenance.

The final report completed 150 of 150 trials with no failed results. During the
first pass, five Team Note trials incorrectly failed because the readiness
script stopped after 480 one-second polls even though extraction was still
progressing. Raising the local readiness allowance to 1,800 polls and retrying
only those five trials completed all five. This is evidence of a timeout
contract bug, not evidence that a longer timeout fixes latency.

## Known readiness issues

| ID | Severity | Known problem | Evidence from `r9` | Required fix |
| --- | --- | --- | --- | --- |
| `TN-RDY-001` | P0 | Native session fan-out creates one extraction barrier per case, and readiness waits for every stream, including low-value and noisy sessions. | 50 cases expanded into 391 sessions; mean Team Note readiness was 277.67 s. | Batch extraction work without losing actor, session, channel, phase, timestamp, or ordering boundaries. Avoid making the case barrier wait on work that cannot affect durable notes. |
| `TN-RDY-002` | P0 | The runner trial timeout and readiness poll limit are independent clocks. | A 30-minute `trial_timeout` did not prevent five failures at the hidden 480-second readiness limit. | Define one deadline contract. Derive readiness from the trial context or an explicit duration in the run config; remove the independent attempt-count timeout. |
| `TN-RDY-003` | P0 | Four single-worker extraction shards cause head-of-line blocking between unrelated sessions. | All runner slots could remain occupied while four extraction workers processed 391 session jobs; individual jobs observed in logs took up to about 41 s. | Preserve same-stream ordering while allowing unrelated streams to progress. Measure queue depth, shard collisions, queue wait, and per-shard utilization before changing concurrency. |
| `TN-RDY-004` | P1 | Extraction responses are too large and occasionally invalid. | A single session produced 10 candidates and 6,000 output tokens in about 41 s. Another response emitted an empty timestamp, failed decoding, and required a job retry. | Tighten the schema, output-token limit, and candidate cap. Treat optional timestamps explicitly and regression-test malformed or empty model fields. |
| `TN-RDY-005` | P1 | Readiness is a one-second SQL polling loop with no explicit completion or terminal-failure signal. | The harness could only observe an incomplete cursor and eventually print a generic timeout, even when a job was retrying after an extractor error. | Expose scope completion and terminal job errors through a provider API or notification. Report the failing stream and job instead of timing out generically. |
| `TN-RDY-006` | P1 | The report aggregates the entire asynchronous path into one readiness number and omits provider-internal cost. | `r9` reports 277.67 s but cannot split queue wait from extraction LLM and persistence time. Its `$0.0189` Team Note cost is consumer-only. | Record enqueue, scheduler, queue, extraction, persistence, and visibility timings plus extraction and embedding tokens/cost. Preserve `unknown` when unavailable. |
| `TN-RDY-007` | P1 | A retry can finish quickly after background extraction completes, but the runner cannot reuse that completion automatically. | The five targeted retries completed in seconds after the first-pass readiness timers expired. | Make readiness resumable and idempotent. A resumed trial should observe the existing scope cursor without repeating completed memory work. |
| `TN-RDY-008` | P1 | Trial result persistence retains only the latest attempt, so retries erase first-pass latency and failure evidence from exported artifacts. | The five final `r9` Team Note records contain roughly one-second readiness waits; their earlier roughly 480-second failed waits are absent from `trials.jsonl` and the summary mean. | Append attempt records instead of replacing them. Export attempt index, retry/resume reason, failure, stage timings, tokens, cost, and the final-attempt selection rule. |
| `TN-RDY-009` | P1 | Runtime provenance does not include the settings that control readiness concurrency and deadlines. | `r9` artifacts omit runner parallelism, trial timeout, readiness allowance, extraction shard count, and workers per shard. | Export the sanitized effective run config and Team Note worker topology. Reports must distinguish artifact-recorded values from operator observations. |

### Temporary operating workaround

Until `TN-RDY-002` is fixed, the known-sufficient operator setting for the
observed `r9` workload is a 30-minute trial timeout and a readiness allowance of
1,800 one-second polls. These values have not been proven to be minimums. This
only prevents false failures; it does not improve latency. If the harness times
out while extraction workers are healthy, retry only the failed trials after
verifying that their source artifacts and scopes are unchanged. Do not restart
the entire paid matrix.

Hourly automation remains disabled. A system whose mean write-to-ready time is
over four minutes and whose failure behavior depends on a hidden poll count is
not ready for unattended recurring evaluation.

## Historical shared-producer baseline

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

### 0. Repair the readiness contract

- Replace the attempt-count deadline with one duration derived from the trial
  context or declared in the run configuration.
- Return scope-level completion, retrying, and terminal-failure states with the
  responsible stream and extraction job.
- Preserve idempotent resume behavior so a harness retry observes completed
  cursors rather than repeating extraction.
- Add an acceptance test where extraction takes longer than the old 480-second
  limit but completes within the declared trial deadline.

### 1. Correct the measurement contract

- Report a shared producer only for legacy runs; native runs report source-event
  count and ingest time instead.
- Split Team Note readiness into enqueue delay, scheduler delay, queue wait,
  extraction LLM time, persistence/admission time, and cursor visibility.
- Report recall-hook latency separately from consumer-model latency.
- Add per-layer input/output tokens and attributed cost; preserve `unknown`
  when a provider does not expose a value.
- Preserve every trial attempt and state which attempt supplies the final score;
  do not let a successful resume erase the first failure or its elapsed time.
- Record runner parallelism, trial timeout, readiness deadline, extraction
  shards, and workers per shard in the artifact runtime provenance.

### 2. Reduce extraction generation

- Tighten the extraction response schema and prompt.
- Cap candidate count and output tokens for the eval/MVP profile.
- Avoid verbose candidate rationale that is not stored or scored.
- Batch compatible session extractions while retaining explicit per-session
  provenance and actor-local order in both the input and output contract.
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
- A native 50-case run completes every Team Note trial without a hidden poll
  limit expiring before the declared trial deadline.
- Every incomplete readiness result identifies whether extraction is queued,
  running, retrying, or terminally failed, including its stream and job ID.
- Reports preserve every attempt with failure reason, stage timings, cost, and
  retry/resume identity while clearly identifying the final scored attempt.
- Artifacts record runner parallelism, trial timeout, readiness deadline,
  extraction shard count, and workers per shard as effective values.
- No regression in Team Note pass count, absence handling, token F1, provenance,
  or actor-local ordering.
- Reports distinguish legacy shared producer time from arm-specific latency and
  report native source event/actor/session counts.
- Reports include Team Note extraction and embedding usage/cost, and clearly
  mark unavailable Mem0 usage instead of estimating it.
- Recurring runs cannot overlap, stop when the monthly budget guardrail is hit,
  retain reproducible artifacts, and remain disabled until explicitly approved.
