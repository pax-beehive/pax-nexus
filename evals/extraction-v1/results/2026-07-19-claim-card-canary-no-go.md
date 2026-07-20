# Claim Card canary: no-go

Date: 2026-07-19

## Fixed cohort

All three arms replay the same `finance-micro3-quick` fixture: three cases,
65 selected source events, four streams, and five primary extraction slices.
They use the same source run and DeepSeek v4 Flash extractor. This is an
extraction-only comparison; no recall or agent answer run was performed.

| Arm | Atom recall | Leakage | Primary output tokens | Mean primary duration |
| --- | ---: | ---: | ---: | ---: |
| interaction-slim baseline | 2/3 | 1 | 35,170 | 71.6 s |
| claim-card-v1 | 0/3 | 3 | 47,697 | 66.8 s |
| claim-card-v2 exact value | 0/3 | 0 | 52,152 | 80.5 s |

Artifacts:

- baseline: `runs/extraction-eval-v1/groupmembench-finance-micro3-v2-canary-20260718-r1-interaction-slim`
- v1: `runs/extraction-eval-v1/groupmembench-finance-micro3-claim-card-20260719-r1`
- v2: `runs/extraction-eval-v1/groupmembench-finance-micro3-claim-card-v2-20260719-r1`

## Findings

`claim-card-v1` successfully produced deterministic cards from structured
Claims, but the model compressed answer-bearing qualifiers into abstract
values. For the knowledge-update fixture it retained a refreshed currency feed
and a rerun, while omitting the required `targeted check` and `impacted close
cycle` qualifiers. The evaluator matched no gold atom and found three
abstention leakage items.

`claim-card-v2` required a contiguous exact source phrase for Claim values.
That constraint made the model move factual content back into the ignored
free-form Candidate body and emit Claims with only a subject. Deterministic
validation rejected 42 Claims for missing predicate/value/speaker, rejected
33 decisions, left 41 events unreviewed, and admitted no state candidate.
It eliminated leakage only by retaining no answerable state.

## Decision

**No-go: do not promote either claim-card strategy and do not run an
end-to-end cohort.** The claim/state protocol is useful as an internal trace,
but it is not presently a reliable model-agnostic storage language. Returning
to the `interaction-slim` semantic candidate is preferable to tightening the
same output schema further.

Future extraction work should concentrate on evidence-backed materiality and
temporal/identity admission over the existing semantic candidate, with the
fixed extraction fixture as the first gate. It should not add another
free-form-to-structured rendering protocol.
