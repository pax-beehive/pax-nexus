# Mem0 passive10 comparison: invalid until cohort ingest is reliable

Date: 2026-07-19

## Intended comparison

Compare Mem0 with the completed `interaction-slim + passive-v1` Team Note
baseline on the same curated ten-case GroupMemBench manifest, consumer model,
and judge model.

## Attempts

| Run | Isolation | Preflight | Judge accuracy | Cases with created Mem0 memories and returned context | Valid comparison |
| --- | --- | ---: | ---: | ---: | --- |
| `mem0-r1` | Reused Mem0 service | none | 1/10 | 1/10 | no |
| `mem0-r2` | Fresh Docker project, volumes, and run ID | pass | 3/10 | 2/10 | no |
| `mem0-messages-r1` | Native-session Mem0 messages | pass | interrupted | incomplete | no |
| `mem0-messages-r2` | Session-first, event fallback, write gate | pass | stopped for latency | incomplete | no |
| `mem0-chunks-r1` | Eight-event chunks | blocked by shared preflight | none | none | no |
| `mem0-chunks-r2` | Eight-event chunks, Mem0-only preflight | pass | running | pending | pending |

Artifacts:

- r1: `runs/eval-v2/groupmembench-finance-v2-interaction-slim-passive10-mem0-20260719-r1`
- r2: `runs/eval-v2/groupmembench-finance-v2-interaction-slim-passive10-mem0-20260719-r2`
- messages r1: `runs/eval-v2/groupmembench-finance-v2-interaction-slim-passive10-mem0-sessions-20260719-r1`
- messages r2: `runs/eval-v2/groupmembench-finance-v2-interaction-slim-passive10-mem0-sessions-20260719-r2`
- chunks r1: `runs/eval-v2/groupmembench-finance-v2-interaction-slim-passive10-mem0-chunks-20260719-r1`
- chunks r2: `runs/eval-v2/groupmembench-finance-v2-interaction-slim-passive10-mem0-chunks-20260719-r2`
- Team Note baseline: `runs/eval-v2/groupmembench-finance-v2-interaction-slim-passive10-20260719-r1`

## Evidence

The r2 preflight successfully created, retrieved, and deleted its synthetic
verification fact. During the real cohort, however, Mem0 logged malformed
fact-extraction JSON for eight cases. Its API returned success with an empty
result array, which the Eval v2 client recorded as a known no-op. Eight Mem0
trials therefore had zero created, updated, or deleted memories and zero
returned context items.

Only `multi_hop_15` (21 created memories, five context items) and
`temporal_26` (seven created memories, five context items) exercised actual
Mem0 retrieval. The r2 pairwise result (`3` wins, `0` losses, `7` ties against
control) is consequently selection-biased by ingest failures and must not be
compared with Team Note's 3/10 judge score.

The first native-session attempt was intentionally interrupted after confirming
that individual malformed fact-extraction responses can still be represented
by Mem0 as `results: []`. It is not a cohort result. The rerun adds two
evaluator controls: source-bearing Mem0 ingest is rejected when it produces no
mutations, and a session that returns no mutations is retried as deterministic
single-event requests. The latter keeps the native session request as the
primary profile while reducing the JSON-generation pressure only on the failed
session.

That session-first profile still exceeded the eight-minute trial window on a
32-event case with 17 source sessions. The replacement profile packs
chronological source events into chunks of eight and records every event ID in
metadata. A zero-mutation chunk still falls back to deterministic single-event
requests, which bounds the normal case without discarding source provenance.

The first chunk run did not enter ingest because the shared preflight waited
for unrelated Team Note extraction. Mem0-only cohorts now use an independent
Mem0 add/search/delete preflight. That preflight passed before chunks r2 began
cohort ingest.

## Decision

Reject r1, r2, and the interrupted messages r1 as benchmark-quality
comparisons. A synthetic preflight is necessary but insufficient: the evaluator
must require successful factual ingest for source-bearing benchmark cases and
explicitly classify a zero-write provider response as ingest failure. The
active chunks r2 run may be accepted only if every completed Mem0 case passes
that write gate and records a non-empty recall observation; otherwise it is
rejected and the Mem0 ingestion profile requires further work before a unified
cohort.
