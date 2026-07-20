# Finance micro3 extraction v2 paired canary r1

Date: 2026-07-18 (America/Los_Angeles)

Run IDs: `groupmembench-finance-micro3-v2-canary-20260718-r1-current`
(control), `groupmembench-finance-micro3-v2-canary-20260718-r1-interaction-slim`
(treatment)

Source run ID:
`groupmembench-finance-v3-micro6-general-recall-v3-20260717-r3`

Profile: `evals/extraction-v1/profiles/finance-micro3-quick.json`
(3 supported cases: `multi_hop_1`, `knowledge_update_20`, `abstention_4`)

Artifacts:
`runs/extraction-eval-v1/groupmembench-finance-micro3-v2-canary-20260718-r1-current/report.json`,
`runs/extraction-eval-v1/groupmembench-finance-micro3-v2-canary-20260718-r1-interaction-slim/report.json`

Extractor: v2 / `deepseek-v4-flash` / variants `current` and `interaction-slim`

## Raw result

| Metric | current | interaction-slim |
|---|---:|---:|
| Cases | 3 | 3 |
| Selected events / streams / slices | 65 / 4 / 5 | 65 / 4 / 5 |
| Primary provider calls (errors) | 5 (0) | 5 (0) |
| Summary / compaction / verifier calls | 0 | 0 |
| Admitted Team Notes | 16 | 13 |
| Required atoms matched | 0 / 3 | 2 / 3 |
| Fact recall | 0.000 | 0.667 |
| Leakage items | 1 | 1 |
| Input tokens | 81,970 | 79,525 |
| Output tokens | 34,091 | 35,170 |
| Cache hit / miss tokens | 56,960 / 25,010 | 54,144 / 25,381 |
| Token-weighted cache hit rate | 69.5% | 68.1% |
| Mean / P95 primary duration | 63.4 s / 68.9 s | 71.6 s / 102.9 s |
| Total model-call duration | 316.9 s | 357.9 s |
| Estimated model cost per arm | approximately US$0.02 | approximately US$0.02 |
| Resumptions | none | none |

Cost estimate uses the blended per-token rate implied by the 2026-07-17 micro6
r1 record; treat it as indicative only.

Trace totals, control: 21 admitted State Decisions (16 create, 2 no_change, 3
update), 3 Claim rejections, 9 Decision rejections, 33 Interaction rejections,
19 `no_state` events, 10 unreviewed events. Treatment: 21 admitted State
Decisions (14 create, 5 no_change, 1 update, 1 keep_conflict_open), 3 Claim
rejections, 8 Decision rejections, no Interaction review by design, 16
`no_state` events, 6 unreviewed events.

## Per-case findings

1. `knowledge_update_20` drives the entire recall gap, and it is an
   extraction-content difference, not an admission failure. Both arms produced
   and admitted a `validation/scope` note grounded in Msg_3463. The control
   body — "Totals, tolerance logic, period-close outputs, and refreshed
   end-of-period currency conversion feed" — drops the "targeted check" action
   and the impacted-close-cycle rerun, so both required atoms miss. The
   treatment body — "Widened to include targeted check on refreshed
   end-of-period currency conversion feed and one rerun of the impacted close
   cycle" — matches both atoms.
2. `abstention_4` leakage differs in kind, not in count. The control admits
   `owner/exception-log` with body "Compliance", promoting request language
   ("ask Compliance to own the exception log") into ownership state; the
   proposal-promotion defect from the 2026-07-17 r1 record persists in the
   control. The treatment instead records the qualified state ("ownership is
   shared between Legal, Reporting, and Compliance until the baseline
   stabilizes; Compliance is not sole owner early"), which is the correct
   abstention semantics but still trips the forbidden-atom regex
   (`compliance.{0,100}owner`). The treatment's leakage item is a scorer
   false positive; the matcher needs negation awareness.
3. `multi_hop_1` misses on both arms. Neither variant admits a note matching
   the Reporting-validation-deadline atom (by 2025-07-18, Msg_3651). With only
   three cases in this profile, the shared miss caps recall at 0.667
   regardless of variant.

## Gate verdict

The retainment gate requires the treatment to be materially faster or smaller
without reducing extraction fact recall or increasing leakage on this fixed
input.

- Faster: no. Mean primary duration +13%, P95 +49% (n = 5 calls per arm, high
  variance, but the treatment is not faster by any measure).
- Smaller: no. Output tokens +3.1%, input tokens -3.0%; neither is material.
- Recall: not reduced; improved 0.000 to 0.667 on one case.
- Leakage: not increased (1 = 1), and the treatment item is a scorer false
  positive.

The treatment is therefore not retainable as a cost optimization under the
stated gate, and the ADR status is unchanged. The unexpected signal is the
recall inversion in favor of the treatment on `knowledge_update_20`; with
n = 3 a single case decides it, so neither the recall win nor the latency loss
is cohort-significant. Before any variant decision: tighten the forbidden-atom
matcher for negation, extend the quick cohort beyond three cases, and
investigate why the control prompt paraphrases away the "targeted check" and
rerun actions from the same source message.

## Protocol notes

- The runner needed a bash 3.2 empty-array expansion fix
  (`scripts/extraction-eval-v1-canary.sh`); fixed in the worktree, not yet
  committed.
- Provider-call classification worked as designed: all 5 calls per arm are
  classified primary, and no summary, compaction, or verifier calls were
  consumed on this profile.
- Both arms completed on the first attempt with zero provider errors; the
  resume path was not exercised.
