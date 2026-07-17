# General Recall v3 Optimization

Status: Proposed

Date: 2026-07-16

Related: Hint Recall v0

## Context

Team Memory serves several recall behaviors:

- passive recall automatically delivers current collaboration facts;
- Hint Recall tells an agent that a promising active-recall path exists;
- active recall lets an agent explicitly search and follow multiple hops;
- durable recall actively browses long-lived knowledge in the LLM Wiki;
- team routing identifies agents, artifacts, decisions, and prior work that may
  be useful to the current task.

The primary recall risk is not only missing candidates. Recall lacks one shared,
explicit, and auditable reasoning model. A single similarity score conflates
topic similarity, factual support, temporal validity, authority, and search
utility. Dense-vector retrieval further hides why a result was selected inside
a high-dimensional space. Team memory adds responsibility, handoff,
disagreement, changing state, and consumer-relative relevance, none of which
can be represented safely by semantic similarity alone.

Recall is itself reasoning. The system already depends on an LLM as a black
box; core recall decisions must not introduce another opaque high-dimensional
decision layer.

## Decision

General Recall v3 will use an explicit, staged, evidence-backed recall planner.

General Recall is a shared decision vocabulary and trace contract. It is not a
unified database, a new paxm protocol, or a cross-product recall service:

- Team Note continues to own short-lived passive collaboration state;
- LLM Wiki continues to own durable knowledge used through active browsing;
- Agent Registry owns explicit Agent Specifications;
- consolidation maintains evidence-backed Observed Agent Profiles;
- paxm provider and tool protocols remain unchanged;
- products may share query-planning, temporal, reason-code, and evaluation
  semantics without querying each other's storage.

## Principles

### Retrieval is explicit reasoning

Every recall produces a reviewable Recall Plan and Recall Trace. The trace
records:

- how the question was interpreted;
- which retrieval lanes ran;
- why each candidate was retrieved;
- which relationships were traversed;
- which candidates failed authorization, temporal, state, or budget checks;
- why final content became evidence, a hint, or a suppressed candidate.

Dense-vector similarity is not a core v3 retrieval or ranking signal.

### Hard constraints precede relevance

The following are hard gates rather than score features:

- collaboration scope;
- authorization and audience;
- task and thread boundaries;
- temporal validity;
- active, resolved, retracted, and superseded state;
- delivery eligibility;
- source provenance availability;
- unsafe or instruction-bearing stored content.

A failed hard gate cannot be offset by any relevance signal.

### One score does not represent recall quality

The planner keeps three judgments separate:

- **Evidence Confidence (E)**: whether a candidate directly and safely supports
  a fact required by the query;
- **Hint Utility (H)**: whether active recall seeded by a lead is likely to find
  useful evidence missing from the passive context;
- **Routing Affinity (R)**: whether an agent, artifact, or knowledge area is a
  useful search or delivery direction.

These judgments are not interchangeable:

~~~text
H != 1 - E
R does not imply authority
sentiment does not affect E
~~~

E and H may be returned through the existing paxm hit relevance field. R is
used only for planning, seed selection, and recipient selection.

## Recall intent

A natural-language query is compiled into an explicit Recall Intent:

~~~yaml
mode: current | as_of | changes_since | history | discover
requested_facts:
  - decision
  - owner
  - blocker
entities:
  - rollout-plan-v3
artifacts: []
actors: []
task_ref: optional
thread_ref: optional
valid_at: optional
changed_since: optional
relation_budget: 1
token_budget: 1200
~~~

Unrecognized fields remain unresolved. Model guesses do not become hard
constraints.

## Retrieval lanes

The planner may run multiple explicit retrieval lanes. Each lane produces its
own candidates and reason codes.

### Exact Scope Lane

This lane matches task, thread, scope, entity, artifact, decision,
responsibility, handoff, waiting-on, audience, and actor identifiers.

### Lexical Lane

