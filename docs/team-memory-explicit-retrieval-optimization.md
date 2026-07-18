# Team Memory Optimization: Explicit Retrieval Research Synthesis

Status: Proposal

Date: 2026-07-17

Related decisions:

- [Extraction v2](./decisions/2026-07-16-extraction-v2.md)
- [General Recall v3 Optimization](./decisions/2026-07-16-general-recall-v3-optimization.md)
- [Hint Recall v0](./decisions/2026-07-16-hint-recall-v0.md)
- [Multi-Agent GroupMemBench Eval v3](./decisions/2026-07-16-multi-agent-groupmembench-eval-v3.md)
- [Recall Stage Trace and Deterministic Replay](./decisions/2026-07-17-recall-replay-and-stage-trace.md)

## Executive recommendation

Team Memory should optimize in this order:

1. make extraction preserve atomic claims, current-state transitions, typed
   relations, and temporal revisions;
2. replace the global dense-vector relevance gate with an explicit Recall Plan
   built from exact fields, lexical evidence, typed relations, temporal state,
   and agent responsibility;
3. select the smallest evidence set that covers the question's explicit answer
   slots, then expose uncovered slots as Hint Recall or active-recall paths;
4. build an evidence-backed directory of who knows what, while keeping observed
   expertise separate from declared Agent Specifications and authority.

The central product rule is:

> Retrieval is reasoning. Every retrieval step must expose what it was trying
> to find, which observable structure it followed, what evidence it found, and
> why it selected, hinted, or rejected that evidence.

The target architecture does not use a dense vector as an authoritative recall
or ranking signal. The current 384-dimensional semantic lane may remain as a
temporary shadow baseline during migration, but it should not decide whether a
fact is current, supports an answer slot, or deserves passive delivery.

## Evidence from the current repository

This proposal uses existing artifacts only. No new eval was run for this
analysis.

### Fixed recall replay

The ten-case persisted-note replay showed that lowering the semantic threshold
from 0.65 to 0.50 recovered available evidence:

| Arm | Policy | Extraction fact recall | Gold recall | Conditional recall | Recall leakage |
|---|---:|---:|---:|---:|---:|
| team_note | 0.65 baseline | 0.348 | 0.261 | 0.750 | 0 |
| team_note | 0.50 + dedup + degrade | 0.348 | 0.348 | 1.000 | 0 |
| team_note_hybrid | 0.65 baseline | 0.435 | 0.348 | 0.700 | 0 |
| team_note_hybrid | 0.50 + dedup + degrade | 0.435 | 0.435 | 0.900 | 0 |

The 0.50 policy was better than 0.45 on cost and context dilution while
recovering the same gold atoms. This justified the current default, but it did
not establish that one global vector threshold generalizes across extraction
states. Evidence:

- [0.65 team_note replay](../runs/recall-replay/baseline-team_note/replay-summary.json)
- [0.50 team_note replay](../runs/recall-replay/t0.50-dd-team_note/replay-summary.json)
- [0.65 hybrid replay](../runs/recall-replay/baseline-team_note_hybrid/replay-summary.json)
- [0.50 hybrid replay](../runs/recall-replay/t0.50-dd-team_note_hybrid/replay-summary.json)

### Full live rolling cohort

The subsequent ten-case live cohort used rolling extraction and the 0.50
policy. It did not reproduce the replay result uniformly:

| Arm | Completed | Judge accuracy | Extraction fact recall | Conditional recall |
|---|---:|---:|---:|---:|
| direct_context | 10/10 | 0.40 | n/a | n/a |
| team_note | 9/10 | 0.30 | 0.50 | 0.636 |
| team_note_hybrid | 9/10 | 0.10 | 0.30 | 1.00 |

The two Team Note arms each had one infrastructure-killed trial, the cohort is
small, and the stage denominators differ because of those failures. These
numbers therefore do not support a product-quality comparison. They do prove
that a threshold tuned on one persisted candidate distribution is not a stable
recall architecture. Evidence:

- [live summary](../runs/eval-v2/groupmembench-finance-v2-recall050-10-20260717-r1/summary.csv)
- [team_note stage summary](../runs/eval-v2/groupmembench-finance-v2-recall050-10-20260717-r1/stage/team_note/stage-summary.json)
- [hybrid stage summary](../runs/eval-v2/groupmembench-finance-v2-recall050-10-20260717-r1/stage/team_note_hybrid/stage-summary.json)

