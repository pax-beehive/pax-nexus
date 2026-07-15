# GroupMemBench evaluation

This evaluation runs deterministic samples from the official GroupMemBench
Finance domain through Dockerized OpenCode, paxm, PostgreSQL, and the production
team-memory service.

Run one balanced 12-case sample with:

```bash
make groupmembench-eval
```

Select a different reproducible sample with:

```bash
GROUPMEMBENCH_SEED=team-memory-v2 make groupmembench-eval
```

Each run selects two cases from each official category, executes four cases at
a time, and compares a recall-disabled control with a Team Note recall arm. The
runner downloads pinned public data into `.build/datasets/groupmembench` and
writes ignored artifacts under `runs/groupmembench-<run-id>`.

This is a bounded-context diagnostic rather than an official benchmark score.
Question-conditioned BM25 selects up to 32 source messages before ingestion,
and local scoring uses exact match, conservative safe-abstention success, and
token F1 instead of the official
semantic judge. See `doc/groupmembench-eval.md` for methodology, current results,
and limitations.
