# Extraction v2

Status: Accepted with rollout exception; production default enabled, quality gates blocked

Date: 2026-07-16

Related:

- [General Recall v3 Optimization](./2026-07-16-general-recall-v3-optimization.md)
- [Multi-Agent GroupMemBench Eval v3](./2026-07-16-multi-agent-groupmembench-eval-v3.md)
- [Hint Recall v0](./2026-07-16-hint-recall-v0.md)
- [DeepSeek Context Caching](https://api-docs.deepseek.com/guides/kv_cache/)

## Context

Team Note extraction turns immutable Session Events into short-lived,
evidence-backed collaboration state. The current extractor already provides
important foundations:

- bounded Session Slices with explicit new and overlapping Events;
- cumulative Extraction Episodes across agents and sessions;
- stable candidate identities and create, update, and resolve actions;
- evidence-event validation and atomic Extraction Runs;
- checkpoint compaction and non-blocking continuity summaries;
- model, prompt version, token usage, and prompt-cache telemetry.

The remaining problem is not simply that the model sometimes omits a fact.
One model response currently has to discover source claims, decide whether they
represent new or existing knowledge, resolve temporal changes, preserve
provenance, and emit storage-ready Candidates. When those judgments are
collapsed into one unexamined generation step, a missing or incorrect Team Note
cannot be attributed to claim detection, identity resolution, state
transition, temporal interpretation, or deterministic admission.

Team memory makes this harder than personal memory. Multiple agents may report
the same state, disagree, hand work off, revise a deadline, or express concern
without establishing a new fact. The system must preserve speaker identity,
valid time, recorded time, disagreement, and responsibility without treating
the latest statement, the strongest sentiment, or the most frequent speaker as
automatically authoritative.

Extraction is also the dominant ingest cost. In the July 16 rolling-extractor
cohort, the token-weighted prompt-cache hit rate was approximately 82.8 to 85.2
percent, while mean model-call duration was approximately 46 to 49 seconds and
P95 duration was approximately 104 to 108 seconds. Extraction v2 must not gain
diagnostic clarity by routinely multiplying LLM calls or destroying the stable
prefix that currently produces those cache hits.

## Decision

Extraction v2 will separate extraction reasoning into explicit internal
products while executing them in one primary, cache-friendly LLM call.

The primary call produces:

1. grounded Claims found in the new Events;
2. proposed State Decisions that map those Claims onto canonical team state;
3. temporal and interaction observations needed to interpret those decisions;
4. an evidence-backed trace explaining each proposed decision.

Deterministic code then validates and admits the proposed decisions as one
atomic Extraction Run. A second targeted LLM call is permitted only for a
bounded set of high-risk ambiguities. It is not the default execution path.

The existing caller-facing seams remain unchanged:

- `teamnote.Runtime` remains the external module interface;
- `extractor.Extractor.Extract` remains the extraction seam;
- `teamnote.NoteStore.ApplyExtractionRun` remains the atomic persistence seam;
- Session Events remain factual evidence;
- Team Notes remain the passive-recall state;
- paxm provider and memory protocols do not change.

Extraction v2 deepens the extractor module rather than exposing its internal
reasoning stages to callers.

## Extraction products

### Claim

A Claim is a source-faithful assertion found in one or more new Session Events.
It records what was said or observed before deciding how canonical state should
change.

A Claim contains at least:

~~~yaml
claim_id: extraction-local stable identifier
claim_type: decision | status | blocker | handoff | artifact | schedule | correction
subject: stable subject
predicate: explicit relationship or property
value: source-faithful value
speaker: source Actor
evidence_event_ids: [event-id]
source_occurred_at: event timestamp
temporal_expression: optional verbatim expression
valid_at: optional resolved valid time
invalid_at: optional resolved invalid time
temporal_resolution: explicit | anchored | unresolved
~~~

Claims are not persisted Team Notes and are never delivered directly. They
exist so evaluation can distinguish failure to observe a source assertion from
failure to reconcile it with state.

Every Claim must cite at least one Event from `new_event_ids`. Prior episode
messages, checkpoints, summaries, and overlapping Events provide context but
cannot be the sole evidence for a new Claim.

### State Decision

A State Decision proposes how one or more Claims affect one canonical memory
identity:

~~~yaml
decision: create | update | resolve | no_change | keep_conflict_open
identity_ref: stable memory identity
claim_ids: [claim-id]
prior_state_ref: optional prior revision or episode identity
reason_codes:
  - explicit_new_fact
  - explicit_correction
  - same_obligation_new_deadline
  - explicit_resolution
  - corroborating_report
  - conflicting_report
  - insufficient_temporal_anchor
candidate: optional storage-ready Candidate
~~~

`no_change` preserves corroborating evidence or a trace without creating a
duplicate Team Note. `keep_conflict_open` records that the source does not
justify collapsing disagreement into one current fact. Neither decision is a
new externally visible Note state by itself in v2.

Create, update, and resolve decisions may produce the existing
`teamnote.Candidate` shape. Deterministic admission remains responsible for
validating actions, kinds, identity references, evidence, scope, audience,
timestamps, limits, and idempotency.

### Interaction Observation

Extraction v2 may identify interaction signals needed to understand team
coordination:

- stance: support, oppose, question, or neutral;
- expressed concern or urgency;
- explicit uncertainty;
- acknowledgment, commitment, request, rejection, or escalation.

These signals must cite source Events and remain observations about an
interaction. Sentiment does not change factual confidence, authority,
authorization, or temporal truth. A strongly negative statement is not better
evidence than a neutral one, and repeated enthusiasm does not turn a proposal
into an approved decision.

Interaction observations may help preserve an unresolved conflict, prioritize
an explicit blocker, or later seed Hint Recall. They are not passively delivered
as factual Team Notes unless the underlying content independently satisfies the
normal Candidate rules.

### Agent Profile Observation

Extraction v2 may emit evidence that an agent has worked in a domain, owned a
responsibility, produced an artifact, or completed a type of task. Consolidation
may gradually accumulate this evidence into an **Observed Agent Profile**.

An Observed Agent Profile:

- requires evidence from multiple independent tasks or artifacts;
- retains evidence IDs, first-seen time, last-seen time, and freshness;
- decays when evidence becomes stale;
- describes demonstrated activity, not permission or authority;
- never silently edits an explicit Agent Specification.

The Agent Registry continues to own declared Agent Specifications. A declared
skill is an explicit contract; an observed domain is an evidence-backed routing
signal. Extraction v2 records observations but does not introduce a second
specification authority inside Team Note.

## Temporal model

Extraction v2 keeps Valid Time and Recorded Time separate:

- **Source Occurred Time** is attached to the immutable Session Event;
- **Valid Time** describes when the collaboration fact holds in its domain;
- **Recorded Time** describes when Team Memory learned or admitted the fact.

The extractor must preserve a source temporal expression before attempting to
resolve it. Absolute timestamps stated in the source may become Valid Time.
Relative expressions may be anchored to the source Event time only when the
reference and timezone are unambiguous. Otherwise they remain unresolved.
Ingestion time must never be substituted for Valid Time.

Later recorded does not mean currently valid. A later statement updates or
resolves an earlier fact only when identity and semantics support that state
transition. Otherwise the extractor preserves both reports as corroborating,
historical, or conflicting evidence.

## Primary-call execution

The normal execution path is:

~~~text
stable system instructions and stable response schema
    + append-only Extraction Episode prefix
    + checkpoint only at an explicit compaction boundary
    + current state delta and new Events at the suffix
        -> one primary LLM response
        -> deterministic validation and normalization
        -> one atomic Extraction Run
~~~

Logical separation does not imply serial model calls. Claims, State Decisions,
temporal observations, interaction observations, and the decision trace are
fields of one structured response.

The response must be bounded. It should avoid restating the entire episode or
canonical state and should return only Claims and decisions caused by the new
Events. Corroboration that causes no state change should be represented
compactly rather than reproducing an existing Candidate body.

## Targeted verification

A second LLM call is allowed only when deterministic validation identifies a
high-risk ambiguity that cannot be safely resolved from the primary response.
Initial trigger classes are:

- conflicting reports about the same identity with no explicit resolution;
- an update or resolve action whose prior identity is uncertain;
- a relative time expression with multiple plausible anchors;
- an apparent correction whose scope or subject may differ from the prior fact;
- an authority-sensitive claim such as approval when the source establishes
  neither authority nor an explicit decision.

The verifier receives the smallest relevant evidence set, the proposed
decision, and the competing prior state. It may confirm the proposal, keep the
conflict open, or reject the state transition. It cannot introduce a Claim or
evidence Event absent from the primary extraction input.

Targeted verification must remain below ten percent of primary extraction
calls on the fixed cohort. Exceeding that rate is an architecture failure to be
addressed through the primary schema or deterministic policy, not a reason to
normalize a two-call pipeline.

## Cache and latency policy

Prompt caching is a design constraint. DeepSeek cache reuse requires matching
prefixes from the beginning of the request, so dynamic content placed near the
front invalidates reuse for everything after it.

Extraction v2 therefore requires:

- stable system instructions, response schema, and few-shot ordering;
- a versioned prompt whose changes are treated as cache-breaking migrations;
- deterministic serialization and ordering for structured context;
- append-only episode messages during a warm episode;
- current state deltas and new Events at the dynamic suffix;
- no fully reserialized canonical snapshot at the front of every call;
- explicit classification of primary, verifier, compaction, and summary calls;
- separate reporting for warm calls, cold calls, and the first call after
  compaction or a prompt-version change.

Checkpoint compaction intentionally replaces an old prefix and may reset cache
reuse. This is an observable transition, not a hidden regression. Compaction
frequency and post-compaction recovery must be reported with the cache metrics.

Every model call records, when the provider exposes them:

- prompt input, output, cache-hit, and cache-miss tokens;
- token-weighted cache-hit rate;
- per-call cache-hit rate;
- time to first token;
- total model-call duration;
- end-to-end Slice-to-Extraction-Run duration;
- retry, timeout, and response-validation outcomes;
- call type and prompt version.

The token-weighted cache-hit rate is:

~~~text
cache_hit_tokens / (cache_hit_tokens + cache_miss_tokens)
~~~

Aggregate cache rate alone is insufficient because a few long warm calls can
hide many cold calls. Evaluation reports both the aggregate and the per-call
distribution.

## Trace and provenance

The Extraction Trace is diagnostic and never enters passive agent context. For
each source Claim and State Decision it records:

- supporting Event IDs and source Actor;
- temporal expression and resolution mode;
- matched prior identity, if any;
- proposed action and reason codes;
- deterministic validation results;
- whether targeted verification ran and its outcome;
- admitted Candidate ID or rejection reason.

An Extraction Run persists enough usage and timing provenance to compare prompt
versions without relying only on process logs. Existing atomic and idempotent
Candidate admission remains unchanged.

## Evaluation plan

Extraction v2 uses two complete decision experiments. Individual mechanisms do
not become separate end-to-end arms.

### Experiment 1: Extraction v2 Shadow

The current extractor and Extraction v2 consume the same fixed Session Events,
episode partitioning, model, context budget, and annotated extraction fixture.
Extraction output is compared before recall. V2 runs in shadow mode and does
not affect delivered Team Notes.

This experiment jointly evaluates:

- Claim coverage and evidence precision;
- create, update, resolve, no-change, and open-conflict decisions;
- current-state and temporal accuracy;
- duplicate and superseded-fact leakage;
- cache use, token use, call amplification, latency, and failures.

Recall remains fixed and is not used to explain an extraction regression.

### Experiment 2: Team and Temporal Consolidation

After Experiment 1 passes, the same architecture is evaluated on multi-agent,
multi-session histories containing handoffs, disagreement, corrections,
relative time, changing responsibility, interaction signals, and repeated
domain evidence.

This experiment evaluates the complete consolidation policy, including
Observed Agent Profile formation. Sentiment, domain evidence, and temporal
resolution are not given separate public arms. Eval v3 is run only after the
fixed stage replay shows that supporting state is extracted and admitted.

## Experiment 1 shadow result (2026-07-17, does not pass)

The v2 protocol was implemented behind the unchanged `extractor.Extractor`
seam (`TEAM_MEMORY_EXTRACTION_VERSION`, default v1) and replayed through the
real pipeline over the fixed ten-case cohort exported from the
`groupmembench-finance-v2-recall050-10-20260717-r2` store
(`runs/extraction-shadow/stage10-v1-20260717-r1` and
`stage10-v2-20260717-r2`). Both arms consumed identical Session Events,
episode partitioning, model, and context budget. The stage evaluator and
fixtures were unchanged; recall was not run.

| Metric | v1 | v2 | Gate |
|---|---|---|---|
| Extraction fact recall | 0.435 (10/23) | 0.348 (8/23) | improve materially: **fail** |
| Leakage | 1 | 0 | no increase: pass |
| Calls per slice | 1.00 | 1.00 | <= 1.1: pass |
| Call errors | 0 | 0 | no increase: pass |
| Token-weighted cache rate | 0.853 | 0.814 | >= 0.80, drop <= 5pp: pass (-3.9pp) |
| Mean call duration | 18.6 s | 26.9 s | no regression: **fail (+45%)** |
| P95 call duration | 41.7 s | 60.9 s | <= baseline: **fail (+46%)** |
| Output tokens | 239,203 | 402,245 | -20%: **fail (+68%)** |

Per-case fact recall was within run-to-run noise of v1 (v2 gained
knowledge_update_23, lost multi_hop_45 and knowledge_update_8; the other
seven cases matched). The cost regressions are structural, not noise: every
fact is written twice, once as a Claim and again as a State Decision
candidate.

The v2 trace provides the first stage-attributed loss taxonomy for this
cohort:

- **Claim detection failure dominates.** Six of the eight atoms missing in
  both protocols (multi_hop_20, knowledge_update_8, multi_hop_45,
  knowledge_update_24, knowledge_update_27) have zero claim coverage: the
  model never records the fact in either protocol, so no state-reconciliation
  architecture can recover them.
- **Mapping loss exists but is smaller.** multi_hop_13's atom was present as
  a Claim ("Ops Lead owns rollback evidence pack") but no admitted note
  preserved the composition.
- **Decision identity discipline is weak.** 516 decisions: 391 create, 72
  update, 53 no_change, 0 resolve. The model creates parallel facts instead
  of updating and never retires anything.
- **Deterministic validation rarely fires** (0 claim rejections, 2 decision
  rejections across 111 calls), so rejections explain no material loss.
- **Targeted verification would fail its own budget.** Would-verify triggers
  fired on 31 to 48 percent of slices, all unresolved_temporal_anchor. A
  second call at that rate violates the ten-percent ceiling; the primary
  prompt must anchor more temporal expressions before a verifier is added.

Consequences for the architecture:

1. The two-product schema as specified costs +68 percent output tokens for no
   measured recall gain. Before any second shadow, the schema must be slimmed:
   decisions carry candidates, claims are emitted only where separation
   changes the outcome (corrections, conflicts, temporal expressions), and
   corroboration stays out of the response.
2. The binding constraint on this cohort is claim detection, not
   reconciliation. Prompt-level improvements to observation completeness
   (per-speaker coverage checks, unmapped-claim review inside the primary
   call) address more loss than any reconciliation machinery.
3. Temporal anchoring must move into the primary schema (anchor to the
   source event time with the expression preserved) rather than a second
   call.
4. The shadow harness (`cmd/team-memory-extraction-shadow` with
   `internal/eval/extractionshadow`) and the per-slice trace are retained as
   the evaluation loop for these iterations; v1 remains the production
   default and v2 stays opt-in.

## Iteration 2 implementation (2026-07-17, shadow pending)

The first shadow result identified deterministic defects and structural cost
that could be fixed without changing the external extractor, runtime, Note
Store, or paxm seams. The second v2 protocol revision was implemented as
`v2-slim-1`; its deterministic admission follow-up is `v2-slim-2`, with these
changes:

- ordinary create, update, and resolve decisions cite Session Events directly;
  a separate Claim is emitted only for conflicts, ambiguous corrections,
  unresolved temporal interpretations, or corroboration that causes no state
  change;
- every new Event must be cited by a decision or Claim, or explicitly listed
  as `no_state`; the trace records unreviewed Events, invalid `no_state`
  declarations, and orphan Claims;
- the stable prompt requires an atomic coverage pass over owners, deadlines,
  exact values, conditions, exceptions, dependencies, and state changes;
- deterministic mapping now preserves `valid_at`, `invalid_at`, and related
  subjects instead of dropping them between State Decision and Candidate;
- temporal metadata is rejected when it lacks a source expression, uses an
  invalid RFC3339 window, or marks an unresolved expression with a resolved
  validity timestamp;
- interaction observations are retained only when their stance and speech act
  use the explicit vocabulary and their evidence cites a new source Event;
- proposal, request, question, acknowledgment, concern, and urgency evidence
  cannot by themselves create, update, or resolve canonical state; an explicit
  approval, commitment, rejection, handoff, escalation, or other factual
  evidence is required, and the rule is enforced after model generation;
- the production-default v1 path applies a narrower deterministic guard for
  explicit proposal and assignment-request phrasing, so this safety fix does
  not depend on adopting the slower v2 response protocol;
- rolling episodes are reset when the model, operator prompt version, or
  internal response-protocol revision changes, preventing a v2 worker from
  replaying v1 assistant responses;
- v2 Extraction Runs use a protocol-scoped idempotency key, so a prior v1 run
  for the same Slice cannot silently win;
- shadow telemetry reports coverage and mapping loss alongside claims,
  decisions, verification triggers, tokens, cache rate, and latency.

This implementation does not make v2 the production default. The fixed paid
shadow must be repeated because prompt-level observation completeness, output
token reduction, cache behavior, and latency cannot be established by unit
tests. Until all Acceptance gates pass, `TEAM_MEMORY_EXTRACTION_VERSION`
continues to default to `v1`.

### Partial micro-canary result

A fresh v2 rolling micro-canary was stopped before completion because the
profile was already outside the latency and cost envelope. After 16 completed
model calls it had consumed 1,118,499 input tokens and 97,723 output tokens;
observed Slice durations ranged from 91.7 to 308.7 seconds. Continuing the run
would not have established an acceptable v2 profile, so no end-to-end quality
claim is made from this partial artifact. The deterministic non-committal
speech-act guard is covered by unit tests, `v2` remains opt-in, and `v1`
remains the default until a cache- and latency-feasible profile passes the
paired fixed shadow.

## Production default decision (2026-07-17)

The product owner explicitly chose to make Extraction v2 the service and
deployment default despite the failed shadow latency, token, and fact-recall
gates above. This is an operator rollout decision, not a reinterpretation of
the shadow result and not evidence that the Acceptance gates passed.

The application, checked-in environment templates, root Compose stack, Eval v2
environment loader, Eval v2 Compose stack, and OpenCode eval stack now default
`TEAM_MEMORY_EXTRACTION_VERSION` to `v2`. Operators can set
`TEAM_MEMORY_EXTRACTION_VERSION=v1` for an immediate protocol rollback. A
protocol change still starts a fresh rolling episode, so v1 and v2 assistant
responses are not mixed.

The remaining acceptance debt is explicit:

- repeat the paired fixed shadow for `v2-slim-2`;
- measure fact recall, leakage, output tokens, cache behavior, latency, and
  failures under the default configuration;
- do not describe the default switch as a measured extraction-quality win;
- roll back to v1 if the production latency or cost envelope is unacceptable.

## Quality acceptance (outstanding)

Extraction v2 may replace the current extractor only when the paired fixed
cohort satisfies all of the following:

- extraction Fact Recall improves materially without reducing evidence
  precision or increasing leakage;
- state-transition and temporal errors are attributable through the Extraction
  Trace;
- unresolved conflicts are not silently collapsed into current facts;
- token-weighted prompt-cache hit rate remains at least 80 percent and falls by
  no more than five percentage points from the paired baseline;
- average model-call count remains at or below 1.1 per extraction window;
- P95 model-call duration does not exceed the paired baseline, with a target of
  at least 15 percent improvement;
- output tokens decrease by at least 20 percent or an equivalent measured
  latency improvement is demonstrated without additional calls;
- timeout, retry, invalid-response, and failed-trial rates do not increase;
- post-compaction cold-call behavior and recovery are reported separately;
- the multi-agent experiment does not promote sentiment or Observed Agent
  Profile evidence into authority or authorization.

Thresholds are evaluated on the same fixed cohort. A result from one case does
not justify a global prompt, limit, or compaction change.

## Extraction Eval v1 result (2026-07-17, blocked)

The first independent `extraction-eval-v1` full-domain run is recorded in
[`evals/extraction-v1/results/2026-07-17-finance-micro6-v2-r1.md`](../../evals/extraction-v1/results/2026-07-17-finance-micro6-v2-r1.md).
It produced raw fact recall of 1/6, three leakage matches, 74 error-free
recorded primary calls, 416,021 output tokens, 6.91 percent token-weighted
cache reuse, 42.8 second mean latency, and 100.9 second P95 latency. Periodic
summary calls are currently folded into extraction usage rather than counted
as separate provider calls, so the harness must expose call type before the
next cost comparison.

This run does not satisfy or fairly measure the six-case quality gate. Its
persisted source contains supporting phases for only three fixture cases; the
other three required facts are absent from the extraction input. Within the
supported subset, the trace confirmed two admission defects: grounded current
state was rejected when the model emitted an otherwise empty `unresolved`
temporal resolution, and proposal/request language could become current state
when its invalid interaction observation was dropped before the speech-act
guard.

Both defects now have deterministic regressions and admission fixes. The paid
artifact predates those fixes. Quality acceptance remains blocked until a
preflight-validated six-case source is regenerated and the fixed v2 protocol
is compared with its control on identical persisted session events. Performance gates
also remain failed: the run consumed 52.8 minutes of model-call time and
approximately US$0.58 for one physical domain.

### Quick optimization loop (implemented, paid canary pending)

`extraction-eval-v1` now has a tracked `finance-micro3-quick` profile covering
the positive multi-hop sentinel, the temporal-admission knowledge update, and
the proposal-leakage abstention case. A live zero-cost source preflight reduced
the persisted Finance input from 1,605 session events to 65 session events over four streams
and exactly five expected primary slices. Preflight fails before extractor
construction when a selected case has no supporting event IDs, a referenced
session event is absent, or the selected cohort exceeds its six-slice ceiling.

The harness now journals every completed slice and rolling episode so an
interrupted run can rebuild notes from saved responses and continue at the
first unpaid slice. Resume validates model, endpoint, protocol variant, prompt,
source scope, selected event IDs, and slice size before reusing artifacts.
Physical provider calls are separately journaled and classified as primary,
summary, compaction, or verifier. Completed asynchronous summaries are persisted
into the rolling episode before the run is declared resumable, and their usage
is no longer hidden inside the primary-slice count.

Two protocol variants are available on the identical quick input: `current`
and `interaction-slim`. The latter requires an empty interaction-observation
array and relies on deterministic source-evidence admission to block proposals
and requests. The paired runner is implemented, but no paid comparison is
recorded yet: the configured external extractor requires explicit approval to
send the selected Finance session-event content. Quality and performance gates remain
blocked until that paired artifact exists.

## Consequences

Extraction failures become attributable to source-claim detection, identity
resolution, state transition, temporal resolution, targeted verification, or
deterministic admission. This supports faster optimization without using recall
metrics to diagnose extraction.

The extractor implementation becomes deeper while its caller interface stays
small. The structured response and trace add internal schema and persistence
work, but callers do not need to orchestrate extraction stages.

Single-call execution preserves the current rolling-prefix cache advantage and
avoids a routine latency multiplier. Stable prompt and serialization rules make
cache behavior part of compatibility rather than an accidental optimization.

Observed Agent Profiles provide evidence-backed routing information without
turning Team Note into an authority registry. Interaction observations preserve
important team dynamics without allowing sentiment to contaminate factual
state.

## Non-goals

Extraction v2 does not:

- change paxm provider or memory protocols;
- redesign recall ranking, Hint Recall, or budget packing;
- make LLM Wiki pages from Session Events;
- infer authorization, approval power, team membership, or declared skills;
- replace explicit Agent Specifications with inferred profiles;
- treat summaries, checkpoints, prior model responses, or sentiment as factual
  evidence;
- add extraction ablations to the three-arm Eval v3 product narrative.

## Rejected alternatives

### Run claim extraction and state reconciliation as two mandatory LLM calls

Rejected because it doubles the normal call path, increases tail latency and
failure surface, and reduces reuse of the existing rolling prefix. Logical
separation is retained in one structured response and in the trace.

### Put the complete canonical state before each new Event batch

Rejected because frequently changing content near the beginning of the prompt
invalidates later prefix-cache reuse. Append-only episode history, suffix
deltas, and explicit compaction boundaries preserve more cacheable context.

### Let the model directly mutate Team Notes

Rejected because evidence, scope, action, identity, temporal, and idempotency
rules must remain deterministic and atomically enforceable.

### Treat the latest statement as current truth

Rejected because recorded order does not establish Valid Time, identity,
authority, or resolution of disagreement.

### Use sentiment to adjust factual confidence

Rejected because emotional strength is not evidence quality. Sentiment remains
an evidence-backed interaction observation only.

### Let consolidation rewrite Agent Specifications

Rejected because observed behavior and declared capability have different
ownership and trust semantics. Consolidation may maintain an Observed Agent
Profile; the Agent Registry owns explicit specifications.

### Create a separate experiment for every extraction mechanism

Rejected because fragmented experiments increase cost, prompt churn, cache
invalidations, and interpretation overhead. The two experiments test complete
architectural decisions while the Extraction Trace provides stage attribution.
