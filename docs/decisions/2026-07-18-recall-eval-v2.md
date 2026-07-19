# Recall Eval v2: Hard Replay plus Verified Agent Recall

Status: Accepted

Date: 2026-07-18

Related: [Recall Eval v1](./2026-07-18-recall-eval-v1.md)

## Context

Recall Eval v1 made planner loss attributable, but the first tracked replay was
too small and too easy: several aggregate recall metrics saturated at 1.0. A
perfect score on a small replay does not show that recall is solved. It can
mean that the fixture lacks lexical distractors, superseded facts, tight
budgets, relation noise, identity changes, or temporal contrasts.

The existing Eval v2/v3 runner already launches an external Agent and records
its session, answer, recall-hook diagnostics, model usage, and judge result.
However, a generic completed Trial is not sufficient evidence for a
recall-specific claim. A command could finish without passive recall running,
without a provider call, or without a real judge session.

## Decision

Recall Eval v2 is a two-gate protocol. It reuses `PlanRecall`, `RecallNotes`,
the production Team Note HTTP provider, and the existing external-Agent runner.
It does not create an eval-only recall implementation.

### Gate 1: deterministic hard replay

Before any paid Agent Trial, the runner executes a fixed replay containing at
least 30 independent Cases. Repeating one Case with several budgets does not
increase the independent Case count. The replay keeps extraction items,
candidate scores, consumer identity, knowledge origins, observation time,
query, candidate limit, and budgets fixed.

The first v2 control cohort contains 30 balanced GroupMemBench Finance
questions and synthetic planner contrasts. It intentionally includes lexical
distractors, relation edges, stale or future candidates, abstention leakage,
cross-agent origins, and six tight-budget cases. Its answer atoms are coarse
generated controls, not reviewed GroupMemBench evidence annotations and not a
benchmark-quality claim. Reviewed persisted-note replay remains the stronger
replacement fixture; the protocol accepts it without changing the runner.

The replay reports candidate recall at lane limit, relation-expanded recall,
selected-set recall, delivered Conditional Recall, context precision, budget
drops, leakage, stage rejection reasons, plan version, and planner latency.
The run stops before launching Agents when the replay is absent, invalid, has
fewer than 30 unique Cases, or fails its configured policy gate.

### Gate 2: production recall consumed by a real Agent

The Agent cohort contains 30 to 50 unique questions. Each paired Trial receives
an isolated external Agent session. The three initial Arms are:

- `no_memory`, which measures what the same Agent can answer without recall;
- `team_note`, which forces the production `team-memory` provider and passive
  recall path before the Agent answers.
- `hint_recall_v0`, which uses an isolated Team Memory service with the Hint
  candidate enabled and permits the same Agent to call the existing bounded
  `active_recall` tool only after a labelled hint is exposed.

The Team Note Arm is scored only when all of the following evidence exists:

1. the consumer command completed and returned a non-empty Agent session ID;
2. that session ID is not reused by another Trial;
3. the recall hook observed a successful recall attempt;
4. at least one provider call occurred and the hook identifies the `team`
   provider used by the production `team-memory` adapter;
5. a judge completed and returned a distinct non-empty judge session ID.

The `no_memory` Arm is unscored if it observes any recall event or provider
call. Zero recall hits remain valid for abstention and true-absence Cases. A missing
hook or provider call is not converted into an incorrect answer. It is an
unscored protocol failure. `recall-agent-report.json` records every Trial's
disposition and reason, Agent execution coverage, judged candidate accuracy,
and paired wins, losses, and ties. Generic Token F1 remains diagnostic only.

The Hint Arm is unscored when its passive recall or `team` provider evidence is
missing, or when the Agent container does not emit active-recall
instrumentation. Zero active calls is a valid measured outcome; a positive
call count proves the actual Agent used the existing tool rather than the eval
runner calling recall directly.

The first Hint Recall activation run uses a fixed persisted-note observation
rather than re-running extraction over the 30,000-message source corpus. A
replayable seeder writes one non-evidentiary navigation lead and one separately
retrievable evidence Note per Case through `ApplyExtractionRun`, including the
source Agent, original user, observation time, and evidence event. Both Team
Note Arms share that scope; only the planner configuration differs. The normal
service keeps Hint Recall disabled, while the isolated Hint service uses the
candidate thresholds under test. The Agent still reaches both services through
the production HTTP provider and can obtain the evidence only through its real
bounded `active_recall` tool.

