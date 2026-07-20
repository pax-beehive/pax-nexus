# Recall Eval v1: Fixed-Extraction Stage Waterfall

Status: Accepted

Date: 2026-07-18

Related: Recall Stage Trace, Deterministic Replay, Cohort-Validated Ranking,
and [Hint Recall v0](./2026-07-16-hint-recall-v0.md)

## Context

End-to-end answer accuracy cannot identify whether a required fact was lost by
extraction, candidate retrieval, relation expansion, set selection, or token
packing. Gold Recall also includes facts absent from extraction, so optimizing
it as a recall-policy score would attribute upstream loss to the wrong module.
The existing deterministic recall replay already pins persisted Team Notes,
adapter scores, requests, observation time, and gold atoms behind the shared
`PlanRecall` seam, but its aggregate report does not expose an atom-level loss
waterfall.

Hint Recall adds two failure modes that ordinary planner replay cannot measure:
an otherwise useful lead may be ineligible for the consuming agent, and a lead
may point to evidence that is stale, future-dated, or valid only at a different
query time. Agent activation is also distinct from hint opportunity quality.
Recall Eval v1 therefore defines identity-aware and temporal-aware contracts
for deterministic hint evaluation before any paid consumer cohort.

## Decision

Recall Eval v1 holds extraction fixed and evaluates recall as:

```text
available atom -> bounded candidate lanes -> one-hop relation expansion
               -> selected set -> token-budget packing -> delivered context
```

Relation reachability means a structurally connected, authorization- and
temporal-eligible one-hop note. Relation expansion additionally requires query
relevance. Both are traced for every ranked primary before shared item and
token budgets are packed, so an unvisited primary cannot hide a budget loss.

### Identity and knowledge origin

Every identity-aware Case pins a **Recall Consumer** consisting of the asking
user, answering agent, and recipient session. The asking user and answering
agent remain logically identical across paired Arms, while each Arm receives an
isolated scope and session so delivery history cannot leak between Trials. A
focused active-recall call inherits the authenticated Recall Consumer from the
passive opportunity; tool arguments cannot replace its user, agent, or scope.

Every candidate pins its **Knowledge Origin**: the user, agent, and source
session that produced its evidence. Knowledge Origin is provenance, not the
Recall Consumer and not the process that performed extraction. Supporting and
answering agent identities must remain stable across Arms so reports can
distinguish same-agent, same-user cross-agent, authorized cross-user, and strict
cross-agent Cases. A strict cross-agent Case requires the Answering Agent to be
absent from the supporting-agent set.

An **Available Atom** remains actor-neutral: the required fact is present in
the fixed extraction Observation. An **Eligible Atom** is an Available Atom
with at least one supporting Note that the Recall Consumer may access and that
is valid for the query's temporal interpretation. Consumer-scoped recall and
hint-opportunity metrics use Eligible Atoms as their denominator. An
unauthorized Available Atom is not a recall miss; it must instead have zero
influence on candidate admission, Hint Score, lead construction, focused query,
and delivered context.

Planner replay contains only candidates already admitted by the captured
adapter and cannot prove that excluded knowledge had zero influence. Recall
Eval v1 therefore adds a deterministic identity contract through the real
`RecallNotes` interface and a production NoteStore adapter. Paired fixtures
compare the same Case with and without a highly relevant unauthorized Note and
require identical hint disposition, score, and focused query.

### Temporal semantics

Every Case pins three distinct times:

- **Observation Time** is when passive recall and its focused active-recall
  intervention occur;
- **Query Time** is the semantic time compiled from `current`, `as-of`, or
  `changes-since`; and
- **Source Time** is when the supporting evidence occurred.

Current authorization is evaluated for the Recall Consumer at Observation
Time. Factual validity is evaluated at Query Time. A historical query may
retrieve a fact that was valid then and is superseded now, but it never restores
authorization that the consumer no longer has. Passive recall and every
focused intervention use the same Observation Time and fixed candidate
snapshot, so the intervention cannot observe later writes.

For a current query, Query Time equals Observation Time. For an as-of query it
is the requested instant, and for changes-since it is the requested lower
boundary paired with the fixed Observation Time.

`ValidAt` and `InvalidAt` define semantic validity. `SourceOccurredAt` records
evidence time. `UpdatedAt` is storage mutation time and must not replace either
one. Focused queries preserve the original temporal mode, boundary, timezone,
and requested fact type. Current queries reject invalid and future facts;
as-of queries may use only evidence valid at their requested instant; and
changes-since queries measure changes after their fixed boundary.

