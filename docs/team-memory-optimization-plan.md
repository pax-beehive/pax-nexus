# Team Memory Optimization Plan

Status: Active

Date: 2026-07-19

## Objective

Improve answer quality and evaluation confidence without widening retrieval or
adding another extraction rendering schema. Ingest, extraction, recall, and
answer judging remain separate control loops. Each tranche starts with a fixed,
deterministic fixture and advances to a paid end-to-end cohort only after its
stage-local gate improves.

## Evidence baseline

- `source-clause-v1` is the selected extraction evaluation baseline on
  `finance-micro6-quick`: 4/6 required Atoms, zero leakage, 61,244 output
  tokens, and 69.3-second P95 across eight primary Slices. It remains the v2
  evaluation candidate-strategy default, while extraction v1 is the production
  protocol default after the paired ten-case rollback decision.
- `interaction-slim + passive-v1` is the best observed ten-case end-to-end arm
  at 3/10 Team Note judge correctness, but its 228-second mean duration and
  single-cohort scope block promotion.
- Note BM25 improved candidate recall from 0.879 to 1.000 without improving
  delivered recall, so candidate breadth is not the next recall bottleneck.
- Hint Recall matched passive accuracy at 6/15 while consuming 14.9 times the
  input tokens.
- Claim Card v1/v2 and Source Span v1/v2 failed their fixed gates and remain
  available only for reproducibility.

## ADR coverage

The tranches are implementation plans under existing architectural decisions;
they do not each require a new ADR.

| Plan or candidate | Governing ADR | Current status and reason |
| --- | --- | --- |
| Extraction Evidence Fidelity | [Extraction v2](./decisions/2026-07-16-extraction-v2.md) | `source-clause-v1` remains the v2 Extraction Evaluation Baseline, but v1 is the production protocol default after v2 lost the paired ten-case quality gate. |
| Extraction Execution Reliability | [Extraction v2](./decisions/2026-07-16-extraction-v2.md) | Implemented; a 120-second provider attempt fits inside a three-minute one-Slice worker job, invalid aggregate budgets fail at startup, and checksum resume remains the durable retry boundary. |
| Implicit State Review v1 | [Extraction v2](./decisions/2026-07-16-extraction-v2.md) | Rejected for promotion; it recovered one implicit-state Atom but regressed two baseline Atoms and increased admitted Notes from 12 to 19. |
| Atom-Level Extraction Loss Attribution | [Extraction v2](./decisions/2026-07-16-extraction-v2.md) | Implemented; extraction eval exports one first-loss entry per required Atom. |
| Eval Validity and Attempt Ledger | [Multi-Agent GroupMemBench Eval v3](./decisions/2026-07-16-multi-agent-groupmembench-eval-v3.md) | Implemented; append-only Attempts and the comparative Validity Report now reject incomplete or unobservable runs. |
| Budget-Aware Final-State Selection | [General Recall v3](./decisions/2026-07-16-general-recall-v3-optimization.md) | Planner implemented; lane-local atom recall and unique-lane contribution reporting remain before tranche completion. |
| Durable Historical Recall | [General Recall v3](./decisions/2026-07-16-general-recall-v3-optimization.md) | Implemented; PostgreSQL supplies recorded-visible, revision-authorized candidates for `as_of`, `history`, and `changes_since`, while replay independently exports the recorded revision ledger as its extraction observation. |
| Hint selectivity/query utility | [Hint Recall v0](./decisions/2026-07-16-hint-recall-v0.md) | Evaluation-only; the real-Agent pilot had no accuracy lift and used 14.9 times the passive input tokens. |
| Claim Card v1/v2 | [v1](./decisions/2026-07-19-claim-card-v1.md), [v2](./decisions/2026-07-19-claim-card-v2.md) | Rejected; both fixed canaries produced zero of three atoms. |
| Source Span v1/v2 | [v1](./decisions/2026-07-19-source-span-v1.md), [v2](./decisions/2026-07-19-source-span-v2.md) | Rejected; end-to-end correctness regressed while fusion and budget rejection grew. |

## Tranche 1: Extraction Evidence Fidelity

Status: V2 evaluation default; not promoted to production

Deepen the existing extractor Module behind the unchanged `Extractor` seam.
Use the existing semantic Candidate schema and deterministic admission. Do not
add a Claim Card, Source Span, verifier call, or ingest-time token-overlap gate.

The first candidate, `evidence-fidelity-v1`, adds a candidate-by-candidate
source fidelity pass to `interaction-slim`. Before returning, the model must
re-read cited source clauses and preserve answer-changing actors, roles, exact
values, negation, time expressions, conditions, actions, and contrastive
qualifiers. It must prefer durable state over routine progress and must not
promote proposals or desired ownership into committed state.

