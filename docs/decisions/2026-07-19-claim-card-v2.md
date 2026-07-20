# Claim Card v2 exact values

Date: 2026-07-19

## Context

The `claim-card-v1` fixed micro3 canary rendered cards deterministically but
matched none of three required atoms. The model used abstract Claim values such
as a validation category plus a rerun, omitting the answerable qualifiers
`targeted` and `impacted close cycle`. Those terms had previously survived in
free-form candidate bodies.

## Decision

Add `claim-card-v2`, with the same deterministic rendering and source-actor
provenance as v1, but a distinct extraction/episode protocol. It requires
`claim.value` to be the shortest contiguous quoted source phrase carrying one
answerable value or condition. Qualifiers, negation, ordering, and actor roles
must remain in that value. Compound source statements produce separate primary
claims and state decisions.

The candidate body remains deterministic and no recall behavior changes.

## Evaluation gate

Rerun the same fixed micro3 fixture. It must exceed v1's zero atom recall and
avoid increasing the three observed abstention leakage items before any larger
or paid agent cohort is authorized.
