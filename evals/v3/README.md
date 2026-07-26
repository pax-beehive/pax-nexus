# Eval v3

Eval v3 is the three-arm multi-agent GroupMemBench protocol:

1. `no_memory_team`;
2. `groupmembench_mem0`;
3. `private_sqlite_plus_team_note`.

Unlike Eval v2, source construction is full-domain and independent of each
question. Every GroupMemBench author becomes a stable Actor Agent. One
Answering Agent is selected deterministically per case and reused across all
three paired arms. The original `asking_user_id` is preserved separately.

Prepare the full-domain manifest:

```bash
make eval-v3-prepare
```

Create local configuration and start the stack:

```bash
cp evals/v3/config.example.yaml evals/v3/config.local.yaml
make eval-v3-up
make eval-v3
make eval-v3-down
```

Eval v3 defaults extraction jobs to a 20-minute timeout and ten attempts because
full-domain native sessions require several rolling slices. These values are
recorded in resolved runtime provenance and do not change production defaults.

The PAX arm builds one SQLite database per Actor Agent under the run output
directory and combines only the selected Answering Agent's database with the
shared Team Note provider. Mem0 uses one shared domain namespace. The template
labels the current self-hosted setup `comparable_baseline`; do not present it as
an exact reproduction of the published Mem0 number.

Cases without reviewed supporting-event annotations report
`answerer_source_overlap=unknown` and do not count toward strict cross-agent
accuracy. The runner never silently relaxes an annotated case when no eligible
Answering Agent exists.

The artifact schema is `pax-eval-v3.3`. `trials.csv` and `trials.jsonl` record
the paired answerer identity, seed, source-overlap status, strict-trial flag,
and observed recall candidates, hits, and injected context items. `summary.csv` includes a `trial_class=strict_cross_agent` slice when
annotated strict trials exist. `artifacts.json` links the three full-domain
ingest receipts under `memory/`, so source construction is auditable separately
from answer quality.

The artifact manifest also records the shared Mem0 namespace, image, models,
extraction profile, retrieval limit and score semantics, observed retrieval
activity, observed accuracy, published comparison targets, and known protocol deviations. The current runner rejects stronger
reproduction labels until an official Mem0 runner and pinned artifacts are
available.

`attempts.jsonl` is the append-only execution ledger. Its artifact references
point to retry-specific raw logs under
`trials/<case>/<arm>/attempts/<sequence>/`.

`validity.json` uses schema `pax-eval-v3-validity-v1` and is the acceptance
decision for comparative scoring. A valid report requires:

- the complete, successfully judged three-arm Trial matrix;
- full-domain source coverage in all three ingest receipts;
- non-zero Team Note and Mem0 mutation plus complete private-SQLite materialization;
- successful calls to the arm-specific recall provider for both memory arms
  and no provider type, count, or observation in the no-memory arm;
- completed latest Attempts with canonical, non-empty consumer and judge
  artifacts;
- a resolved configuration whose hash matches the durable Run.

The artifact manifest links and embeds this report. An invalid run exports raw
JSONL and available provenance, removes stale CSV/HTML comparisons, and exits
non-zero; its manifest is labeled `raw_invalid_evidence` and contains no
derived accuracy or cost summary. Judge-only recovery claims a new Attempt
under the Run lock, verifies the stored configuration hash, uses the durable
consumer result as its only answer input, and writes new judge evidence into
the canonical Attempt directory. A transient judge failure preserves the
completed consumer result and can be retried without rerunning the Trial. A
crashed rejudge Attempt is marked interrupted on the next claim without hiding
or replacing that consumer result. Recovery selects the newest prior Attempt
that actually owns a canonical, non-empty consumer artifact, so a crash before
artifact copy does not poison later retries. Run provenance checks include
dataset and dataset revision as well as the configuration hash. Consumer
evidence copies use fsync plus atomic rename, and the gate rejects malformed or
truncated consumer and judge JSONL.

Eval v3 is the outer architecture comparison, not the recall-policy tuning
loop. Before using it to validate a recall change, first improve the fixed
recall replay and inspect its stage metrics. Gold-source, delivered-evidence,
leakage, storage-size, and split recall-latency metrics still require the
separate reviewed-evidence/stage pipeline before the ADR's product-use
acceptance gate is complete.

## Two-arm mode (`arm_set: two_arm_no_mem0`)

