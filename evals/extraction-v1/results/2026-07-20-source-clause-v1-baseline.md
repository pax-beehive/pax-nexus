# Source Clause v1 extraction evaluation baseline

Date: 2026-07-20 (America/Los_Angeles)

Decision: adopt `source-clause-v1` as the Extraction Evaluation Baseline.
This names the control for future extraction work; it does not change the
production candidate-strategy default or claim statistical superiority.

## Fixed inputs

- Run: `groupmembench-finance-micro6-source-clause-v1-20260719-r2`
- Source: `groupmembench-finance-v3-micro6-expanded-20260718-r1`
- Profile: `finance-micro6-quick`
- Source inventory: 2,595 Events
- Selected input: 104 Events, seven streams, eight primary Slices
- Extractor: v2 / `source-clause-v1` / `deepseek-v4-flash`
- Execution policy: 240-second attempt deadline, at most two attempts

Zero-cost preflight verified every selected and supporting Event before model
construction. The valid Run completed on its first physical attempt for every
logical call. An earlier 90-second run timed out before its first Slice and is
execution evidence only, not a quality result.

## Baseline metrics

| Metric | Source Clause v1 |
| --- | ---: |
| Required Atoms matched | 4/6 |
| Fact recall | 0.667 |
| Leakage | 0 |
| Admitted Team Notes | 12 |
| Primary / summary calls | 8 / 1 |
| Provider errors / retries / timeouts | 0 / 0 / 0 |
| Primary output tokens | 61,244 |
| Primary cache-hit rate | 69.3% |
| Mean / P95 primary duration | 57.2 s / 69.2 s |

The matched Atoms cover both `knowledge_update_20` Atoms plus `temporal_7`
and `term_ambiguity_11`. `multi_hop_1` and `user_implicit_7` remain missing.
Their supporting Events were present and reviewed, but both were classified
`no_state`; the Extraction Loss Ledger therefore places both first losses at
Event review rather than source coverage, deterministic admission, or Team
Note materialization.

## Comparison and use

The prior three `current` runs ranged from 0.167 to 0.500 fact recall with
zero to two leakage items. The prior three `interaction-slim` runs all scored
zero fact recall with zero to two leakage items. At 0.667 recall and zero
leakage, `source-clause-v1` is the best observed valid single Run on the fixed
cohort and is selected as the new engineering control.

Future extraction candidates must keep the source, profile, scorer, and recall
policy fixed and compare atom recall, leakage, first-loss stages, provider
calls, tokens, cache rate, and latency against this artifact. Repeating paired
seeds remains required before claiming repeatable superiority or changing a
production default.

Artifacts:

- `runs/extraction-eval-v1/groupmembench-finance-micro6-source-clause-v1-20260719-r2/report.json`
- `runs/extraction-eval-v1/groupmembench-finance-micro6-source-clause-v1-20260719-r2/cases.jsonl`
- `runs/extraction-eval-v1/groupmembench-finance-micro6-source-clause-v1-20260719-r2/atom-losses.jsonl`
- `runs/extraction-eval-v1/groupmembench-finance-micro6-source-clause-v1-20260719-r2/provider-calls.jsonl`
- `runs/extraction-eval-v1/groupmembench-finance-micro6-source-clause-v1-20260719-r2/config.resolved.json`
