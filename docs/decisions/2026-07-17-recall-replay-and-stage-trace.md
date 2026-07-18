# Recall Stage Trace, Deterministic Replay, and Cohort-Validated Ranking

Status: Accepted

Date: 2026-07-17

Related: Rolling Extraction Context, General Recall v3 Optimization

## Context

The rolling extraction evaluation (2026-07-16) showed the largest loss had
moved to recall: 7 of 23 required atoms delivered in each arm, with 3–5
available atoms lost after extraction. Two structural gaps made recall
expensive to optimize safely:

- `PlanRecall` discarded every rejection reason, so no stage attribution
  existed for candidate retrieval, relation expansion, selection, and budget
  packing.
- No deterministic replay fixture existed, so each retrieval experiment
  required a paid end-to-end cohort. The one such experiment (threshold 0.60,
  run r4) regressed judge accuracy without explaining why.

## Decision

**Recall Trace behind the `PlanRecall` seam.** `PlanRecall` now returns a
`RecallTrace` recording, per candidate: fusion-lane membership, relevance-gate
rejection, duplicate suppression, max-items stop, token-budget drop, and
delivery-claim loss. The Postgres adapter persists the trace as a JSONB column
on `team_note_recall_observations` (migration 013), and stage capture exports
it in the recall observation provenance. `RecallNotes` is unchanged.

**Deterministic replay fixture.** `cmd/team-memory-recall-replay -export`
captures a fixed cohort (schema `pax-recall-replay-v2`): the matching
production recall observation time, all recall-eligible
persisted Team Notes with the exact lexical and semantic scores produced by
the live retrieval code path, the extraction snapshot, the recall request, and
the gold atoms. The default mode replays `PlanRecall` with an overridable
policy and scores deliveries with the unchanged stage evaluator. The tracked
fixtures cover the ten gold cases in both arms, exported from the
`team-note-optimization-30-20260716-c20fdd7` store; the r3 rolling-run store
was not retained, so replay validation pins this cohort instead.
The tracked legacy v1 fixtures remain readable by inferring observation time
from their latest captured candidate timestamp, or the Unix epoch for an empty
candidate set. New exports require the explicit v2 field so current and
future-validity decisions match production.

**Cohort-validated ranking changes.** Measured on both arms (20 case-arms),
not one case:

| Policy | Gold recall | Conditional recall | Context precision | Leakage |
|---|---|---|---|---|
| 0.65 baseline | 0.261 / 0.348 | 0.750 / 0.700 | 0.124 / 0.245 | 0 |
| 0.55 | 0.304 / 0.348 | 0.875 / 0.700 | 0.134 / 0.199 | 0 |
| **0.50 + dedup + degrade** | **0.348 / 0.435** | **1.000 / 0.900** | 0.141 / 0.224 | 0 |
| 0.45 | 0.348 / 0.435 | 1.000 / 0.900 | 0.138 / 0.221 | 0 |

(team_note / team_note_hybrid; precision is the per-case mean.)

Adopted as production defaults:

- `TEAM_MEMORY_SEMANTIC_THRESHOLD` default 0.65 → **0.50**. Trace evidence
  showed every gold atom lost at 0.65 died with semantic scores 0.46–0.57 at
  the fusion gate or relevance gate; 0.50 recovers them on both arms while
  0.45 adds nothing and only dilutes. The candidate limit stays 16: lost gold
  notes had lexical score 0, so no lane limit would have admitted them.
- **Duplicate suppression**: a candidate whose body term-set Jaccard overlap
  with an already selected note reaches 0.8 is skipped, and selected notes are
  kept out of later `[related:]` blocks. Subject equality alone does not
  suppress: distinct agent reports on one subject are distinct facts (pinned
  by `TestStatusIdentityPreservesDifferentAgentReports`). On the rolling r3
  capture, three phrasings of one fact consumed three of seven delivered
  slots; body-overlap suppression targets that pattern.
- **Budget degradation**: when a note with its related block exceeds the
  remaining budget, the note is retried without the block before being
  dropped. Budget drops per cohort fell only modestly because more candidates
  now pass the gate; the mechanism protects long composed notes.

## Consequences

Recall policy can now be iterated at zero marginal cost: export once, then
every ranking change is a replay run with per-stage metrics. Live recalls
carry the trace in the observation, so future live runs are replayable
without a bespoke export.

The semantic threshold is a validated default, not a tuned constant: judge
accuracy on the paid end-to-end cohort remains the acceptance gate before
further threshold movement. The r4 regression at 0.60 happened on different
extraction state (rolling) and does not contradict this cohort result; it is
evidence for the methodology of pinning extraction when calibrating recall.

Known limits: the replay cohort is ten cases exported from a slice-extraction
run; rolling-extraction notes (the new default mode) should be exported from
the next live run and compared. One available atom in the hybrid arm still
dies at token budget because the note ranks below denser competitors; budget
re-prioritization is deliberately out of scope. Judge-accuracy validation
requires the paid cohort:

```bash
make eval-v2-up
go run ./cmd/team-memory-eval-v2 -config <cohort-config>
```

## Live validation addendum (2026-07-17, mini cohort)

A four-case live cohort
(`runs/eval-v2/groupmembench-finance-v2-recall050-mini-20260717-r1`, rolling
extraction, threshold 0.50, dedup and degrade enabled) against the r3 rolling
run at threshold 0.65 on the same cases:

- Stage conditional recall: knowledge_update_5 0.0 → 1.0 (both arms),
  team_note_hybrid multi_hop_39 0.5 → 1.0; no case regressed.
  team_note_hybrid multi_hop_15 stayed 0.5, matching the replay prediction
  that its last atom dies at token budget.
- Judge: team_note knowledge_update_5 flipped false → true; multi_hop_15
  flipped true → false (single-case noise); multi_hop_39 team_note failed on
  an infrastructure kill before recall. team_note_hybrid judge outcomes were
  unchanged, including knowledge_update_5 remaining false despite full atom
  delivery — answer correctness has drivers beyond delivered facts.
- Live recall observations now carry the stage trace (e.g.
  knowledge_update_5: 36 candidates, 16 fusion-kept, 7 planned at 475/500
  tokens, rejections attributed per stage), confirming the trace plumbing in
  production.

The stage-level gain reproduces live; judge accuracy on the full ten-case
cohort remains the acceptance measure.

## Eval v3 micro-canary addendum (2026-07-17)

The three-case Eval v3 micro-canary exposed two recall-policy defects and one
evaluation-provenance defect. A frozen two-case replay now tracks the 463
eligible Team Notes per case, the captured `max_items=5` constraint, exact
lexical and semantic scores, and the shared Eval v3 Team Note scope.

The policy changes are structural rather than a wider retrieval setting:

- scalar queries rank subject-specific answers ahead of adjacent high-scoring
  facts;
- current/latest queries rank explicit query coverage first, then traverse
  one-hop relations and prefer the newest related fact;
- a composed delivery records every source Note ID, allowing conditional
  recall to credit a selected related Note instead of only the outer anchor.

At the unchanged semantic threshold 0.50, candidate limit 16, token budget
500, and max-items limit 5, the fixed replay moved from 0/3 to 3/3 required
atoms for both Gold Recall and Conditional Recall. Missed available atoms fell
from 3 to 0, and superseded-fact leakage in delivered context fell from 1 to
0. Extraction leakage remains observable separately and is not attributed to
recall. This replay result does not replace a later paid end-to-end judge run.