This lane uses inspectable term retrieval such as BM25, field matches, entity
aliases, and normalized tokens. The trace stores the matched terms and fields,
not only a total score.

### Temporal Lane

The lane supports four explicit time intents:

- current: facts valid now;
- as_of(T): facts valid at time T;
- changes_since(T): facts added, corrected, superseded, or retracted after T;
- history: a fact and its revision chain.

The model distinguishes:

- **Valid Time**: when a fact holds in the collaboration domain;
- **Recorded Time**: when the system learned or recorded the fact.

Latest recorded does not imply currently valid.

### Relation Lane

The lane may traverse a bounded whitelist:

- supersedes and superseded_by;
- depends_on and blocks;
- assigned_to and responsible_for;
- handoff_to and waiting_on;
- decision_about;
- artifact_for;
- evidence_for;
- disagrees_with.

V3 defaults to one hop. Every expanded candidate still passes authorization,
temporal relevance, and the shared token budget.

### Coordination Lane

This lane retrieves explicit collaboration acts such as request, commit,
approve, reject, question, handoff, and escalate. These acts represent speech
acts or commitments, not text sentiment.

### Agent Routing Lane

The lane considers routing signals in this order:

1. current explicit responsibility;
2. waiting-on or handoff target;
3. declared skill from the Agent Specification;
4. demonstrated domain from the Observed Agent Profile;
5. bounded broadcast.

An observed domain provides a search direction. It never grants authority,
authorization, or factual priority.

## Candidate reasoning

Each candidate contains structured reason codes:

~~~yaml
id: note-123
source: team_note
reasons:
  - exact_task_match
  - entity_match: rollout-plan-v3
  - relation: decision_about
  - changed_after_query_boundary
  - current_responsible_agent
temporal:
  valid_at_query_time: true
  superseded: false
provenance:
  evidence_event_ids:
    - event-42
exclusions: []
~~~

Relation expansion also records its full path:

~~~text
query entity
  -> decision_about
  -> rollout-plan-v3
  -> superseded_by
  -> rollout-plan-v4
~~~

## Authority, conflict, and stance

V3 does not use a global agent authority score. Authority is bound to a claim
or domain, a valid time, and explicit role, approval, or source evidence.

When statements disagree, recall preserves provenance and a conflict group
instead of applying last-write-wins. Passive recall must not render an
unresolved conflict as one fact. Active recall may return both sides and their
evidence.

The system models target-bound coordination signals:

~~~yaml
actor: agent-b
target: rollout-plan-v3
stance: oppose
speech_act: request_change
affective_signal: high_concern
evidence_event_id: event-87
~~~

The canonical concepts are:

- **Stance**: support, oppose, question, conditional support, or abstain;
- **Speech Act**: propose, request, commit, approve, reject, handoff, or
  escalate;
- **Epistemic Status**: observed, reported, inferred, or uncertain;
- **Affective Signal**: concern, frustration, urgency, or confidence.

An Affective Signal may raise escalation, change-recall, or Hint Recall
priority. It cannot affect factual truth, authority, or conflict resolution.
The system does not derive personal labels such as "Agent B is pessimistic."
V3 admits only explicitly expressed affective signals or signals extracted by
high-precision rules; a general sentiment classifier is not required.

## Agent consolidation

Consolidation may form a long-lived agent capability view, but it does not
automatically rewrite an Agent Specification.

### Agent Specification

An Agent Specification is an explicit, versioned, owner-controlled contract
containing declared skills, tools, input and output contracts, permissions,
supported roles, and security requirements. It changes only through an
explicit version update.

### Capability Evidence

Capability Evidence is immutable evidence derived from sessions, artifacts,
reviews, and task outcomes:

~~~yaml
agent_id: agent-a
domain: postgres-migration
activity: implemented
outcome: accepted
artifact_ref: migration-013
evidence_event_ids:
  - event-101
observed_at: ...
~~~

### Observed Agent Profile

