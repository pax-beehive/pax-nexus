# Hint Recall v1 with paxm v0.2.0 — 15-case pilot

Date: 2026-07-19

Run: `groupmembench-finance-recall-v2-hint-v1-paxm-v0.2.0-pilot15-r3`

Configuration: `evals/recall-v2/config.hint-recall-v1-paxm-v0.2.0-pilot15.yaml`

This is a diagnostic pilot, not hard-cohort acceptance evidence. It uses a
fixed, category-balanced 15-case subset of the hard 30-case annotations.

## Validity

- paxm consumer image: `v0.2.0`, verified at Docker build time.
- 45/45 trials scored; Agent execution coverage was 100%.
- Cohort: six categories; five temporal cases; two identity-dependent cases;
  two reviewed knowledge-source cases; two strict cross-agent cases.
- Hint Recall activated in 13/15 trials and made exactly 13 focused calls.
- No hint authorization or recall-provider failure was observed.

## Accuracy

| Arm | Correct | Accuracy | Paired vs passive Team Note |
| --- | ---: | ---: | --- |
| no_memory | 3/15 | 20.0% | — |
| team_note | 6/15 | 40.0% | 3 wins, 0 losses, 12 ties vs no_memory |
| hint_recall_v0 | 6/15 | 40.0% | 0 wins, 0 losses, 15 ties |

## Cost and decision

| Arm | Input tokens | Output tokens | Focused calls |
| --- | ---: | ---: | ---: |
| no_memory | 219 | 85 | 0 |
| team_note | 1,359 | 168 | 0 |
| hint_recall_v0 | 20,224 | 1,170 | 13 |

Hint Recall v1 did not improve answer accuracy over passive Team Note in this
pilot, while using 14.9x passive-Team-Note input tokens. Keep it evaluation-only
and do not promote it to the default recall strategy. The next useful change is
to improve activation selectivity or focused-query utility before running the
hard 30-case acceptance cohort.