### Failure attribution

The live traces identify three different failure classes.

**Current state existed but lost the budget.** In `knowledge_update_5`, the
extractor produced the correct update: pause scenario creation for the flagged
bucket and have Legal and Ops freeze trigger-impacting fields and audit-trail
rules. Recall instead selected several older bucket-tagging and ownership notes.
The correct current-state note was rejected at the token budget. This is not an
embedding-model problem; it is missing state-chain and answer-slot priority.

**Evidence was present but the delivered set encouraged over-answering.** In
`multi_hop_15`, the required answer was the rerun slot and timestamped evidence
linkage. Recall delivered a broad collection of build hashes, owners, ETAs,
blockers, and QA conditions. The judge rejected the answer for adding
substantive requirements beyond the gold answer. This is a minimal-sufficient-
context and claim-atomicity problem.

**Required evidence never reached canonical state.** `multi_hop_20`,
`knowledge_update_8`, `knowledge_update_24`, and `knowledge_update_27` had zero
matched extracted atoms in the live Team Note arm. Recall cannot recover these
cases. Extraction remains the first optimization priority.

The stage observations containing these traces are available in:

- [team_note observations](../runs/eval-v2/groupmembench-finance-v2-recall050-10-20260717-r1/stage/team_note/observations.jsonl)
- [hybrid observations](../runs/eval-v2/groupmembench-finance-v2-recall050-10-20260717-r1/stage/team_note_hybrid/observations.jsonl)

## What the literature contributes

The following papers are useful because they expose a structural mechanism,
not because their full architecture should be copied.

