# Implicit State Review v1 no-go

Date: 2026-07-20 (America/Los_Angeles)

Decision: do not promote the implicit-state prompt. Keep `source-clause-v1` as
the production and extraction-evaluation default. Preserve the exact prompt as
the reproducibility-only `source-clause-implicit-state-v1` candidate.

## Fixed comparison

Both Runs used source
`groupmembench-finance-v3-micro6-expanded-20260718-r1`, the
`finance-micro6-quick` profile, 104 selected Events, seven streams, eight
primary Slices, the v2 extractor, and `deepseek-v4-flash`. Recall policy and
scoring were unchanged.

The experimental Run was made while the added prompt was temporarily carried
under the `source-clause-v1` runtime name, so its resolved configuration records
that name. The prompt and protocol are now isolated under
`source-clause-implicit-state-v1`; this avoids changing the selected baseline
and gives the experiment an explicit reproducibility identity.

| Metric | `source-clause-v1` baseline | Implicit-state experiment |
| --- | ---: | ---: |
| Required Atoms matched | 4/6 | 3/6 |
| Fact recall | 0.667 | 0.500 |
| Leakage | 0 | 0 |
| Admitted Team Notes | 12 | 19 |
| Primary / summary calls | 8 / 1 | 8 / 2 |
| Provider errors / retries / timeouts | 0 / 0 / 0 | 0 / 0 / 0 |
| Primary output tokens | 61,244 | 49,214 |
| Primary cache-hit rate | 69.3% | 63.0% |
| Mean / P95 primary duration | 57.2 s / 69.2 s | 43.3 s / 52.3 s |

## Loss movement

The prompt recovered `user_implicit_7`: Event `Msg_18259` became a matched
incomplete-state blocker instead of `no_state`. It did not recover
`multi_hop_1`; Event `Msg_3651` remained reviewed and classified `no_state`.

The same Run lost two baseline Atoms. `temporal_7` Event `Msg_11264` and
`term_ambiguity_11` Event `Msg_25212` were both present in the fixed input but
were unreviewed. Knowledge-update coverage remained 2/2 and leakage stayed
zero. The net result is therefore a one-Atom regression and seven additional
Team Notes, despite lower latency and output tokens.

This is a no-go. A broad prompt instruction can move attention between Event
classes rather than reliably increase total durable-state coverage. Any next
attempt must narrow the mechanism, retain the fixed cohort, and first show no
loss of the temporal and term-ambiguity Atoms.

The 120-second provider deadline also completed all ten physical calls without
error, timeout, or retry. That supports retaining the separate execution-budget
change, but is not evidence for promoting this extraction prompt.

Artifacts:

- `runs/extraction-eval-v1/groupmembench-finance-micro6-source-clause-v1-20260719-r2/report.json`
- `runs/extraction-eval-v1/groupmembench-finance-micro6-source-clause-implicit-state-20260720-r1/report.json`
- `runs/extraction-eval-v1/groupmembench-finance-micro6-source-clause-implicit-state-20260720-r1/atom-losses.jsonl`
- `runs/extraction-eval-v1/groupmembench-finance-micro6-source-clause-implicit-state-20260720-r1/provider-calls.jsonl`
- `runs/extraction-eval-v1/groupmembench-finance-micro6-source-clause-implicit-state-20260720-r1/config.resolved.json`