The external `RecallNotes` interface remains unchanged. The Evaluation context
deepens the existing recall replay module instead of creating a second recall
implementation. A replay fixture records each required atom's supporting Team
Note IDs and a digest of the pinned candidate snapshot. Legacy replay fixtures
are upgraded in memory by matching their required-atom patterns against the
fixed extraction observation.

Every run emits:

- candidate recall at the configured lane limit;
- relation-expanded recall;
- selected-set recall;
- delivered Conditional Recall;
- mean context precision;
- available atoms lost to item or token budget;
- superseded-fact leakage; and
- a Recall Loss Ledger naming the first stage and reason that lost each atom.

The runner times only `PlanRecall`, records nanoseconds per case, and reports
the cohort mean and nearest-rank P95. Stage scoring and artifact I/O are outside
that latency measurement.

### Hint Recall evaluation

Hint Recall is evaluated as three separate control loops that share the same
product seams rather than a second recall implementation:

1. **Planner opportunity replay** runs passive planning, constructs a safe lead
   and focused query, and replays active recall within one- and two-call budgets.
   A positive Hint Opportunity retrieves at least one new Eligible Atom absent
   from passive evidence without forbidden, unauthorized, future, or
   superseded leakage.
2. **Identity and temporal contract evaluation** calls the real `RecallNotes`
   interface without an agent. It verifies authorization, audience, provenance,
   temporal intent preservation, hint deduplication, and the rule that returning
   a hint does not claim its underlying Note as delivered.
3. **Consumer evaluation** runs an isolated `hint_recall_v0` agent Arm against
   unchanged passive and hybrid baselines. It measures whether the hint reaches
   the agent, whether the same authenticated agent calls active recall, and
   whether the final answer improves.

Hint fixtures pin the Recall Consumer, Knowledge Origins, supporting agents,
audience or eligibility decisions, Observation Time, temporal intent and
boundary, candidate validity, source evidence IDs, passive and active budgets,
and candidate snapshot digest. Reports slice opportunity and delivery metrics
by identity relationship and temporal mode instead of allowing an aggregate
score to hide a cross-agent or historical regression.

In addition to the base recall waterfall, hint stage reports emit:

- Evidence Score and Hint Score precision, recall, Brier score, and calibration
  error over Eligible Atoms;
- focused active-recall success at one and two calls and new Eligible Atom gain;
- unauthorized hint influence, provenance-attribution errors, and wrong-time,
  future, forbidden, and superseded leakage;
- hint exposure, agent activation, useful, unnecessary, and failed tool calls;
- answer-judge accuracy and paired wins, losses, and ties against passive and
  hybrid Arms; and
- hint and active-recall latency, token use, and cost.

Gold Recall remains an outer diagnostic. Token F1 is not an acceptance metric.
Extraction output, embedding scores, query, actor, observation time, candidate
limit, item limit, and token budget are fixed while recall policy is calibrated.

## Acceptance

A recall policy candidate reaches the paid end-to-end cohort only when the
fixed acceptance cohort has candidate recall at lane limit at least 0.95,
Conditional Recall at least 0.90, no superseded-fact leakage, no material
category regression, and no more than five percent of available atoms lost to
budget. Context precision, planned tokens, and planner latency are
non-regression gates relative to the recorded baseline.

A Hint Recall candidate additionally requires zero unauthorized hint influence,
zero provenance-attribution errors, and zero wrong-time, future, forbidden, or
superseded leakage on the fixed identity/temporal matrix. Its Hint Score must
clear the unchanged paxm gates without score inflation. It remains
evaluation-only until the real-agent Arm improves judge accuracy or safe
success over passive and hybrid baselines without a material regression in any
identity relationship or temporal mode.

## Consequences

Recall policy iteration has zero marginal model cost and every missed available
atom has a deterministic stage attribution. Changes to extraction or embedding
models require a new fixture provenance and snapshot digest rather than being
compared inside one recall run. Planner replay fixtures contain only the
recall-eligible candidate set visible to the captured actor; adapter
authorization and temporal behavior are validated separately through the
deterministic `RecallNotes` contract before the live agent cohort. Hint policy
changes remain behind `PlanRecall`, preserving `RecallNotes` as the external
module interface.
