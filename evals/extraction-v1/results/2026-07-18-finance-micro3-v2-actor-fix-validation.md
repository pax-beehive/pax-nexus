# Finance micro3 actor-fix and fixture-pattern validation r2

Date: 2026-07-18 (America/Los_Angeles)

Validates two defect fixes against the fixed quick cohort:

1. Interaction-actor omission (defect: the interaction channel was dead).
   `interactionPromptV2Current` never named the `actor` field, so the model
   left it empty and deterministic validation discarded every interaction
   observation (33 of 33 on the quick baseline; 1,584 of 1,584 on the full
   cohort). Fix: the prompt now spells out the observation object, marks
   `actor` required (the cited event's speaker), lists the speech-act
   vocabulary, and states that actor-less observations are discarded.
   Protocol revision bumped `v2-slim-2` to `v2-slim-4`.
2. Forbidden-atom pattern coverage (defect: scorer false negatives). The
   `abstention_4` pattern only matched the nouns `owner`/`designated` near
   `compliance`. Fix: pattern now covers verb forms with word boundaries,
   `\b(designated|owners?|owns|maintains)\b` in both directions. Word
   boundaries also stop `ownership` from false-matching, so correct shared-
   ownership statements no longer even raw-match.

Validation run ID: `groupmembench-finance-micro3-v2-canary-20260718-r2-current`
Baseline: `groupmembench-finance-micro3-v2-canary-20260718-r1-current`
Same profile (`finance-micro3-quick`), source, fixtures (pattern excepted),
and extractor variant `current`.

## Result

| Metric | baseline r1 | validation r2 |
|---|---:|---:|
| Fact recall (3 cases) | 0.000 | 0.000 |
| Raw leakage items | 1 | 2 |
| Suppressed leakage items | n/a | 0 |
| Admitted notes | 16 | 14 |
| Interaction rejections | 33 | 0 |
| State decisions / decision rejections | 21 / 9 | 17 / 15 |
| Primary / summary calls (errors) | 5 / 0 (0) | 5 / 1 (0) |
| Output tokens | 34,091 | 37,655 |
| P95 primary duration | 68.9 s | 80.3 s |

## Verdicts

- Defect 1 verified: interaction rejections dropped 33 to 0. Kept
  observations now feed the speech-act admission guard (decision rejections
  rose 9 to 15, notes 16 to 14), which is the guard working with restored
  inputs rather than a new failure.
- Defect 2 verified: both leakage items are genuine and audited.
  `owner/exception-log` ("Compliance owns the exception log.", Msg_28687)
  matches via the new `owns` verb form; `state/current-universe-frozen`
  ("Current provider set is frozen as a controlled freeze.") cites forbidden
  event Msg_16876 and counts through the event-id path. Both unsuppressed,
  both recorded in `leakage_details`. Negation suppression was not triggered
  because neither leak is a qualified statement.
- Leakage counts before and after this date are not comparable: the widened
  pattern and the event-id audit change abstention scoring semantics.

## What did not move, and why

- `multi_hop_1` recall is still 0, and the mechanism is now pinned: no state
  decision cites Msg_3651 at all. The model reads "I'm leaning toward ..."
  as proposal-only and, following the interaction contract, emits no state
  transition for the deadline. The open design question is a capture path
  for proposal-carried concrete facts (owner, deadline, value) with hedged
  bodies, without reopening abstention-style ownership promotion. This needs
  an explicit protocol decision, not a prompt tweak.
- `knowledge_update_20` recall is still 0 (paraphrase loss of "targeted
  check"/rerun) and proposal-promoted ownership notes still appear; both are
  extraction-content iteration targets for the next prompt/protocol round.

## Protocol notes

- Zero provider errors, no resumption. One periodic summary call was
  consumed and classified separately on this run.
- Fixture change is data-only: `evals/extraction-v1/groupmembench-finance-micro6.json`.
- Code changes: `internal/teamnote/extractor/v2_prompt.go` (interaction
  contract + revision bump). Extractor and stageeval package tests pass.
