# Extraction Eval v1

`extraction-eval-v1` measures Team Note extraction independently from recall
ranking and answer generation. It is a separate protocol from Eval v3:

- fixtures live under `evals/extraction-v1/`;
- the report schema is `pax-extraction-eval-v1`;
- artifacts default to `runs/extraction-eval-v1/<run-id>/`;
- the command never writes Eval v3 manifests, trials, reports, or arm results.

The source is a completed persisted run. Cases that share a source scope are
grouped into one domain, so the source events are extracted once and the same
admitted Team Notes are scored against every case's required and forbidden atoms.
For the Finance micro6 cohort, that means one 1605-event extraction replay and
six case-level scores, rather than six duplicate replays.

## What it reports

- extraction fact recall and missing atom IDs;
- forbidden-fact leakage;
- evidence precision and recall where reviewed supporting event IDs exist;
- calls, errors, tokens, cache use, latency, and v2 trace counters;
- domain-level event, stream, slice, and admitted-Team-Note counts.

`recall` and answer-judge metrics are intentionally absent. A missing atom here
is an extraction failure; a present atom that later fails to reach an answer is
outside this protocol.

## Finance micro6

The independent fixture is
`evals/extraction-v1/groupmembench-finance-micro6.json`. Run it against a source
scope that has the full Finance domain persisted:

```bash
go run ./cmd/team-memory-extraction-eval-v1 \
  -dsn "$EVAL_V2_POSTGRES_DSN" \
  -manifest evals/extraction-v1/groupmembench-finance-micro6.manifest.json \
  -fixtures evals/extraction-v1/groupmembench-finance-micro6.json \
  -source-run-id groupmembench-finance-v3-micro6-general-recall-v3-20260717-r3 \
  -run-id groupmembench-finance-micro6-extraction-v2-20260717-r1 \
  -extractor v2
```

The extractor endpoint, model, and API key default to
`TEAM_MEMORY_EXTRACTOR_BASE_URL`, `TEAM_MEMORY_EXTRACTOR_MODEL`, and
`TEAM_MEMORY_EXTRACTOR_API_KEY`. Use `-scope-suffix` only when the persisted
source scope itself has a suffix.

The command refuses an existing completed directory. During a run it keeps a
durable slice and provider-call journal in the output directory; use `-resume`
only for an incomplete directory created by the same run ID and configuration.

## Quick optimization loop

The tracked `finance-micro3-quick` profile selects fixed evidence neighborhoods
for `multi_hop_1`, `knowledge_update_20`, and `abstention_4`. It reduces the
current 1,605-session-event source to 65 session events over four streams and
five expected primary slices. Preflight validates every supporting session
event before constructing the model client:

```bash
go run ./cmd/team-memory-extraction-eval-v1 \
  -dsn "$EVAL_V2_POSTGRES_DSN" \
  -manifest evals/extraction-v1/groupmembench-finance-micro6.manifest.json \
  -fixtures evals/extraction-v1/groupmembench-finance-micro6.json \
  -profile evals/extraction-v1/profiles/finance-micro3-quick.json \
  -source-run-id "$SOURCE_RUN_ID" \
  -run-id preflight \
  -preflight-only
```

Run the paired current-v2 versus interaction-slim-v2 speed canary with:

```bash
EXTRACTION_EVAL_DSN="$EVAL_V2_POSTGRES_DSN" \
  scripts/extraction-eval-v1-canary.sh "$SOURCE_RUN_ID" "$RUN_PREFIX"
```

The canary uses `scripts/load-extraction-eval-v1-env.sh`, which loads `.env`
and an optional `.env.extraction-eval-v1` overlay. It has no Eval v3 loader or
artifact dependency.

Each physical provider call is appended immediately to
`provider-calls.jsonl`, classified as `primary`, `summary`, `compaction`, or
`verifier`. Each completed primary slice is synced to `slices.jsonl`, and the
rolling episode including raw saved responses is synced to `episodes.json`.
Completed periodic summaries are also persisted to that episode before the
background call is considered finished.
Re-running the canary script resumes an incomplete arm automatically; a direct
command can use `-resume`.

## Artifacts

- `report.json`: cohort summary, domain inventory, case scores, and telemetry;
- `cases.jsonl`: one extraction score per benchmark case;
- `notes.jsonl`: admitted Team Notes emitted once per physical domain;
- `slices.jsonl`: per-call usage and v2 traces emitted once per domain;
- `config.resolved.json`: non-secret resolved inputs and extractor identity.

## Recorded runs

- [2026-07-17 Finance micro6 v2 r1](./results/2026-07-17-finance-micro6-v2-r1.md):
  first full-domain baseline, including the source-cohort validity limitation
  and the deterministic defects found from its trace.
