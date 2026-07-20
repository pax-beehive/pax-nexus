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
not a published benchmark. `observations.empty.jsonl` verifies the scorer and
provides a copyable template; its zero scores are not a product result.

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

## Deterministic recall replay

`cmd/team-memory-recall-replay` replays persisted Team Note candidates through
recall policy without a consumer Agent, a live store, or an LLM. It is the
fixed cohort for validating retrieval changes before any paid end-to-end run.

Export pins a cohort from a persisted eval store (candidate notes with the
exact lexical and semantic scores produced by the recall code path, extraction
snapshot, recall request, observation time, and gold atoms), using schema
`pax-recall-replay-v4`. V4 adds Recall Consumer eligibility decisions, Knowledge
Origin identity, the three temporal clocks, and optional captured Evidence
Score and Hint Opportunity interventions. V3 added required-atom-to-Team-Note
support metadata and a candidate snapshot SHA-256. Tracked legacy v1/v2/v3
fixtures are migrated in memory, using the Unix epoch when a case has no
candidates:

```bash
go run ./cmd/team-memory-recall-replay -export \
  -stage-fixtures evals/stage/groupmembench-finance-10.json \
  -dsn postgres://team_memory:team_memory@localhost:55433/team_memory?sslmode=disable \
  -run-id <eval-run-id> -arm team_note \
  -embedding-base-url http://localhost:58081 \
  -output evals/stage/replay/<eval-run-id>-team_note.json
```

Replay re-runs `PlanRecall` with an overridable policy and scores the planned
deliveries with the same stage evaluator, so replay metrics are comparable to
live stage captures:

```bash
go run ./cmd/team-memory-recall-replay \
  -fixtures evals/stage/replay/<eval-run-id>-team_note.json \
  -semantic-threshold 0.50 -candidate-limit 16 -dedup -degrade-related \
  -output-dir runs/recall-replay/<label>
```

The runner implements the `pax-recall-eval-v1` report contract. It writes:

- `replay-results.jsonl`: per-case stage results, Recall Trace, and atom losses;
- `replay-summary.json`: candidate, relation-expanded, selected-set, and
  delivered recall plus context precision, budget loss, leakage, and aggregate
  stage counters. It also reports `PlanRecall` call count, mean duration, and
  nearest-rank P95 duration in nanoseconds. Identity and temporal slices use
  Eligible Atoms, rather than actor-neutral Available Atoms, as their
  denominator. `delivered_conditional_recall` remains delivery over Available
  Atoms, while `delivered_eligible_recall` is the consumer-scoped metric;
- `recall-loss-ledger.jsonl`: one deterministic stage outcome per required
  atom, including Recall Consumer, Knowledge Origins, Observation Time, Query
  Time, Source Times, and the eligibility decision.

When a v4 Case includes `hint_observation`, the same summary also reports
Evidence Score and Hint Score precision, recall, Brier score, calibration
error, focused-recall success within one and two calls, new Eligible Atom gain,
identity/temporal slices, usage, and safety violations. The observation is a
captured planner intervention; it does not claim that an agent saw the hint or
called active recall. Agent exposure, activation, answer judging, and paired
wins/losses remain measurements of the isolated `hint_recall_v0` consumer Arm.

`EvaluateUnauthorizedInfluencePair` separately drives the production
`RecallNotes` interface against isolated baseline and challenge scopes. It
verifies that a named unauthorized Note never enters the delivered envelope
and that visible text, relevance, certainty, revision, and Knowledge Origin
are unchanged. Candidate-stage exclusion remains visible in the fixed replay
eligibility decisions rather than widening the external module interface.

The default zero-cost baseline is also available through:

```bash
make recall-eval-v1
```

Override `RECALL_EVAL_FIXTURE`, `RECALL_EVAL_OUTPUT`,
`RECALL_EVAL_SEMANTIC_THRESHOLD`, or `RECALL_EVAL_CANDIDATE_LIMIT` to compare a
fixed fixture under another recall policy. The tracked fixtures under
`evals/stage/replay/` were exported from the
`team-note-optimization-30-20260716-c20fdd7` store for both arms.

`evals/stage/replay/relation-marginal-utility-v1.json` is a four-case curated
contrast cohort covering status, schedule, ownership, and blocker relations.
Each case has one relation that adds an uncovered query fact and one relation
that is merely adjacent or repetitive. Use
`-disable-relation-marginal-utility` for the legacy comparison arm; production
zero-value policy enables marginal-utility filtering.

## Live Eval v2 capture

The acceptance config enables `stage_capture` for `team_note` and
`team_note_hybrid`. Successful Team Note recalls, including zero-hit recalls,
persist request controls by query digest and the exact returned envelope in
PostgreSQL for seven days. The same transaction records the current active
Note/evidence extraction snapshot, so later TTL or resolution changes cannot
move the stage boundary. After trials finish, Eval v2 verifies each source
SHA-256, reads the paired snapshot and recall, and writes per-arm artifacts under:

```text
<output_dir>/stage/<arm>/observations.jsonl
<output_dir>/stage/<arm>/stage-results.jsonl
<output_dir>/stage/<arm>/stage-summary.json
```

`<output_dir>/stage/artifacts.json` indexes those files, and the main Eval v2
artifact manifest links it. Capture validates fixture user and query against
the eval manifest, then matches the successful recall by user, agent, session,
query digest, and token budget. If a trial/session or successful matching recall
is absent, the adapter emits an unscored Observation error; it is counted
separately instead of being fabricated as a zero-quality recall.
