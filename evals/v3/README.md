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

The PAX arm builds one SQLite database per Actor Agent under the run output
directory and combines only the selected Answering Agent's database with the
shared Team Note provider. Mem0 uses one shared domain namespace. The template
labels the current self-hosted setup `comparable_baseline`; do not present it as
an exact reproduction of the published Mem0 number.

Cases without reviewed supporting-event annotations report
`answerer_source_overlap=unknown` and do not count toward strict cross-agent
accuracy. The runner never silently relaxes an annotated case when no eligible
Answering Agent exists.

The artifact schema is `pax-eval-v3.0`. `trials.csv` and `trials.jsonl` record
the paired answerer identity, seed, source-overlap status, and strict-trial
flag. `summary.csv` includes a `trial_class=strict_cross_agent` slice when
annotated strict trials exist. `artifacts.json` links the three full-domain
ingest receipts under `memory/`, so source construction is auditable separately
from answer quality.

The artifact manifest also records the shared Mem0 namespace, image, models,
retrieval limit and score semantics, observed accuracy, published comparison
targets, and known protocol deviations. The current runner rejects stronger
reproduction labels until an official Mem0 runner and pinned artifacts are
available.

Eval v3 is the outer architecture comparison, not the recall-policy tuning
loop. Before using it to validate a recall change, first improve the fixed
recall replay and inspect its stage metrics. Gold-source, delivered-evidence,
leakage, storage-size, and split recall-latency metrics still require the
separate reviewed-evidence/stage pipeline before the ADR's product-use
acceptance gate is complete.
