# Finance micro3 "Proposed:" marker experiment r3 (negative result, reverted)

Date: 2026-07-18 (America/Los_Angeles)

Run IDs: `groupmembench-finance-micro3-v2-canary-20260718-r3-current`,
`groupmembench-finance-micro3-v2-canary-20260718-r3-interaction-slim`

Baseline: r2 (`...-r2-current`, actor fix + strict guard) and r1
(`...-r1-interaction-slim`).

## What was tried

A capture path for proposal-carried concrete plan parameters (the
`multi_hop_1` deadline gap), built as:

- Guard: `stateDecisionAdmissionReason` skipped both non-committal rejections
  for `create` decisions whose candidate body starts with `Proposed: `.
- Prompt (both variants): proposals still cannot create state, except
  uncontested concrete plan parameters (date, deadline, value, scope,
  condition) captured with a `Proposed: ` body prefix; ownership, approval,
  and authority assignments stay strict and contested ones use
  `keep_conflict_open`.
- Candidate body preservation rule gained "actions" (keep verbs like
  "targeted check" and "rerun").
- Protocol revisions bumped to `v2-slim-5` / `v2-slim-5-interaction-slim`.

## Result

| Metric | r2 current | r3 current | r1 slim | r3 slim |
|---|---:|---:|---:|---:|
| Fact recall (3 cases) | 0.000 | 0.000 | 0.667 | 0.667 |
| Raw leakage | 2 | 0 | 1 | 5 |
| Admitted notes | 14 | 1 | 13 | 25 |
| State decisions | 17 | 2 | 21 | 34 |
| Decision rejections | 15 | 31 | 8 | 6 |

## Failure mechanisms

1. `current` collapsed (14 to 1 notes): the restored speech-act guard (actor
   fix) now sees every `propose` act, and the strengthened strict language
   pushed the model to frame more content as proposals without adopting the
   marker path. 19 rejections for "non-committal speech act" and 12 for
   "non-committal source proposal or request"; unreviewed events rose 10 to
   37. The two prompt rules (strict base rule vs. marker exception) conflict
   in the model's reading, and the strict rule dominates.
2. `interaction-slim` over-admitted (leakage 1 to 5): the model did adopt
   the marker path — including ownership assignments, e.g. "Proposed:
   Compliance names the exception log owner for the July 19 control
   snapshot." The bypass is modality-based, not content-aware, so marked
   authority claims were admitted. 4 of the 5 leaks cite forbidden event
   Msg_16876; 1 matches the widened `unsupported_compliance_owner` pattern.
   The prompt's "ownership never uses this path" clause did not hold.
3. `multi_hop_1` stayed missed on both arms: even with the capture path
   available, neither variant produced a deadline decision. Availability of
   the path is not sufficient; the model does not spontaneously route
   leaning-language facts to it.

## Decision

Full revert to the r2 protocol state: guard restored (no bypass), prompts
restored (`v2-slim-4` current with the actor contract, `v2-slim-3` slim),
marker tests removed. `git diff HEAD` on `v2.go` and `v2_test.go` is empty;
`v2_prompt.go` differs only by the actor contract and the `v2-slim-4`
revision. Extractor package tests pass.

Kept from earlier rounds (unaffected): negation-aware leakage scoring with
audit details, the widened forbidden pattern, the actor contract itself, and
the bash 3.2 runner fix.

## What r3 teaches the next design

- A modality marker alone cannot gate a capture path: the guard bypass must
  be content-aware (reject authority/ownership claims deterministically,
  e.g. via identity_ref/subject conventions), or the path must be limited to
  a mechanically checkable fact class (e.g. bodies containing an explicit
  date anchored to a cited event).
- The strict rule and the exception cannot be advertised with equal weight
  in one prompt; the model resolves the conflict toward strictness in
  `current` and toward permissiveness in `interaction-slim`. A single-rule
  formulation (one sentence, no "two exceptions" structure) is required.
- One case (`multi_hop_1`) should not drive a global protocol change without
  a second case of the same class in the cohort; cohort expansion
  (`temporal_7` also carries a 2025-07-18 deadline) is a prerequisite for
  trusting any capture-path variant.
- Alternative worth considering: treat the `multi_hop_1` miss as
  protocol-faithful abstention on leaning language and question the fixture
  gold instead of the extractor. The r3 evidence (both arms still miss) is
  consistent with the model actively judging the source non-committal.