Gate:

- run `finance-micro6-quick` for at least three paired seeds;
- compare atom recall, leakage, unreviewed Events, output tokens, provider-call
  count, and latency against `interaction-slim`;
- keep extraction input and recall policy fixed;
- require a repeatable coverage improvement without additional leakage before
  an end-to-end cohort.

Baseline decision on 2026-07-20: the project selected the first valid
`source-clause-v1` `finance-micro6-quick` Run as the engineering control after
it matched 4/6 Atoms with zero leakage. The three-paired-seed gate above now
governs claims of repeatable superiority and production rollout; it does not
block use of the selected artifact as the comparison baseline.

The successor slice is `source-clause-v1`. Every state-changing decision must
copy the shortest exact contiguous supporting clause from a cited Event.
Deterministic admission validates that citation and evaluates modality inside
that clause, without changing the external Candidate schema. Temporal
admission uses the maximum source time of new Events as the immutable
Extraction Observation Time; wall-clock replay is forbidden.

After source-clause and temporal gates pass, the next independent fidelity
slice may examine whether whole-body update replacement supersedes away
previously retained answer-bearing qualifiers.

Implemented on 2026-07-20: source-clause extraction now carries its validated
transition authority beside, rather than inside, the Candidate. On update,
deterministic admission requires protected dates, values, conditions,
negation/modality, and responsibility clauses to remain in the complete new
rendering unless an exact cited source clause explicitly authorizes their
replacement. Ambiguous destructive revisions quarantine the Extraction Run
without mutating the current note. Accepted revisions retain prior evidence
and related-subject provenance. Transition authority participates in the
immutable candidate checksum, while the external Candidate schema remains
unchanged.

First canary result: `evidence-fidelity-v1` matched the `interaction-slim`
baseline at 2/3 atoms but increased leakage from one item to two, decision
rejections from 8 to 16, and unreviewed Events from 6 to 13. The candidate is
not retained from this run. Do not run the remaining seeds unchanged; first
make Candidate evidence source-clause atomic so adjacent committed-looking
Events cannot launder proposal-only ownership into canonical state. See the
[micro3 result](../evals/extraction-v1/results/2026-07-19-evidence-fidelity-v1-micro3-r1.md).

## Tranche 2: Extraction Execution Reliability

Status: Planner implemented; evaluation reporting remains

Concentrate provider deadlines, error classification, bounded retries,
response budgets, and provider-call telemetry in the Extraction Execution
Module. Retain the existing checksum-based durable Episode resume path. Do not
add concurrency or a second durable attempt store until retry and deadline
evidence is available.

Gate: fixed-fixture fact coverage must not regress; P95 provider duration,
retry rate, invalid-response rate, output tokens, calls per window, and cache
rate must satisfy the outstanding Extraction v2 acceptance criteria.

Budget hardening on 2026-07-20 made the normal serial envelope explicit: one
Slice per durable worker job, a 120-second provider attempt deadline, one
provider attempt by default, and a three-minute job deadline. Startup rejects
configuration where the maximum serial provider budget, including retry
backoff, reaches or exceeds the worker deadline. Additional Slices are claimed
as later durable jobs instead of sharing one deadline. The fixed implicit-state
Run completed all ten physical calls under the 120-second deadline with zero
errors, retries, or timeouts.

The shared Extraction Execution Envelope now owns those defaults and validates
their aggregate arithmetic for production startup, runtime, queue workers,
provider calls, compaction, summaries, and extraction eval. Overflow and a
provider budget that leaves no outcome-persistence margin fail closed.

When synchronous compaction is enabled, validation reserves the initial
compaction call, one possible hard-limit fallback call, and the primary call
for every Slice. Background summary and compaction deadlines are derived from
the configured provider attempt and retry budget with an additional outcome
persistence margin; they no longer duplicate a fixed two-minute timeout.

## Tranche 2a: Atom-Level Extraction Loss Attribution

Status: Implemented

Export one ledger entry per required Atom from the extraction eval. The entry
must retain supporting Event IDs, whether those Events reached the fixed input,
whether they were reviewed, and the first observable loss stage and reason.
The ledger is diagnostic evidence, not a new quality score, and must not infer
an extraction cause from recall or judge output.

## Tranche 2b: Implicit State Review

Status: Rejected for promotion; retained for reproducibility

The `source-clause-implicit-state-v1` candidate asks the primary call to review
observable incomplete state and deadline-bound role capability before emitting
`no_state`, while preserving modality and excluding requests or suggestions.
Deterministic admission and the Candidate schema remain unchanged.

