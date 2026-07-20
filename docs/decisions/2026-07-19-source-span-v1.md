# Source Span v1 extraction candidate

Date: 2026-07-19

## Decision

Add `source-span-v1` as an opt-in extraction candidate. It persists one
immutable raw source span for each newly observed task/thread slice. The model
may label only the span's `subject` and `topics`; deterministic code supplies
the body, event identifiers, actor identity, event timestamps, task, and
thread from the source events.

The candidate never asks the model to create, update, resolve, or summarize
canonical collaboration state. If the model returns empty metadata, a
deterministic `session source span` label is used and the raw source is still
persisted.

## Boundaries

- `source-span-v1` is an extraction candidate selected with
  `EXTRACTION_CANDIDATE_STRATEGY`; it has a distinct extraction-run version so
  it cannot replay a state-normalizing v2 run for the same slice.
- Source spans use the ordinary `Candidate` and `NoteStore` seams. No
  `RecallNotes`, `PlanRecall`, scoring threshold, relation rule, or budget rule
  branches on this candidate name.
- `source_span` is a conservative note kind whose rendered certainty is
  proposed. Agents must treat the delivered text as cited source material, not
  a normalized current-state assertion.

## Evaluation

Evaluate extraction separately from recall. The source-span cohort should
measure source-event retention, metadata coverage, provenance correctness,
and raw-span token cost. Existing recall replay and end-to-end answer judging
remain unchanged consumers; they must not gain candidate-specific behavior.