The observation time is pinned to `2026-07-18T00:00:00Z`; Eval-only leases
keep the resulting Notes recallable without changing production TTL policy.
Each extraction input checksum covers its candidate, source time, and evidence
event. The seed receipt records the canonical observation, Agent manifest, and
annotation SHA-256 digests, and the artifact manifest links that receipt.

This fixed-observation run measures hint exposure, Agent activation, focused
retrieval, and answer effect. Because the evidence Notes are constructed from
gold answers, its judge accuracy is not a GroupMemBench benchmark-quality
claim. A full-domain run remains a separate end-to-end extraction-plus-recall
gate and must not be used to attribute a recall change without first proving
that the required atom was extracted.

Hint-candidate generation has its own semantic threshold. Lowering it must not
admit ordinary evidence below the production evidence semantic threshold; the
trace must retain those candidates outside `SelectedSet` and `DeliveredItems`
unless a focused active query independently makes them evidence-eligible.

### Identity, knowledge source, and time

The original Asking User and a deterministically selected Answering Agent are
held constant across paired Arms. The Answering Agent excludes reviewed
supporting agents when a Case has Knowledge Source annotations. The cohort
artifact separately reports participant coverage, identity-dependent Cases,
reviewed Knowledge Source coverage, and strict cross-agent coverage; unknown
source identity is never presented as strict cross-agent evidence.

Every Agent session receives the same run-scoped fixed observation, while each
Trial has a fresh delivery session. Temporal and
knowledge-update categories remain visible slices. Deterministic replay pins
Observation Time and candidate validity; future and invalid candidates are
expected to be rejected before selection. A later reviewed cohort should add
explicit `as-of` and `changes-since` cases rather than inferring those modes
from current-date questions.

### Policy-candidate use

The deterministic gate is the recall-policy comparison loop. It can run the
same snapshot with the current policy and a candidate policy, including legacy
versus Relation Expansion Marginal Utility, without spending model tokens.
The Agent gate validates that the chosen production build actually reaches and
helps an Agent. It does not treat the no-memory Arm as a substitute for a
paired policy A/B. A future live policy A/B must run isolated Team Memory
services with pinned build/config provenance; it must not mutate one global
policy between concurrent Trials.

## Acceptance

A Recall Eval v2 result is valid only when deterministic replay completes and
Agent execution coverage is 1.0. Any unscored Trial invalidates the run-level
quality claim. Candidate retrieval, relation expansion, selection, budget,
Agent answer, and judge results must all be present in the artifact set.

The hard cohort intentionally includes relation-only atoms, so its direct
candidate-recall floor is 0.80 and relation-expanded recall must recover to at
least 0.95. Conditional Recall remains at least 0.90, with no
superseded-fact leakage, no material category regression, and no more than five
percent of available atoms lost to budget. Agent acceptance additionally
requires no category, temporal, or strict cross-agent regression and reports
paired judge wins, losses, and ties instead of relying on Token F1. Agent
artifacts calculate accuracy deltas by category, temporal mode, strict
cross-agent status, and reviewed versus unknown Knowledge Source status; a
negative slice delta fails the run.

## Consequences

The 34-case deterministic baseline is no longer saturated: 30 independent hard
questions plus four reviewed Relation Expansion Marginal Utility cases produce
candidate recall 0.879, relation-expanded recall 1.00, selected-set recall
0.939, delivered Conditional Recall 0.909, context precision 0.603, one
budget-dropped atom, and three missed available atoms. Current relation utility
plans 1245 tokens versus 1352 for legacy
relation packing. These numbers are control-fixture results, not end-to-end
Agent results.

The external Agent cohort cannot silently degrade into direct API replay. A
developer can inspect the exact session and recall evidence for every scored
Trial. The cost is a stricter bootstrap: incomplete source annotations are
reported honestly, and a production Agent run requires the local evaluation
stack, model credentials, extraction completion, and judge availability.
