# Hint Recall v1 diagnostic-10

Run: `groupmembench-finance-recall-v2-hint-v1-diagnostic10-r5`.

This is a fast diagnostic cohort, not a replacement for the fixed 30-case
acceptance cohort. It contains ten independently selected cases across all six
categories, including four temporal cases, two identity-dependent cases, and
one reviewed strict-cross-agent case. All thirty Agent trials completed and
were judged.

## Candidate settings

- `TEAM_MEMORY_HINT_MIN_QUERY_RELEVANCE=0.10`
- `TEAM_MEMORY_HINT_MIN_MARGINAL_UTILITY=0.85`
- `PAXM_ACTIVE_RECALL_MAX_CALLS=1`

The transport arm remains named `hint_recall_v0` for paired-report continuity;
the Team Memory plan and scoring versions are Hint Recall v1.

## Results

| Arm | Correct | Accuracy | Active recall calls | Input tokens |
| --- | ---: | ---: | ---: | ---: |
| No memory | 2/10 | 20.0% | 0 | 959 |
| Passive Team Note | 4/10 | 40.0% | 0 | 1,320 |
| Hint Recall v1 | 4/10 | 40.0% | 6 | 8,820 |

Against Passive Team Note, Hint Recall v1 had zero wins, zero losses, and ten
ties. It activated active recall in 6/10 trials, and every activated trial
made exactly one call. That is a material activation reduction from the prior
v0 30-case run (28/30 activated trials and 56 total calls), but these cohorts
are different and must not be used as a numerical before/after accuracy claim.

## Decision

The selectivity and one-call cap work as intended: active recall is no longer
near-universal and no case exceeded its focused-call budget. The diagnostic
cohort does not show an answer-quality gain over passive Team Note, so this
candidate is not ready to replace the baseline or justify a global rollout.
It should next run on the fixed 30-case cohort after rebuilding the eval
consumer image so the updated exact-focused-query instruction is exercised
end-to-end.
