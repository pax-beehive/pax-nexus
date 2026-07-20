# Claim Card v1 extraction candidate

Date: 2026-07-19

## Decision

Add `claim-card-v1` as an opt-in extraction candidate. It reuses extraction
v2's single model call and its Claim and StateDecision products, but does not
persist the model-authored Candidate body, kind, subject, identity, or related
subjects.

Every create, update, and resolve decision must reference exactly one admitted
primary Claim. Deterministic code renders the persisted note from that Claim's
type, subject, predicate, value, speaker, source temporal fields, and the
task/thread scope. The note identity is a stable digest of scope, subject, and
predicate. The StateDecision still supplies the transition action and its
admitted evidence/temporal window.

## Rationale

The rejected source-span experiments preserved raw evidence but did not make
the final answerable state more retrievable. The existing v2 extractor already
separates source claims from state decisions, but ordinary decisions can still
persist a free-form model Candidate body. Claim cards make that final rendering
inspectable and stable without adding a provider call or changing recall.

## Boundaries

- This is extraction-only. `RecallNotes`, `PlanRecall`, ranking, relation
  expansion, authorization, and token packing have no candidate-name branch.
- Claim-card output is a normal Candidate and NoteStore input. Existing generic
  recall consumes it through the ordinary note kind and body fields.
- Source identity is the cited Session Event actor, not the model-provided
  Claim speaker; it is not an authority assertion. Temporal expressions and
  valid/invalid windows retain the v2 admission
  rules; a timestamp without source language is discarded.
- A direct decision with zero or multiple primary claims is rejected for this
  candidate instead of falling back to the model body. That makes incomplete
  structured extraction observable in the trace.

## Evaluation gate

First evaluate against the fixed extraction fixture using the current recall
configuration only as a persisted-note consumer. Compare atom availability,
temporal correctness, source identity, rejected decisions, leakage, note-body
size, and provider cost against `current` and `typed-2`. Run the paid agent
cohort only if extraction atom recall improves without additional leakage.