Opt in by setting `arm_set: two_arm_no_mem0` in the config. Scripts that shell
out to `scripts/eval-v3-opencode.sh` must set the matching
`EVAL_V3_ARM_SET=two_arm_no_mem0` in the environment — the two must agree, or
the ingest step and `validity.json` will disagree about which arms ran. This
mode runs only `no_memory_team` and `private_sqlite_plus_team_note`;
`groupmembench_mem0` is not a permitted arm in a `two_arm_no_mem0` config.

It exists because Mem0 ingest costs roughly nine hours per full-domain round,
which makes sweeping candidate Team Note extraction models against a live
Mem0 arm infeasible. Skipping Mem0 ingest and its receipt requirement is the
only thing this mode changes: Team Note ingest, private-SQLite ingest, and
the full-domain extraction readiness wait are unchanged. That readiness wait
is what enriches `team-note-ingest.json` with `memory_items`, the guardrail
that catches an extractor that produced nothing, so it still runs in full.

`validity.json` attests to this reduced contract explicitly: its `arm_set`
field records `two_arm_no_mem0`, and it does not require a Mem0 ingest receipt
or a Mem0 recall observation. Numbers from a two-arm run are not comparable to
three-arm runs, and are not comparable to any published Mem0 figure — this
mode cannot speak to Mem0 at all. It answers "which extractor produces better
Team Note memory," not "how does PAX compare to Mem0."

Default behavior is unchanged: with `arm_set` absent or `three_arm`
(equivalently, `EVAL_V3_ARM_SET` unset or `three_arm`), all three arms run and
Mem0 ingest and its receipt are required exactly as before.

## Decomposed mode (`arm_set: decomposed`)

Opt in by setting `arm_set: decomposed` in the config and, in the environment
used by `scripts/eval-v3-opencode.sh`, `EVAL_V3_ARM_SET=decomposed` — the two
must agree for the same reason as two-arm mode. This mode runs four arms:
`no_memory_team`, `team_note_only`, `private_sqlite_only`, and
`private_sqlite_plus_team_note`. `groupmembench_mem0` is not a permitted arm,
so Mem0 ingest is skipped exactly as in two-arm mode.

`private_sqlite_plus_team_note` bundles the shared Team Note store with a
private SQLite database holding every full-domain source event verbatim, so
neither source's contribution to accuracy can be attributed on its own. The
two new arms isolate them:

- `team_note_only` uses the `team-memory` provider with no private SQLite
  database mounted at all — the per-agent private database is unreachable.
- `private_sqlite_only` uses the same `team-memory-sqlite` provider type as
  the combined arm (so `memory_recall_provider_type` matches), but the
  consumer's Team Note provider is never registered — see
  `PAXM_TEAM_PROVIDER_DISABLED` in `evals/opencode/docker/opencode/entrypoint.sh`.
  It is omitted from the provider config entirely, not merely excluded from
  the recall profile, so no team-memory credential is needed and the shared
  store cannot be reached.

Verify isolation from `trials.jsonl`, not from the config: `team_note_only`
trials must show `memory_recall_providers` containing only the team entry, and
`private_sqlite_only` trials only the private entry.

See `evals/v3/config.decomposed.example.yaml` for a config exercising all four
arms.

## Extractor model sweep

Run the two-arm no-Mem0 protocol once per candidate Team Note extraction
model, holding the consumer and judge fixed, so any accuracy delta between
`no_memory_team` and `private_sqlite_plus_team_note` is attributable to the
extracted memory rather than the model reading it. The sweep does not ingest
or run Mem0 at all — see "Two-arm mode" above for what that means for these
numbers.

Check the plan without starting Docker:

```bash
make eval-v3-extractor-sweep DRY_RUN=--dry-run PREFIX=my-sweep
```

Run it:

```bash
make eval-v3-extractor-sweep PREFIX=my-sweep
```

Candidates live in `evals/v3/sweep/extractor-<slug>.env`. Each names the
variable holding its provider key rather than the key itself; set the value in
the gitignored `.env.eval-v2`. Restrict a run to specific candidates with
`SLUGS="deepseek-v4-flash gemini-3.6-flash"`, and change the question set with
`MANIFEST=...`.

Artifacts land in `runs/eval-v3-sweep/<prefix>/<slug>/`, with the rendered
config for each round under `runs/eval-v3-sweep/<prefix>/configs/`.

The driver resets the stack (`down -v`) before every round. This is required,
not hygiene: Team Note rows surviving from the previous extractor would corrupt
all three arms, and nothing in the artifacts would reveal it.

Read `validity.json` before comparing any numbers across rounds. A round whose
extractor produced no memory is reported as invalid rather than as a genuine
zero accuracy.
