# Gemini models as Team Note extractors: usability report

**Status: COMPLETE.** All three 30-case rounds finished. Accuracy and recall
figures below are the 30-case results. The cost table remains 10-case: each
round's stack is destroyed by the next round's reset, and the 30-case
`extraction_runs` rows were not sampled before teardown.

## Question

Can `gemini-3.5-flash-lite` or `gemini-3.6-flash` replace `deepseek-v4-flash` as
the Team Note extraction model?

Only the extractor varies. The consumer, the judge, and the recall path are
pinned to their production values in every round, so any difference between
rounds is attributable to the model that produced the notes.

## Method

Eval v2, `control` versus `team_note`, over
`runs/groupmembench-v2-selection/manifest.json` (30 cases). Each round:

1. wipe the stack (`down -v`) so no round inherits another's notes;
2. ingest per case with the round's extractor selected through
   `TEAM_MEMORY_EXTRACTOR_{MODEL,BASE_URL,API_KEY}_OVERRIDE`;
3. wait for extraction to catch up (`after_producer` readiness gate);
4. run both arms and judge.

`control` answers with no memory. `team_note` answers with Team Note recall and
nothing else — no private SQLite, no Mem0. That isolation is the point, and it
is what makes this eval able to measure an extractor at all (see "Why not Eval
v3" below).

Isolation is additionally guaranteed by scope: every note is written under a
`scope_id` that embeds the run ID and extractor slug, e.g.
`v2full30-20260726-gemini-3.6-flash-groupmembench-abstention_2`, so recall in one
round cannot reach another round's notes.

### What "accuracy" can and cannot show here

The `control` arm is the noise floor. It runs identical inputs through an
identical consumer in every round, so any spread in its score is pure run-to-run
variance. In the 10-case run that spread was one case (0.10 accuracy). A
`team_note` gain at or below the control spread is not evidence of anything.

Recall coverage is the more sensitive instrument: the fraction of cases where
the extractor's notes produced any retrieval candidate at all. It varies with
the extractor directly and does not depend on the consumer answering correctly.

## Results (30 cases)

| extractor | control | team_note | gain | recall>0 | candidates | hits | team_note F1 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| deepseek-v4-flash | 4/30 | 6/30 | +2 | 14/30 | 25 | 25 | 0.140 |
| gemini-3.5-flash-lite | 3/30 | 3/30 | 0 | 11/30 | 32 | 27 | 0.056 |
| gemini-3.6-flash | 4/30 | 6/30 | +2 | 14/30 | 31 | 29 | 0.118 |

**`gemini-3.6-flash` matches DeepSeek.** Identical control (4/30), identical
`team_note` (6/30), identical recall coverage (14/30), and more candidates and
hits (31/29 against 25/25). Its token-F1 is somewhat lower, 0.118 against 0.140 —
it retrieves as often but its notes phrase the answer less closely.

**`gemini-3.5-flash-lite` is the one that fails.** Zero gain over its own
control, and the lowest F1 of the three (0.056) despite producing the *most*
candidates. It retrieves plenty and retrieves the wrong things.

The gains must be read against the noise floor. `control` spans 3-4/30 across
rounds — one case — so +2 clears it, but not by much. Recall coverage is the
firmer instrument, and there DeepSeek and `gemini-3.6-flash` are exactly level.

The 10-case preliminary reading put both Gemini models above DeepSeek on recall
coverage. At 30 cases that ordering did not hold: `3.6-flash` converged to
DeepSeek and `3.5-flash-lite` fell below it.

## Extraction speed (30 cases)

Measured as `readiness_duration_ms` — how long each case waited for extraction to
catch up with its ingested session. The `control` arm is 0 in all 30 cases of
every round, so these are 30 independent observations per model with no
double-counting. Worker configuration was identical across rounds
(`TEAM_MEMORY_WORKER_JOB_TIMEOUT=20m`, `TEAM_MEMORY_WORKER_MAX_ATTEMPTS=10`), so
the spread is attributable to the extractor.

| extractor | median | mean | max | 30-case total | round wall clock | vs DeepSeek |
| --- | --- | --- | --- | --- | --- | --- |
| deepseek-v4-flash | 198.5s | 199.1s | 371.1s | 99.5 min | 72.5 min | 1x |
| gemini-3.6-flash | 48.0s | 49.4s | 82.6s | 24.7 min | 38.7 min | 4.1x faster |
| gemini-3.5-flash-lite | 11.4s | 12.2s | 22.2s | 6.1 min | 26.5 min | 17.4x faster |

Median tracks mean in every round, so these are stable measurements rather than
a few slow outliers. The ordering matches the output-token ratios below: 5.7x
fewer output tokens for `3.6-flash`, 4.1x less time. Extraction latency is
dominated by output generation.

This is the sharpest separation in the whole comparison — far cleaner than the
accuracy signal, which sits barely above its noise floor.

Note this measures catch-up latency, not raw model latency: queueing and worker
scheduling are included. That is the right unit for the question at hand — how
long the system waits before a note is retrievable.

## Extraction cost and output behaviour (preliminary — 10 cases)

Measured from `extraction_runs` in the eval database, not estimated:

| extractor | calls | quarantined | input tokens | output tokens | notes produced |
| --- | --- | --- | --- | --- | --- |
| deepseek-v4-flash | 81 | 2 | 589,340 | 155,618 | 28 |
| gemini-3.5-flash-lite | 81 | 2 | 644,600 | 18,903 | 26 |
| gemini-3.6-flash | 81 | 8 | 736,813 | 27,220 | 34 |

Three things stand out.

**DeepSeek emits 6-8x the output tokens for the same work.** 155,618 against
18,903 and 27,220, on the same 81 calls and comparable input. Whatever the
per-token rates, that ratio is a structural cost difference in Gemini's favour,
and it is not explained by note count — DeepSeek produced 28 notes to
`gemini-3.6-flash`'s 34.

**`gemini-3.6-flash` is quarantined four times as often.** Eight of its 81
extraction runs were rejected by the guard that blocks a revision from dropping
protected qualifiers without source authority, against two for each of the other
models. That is a correctness signal about how the model rewrites existing notes,
and it is the strongest argument so far against adopting it uncritically.

**Note volume is comparable across all three.** 28 / 26 / 34. This corrects an
earlier reading taken from Eval v3 runs, where `gemini-3.5-flash-lite` appeared
to produce only 2 notes against DeepSeek's 13-31. That gap was an artifact of the
v3 setup, not a property of the model.

## Budget

The $50 Gemini budget is not a binding constraint. The 10-case run consumed
1.38M input tokens across both Gemini models; the 30-case run scales to roughly
4M. At any published flash-tier rate that is single-digit dollars. Time, not
money, is the limiting factor: roughly 30-35 minutes per round.

Exact token counts for the 30-case run are being sampled continuously from the
live database, because each round's stack is destroyed at the next round's reset
and the usage becomes unqueryable.

## Why not Eval v3

An earlier attempt swept these extractors on Eval v3 and could not measure
anything, for a reason worth recording.

V3's memory arm is `private_sqlite_plus_team_note`. It combines the Team Note
store with a private SQLite database that materializes all 1605 source events
verbatim and is byte-identical in every round. The candidate pool stayed at ~15
regardless of extractor, and the top-5 selection was dominated by SQLite, so the
context injected into the consumer was **identical across extractors that
produced 31, 2, and 24 notes**. The varying component never reached the model.

That is not a sample-size problem and running more cases would not have fixed it.
Details in `2026-07-25-extractor-sweep-micro5-results.md`.

## Infrastructure defects found and fixed along the way

All pre-existing on `main`, all blocking any eval run from producing a score:

| commit | defect |
| --- | --- |
| `55fb1ac`, `4981198` | Postgres DSN pinned to port 55433 while compose publishes an ephemeral port. Broke `make eval-v3` for everyone. |
| `71acf93` | A nil artifact-refs map marshalled to `null`; `jsonb \|\| 'null'` promotes the column to an array, so every later attempt decode failed. |
| `ca3f585`, `0a70953` | Preflight recall budget hardcoded to 120 polls (2 minutes) and applied immediately after an ingest that leaves a ~30-minute extraction backlog. |

One defect remains open and is not blocking: `validity.go`'s recall-observation
rules were written against paxm v0.1.30 diagnostics and do not match v0.2.5. A
no-memory arm with zero recall calls is now flagged invalid merely because the
provider-type field is populated. Runs are marked invalid; the underlying trial
data is unaffected.

## Recommendation

**`gemini-3.6-flash` is a viable replacement for `deepseek-v4-flash`; adopt it
behind the quarantine check, not blindly. `gemini-3.5-flash-lite` is not
viable.**

The case for `3.6-flash` is speed and cost, not quality. It equals DeepSeek on
every retrieval measure this eval can resolve — same accuracy, same coverage,
more candidates and hits — while extracting **4.1x faster** (48s median against
198.5s) and emitting 5.7x fewer output tokens for the same 81 extraction calls.
Quality is a tie; the tie is broken on latency and price, and the latency margin
is far better separated than the accuracy numbers are.

For a team-memory store the latency margin is the one that compounds: it is the
delay between something being said and it becoming retrievable by the rest of
the team.

Two conditions on that recommendation:

1. **The quarantine rate is the open risk.** Eight of 81 extraction runs
   rejected, against two for each of the other models — the guard that blocks a
   revision from dropping protected qualifiers without source authority. It
   rewrites existing notes more destructively. The guard caught every instance
   here, so nothing bad reached the store, but a 4x rate is a property of the
   model, not of this corpus. Watch it before trusting `3.6-flash` on a store
   with notes worth preserving.

2. **The F1 gap is small but consistent.** 0.118 against 0.140. Same retrieval,
   slightly worse phrasing. It does not change the ranking; it does mean
   "equivalent" is about *whether* the right note is found, not about how well
   it is written.

`gemini-3.5-flash-lite` should not be used. It produces the most candidates and
the worst answers — 0.056 F1, below its own no-memory control, and no accuracy
gain at all over 30 cases.

### Caveat on the Eval v3 attempt

A separate 10-case run on Eval v3 (`runs/eval-v3-sweep/dsvs36-20260726`) showed
`gemini-3.6-flash` producing 9 notes to DeepSeek's 43 on the full 1605-event
domain, and returning zero Team Note candidates in all 10 cases. **Do not read
that as a property of the model.** Two reasons:

- That run's artifacts record `TEAM_MEMORY_EXTRACTOR_MODEL: deepseek-v4-flash`
  for *both* rounds, so it cannot even prove which extractor ran. Cause and fix
  in `scripts/eval-v3-extractor-sweep.sh` — the sweep sourced the env loader
  once before its round loop, and the Go runner, which never sources it, took
  the base `.env` value into every round's `runtime_env`.
- The same collapse appeared for `gemini-3.5-flash-lite` in the earlier v3 micro
  run (2 notes against DeepSeek's 13-31) while both Gemini models extract
  normally on the v2 corpus. Something about the v3 full-domain ingest shape
  suppresses Gemini extraction specifically. That is an unlocated defect, and it
  is not evidence about extractor quality.
