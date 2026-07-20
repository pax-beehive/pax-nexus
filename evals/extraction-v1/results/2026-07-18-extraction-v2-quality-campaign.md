# Extraction v2 quality campaign: admission hardening and stability series

Date: 2026-07-18 (America/Los_Angeles)

Objective: reliable extraction scoring stably >= 0.6 on the fixed 6-case
Finance quick cohort (`finance-micro6-quick`), within a US$20 run-cost
budget. This record covers the deterministic fix series, the mechanism
evidence, and the stability measurement.

## Deterministic fix series (all unit-tested, lint green)

- Fix A: removed the speech-act admission veto. Interaction observations are
  recorded but no longer gate decisions; non-committal evidence is judged by
  inspecting the cited source text only. Root cause: the model misclassifies
  speech acts often enough that the veto killed correct decisions
  (`express_concern` on a direct blocker statement, `propose` on a
  past-tense scope change).
- Fix B: temporal metadata without a source expression is stripped instead
  of rejecting the whole decision.
- Fix C: an already-past `invalid_at` is dropped on create (a stillborn note
  is worse than a live note carrying its deadline in the body).
- Fix D: `unresolved` resolutions drop their validity timestamps instead of
  rejecting.
- Fix E (scoring side): extraction items now render `[valid_at: DATE]` /
  `[invalid_at: DATE]` so facts the extractor placed in the structured
  temporal window are credited (matches what recall consumers read).
- Fix F: a dangling claim reference no longer voids direct decision
  evidence; the dead reference is skipped.
- Supporting eval work: negation-aware leakage scoring with per-item audit
  details, widened forbidden pattern (verb forms + word boundaries),
  interaction-actor prompt contract (`v2-slim-4`), expanded 6-case cohort
  with annotated supporting events and a direct-DB-ingested 2,595-event
  scope.

## Mechanism evidence (trace-level, per missed case)

- `user_implicit_7`: perfect blocker decision vetoed by speech-act
  misclassification -> fixed by A (hit in r2, r3, r2-stable-slim varies).
- `knowledge_update_20`: speech-act veto / temporal rejection / dangling
  claim reference / later supersede-away of the gold body -> A, B, F address
  the first three; supersession is prompt-level.
- `temporal_7`: deadline resolved into `valid_at` was invisible to scoring
  -> fixed by E (hit in r2-stable-slim; r2-stable-current wrote the wrong
  date, model resolution error).
- `term_ambiguity_11`: kept note died from past `invalid_at` -> fixed by C
  (hit in r2-stable-current). The BI-vs-Finance-Ops ambiguity trap still
  fires in some runs (extraction discrimination, adversarial by design).
- `multi_hop_1`: source is genuine leaning language; the source-language
  guard correctly rejects it. Protocol-faithful miss, not a defect.
- `abstention_4`: ownership promotion and forbidden-event citation vary per
  run (0-4 raw leaks); the negation-suppression audit path works (1
  suppressed item correctly identified in stable-r1-current).

## Stability series (6-case quick profile, both variants)

Code: r1 = Fix A-E; r2/r3 = Fix A-F. Cost ~US$0.09 per paired run.

| Run | current | interaction-slim |
|---|---:|---:|
| stable r1 | 0.000 (leak 4 + 1 suppressed) | 0.333 (leak 2 + 1 suppressed) |
| stable r2 | 0.333 (leak 0) | 0.333 (leak 3) |
| stable r3 | 0.333 (leak 1) | 0.333 (leak 2 + 1 suppressed) |

Series mean recall 0.278; the last five arms all landed exactly at 0.333,
so per-run variance is narrowing around a 0.33 plateau rather than toward
the 0.6 target. knowledge_update_20 hit in 4 of 6 arms; temporal_7 and
term_ambiguity_11 hit once each; user_implicit_7 never hit in the series;
multi_hop_1 is a protocol-faithful miss. Raw leakage ranged 0-4 per arm
with no stabilization.

## Cost accounting (this campaign, all paid runs)

Blended rate implied by the 2026-07-17 micro6 record (~US$0.145/M tokens):
roughly US$1.0-1.2 total across 14 completed paid runs (3-case pairs,
full-cohort run, 6-case pairs, stability series) — about 6% of the budget.
A paired 6-case quick run costs ~US$0.09 and ~25 minutes wall time.

## Model comparison (deepseek-v4-flash vs deepseek-v4-pro)

The endpoint offers exactly two models. The flash stability series above
plateaus at 0.333, so one 6-case pair was run with `deepseek-v4-pro`:

| Run | model | recall | leakage |
|---|---|---:|---:|
| pro r1 current | deepseek-v4-pro | 0.667 (4/6: KU20 both atoms, temporal_7, user_implicit_7) | 1 |
| pro r1 interaction-slim | deepseek-v4-pro | TBD | TBD |

First target-line crossing of the campaign. Stability requires repetition;
pro r2/r3 confirmation runs follow.

## Interim verdict (pending r3)

The deterministic admission layer is no longer the recall bottleneck:
decision rejections dropped to background levels, dead notes are rescued,
and temporal-window facts are scored. The residual is per-run model
behavior variance on an adversarial 6-case cohort: wrong date resolution,
actor dropped from note bodies, facts not produced, ownership promoted.
Single-run recall across the series spans 0.0-0.5 and the mean is well
below the 0.6 target. Final verdict and recommended path follow once r3
completes the distribution.