| Work | Useful evidence or mechanism | Decision for Team Memory |
|---|---|---|
| [GroupMemBench](https://arxiv.org/abs/2605.14498) | Multi-party memory depends on speaker-grounded belief, group dynamics, audience language, updates, ambiguity, time, and abstention. BM25 matches or beats many memory systems; the paper attributes failures to ingestion erasing lexical and structural information. | Preserve speaker, reply, lexical identifiers, and update structure. Do not assume denser retrieval fixes lossy extraction. |
| [LongMemEval](https://arxiv.org/abs/2410.10813) | Separates extraction, multi-session reasoning, temporal reasoning, updates, and abstention. Reports gains from session decomposition, fact-augmented keys, and time-aware query expansion. | Keep stage control loops separate. Add explicit fact keys and temporal intent rather than a larger top-k. |
| [EverMemBench](https://arxiv.org/abs/2602.01313) | Finds that temporal reasoning needs version semantics beyond timestamp matching and that similarity retrieval struggles with implicit relevance. | Represent revision and supersession chains. Use typed paths to bridge implicit relations. |
| [MemoryAgentBench](https://arxiv.org/abs/2507.05257) | Frames memory as accurate retrieval, test-time learning, long-range understanding, and selective forgetting. | Evaluate supersession and forgetting as first-class behavior, not only Recall@k. |
| [Zep temporal knowledge graph](https://arxiv.org/abs/2501.13956) | Connects episodes to extracted facts, preserves source provenance, and models valid and invalid times. | Adopt bi-temporal facts, source links, and invalidation. Do not adopt dense retrieval as the authoritative selector. |
| [Collaborative Memory](https://arxiv.org/abs/2505.18279) | Separates private and shared memory, attaches immutable provenance, and applies current access policy when projecting a view. | Preserve private SQLite plus shared Team Note topology, and recheck ACL at recall time. |
| [G-Memory](https://arxiv.org/abs/2506.07398) | Separates fine-grained interaction trajectories, query structures, and reusable team insights. | Keep raw team interaction, query trails, and consolidated insight as different memory products. Do not flatten them into one note collection. |
| [Transactive Memory Systems](https://pubsonline.informs.org/doi/abs/10.1287/orsc.1040.0069) | Models team memory around task-expertise-person units and distinguishes accuracy, sharedness, and validation of the expertise directory. | Build an evidence-backed “who knows what” directory. Observed expertise is routing evidence, not authority. |
| [IRCoT](https://arxiv.org/abs/2212.10509) and [ReAct](https://arxiv.org/abs/2210.03629) | Multi-hop retrieval improves when the next retrieval action is derived from what has already been found, with an inspectable action trajectory. | Active recall should expose subqueries, traversed relations, observations, and stopping reasons. |
| [FLARE](https://arxiv.org/abs/2305.06983) and [Self-RAG](https://arxiv.org/abs/2310.11511) | Retrieval can be triggered only when generation needs more support; indiscriminate retrieval can hurt output. | Hint Recall is the safe adaptation: expose a promising search action without injecting uncertain text as evidence. Do not depend on hidden token confidence as the product contract. |
| [Mem0](https://arxiv.org/abs/2504.19413) | Uses dynamic extraction and consolidation, with an optional graph representation for relational memory. | Retain explicit create, update, resolve, and no-change decisions, but evaluate them on group-memory updates rather than relying on LoCoMo results. |
| [A-MEM](https://arxiv.org/abs/2502.12110) and [EverMemOS](https://arxiv.org/abs/2601.02163) | Organize memory into linked notes or episodic cells and consolidated scenes that evolve over time. | Borrow typed linking and slow consolidation. Reject unversioned LLM rewrites of historical memory and opaque scene-only retrieval. |

### Research synthesis

Across these works, the transferable pattern is not “use a graph” or “use an
agentic retriever.” It is the separation of five things:

1. immutable episodes;
2. atomic, source-linked claims;
3. current and historical state;
4. an explicit directory of entities, relations, time, and expertise;
5. a query-time plan that retrieves the smallest sufficient evidence set.

This matches the current repository's strongest seams: Session Events,
Extraction Episodes, atomic Extraction Runs, Team Notes, `PlanRecall`, and
Recall Trace.

## Target memory model

The target model remains a deep Team Note module behind the existing
`Runtime` interface.

~~~text
immutable Session Events
    -> Extraction v2
       - atomic Claims
       - State Decisions
       - typed Relations
       - temporal revisions
       - interaction/profile observations
    -> canonical Team State
       - current Team Notes
       - revision/supersession chains
       - evidence links
       - explicit indexes
    -> Recall Plan
       - intent and answer slots
       - retrieval lanes and paths
       - hard gates
       - coverage-aware set selection
    -> evidence | hint | suppress
~~~

`RecallNotes` remains unchanged for callers. The planner and trace become
deeper behind `PlanRecall`; paxm does not need a new protocol.

## Optimization 1: finish Extraction v2 as canonical-state construction

Extraction v2 should produce atomic Claims and explicit State Decisions in one
cache-friendly model call, as specified in its ADR. The implementation should
optimize for the failure classes visible in the live cohort:

- one independent claim per owner, deadline, control, condition, or state
  transition;
- a stable `identity_ref` that is shared across agents reporting the same
  real-world obligation or decision;
- explicit create, update, resolve, no-change, and keep-conflict-open decisions;
- Valid Time, Recorded Time, and source occurrence kept separate;
- typed relations such as `supersedes`, `depends_on`, `blocks`, `assigned_to`,
  `handoff_to`, `evidence_for`, and `disagrees_with`;
- source verbatim identifiers and lexical aliases retained beside normalized
  entities;
- sentiment and stance emitted only as interaction observations;
- agent-domain observations emitted with task and artifact evidence.

The deterministic admission layer validates evidence, state preconditions,
scope, audience, relation endpoints, and temporal fields. Checkpoints and model
responses remain context, never factual evidence.

The primary quality gate is extraction Fact Recall with evidence precision and
superseded-fact leakage. Recall must stay fixed during this calibration.
Extraction latency, prompt-cache hit rate, TTFT, output tokens, and targeted-
verifier rate are co-equal acceptance constraints.

## Optimization 2: replace semantic gating with an explicit Recall Plan

### Recall Intent

The query is compiled into a structured, inspectable intent. The first version
should be deterministic and should not add an LLM call to passive recall.

~~~yaml
mode: current | as_of | changes_since | history | discover
answer_slots:
  - type: owner
    subject: rollback evidence pack
    condition: July 26 milestone slips
entities:
  - rollback evidence pack
actors: []
artifacts: []
temporal:
  valid_at: optional
relations:
  - assigned_to
  - contingent_on
scope:
  task_ref: optional
  thread_ref: optional
budgets:
  relation_hops: 1
  tokens: 500
~~~

Unknown fields remain unresolved. The compiler must not turn a model guess into
a hard constraint.

### Explicit retrieval lanes

Each lane returns candidates plus reason codes. Lanes are unioned; they are not
compressed into one relevance score.

1. **Exact field lane**: task, thread, identity, artifact, actor, external ID,
   path, date, and canonical entity aliases.
2. **Lexical lane**: Postgres full-text and observable term/field matches.
   Persisted production traces retain field categories and match counts rather
   than plaintext query terms.
3. **Temporal/state lane**: current revision, as-of revision, changed-since,
   superseded-by, resolved, and open-conflict chains.
4. **Typed relation lane**: bounded traversal over a whitelist, with direction
   and the complete path recorded.
5. **Coordination lane**: explicit owner, handoff, waiting-on, request,
   commitment, approval, rejection, and escalation acts.
6. **Agent directory lane**: declared Agent Specification first, then
   evidence-backed Observed Agent Profile.

The current vector lane becomes shadow-only while the explicit planner is
validated. It may report what it would have retrieved, but it does not admit
passive evidence or determine the final ordering. If explicit lanes match or
improve the fixed and live cohorts, embeddings and their global threshold can
be removed from the production recall path.

### Hard gates

Authorization, audience, collaboration scope, task/thread scope, delivery
eligibility, current/as-of state, invalidation, and expiry remain hard gates.
No relevance or popularity signal may compensate for a failed gate.

### Coverage-aware set selection

Selection optimizes an explicit tuple rather than a weighted relevance score:

~~~text
(
  satisfies_current_state,
  uncovered_answer_slots,
  direct_before_traversed,
  correction_or_conflict_visibility,
  source_provenance_quality,
  incremental_token_cost,
  stable_tie_breaker
)
~~~

The planner first selects evidence that covers previously uncovered answer
slots. It then adds only evidence that supplies a required qualifier,
correction, conflict, or relation hop. A related note is not automatically
appended because it shares a subject. This directly addresses the live
`knowledge_update_5` and `multi_hop_15` failures.

The selected output is a minimal evidence bundle:

~~~yaml
slot: current_action_for_flagged_bucket
evidence:
  - note_id: ...
    revision: ...
    claim: pause scenario creation
  - note_id: ...
    revision: ...
    claim: Legal and Ops freeze trigger fields and audit-trail rules
excluded:
  - note_id: ...
    reason: superseded_state
  - note_id: ...
    reason: no_incremental_slot_coverage
  - note_id: ...
    reason: token_budget_lower_priority
~~~

### Recall Trace v2

The existing trace proves where candidates are dropped, but it does not yet
explain retrieval as reasoning. Trace v2 should add:

- parsed intent and answer slots;
- lane membership, query-safe match counts, fields, and alias categories;
- typed relation paths and hop counts;
- temporal revision selected and superseded revisions rejected;
- hard-gate outcomes;
- slot coverage before and after each selection;
- incremental token cost;
- evidence, hint, or suppress disposition;
- a stable reason code for every rejection.

The trace is a structured decision artifact, not hidden chain-of-thought, and
never enters the agent context.

## Optimization 3: make team memory transactive and agent-directed

Team memory should remember both shared state and where knowledge lives.

### Agent Specification and Observed Agent Profile

The Agent Registry owns explicit Agent Specifications. Consolidation may build
an Observed Agent Profile from repeated evidence:

~~~yaml
agent_id: agent-7
observed_domains:
  - domain: deployment security
    independent_tasks: 4
    artifacts: [artifact-id]
    first_seen: ...
    last_seen: ...
    evidence_event_ids: [...]
    status: emerging | established | stale
~~~

A profile requires multiple independent tasks or artifacts, retains negative
and contradictory evidence, and decays with time. It provides a routing path,
not authority, authorization, or truth priority.

This is the computational form of a transactive memory directory: task,
expertise, and person are linked explicitly, and the system can show why it
believes an agent is a useful recall direction.

### Sentiment and interaction state

Concern, urgency, uncertainty, support, opposition, and frustration may reveal
coordination risk. They must remain evidence-backed interaction observations.
They may:

- keep a disagreement open;
- raise an explicit unresolved blocker;
- suggest an active-recall path to the reporting agent.

They may not:

- increase factual confidence;
- override an explicit decision;
- imply expertise, authority, or approval;
- become passive factual evidence by themselves.

### Hint and active recall

If passive evidence does not cover all answer slots but the planner finds a
safe, promising path, it emits one Hint Recall instruction through the existing
paxm-compatible result shape. The hint includes:

- the uncovered slot;
- the safe entity, actor, artifact, or relation seed;
- a proposed active-recall query;
- a statement that the hint is not evidence.

Active recall then interleaves explicit subqueries and observations, similar to
IRCoT or ReAct:

~~~text
uncovered slot
  -> query exact entity or actor
  -> observe evidence IDs and typed relations
  -> traverse one justified relation
  -> stop when covered, exhausted, unauthorized, or over budget
~~~

Every hop is visible. The agent performs the reasoning; Team Memory supplies
inspectable search operations rather than an opaque latent neighborhood.

## Delivery sequence

The work should be delivered as three coherent releases, not a separate
experiment for every mechanism.

### Release A: Canonical State

Complete Extraction v2 shadow output, atomic claims, state transitions, typed
relations, and temporal revisions behind the existing extractor seam.

Accept when the fixed extraction cohort improves materially without worse
evidence precision or leakage, while preserving at least an 80 percent
token-weighted prompt-cache hit rate, no P95 latency regression, and an average
model-call count no greater than 1.1 per extraction window.

### Release B: Explicit Recall Planner

Introduce Recall Intent, structural lanes, state-first coverage selection, and
Trace v2 behind `PlanRecall`. Keep the current semantic lane in shadow, then
remove it from authoritative recall after cohort validation.

Accept when both the pinned replay and a fresh rolling-extraction live cohort
show:

- no regression in candidate and conditional recall;
- improved context precision and minimal-sufficient-answer behavior;
- no superseded or authorization leakage;
- no global-threshold dependence;
- bounded passive-recall latency without an additional LLM call.

### Release C: Team Directory and Agentic Recall

Add evidence-backed Observed Agent Profiles, coordination observations, Hint
Recall, and explicit active-recall trails. Keep Agent Specifications and access
policy under their current owners.

Accept through Eval v3's three-arm multi-agent protocol, emphasizing strict
cross-agent answer accuracy, asking-user correctness, profile-routing accuracy,
and zero private-memory leakage. Do not add more public end-to-end arms; use
stage replay for internal ablations.

## Latency and cache constraints

The design keeps LLM work off the passive recall path:

- passive query intent, retrieval lanes, hard gates, traversal, and packing are
  deterministic Go and PostgreSQL operations;
- Extraction v2 uses one primary cached call, with a targeted verifier only for
  rare high-risk ambiguity;
- active recall uses the requesting agent's existing tool loop rather than a
  hidden recall-planner model;
- consolidation and Observed Agent Profile updates run asynchronously outside
  the answer latency path;
- stable extraction instructions and schemas remain at the prompt prefix;
- dynamic state deltas and new Events stay at the suffix;
- compaction, cold start, verifier, and summary calls are timed separately.

The current mean extraction calls of roughly 46 to 49 seconds and P95 of roughly
104 to 108 seconds make extra serial LLM calls unacceptable as the default.
Recall latency must also stay below the provider's passive-recall timeout with
margin; a quality gain that usually arrives after context assembly is not a
usable gain.

## What not to optimize next

Do not make the next iteration:

- another global semantic-threshold sweep;
- a larger embedding model or higher-dimensional vector index;
- a wider candidate limit without slot and stage evidence;
- mandatory LLM reranking or a multi-call passive planner;
- automatic LLM rewriting of historical evidence;
- sentiment-weighted factual confidence;
- an untyped graph whose edges are merely semantic similarity;
- more Eval v3 arms before extraction and recall stage metrics improve.

These changes either preserve the current ambiguity or add a new black box
without addressing the observed failure stage.

## Expected outcome

The intended result is not simply higher Recall@k. It is a team memory system
that can answer four reviewable questions for every delivered item:

1. What explicit answer slot did this evidence cover?
2. Which field, term, temporal revision, actor, or typed relation found it?
3. Why is it current, authorized, and better than the alternatives?
4. If it was insufficient, what exact active-recall path remains?

That is the operational meaning of explicit retrieval for Team Memory.
