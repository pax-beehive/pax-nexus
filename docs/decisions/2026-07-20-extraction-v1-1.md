# Extraction v1.1

Status: Experimental

Date: 2026-07-20

Related:

- [Extraction v2](./2026-07-16-extraction-v2.md)
- [v1 production rollback](../../evals/extraction-v1/results/2026-07-20-stage10-v1-rollback.md)

## Context

The paired ten-case extraction shadow restored v1 as the production protocol
because it recovered more scored and source-supported Atoms than v2
`source-clause-v1`. V1 nevertheless leaked one superseded fact. It promoted an
older preference to active state, then could not unify later cross-agent
corrections because the mutable status lacked a stable identity.

## Decision

Introduce extraction protocol `v1.1` as a separate, opt-in rolling protocol.
It preserves the v1 candidate response schema and external `Extractor`
interface while adding a narrow deterministic repair and a protocol contract:

- the known `I'd keep` preference-tail pattern cannot create canonical state,
  including when a candidate merges it with an independently committed prefix;
- every status or handoff candidate must carry a model-supplied semantic
  `identity_ref`; the server rejects a missing value, and the protocol requires
  later agents to reuse it when updating or resolving the same real-world
  state.

The admission boundary can deterministically enforce identity presence, but it
cannot prove that two differently worded identities denote the same real-world
state. Semantic stability and the broader proposal, preference, request,
hypothetical, and conditional distinctions are protocol obligations measured
through cross-agent update/leakage cases, not overstated server-side guarantees.

V1.1 has its own episode protocol version. It never replays or appends to a v1
or v2 rolling episode. V1 remains unchanged for reproducibility and remains the
production default until v1.1 passes the fixed cohort.

## Acceptance gate

Run v1.1 on the same ten-case, 23-Atom GroupMemBench Finance source used for
the rollback decision. Compare against both v1 and v2 with extraction output
as the observation:

- eliminate the `knowledge_update_27` superseded-event leakage;
- retain or improve v1 source-supported subset recall of 5/11;
- do not increase provider errors or P95 latency materially;
- record candidate rejections, Note count, token use, and cache rate.

Promotion requires a valid artifact satisfying the quality gate. A prompt or
unit-test improvement alone is insufficient.

## Evaluation and strategy rollback

The preliminary narrow-policy run `stage10-v11-current-head-20260720-r3` matched
9/23 raw Atoms and 6/11 source-supported Atoms with zero leakage and zero call
errors, but it preceded final classifier isolation and is not the final claim.
The final narrow-policy run `stage10-v11-current-head-20260720-r6` matched 7/23
raw and 6/11 source-supported Atoms with zero leakage, zero call errors, and a
13.3-second mean / 37.8-second P95. An attempted hardening then added broad marker admission and removed rejected
candidates from subsequent rolling Episode context. The valid hardening run
`stage10-v11-current-head-20260720-r5` fell to 4/23 raw and 4/11
source-supported recall, despite retaining zero leakage and improving latency.

The hardening therefore failed the fixed quality gate and was rolled back. V1.1
retains the narrow known-leak repair and remains experimental. V1 remains the
production default until a second valid narrow-policy cohort demonstrates that
the R6 result is stable. See the
[evaluation result](../../evals/extraction-v1/results/2026-07-20-stage10-v1-1.md).

## Consequences

V1.1 may reject mutable model output that omits semantic identity, reducing
raw Note volume. That is intentional: ambiguous parallel state is not silently
admitted. The fixed evaluation will determine whether the stronger precision
contract preserves enough recall to justify promotion.
