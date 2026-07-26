# Extractor sweep, micro-5: results and blockers

Run `micro5-20260725`, two-arm mode, manifest
`runs/groupmembench-v3-micro-canary-v1/manifest.five.json` (5 cases, full-domain
source of 1605 events / 15 sessions / 8 actors). Consumer, judge, and Mem0 all
pinned to DeepSeek; only the Team Note extractor varied.

## Outcome

Seven runs. The final one (run 7) cleared every blocker and scored all three
rounds; the scores are unusable for the reason given under "The decisive
finding" below. Every round is `valid: false`, on a recall-instrumentation
check whose expectations no longer match the paxm version in use.

Total wall clock for run 7's three rounds: 52 minutes.

Scores from run 7, recorded only to show they carry no signal:

| extractor | memory_items | no_memory_team | private_sqlite_plus_team_note |
| --- | --- | --- | --- |
| deepseek-v4-flash | 31 | 0.20 | 0.00 |
| gemini-3.5-flash-lite | 2 | 0.20 | 0.00 |
| gemini-3.6-flash | 24 | 0.20 | 0.20 |

## The one real signal

Ingest succeeded in all three rounds, and the extractors differed sharply in how
much memory they produced from the identical 1605-event domain:

| extractor | `memory_items` |
| --- | --- |
| `deepseek-v4-flash` | 13 |
| `gemini-3.5-flash-lite` | 2 |
| `gemini-3.6-flash` | 24 |

Treat this as a smoke-test observation, not a finding. It is one sample per
model on a five-case canary, note count is not note quality, and nothing here
was scored. It does establish that the sweep mechanism drives the extractor
end to end: `config.resolved.json` recorded the correct
`TEAM_MEMORY_EXTRACTOR_MODEL` and `TEAM_MEMORY_EXTRACTOR_BASE_URL` per round,
and the per-round stack reset held.

`gemini-3.5-flash-lite` producing 2 items against `gemini-3.6-flash`'s 24 is
worth a look on its own — if it holds up at full-selection scale, it suggests
the lite model is largely failing to extract rather than extracting selectively.

## Blocker 1: nil artifact refs corrupt the attempts column

Hit by the `deepseek-v4-flash` round, which was the only one to clear preflight.

```
load eval trial attempts: decode eval trial attempt artifact references:
json: cannot unmarshal array into Go value of type map[string]string
```

Chain:

1. `internal/eval/v2/runner.go:626` calls `UpdateAttempt(ctx, attempt, stage, nil)` with a nil map.
2. `internal/eval/v2/postgresstore/store.go:267` marshals that nil map to the JSON literal `null`.
3. `store.go:273` applies `artifact_refs = artifact_refs || $6::jsonb`. In PostgreSQL, concatenating a jsonb object with a jsonb non-object promotes both to an array, so `'{}'::jsonb || 'null'::jsonb` yields `[{}, null]`.
4. `Attempts()` (`store.go:298`) then decodes that array into `map[string]string` and fails.

Pre-existing and unrelated to the sweep: the branch never touched
`postgresstore/` or `runner.go`. It blocks every round that reaches the trial
stage, in two-arm and three-arm mode alike.

Likely fix: make `UpdateAttempt` treat a nil or empty map as `{}` rather than
`null`, so the concatenation stays object-to-object. Worth also checking whether
any other `||` on a jsonb column can receive a non-object.

## Blocker 2: the preflight recall canary times out (not extractor-specific)

**Correction.** This section originally attributed the failure to the Gemini
extractors, on the evidence that both Gemini rounds failed it while the DeepSeek
round passed. A later run refuted that: the DeepSeek round failed the same check,
with the same message, on the same configuration that had passed before. The
cause is timing, not the extractor.

| run | deepseek-v4-flash | gemini-3.5-flash-lite | gemini-3.6-flash |
| --- | --- | --- | --- |
| 5 | preflight passed | preflight failed | preflight failed |
| 6 | preflight **failed** | not reached | not reached |

Root cause: the recall poll budget was `defaultAttempts = 120` at a 1-second
interval (`internal/eval/v2/memoryprobe/client.go:28,84-86`) — a hardcoded
2-minute wait. Preflight runs immediately after full-domain ingest, which pushes
1605 events through an asynchronous extraction worker over roughly 30 minutes.
The canary session queues behind that backlog, so whether it is extracted within
two minutes depends on worker timing.