An Observed Agent Profile is a derived projection over Capability Evidence. It
may contain demonstrated domains, recent working context, recurring
collaborators, reviewed work, evidence count, distinct task count, freshness,
confidence, and supporting evidence references.

An Observed Agent Profile:

- supports routing and hint generation;
- does not determine authorization, authority, or factual truth;
- decays over time;
- can be corrected by the agent or owner;
- does not infer capability from message volume;
- distinguishes repeated activity from independent tasks;
- does not automatically become a specification.

Promotion requires an explicit step:

~~~text
Capability Evidence
    -> Observed Agent Profile
    -> explicit review or ratification
    -> new Agent Specification version
~~~

## Ranking and selection

V3 does not first compress all signals into one weighted relevance score. The
planner uses a lexicographic decision key:

1. hard-gate eligibility;
2. temporal state correctness;
3. direct required-fact coverage;
4. responsibility and commitment relevance;
5. explicit entity and lexical coverage;
6. bounded relation distance;
7. novelty against already selected evidence;
8. observed-domain routing affinity;
9. deterministic recency and ID tie-breakers.

Topic similarity therefore cannot override superseded, unauthorized, or
temporally invalid state.

### Evidence Confidence

E estimates:

~~~text
P(candidate directly and safely supports a required fact)
~~~

V3 uses a small, inspectable, monotonic feature set: direct entity and field
match, required fact type match, lexical coverage, temporal validity, source
basis, conflict state, supersession state, and evidence availability.

Features first produce an explicit point scorecard. Offline calibration maps
the scorecard to a probability. The trace records each feature, point
contribution, calibration version, and final E. Unexplained embedding distance
is not a primary E feature.

### Hint Utility

H estimates:

~~~text
P(focused active recall retrieves new useful evidence within budget)
~~~

Its inspectable features include passive evidence gap, seed specificity,
untraversed relations, independent support, expected novelty, ambiguity,
expected recall cost, and forbidden or superseded leakage risk. Active-recall
intervention fixtures calibrate H; a medium E does not imply a high H.

### Routing Affinity

R remains an explicit tuple rather than a required probability:

~~~text
(current responsibility,
 waiting-on relationship,
 declared skill match,
 demonstrated-domain evidence,
 profile freshness,
 independent-task count)
~~~

It determines active-recall seeds, suggested agents or domains in hints, and
candidate recipients. It does not determine evidence delivery.

## Dispositions

The planner assigns a final disposition:

~~~text
eligible and E >= evidence threshold
    -> evidence

not admitted as evidence and H >= hint threshold
    -> hint

otherwise
    -> suppress
~~~

Evidence consumes the shared budget first. A hint contains no underlying
candidate claim, does not mark that candidate as delivered, and is limited to
one per passive recall opportunity. Hint Utility must naturally clear existing
paxm gates; it cannot be inflated. Profile and sentiment signals are not
passive factual evidence.

An allowed hint may look like:

~~~text
[Recall hint - not evidence]

Agent B recently worked on deployment security and expressed concern about
rollout-plan-v3. If this could affect the answer, actively recall:
"deployment security review for rollout-plan-v3, including Agent B's reports
and unresolved objections".
~~~

The hint exposes a search path. It does not assert that Agent B is correct, is
the final authority, or has established a security defect.

## Budget packing

All lanes and relation expansion share one budget. Set selection prioritizes:

1. required-fact coverage;
2. corrections and supersession;
3. unresolved conflict visibility;
4. responsibility and actionable commitments;
5. evidence diversity;
6. token efficiency.

Duplicate claims retain the version with the best provenance and temporal fit.
Every budget rejection records a reason such as duplicate_claim,
lower_authority_for_same_claim, temporally_superseded, uncovered_relation_cost,
lower_incremental_coverage, or token_budget_exceeded.

## Recall trace

Each recall records an evaluation and diagnostic artifact:

