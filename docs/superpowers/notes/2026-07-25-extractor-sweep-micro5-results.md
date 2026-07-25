# Extractor sweep, micro-5: results and blockers

Run `micro5-20260725`, two-arm mode, manifest
`runs/groupmembench-v3-micro-canary-v1/manifest.five.json` (5 cases, full-domain
source of 1605 events / 15 sessions / 8 actors). Consumer, judge, and Mem0 all
pinned to DeepSeek; only the Team Note extractor varied.

## Outcome

All three rounds completed ingest. No round reached a scored trial, so there are
no accuracy numbers. Every round is `valid: false`, correctly.

Total wall clock for three rounds: 46 minutes (20:30:32Z - 21:17:12Z).

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

## Blocker 2: Gemini rounds fail the preflight recall canary

Both Gemini rounds failed before trials:

```
preflight Team Note recall: team note origin
"micro5-20260725-gemini-3.6-flash-preflight-eval-95254bd8c3fa27d7bb5b7c7f6440f58a"
was not recalled after 120 attempts
```

The DeepSeek round passed the same preflight, so the mechanism works. Note that
`gemini-3.6-flash` produced *more* memory items than DeepSeek (24 vs 13) and
still failed, so "the extractor produced nothing" does not explain it.

Not yet diagnosed. Candidates worth checking in order:

- Whether the preflight canary note is extracted at all under a Gemini
  extractor, versus extracted but not matching the origin the preflight polls for.
- Whether the Gemini output shape differs in a way the origin-tagging path
  depends on — the preflight matches on an exact origin string.
- Whether extraction of the small preflight scope simply had not finished within
  120 attempts, which would make this a timeout rather than a correctness bug.

The preflight scope is separate from the domain scope and is torn down with the
stack each round, so this needs a targeted run with the stack left up to
diagnose.

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

## Recommended next step

Fix Blocker 1 first. It is small, well understood, blocks both arm sets, and
without it no sweep can ever produce a score. Then re-run micro-5: the DeepSeek
round should complete and produce the first real two-arm numbers, and the Gemini
rounds will still stop at preflight, which isolates Blocker 2 for a targeted
diagnosis with the stack left up.

Do not run the full selection until a micro-5 round scores end to end.
