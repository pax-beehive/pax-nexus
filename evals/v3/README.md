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
through the durable Store and writes the original consumer plus new judge
evidence into its canonical artifact directory.

Eval v3 is the outer architecture comparison, not the recall-policy tuning
loop. Before using it to validate a recall change, first improve the fixed
recall replay and inspect its stage metrics. Gold-source, delivered-evidence,
leakage, storage-size, and split recall-latency metrics still require the
separate reviewed-evidence/stage pipeline before the ADR's product-use
acceptance gate is complete.
