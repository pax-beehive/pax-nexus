# Stage-local memory evaluation

This runner scores extraction and recall without invoking a consumer Agent or
an LLM judge. It is the inner-loop diagnostic for the decision recorded in
`internal/eval/docs/adr/0001-stage-local-memory-evaluation.md`.

## Contracts

The fixture JSON uses `pax-stage-eval-v1`. Each required atom has a stable ID,
one or more RE2 patterns, and optional supporting Event IDs. A fixture can also
name forbidden atoms, forbidden Event IDs, and superseded Event IDs.

Each fixture pins the source Event artifact by SHA-256 plus the recall consumer,
query, and token budget. The observation JSONL repeats those controls and has
exactly two records per case:

```json
{"case_id":"example","stage":"extraction","source_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","duration_ms":120,"provenance":{"model":"extractor-v1"},"items":[{"id":"note-1","text":"Ops Lead owns rollback evidence.","evidence_event_ids":["Msg_1"]}]}
{"case_id":"example","stage":"recall","source_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","recall_context":{"consumer_user_id":"User_3","query":"Who owns rollback evidence?","token_budget":512},"duration_ms":8,"items":[{"id":"note-1","text":"Ops Lead owns rollback evidence.","evidence_event_ids":["Msg_1"]}]}
```

Extraction items are admitted Note snapshots. Recall items are the ordered
Notes delivered for the fixed consumer query and budget. Observation adapters
should preserve raw identity and evidence; they must not pre-score items.
Every recalled item ID must exist in the paired extraction Observation.

## Run

The tracked ten-case GroupMemBench annotation is an initial development subset,
not a published benchmark. This command is currently a deterministic scorer and
artifact contract; capture from a live Team Note run remains an adapter concern.
`observations.empty.jsonl` verifies the scorer and provides a copyable template;
its zero scores are not a product result.

```bash
go run ./cmd/team-memory-stage-eval \
  -fixtures evals/stage/groupmembench-finance-10.json \
  -observations evals/stage/observations.empty.jsonl \
  -output-dir /tmp/team-memory-stage-eval
```

The runner writes:

- `stage-results.jsonl`: per-case extraction and recall attribution;
- `stage-summary.json`: micro-aggregated stage metrics and failure counts.

The key split is:

- `upstream_missed_atoms`: required atoms absent after extraction;
- `recall_missed_available_atoms`: extracted atoms lost during recall;
- `recall_gold_recall`: delivery against all required atoms;
- `recall_conditional_recall`: delivery against atoms available after
  extraction.

Observations with `error` are marked unscored and excluded from the relevant
quality denominator. `extraction_errors` and `recall_errors` report them
separately. The optional `provenance` map should pin model, prompt, provider,
and product revisions for real captures.

Evidence and leakage checks are deterministic. An LLM judge remains an outer
acceptance measure for final answers, not the optimizer for these stage loops.
