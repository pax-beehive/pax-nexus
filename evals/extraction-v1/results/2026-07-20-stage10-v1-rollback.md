# Extraction v1 production rollback

Date: 2026-07-20 (America/Los_Angeles)

Decision: restore extraction v1 as the production protocol default. Keep v2
`source-clause-v1` available as the explicit extraction evaluation arm.

## Fixed inputs

- Source run: `groupmembench-finance-v2-recall050-10-20260717-r2`
- Manifest: `runs/groupmembench-v2-selection/manifest.stage-10.json`
- Fixture: `evals/stage/groupmembench-finance-10.json`
- Cohort: 10 cases, 23 scored Atoms, 111 Slices
- Model: `deepseek-v4-flash`
- Parallelism: 2
- Current revision: `510f659`

Both arms used the same persisted source, fixture, model, evaluator, and
parallelism. Only the extraction protocol changed.

## Results

| Metric | v1 | v2 `source-clause-v1` rev 8 |
| --- | ---: | ---: |
| Scored Atom recall | 7/23 (30.4%) | 5/23 (21.7%) |
| Source-supported subset recall | 5/11 (45.5%) | 4/11 (36.4%) |
| Leakage | 1 | 0 |
| Notes | 271 | 48 |
| Calls / errors | 111 / 0 | 111 / 0 |
| Input tokens | 917,290 | 937,847 |
| Output tokens | 195,693 | 203,590 |
| Cache-hit rate | 84.6% | 87.6% |
| Mean duration | 16.0 s | 17.7 s |
| P95 duration | 41.3 s | 61.6 s |

The source-supported subset includes only Atoms whose annotated supporting
Events exist in the persisted source. The raw scorer also matched two v1 Atoms
and one v2 Atom whose fixture entries do not identify supporting Events, so
those matches are excluded from the subset comparison.

V1 gained `control_ownership`, `reporting_timing`, and
`legal_ops_freeze_fields` relative to v2, while v2 alone matched
`consent_and_audit_fields`. The v1 leakage was
`knowledge_update_27` note `extract-a4419b94e5d32aee-4`, which incorrectly
retained assisted flow outside the first-pass scope.

## Decision rationale

V2 did not demonstrate the quality gain required to replace v1: it lost two
raw Atom matches and one source-supported match, while also using more tokens
and taking longer. Its zero-leakage result and much lower Note count remain
important strengths, so the structured protocol is retained for targeted
experimentation rather than removed.

The next optimization line is extraction v1.1: keep the v1 candidate schema,
add clause-level modality admission, require stable cross-agent identities for
mutable state, and evaluate it as a distinct rolling episode protocol before
any production promotion.

Production defaults change to v1 in application configuration, the root
Compose stack, and `.env.example`. Eval v2 and OpenCode evaluation defaults
remain v2 by design.

Artifacts:

- `runs/extraction-shadow/stage10-v1-current-head-20260720-r1/shadow-report.json`
- `runs/extraction-shadow/stage10-v1-current-head-20260720-r1/notes.jsonl`
- `runs/extraction-shadow/stage10-v1-current-head-20260720-r1/slices.jsonl`
- `runs/extraction-shadow/stage10-source-clause-v1-protocol8-20260720-r2/shadow-report.json`
- `runs/extraction-shadow/stage10-source-clause-v1-protocol8-20260720-r2/notes.jsonl`
- `runs/extraction-shadow/stage10-source-clause-v1-protocol8-20260720-r2/slices.jsonl`
