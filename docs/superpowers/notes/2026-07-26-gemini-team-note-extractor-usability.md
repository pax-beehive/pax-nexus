# Gemini models as Team Note extractors: usability report

**Status: IN PROGRESS.** The 30-case run started 2026-07-26 05:12Z and is still
executing. Sections marked *(preliminary)* carry results from the completed
10-case run and will be replaced with 30-case figures. Everything else is final.

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

## Results (preliminary — 10 cases)

| extractor | control | team_note | gain | recall>0 | candidates | hits |
| --- | --- | --- | --- | --- | --- | --- |
| deepseek-v4-flash | 2/10 | 2/10 | +0.00 | 4/10 | 5 | 5 |
| gemini-3.5-flash-lite | 2/10 | 3/10 | +0.10 | 6/10 | 14 | 12 |
| gemini-3.6-flash | 1/10 | 3/10 | +0.20 | 7/10 | 11 | 9 |

Recall differed across extractors in 7 of 10 cases, so the signal separates.

Both accuracy gains sit at or barely above the one-case noise floor, so the
ranking is not yet meaningful. The recall-coverage ordering — both Gemini models
above DeepSeek — is the finding that survives at this sample size.

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

*Pending 30-case results.*