On the fixed `finance-micro6-quick` cohort it recovered `user_implicit_7`, but
still missed `multi_hop_1` and made the previously matched `temporal_7` and
`term_ambiguity_11` supporting Events unreviewed. Recall fell from 4/6 to 3/6,
leakage stayed zero, and admitted Notes rose from 12 to 19. The candidate is a
no-go and `source-clause-v1` remains the v2 evaluation default. See the
[result](../evals/extraction-v1/results/2026-07-20-implicit-state-review-v1-no-go.md).

## Tranche 3: Eval Validity and Attempt Ledger

Status: Implemented

Add an append-only Trial Attempt ledger behind the Eval Store seam. Preserve
each retry's stage, timing, exit classification, and artifact references rather
than retaining only the final Trial result. A validity gate must verify source
coverage, actual memory mutation, recall observation, stage artifacts, and
resolved configuration provenance before a comparative score is accepted.

Implemented on 2026-07-19: PostgreSQL now retains one row per claimed Attempt,
including stage, classified failure, timestamps, and the attempt-specific
artifact directory. The runner writes raw command output below
`trials/<case>/<arm>/attempts/<sequence>/` and exports `attempts.jsonl`. Resume
marks abandoned running Attempts as interrupted before reclaiming the Trial.
The comparative validity gate is now implemented as
`pax-eval-v3-validity-v1`. It requires the complete judged Trial matrix,
full-domain coverage in all three ingest receipts, observable mutations in
Team Note, Mem0, and private SQLite, successful recall observations for both
memory arms with the correct provider identity and call evidence, no provider
activity in the no-memory arm, completed canonical latest-Attempt
consumer/judge artifacts, and a resolved configuration hash matching the
durable Run. Invalid runs export `validity.json` plus raw evidence, remove
stale comparison reports, omit derived manifest scores, and return an
acceptance error. Judge-only recovery is recorded as a new immutable Attempt
through the locked durable Store seam; it verifies Run configuration, judges
the stored consumer result, and preserves retryability after judge failure or
an interrupted rejudge claim. Consumer evidence is selected from the newest
canonical, non-empty prior Attempt, not merely the highest Attempt number. Run
identity includes dataset revision in addition to configuration hash. Atomic
consumer publication and JSONL validation prevent partial evidence from
passing the gate.

## Tranche 4: Budget-Aware Final-State Selection

Status: Implemented

Keep `RecallNotes` unchanged and place selection behind `PlanRecall`. Optimize
for uncovered answer-bearing facts per token, final-state evidence, and
same-family deduplication. Add lane-local atom recall and unique lane
contribution to replay reports. Do not increase the global candidate limit or
lower the semantic threshold from one case.

Implemented on 2026-07-20. Current-mode planning first collapses candidates by
canonical Note ID or durable key and keeps the highest revision/effective
state; `as_of`, `history`, and `changes_since` retain their historical chains.
The planner then compares primary-plus-relation bundles by uncovered requested
fact slots before the existing token and item packing gates. Equal-gain cases
preserve the established evidence/ranking order, avoiding a recall regression
from token efficiency alone. Replay fixtures now retain the durable note key,
and traces record superseded-family rejection separately from ordinary budget
drops. No retrieval threshold or candidate limit changed.

## Tranche 5: Durable Historical Recall

Status: Implemented

Build reviewed `as_of`, `history`, and revision-transition fixtures before
exporting retired PostgreSQL Note revisions as recall candidates. Observation
time must select the historically visible revision and prevent future-revision
leakage. Treat this as contract correctness until an end-to-end gain is shown.

Implemented on 2026-07-20. Explicit historical Recall Intent reads immutable
`note_revisions` joined to the Candidate snapshot that created each revision.
`as_of` selects the latest revision valid at the requested time; `history`
returns the recorded-visible revision chain; `changes_since` includes later
transitions such as resolution. Every path applies Observation Time and the
revision's audience and task/thread scope. Planner-only revision IDs keep chain
members distinct, while delivery and the external `RecallNotes` envelope retain
the canonical Note ID plus revision. Historical replay independently exports
all revisions recorded by Observation Time as its Extraction Observation, then
exports retrieval candidates through authorization, scope, delivery, and query
gates. This preserves extraction/recall loss attribution and prevents a current
or future-recorded revision from contaminating the fixed fixture.

## Evaluation-only follow-up

Hint Recall remains evaluation-only. Work may improve activation selectivity or
focused-query utility on replay, but production enablement and a hard 30-case
cohort remain blocked.

## Closed lines

- Claim Card v1/v2
- Source Span v1/v2
- BM25 as the production candidate source
- global threshold or budget widening from a single case
- default Hint Recall or compaction enablement
