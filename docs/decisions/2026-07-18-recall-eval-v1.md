# Recall Eval v1: Fixed-Extraction Stage Waterfall

Status: Accepted

Date: 2026-07-18

Related: Recall Stage Trace, Deterministic Replay, and Cohort-Validated Ranking

## Context

End-to-end answer accuracy cannot identify whether a required fact was lost by
extraction, candidate retrieval, relation expansion, set selection, or token
packing. Gold Recall also includes facts absent from extraction, so optimizing
it as a recall-policy score would attribute upstream loss to the wrong module.
The existing deterministic recall replay already pins persisted Team Notes,
adapter scores, requests, observation time, and gold atoms behind the shared
`PlanRecall` seam, but its aggregate report does not expose an atom-level loss
waterfall.

## Decision

Recall Eval v1 holds extraction fixed and evaluates recall as:

```text
available atom -> bounded candidate lanes -> one-hop relation expansion
               -> selected set -> token-budget packing -> delivered context
```

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

## Consequences

Recall policy iteration has zero marginal model cost and every missed available
atom has a deterministic stage attribution. Changes to extraction or embedding
models require a new fixture provenance and snapshot digest rather than being
compared inside one recall run. Authorization remains an adapter concern and is
validated in the live cohort; replay fixtures contain only the recall-eligible
candidate set visible to the captured actor.