~~~yaml
plan_version: general-recall-v3
scoring_version: ...
intent: ...
lanes_executed: ...
candidates:
  - retrieval_reasons
  - relation_path
  - hard_gate_results
  - temporal_resolution
  - score_contributions
  - disposition
  - rejection_reason
selected_set: ...
budget_drops: ...
delivered_items: ...
~~~

The trace never enters the agent context.

## Evaluation

General Recall v3 continues to use stage-local control loops. Extraction and
persisted state remain fixed while recall is evaluated.

Candidate retrieval reports:

- candidate recall at each lane limit;
- exact, lexical, temporal, and relation lane contribution;
- relation-path success;
- required-atom candidate coverage.

State correctness reports:

- current-state and as-of accuracy;
- change-since recall;
- superseded-fact leakage;
- unresolved-conflict collapse rate;
- authorization leakage.

Final selection reports:

- Conditional Recall over available atoms;
- context precision and required-fact coverage;
- budget drops by reason;
- duplicate-claim rate;
- provenance coverage.

Hint Recall reports:

- hint-opportunity precision;
- active-recall activation rate;
- useful evidence found within one and two calls;
- new atoms beyond passive evidence;
- unnecessary active-recall rate;
- hint-induced answer loss.

Agent routing reports:

- correct responsible-agent recall;
- useful expert-discovery rate;
- stale-profile routing rate;
- routing concentration by agent;
- exploration coverage;
- cases where profile routing contradicts current responsibility.

End-to-end evaluation reports judged answer accuracy, pairwise wins and losses,
forbidden or superseded leakage, latency, token usage, active-recall calls, and
total cost. Token F1 remains a diagnostic rather than an answer-quality claim.

## Rollout

### V3.0: Observe

- produce Recall Plans and Recall Traces without changing production results;
- record lanes, temporal resolution, and rejection reasons;
- establish a fixed replay baseline.

### V3.1: Explicit retrieval

- enable exact, lexical, and temporal lanes;
- introduce lexicographic selection;
- compare against current passive recall.

### V3.2: Relations and coordination

- enable one-hop relation expansion;
- introduce responsibility, handoff, stance, and conflict;
- retain sentiment only as an auxiliary signal.

### V3.3: Profile routing and hints

- build Capability Evidence and Observed Agent Profiles;
- evaluate routing in shadow mode;
- integrate Hint Recall v0 without changing paxm.

Each phase must improve the fixed replay without regressing safety metrics
before entering the paid end-to-end cohort.

## Consequences

Recall becomes replayable, explainable, and optimizable by stage. Team
responsibility, disagreement, and temporal change become first-class concepts.
Passive, Hint, and Active Recall use consistent but separate judgments. Agent
domains may emerge over time without being mistaken for declared
specifications. The design preserves the paxm protocol and does not depend on
a high-dimensional vector space for core recall.

The design also introduces query parsing, relation vocabulary, temporal-state
resolution, three evaluation loops for E, H, and R, reliable outcome provenance
for consolidation, and additional trace and fixture costs.

## Rejected alternatives

### Dense-vector-first recall

Rejected because it cannot explain candidate relevance or safely express
authority, supersession, valid time, responsibility, and disagreement.

Future experiments may use embeddings to generate low-priority candidates, but
they cannot bypass hard gates, directly decide evidence delivery, become the
only recall path, or ship by default without an independent stage evaluation.

### One global relevance score

Rejected because factual support, active-search utility, and team routing are
different decisions.

### Last-write-wins temporal state

Rejected because the latest record is not necessarily the currently valid fact,
and last-write-wins erases disagreement and revision history.

### Sentiment as authority or conflict resolution

Rejected because emotion is not stance, approval, or factual correctness.

### Consolidation automatically rewrites Agent Specification

Rejected because an observed profile is a probabilistic derived view while a
specification is an explicit contract.

### Unified Team Note and LLM Wiki storage

Rejected because short-lived collaboration state and durable knowledge have
different lifecycles and recall behavior. V3 unifies decision semantics and
observability, not product storage boundaries.
