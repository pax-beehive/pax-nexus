# Finance micro6 expanded cohort: construction and 6-case baseline r1

Date: 2026-07-18 (America/Los_Angeles)

Run IDs: `groupmembench-finance-micro6-v2-canary-20260718-r1-current`,
`groupmembench-finance-micro6-v2-canary-20260718-r1-interaction-slim`

Source scope: `groupmembench-finance-v3-micro6-expanded-20260718-r1-groupmembench-Finance`
(2,595 events, 5 phases, direct DB insert from
`runs/groupmembench-v3-selection` — direct insert avoids the paid extraction
the service ingest path would trigger; the extraction eval replays from
`session_events` only, so realism is unaffected).

Profile: `evals/extraction-v1/profiles/finance-micro6-quick.json`
(104 events, 7 streams, 8 slices; preflight verified all supporting events).

## Cohort expansion work

Supporting event annotations (verified programmatically against gold
patterns):

- `temporal_7` → Msg_11264 ("Locked on my side! Finance + IT can send
  primary/backup owners and one evidence source per path by Friday EOD"),
  phase `Cash Management Module Build`.
- `user_implicit_7` → Msg_18259 ("the control-mapping revision is still
  moving, so I can't lock the regulatory formatting"), phase `Implement
  Report Generation Engine`.
- `term_ambiguity_11` → Msg_25212 ("Locked. Finance Ops stamps the cutoff
  owner today", timestamp 2025-07-17), same phase.

Gold-pattern calibration (corpus phrasing, gold answer unchanged):

- `temporal_7`: connectors `and|&` extended with `+` and `/`; date
  alternatives extended with `friday` (the corpus never spells out
  "July 18"; Friday EOD resolves to the gold answer 2025-07-18).
- `term_ambiguity_11`: date alternatives extended with `today` (the
  commitment says "today" on 2025-07-17).

## 6-case baseline

| Metric | current | interaction-slim |
|---|---:|---:|
| Fact recall (6 cases) | 0.167 (1/6) | 0.000 (0/6) |
| Raw / suppressed leakage | 0 / 0 | 0 / 0 |
| Admitted notes | 16 | 9 |
| State decisions / rejections | 19 / 39 | 11 / 53 |
| Primary / summary calls (errors) | 8 / 2 (0) | 8 / 1 (0) |
| Output tokens | 71,918 | 63,854 |
| P95 primary duration | 110.7 s | 93.4 s |

## Per-case findings

1. `temporal_7` — matched in `current`: "Finance Ops and IT must post
   primary/backup/evidence per exception path by Friday EOD." (Msg_11264).
   The supporting source phrasing is committal ("Locked on my side"), so the
   existing protocol captures it; the fixture calibration (friday, `+`)
   scored it. Missed in `interaction-slim`.
2. `term_ambiguity_11` — the ambiguity trap fired in `interaction-slim`,
   which extracted the wrong owner: `owner/cutoff` = "BI is cutoff owner."
   The corpus contains both "Finance Ops stamps the cutoff owner today"
   (Msg_25212, gold) and "BI stamps ..." decoys. `current` produced no
   cutoff note at all.
3. `user_implicit_7`, `multi_hop_1`, `knowledge_update_20` — missed on both
   arms; no state decision cites the supporting events (Msg_18259, Msg_3651,
   Msg_3463) in any slice. Same mechanism as before: the facts never become
   decision candidates.
4. `abstention_4` — first fully clean run: 0 raw leakage, 0 suppressed, on
   both arms. Do not over-attribute: the expanded window changes what the
   model sees, and earlier runs leaked on the same case.

## Variance warning

`knowledge_update_20` matched in r1-slim and r3-slim but missed here;
`abstention_4` leaked in three earlier runs and is clean here. Single runs
on this cohort remain high-variance at the case level. Treat any one run as
a signal source for mechanism analysis (traces, note bodies), not as a
ranking. For variant or protocol comparisons, repeat runs or aggregate
across seeds before deciding.

## State of the iteration loop

The loop is now: 6-case quick cohort at roughly US$0.05 and 25 minutes per
paired run, trusted scoring (negation-aware leakage with audit details,
calibrated patterns), resume support, and verified quick-vs-full fidelity.
The extraction baseline to beat is `current` 0.167 / `interaction-slim`
0.000 with 0 leakage on this cohort.

One-off data tooling used for this expansion lives in `tmp/`
(`annotate-supporting-ids.py`, `build-expanded-scope.py`,
`expanded-scope-insert.sql`); promote to `scripts/` only if a second
expansion is planned.
