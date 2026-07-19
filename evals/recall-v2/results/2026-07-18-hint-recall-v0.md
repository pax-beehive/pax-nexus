# Hint Recall v0 deterministic baseline

Date: 2026-07-18

Fixture: `groupmembench-finance-hint12-replay-v1.json`

The cohort selects two distinct questions from each of the six GroupMemBench
Finance categories. Ten Cases contain a useful focused-recall intervention;
two abstention Cases are negative controls. The fixture is synthetic planner
and intervention evidence, not an external-Agent answer-quality result.

## Result

- Scored opportunities: 12
- Positive opportunities: 10
- Exposed hints: 12
- Hint precision: 0.833
- Hint recall: 1.000
- Focused-recall success at one call: 10/12
- Focused-recall calls: 14
- New Eligible Atoms: 10
- Planned hint tokens: 875 total
- Unauthorized, future, wrong-time, forbidden, and superseded leakage: 0
- Provenance, identity, temporal-preservation, and delivery-claim errors: 0

The two false-positive hints are the intentional abstention controls. This
shows the bridge can recover missing evidence safely, but the heuristic is not
selective enough to enable by default. External-Agent activation and judge
accuracy remain the next gate.

The ordinary candidate, selected-set, and delivered Recall fields are zero in
this intervention replay by design: a hint is explicitly not evidence. Hint
success is scored only after the focused active-recall intervention returns a
new eligible atom.

## Real-Agent result

Run: `groupmembench-finance-recall-v2-hint-v0-fixed-r5`

The 30-case fixed-observation cohort ran through OpenCode with three arms, for
90 Agent trials. All 90 trials passed the mandatory recall/provider evidence
audit with no unscored Trial. The linked seed receipt pins the observation
time and the canonical observation, Agent manifest, and annotation digests.

| Arm | Judge correct | Accuracy |
| --- | ---: | ---: |
| No memory | 5/30 | 16.7% |
| Passive Team Note | 11/30 | 36.7% |
| Hint Recall v0 | 13/30 | 43.3% |

Against Passive Team Note, Hint Recall v0 recorded 2 wins, 0 losses, and 28
ties. It activated focused recall in 28/30 trials (93.3%) and made 56 active
recall calls. The hint arm used about 2.3 times the reported consumer cost and
35.9 times the input tokens of Passive Team Note, largely because each
activated Agent consumed two recall tool responses.

Category accuracy for Passive Team Note -> Hint Recall v0 was:

- abstention: 100% -> 100%
- knowledge update: 80% -> 80%
- multi-hop: 20% -> 40%
- temporal: 0% -> 20%
- term ambiguity: 0% -> 0%
- user implicit: 20% -> 20%

The two reviewed-source, strict cross-agent cases remained 0/2 in both arms.
No identity, temporal, or knowledge-source slice regressed, but these slices
are too small for a default-on decision.

## Decision

Keep Hint Recall v0 implemented but disabled by default. The run verifies the
full mechanism: a non-evidentiary hint reaches a real Agent, causes focused
active recall, and can improve judged answers without claiming the hint as a
delivered note. It does not yet establish broad utility: only two answers
improved, 93.3% of cases paid the intervention cost, deterministic hint
precision was 83.3%, and strict cross-agent and term-ambiguity quality did not
improve.

The next candidate should optimize activation/selectivity before widening
retrieval: predict whether focused recall has positive marginal utility, allow
one call by default, and abstain on low-specificity navigation leads.

This Agent cohort uses persisted synthetic recall observations derived from
the selected GroupMemBench cases. It isolates recall behavior and is not a
replacement for a paid end-to-end GroupMemBench extraction benchmark.