The same codebase already treats this class of wait as configurable and generous:
domain readiness polls up to `PAX_EVAL_READINESS_ATTEMPTS`, set to 1800 in
`.env.eval-v2`. The preflight recall budget was the outlier.

The original observation, kept for the record:

```
preflight Team Note recall: team note origin
"micro5-20260725-gemini-3.6-flash-preflight-eval-95254bd8c3fa27d7bb5b7c7f6440f58a"
was not recalled after 120 attempts
```

Note that `gemini-3.6-flash` produced *more* memory items than DeepSeek (24 vs
13) and still failed, so "the extractor produced nothing" never explained it —
which was the first hint that the extractor attribution was wrong.

Of the three candidate explanations considered at the time, the third proved
correct: extraction of the preflight scope simply had not finished within the
120 attempts, making this a timeout rather than a correctness bug.

## What is verified working

- Two-arm mode: `mem0-ingest.json` and `mem0.complete` absent in every round,
  `arm_set: two_arm_no_mem0` recorded in every `validity.json`, and the Mem0
  validity checks correctly skipped while everything else stayed in force.
- The extraction readiness wait still runs in two-arm mode: `memory_items` was
  populated in all three receipts, which is what makes the zero-memory guardrail
  meaningful.
- Per-round stack reset, DSN resolution from the ephemeral published port,
  provenance recording of the swept extractor, and non-aborting per-round
  failure reporting.

## The decisive finding: Eval v3's memory arm cannot measure an extractor

Run 7 cleared all three blockers and scored every round. The scores are not
usable, and the reason invalidates the whole approach of sweeping extractors on
Eval v3.

Per-trial recall for the `private_sqlite_plus_team_note` arm, across three
rounds whose extractors produced 31, 2, and 24 memory items respectively:

| case | candidates | hits | context items injected |
| --- | --- | --- | --- |
| abstention_4 | 15-17 | 5 | 5 |
| knowledge_update_20 | 15 | 5 | 5 |
| multi_hop_1 | 15 | 5 | 5 |
| temporal_28 | 15 | 1 | 1 |
| user_implicit_9 | 15-19 | 5 | 5 |

The context actually injected into the consumer is **identical in all three
rounds**. An extractor that produced 2 notes and one that produced 31 put the
same five items in front of the model.

The cause is the arm's composition. `private_sqlite_plus_team_note` combines two
memory sources:

- the private SQLite database, which materializes all 1605 source events
  verbatim and is byte-identical every round;
- the Team Note store, the only component the sweep varies.

The candidate pool is ~15 regardless of extractor, and moving from 2 notes to 31
shifts it only to 17-19, so the top-5 selection is dominated by SQLite. The
swept component is drowned out before it reaches the model.

This is not a small-sample problem. Running the full selection would not fix it:
the varying component is absent from the model's context by construction, so
scores at any scale measure the fixed component plus noise.

### Where an extractor sweep belongs instead

Eval v2 already has configs that isolate a single memory provider, which is
exactly the structure this question needs:

- `evals/v2/config.interaction-slim-passive10.local.yaml` — arms `control`, `team_note`
- `evals/v2/config.source-span-v1-passive10.local.yaml` — arms `control`, `team_note`
- `evals/v2/config.source-span-v2-passive10.local.yaml` — arms `control`, `team_note`

With `control` versus `team_note` alone, every difference between arms is
attributable to the extracted notes, and every difference between rounds is
attributable to the extractor that produced them.

What carries over from this work: the `_OVERRIDE` extractor selection, the
candidate fragments, the sweep driver with its per-round reset and non-aborting
rounds, and all three bug fixes. What has to change is the eval version and the
config template the driver renders.

## Status of the blockers

Blocker 1 is fixed (`71acf93`). Run 6 confirmed it: the DeepSeek round cleared
ingest and reached preflight with no attempts-decode error, which is further
than any earlier run.

Blocker 2 is fixed by making the recall budget configurable, after run 6
supplied the evidence that refuted the original extractor-specific diagnosis.

Do not run the full selection until a micro-5 round scores end to end.
