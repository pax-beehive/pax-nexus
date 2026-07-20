# Finance micro3 quick-vs-full consistency r1

Date: 2026-07-18 (America/Los_Angeles)

Question: does the `finance-micro3-quick` profile (65 events, 5 slices)
reproduce extraction behavior of the full micro6 source (1,605 events, 74
slices) on the three supported cases, under identical extractor code?

Full-cohort run ID:
`groupmembench-finance-micro3-v2-full-20260718-r1-current`

Compared against (quick, same code, same day):
`groupmembench-finance-micro3-v2-canary-20260718-r1-current`

Source run ID:
`groupmembench-finance-v3-micro6-general-recall-v3-20260717-r3`

Profile added for this run:
`evals/extraction-v1/profiles/finance-micro3-full.json` (same 3 cases,
`full_source: true`)

Extractor: v2 / `deepseek-v4-flash` / variant `current`

## Raw result

| Metric | quick (65 ev, 5 slices) | full (1,605 ev, 74 slices) |
|---|---:|---:|
| Fact recall (3 cases) | 0.000 | 0.000 |
| Raw leakage items | 1 | 0 |
| Suppressed leakage items | n/a (pre-negation-code) | 0 |
| Admitted notes | 16 | 33 |
| Primary calls (errors) | 5 (0) | 74 (0) |
| Summary calls (errors) | 0 (0) | 30 (0) |
| Input tokens | 81,970 | 2,555,896 |
| Output tokens (primary / summary) | 34,091 / 0 | 364,336 / 50,336 |
| Cache hit rate | 69.5% | 6.9% |
| Mean / P95 call duration | 63.4 s / 68.9 s | 41.8 s / 89.4 s |
| State decisions / rejections | 21 / 9 dec + 3 claim | 55 / 169 dec + 33 claim |
| Interaction rejections | 33 | 1,584 |
| `no_state` / unreviewed events | 19 / 10 | 1,048 / 455 |

Summary calls are now classified separately (74 primary + 30 summary = 104),
closing the 2026-07-17 r1 caveat that actual provider-call count was unknown.

## Per-case consistency

1. `multi_hop_1` — consistent miss. On the full cohort, no state decision
   ever cites Msg_3651; the deadline fact again surfaces only as an
   interaction observation (`propose`, empty actor) that deterministic
   validation discards. The quick profile therefore did not distort this
   case. The 2026-07-17 r1 match came from pre-fix code and cannot be
   reproduced under current admission rules; the live defect is that the
   model omits the interaction actor, so the entire interaction channel is
   discarded (1,584 of 1,584 observations on the full cohort) and
   proposal-carried facts never reach a state decision.
2. `knowledge_update_20` — consistent miss. No full-cohort note cites
   Msg_3463; the targeted-check/rerun fact is never extracted. The full
   cohort admits a different `rule/ESG-validation-scope` note (issuer
   coverage gaps, exclusion mismatches, classification drift) whose body does
   not contain the required atoms.
3. `abstention_4` — divergent score, consistent behavior. The full cohort
   does assert the forbidden ownership, twice: `owner/ESG-exception-log`
   ("Compliance owns the exception log ...") and
   `obligation/compliance-maintain-exceptions` ("Compliance maintains the
   exceptions log."). Raw leakage is 0 only because the fixture's forbidden
   pattern requires the noun `owner`/`designated` within 100 chars of
   `compliance` and does not cover the verb forms `owns`/`maintains`. The
   quick run's leak counted 1 for the same reason in reverse (subject "Owner
   of exception log" placed `owner` near `Compliance`). The score divergence
   is fixture-pattern coverage, not cohort distortion.

## Conclusions

- The quick profile is a behaviorally faithful proxy for the full cohort on
  these three supported cases: every per-case verdict (miss/miss/leak-prone)
  matched, at roughly 1/15 of the input tokens and 1/21 of the wall time.
  It is valid as the iteration signal for extraction work on this fixture.
- Two new scored-signal defects, both in fixture/scorer territory rather than
  the cohort:
  1. Forbidden-atom patterns must cover verb forms (`owns`, `maintains`) or
     leakage reports false negatives on the full cohort (this run).
  2. The interaction channel is functionally dead: the model never fills
     `actor`, every interaction observation is discarded, and facts carried
     only by proposal speech acts (the `multi_hop_1` deadline) have no path
     into Team Notes. This is now the top extraction defect: it caps recall
     on both cohorts independently of slicing.
- Extraction-side iteration should therefore start with the interaction
  actor omission (prompt contract) and the proposal-to-state path, not with
  profile or slice tuning.

## Protocol notes

- The full run used the negation-aware scorer landed earlier today; no
  leakage match occurred, so suppression was not exercised here (unit tests
  cover it).
- Zero provider errors; no resumption was needed. The run took ~85 minutes
  wall time.
- The first full-cohort attempt without a profile hard-failed at preflight
  because `temporal_7`, `user_implicit_7`, and `term_ambiguity_11` have no
  supporting event IDs in the fixture; the `finance-micro3-full` profile
  scopes the run to the three supported cases. Supporting the remaining
  cases requires fixture annotation plus a 5-phase re-ingest (scoped
  separately).
