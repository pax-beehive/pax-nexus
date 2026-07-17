# Multi-Agent GroupMemBench Eval v3

Status: Accepted; protocol runner implemented, product-use acceptance pending

Date: 2026-07-16

Implementation: 2026-07-17

Related:

- [General Recall v3 Optimization](./2026-07-16-general-recall-v3-optimization.md)
- [Hint Recall v0](./2026-07-16-hint-recall-v0.md)
- [GroupMemBench paper](https://arxiv.org/abs/2605.14498)
- [GroupMemBench repository](https://github.com/KimperYang/GroupMemBench)

## Context

The current GroupMemBench evaluation is primarily a producer-to-consumer
transfer test. One producer reads messages from multiple authors and prepares
memory for one consumer. This can measure bounded extraction and transfer, but
it does not directly represent a team of agents with separate identities,
sessions, and private memories.

PAX is intended to provide collaboration memory for a team. Its primary product
claim is therefore stronger and more specific:

> A teammate who did not author the supporting evidence can take over a task
> and answer using its private memory plus shared collaboration memory.

The end-to-end evaluation should make this information flow visible. It should
also compare PAX against a recognizable external baseline rather than a custom
Mem0 configuration whose relationship to published GroupMemBench results is
unclear.

GroupMemBench binds every question to an asking user. Speaker identity is
semantically significant for user-implicit, knowledge-update, and
term-ambiguity questions. Randomizing the runtime agent must not replace that
asking-user identity or change the gold answer.

The published GroupMemBench paper reports a Mem0 baseline, including 25.73
percent aggregate accuracy and 4.67 percent knowledge-update accuracy. It
states that ingestion uses gpt-4o-mini, answering uses GPT-5, dense retrieval
uses text-embedding-3-large, and systems follow their official
implementations. The public repository currently contains executable BM25 and
text-embedding-3-large baselines, but not the Mem0 runner, exact namespace
mapping, pinned Mem0 version, or published per-question Mem0 results.
Consequently, the public artifacts currently support protocol reconstruction,
not bit-for-bit reproduction.

## Decision

Eval v3 will model GroupMemBench as a team of stable Actor Agents and compare
exactly three end-to-end experimental arms:

1. no-memory agent team;
2. GroupMemBench-compatible shared Mem0;
3. agent-private SQLite plus shared Team Note.

The evaluation will use the full, question-independent domain history for
memory construction. It will not preselect source messages using the evaluation
question before ingestion.

Stage-local extraction and recall evaluation remains a separate diagnostic
loop. It does not add end-to-end arms.

The initial implementation is available through `cmd/team-memory-eval-v3`,
`internal/eval/v3`, `evals/v3`, and the `eval-v3-*` scripts. The shipped Mem0
template is deliberately labeled `comparable_baseline`; implementation does
not by itself establish benchmark quality or reproduce the published Mem0
score.

## Terminology

**Benchmark Case**:
One GroupMemBench conversation prefix, question, original asking-user identity,
gold answer, category, and available evidence annotations.

**Experimental Arm**:
One memory architecture applied to the same Benchmark Case.

**Actor Agent**:
A stable agent identity representing one GroupMemBench User_n participant.

**Asking User**:
The GroupMemBench User_n to whom the question is semantically bound. This
identity remains fixed across arms and Answering Agents.

**Answering Agent**:
The teammate selected by a deterministic random seed to execute the answer
trial on behalf of the Asking User.

**Answerer Seed**:
The reproducible seed used to choose the Answering Agent.

**Trial**:
One Benchmark Case, Experimental Arm, and Answerer Seed combination.

**Strict Cross-Agent Trial**:
A Trial whose Answering Agent did not author any gold-supporting Event and is
not the Asking User.

## Multi-agent history construction

Each GroupMemBench User_n maps to one stable Actor Agent. Messages are replayed
as immutable Session Events rather than regenerated or paraphrased by an LLM.

Sessions are partitioned by author, channel, phase, and contiguous activity
according to the existing GroupMemBench-to-PAX mapping. Original timestamps,
reply references, roles, topics, and speaker identities remain attached.

The same source Events, ordering, dataset revision, questions, model versions,
prompts, context budgets, and random seeds are used across all arms. Memory
topology is the intended treatment variable.

The replay may run through real agent runtimes to exercise identity and provider
boundaries, but the runtime must not rewrite the benchmark conversation before
memory ingestion.

## Answering Agent selection

Answering Agent selection is seeded and paired:

~~~text
answerer = SelectEligible(
    dataset_revision,
    case_id,
    answerer_seed
)
~~~

The same Answering Agent is used for all three arms of a paired Trial.

For PAX-native cases with reviewed supporting Event annotations, eligible
answerers are:

~~~text
case participants
    - authors of gold-supporting Events
    - original Asking User
~~~

This ensures that the primary metric measures information transfer rather than
an agent remembering its own statement.

If no eligible agent remains, the Trial is marked
no_cross_agent_answerer_available rather than silently relaxing the rule.

For official-compatible cases without reviewed supporting Event annotations,
the runner selects a participant other than the Asking User and records
answerer_source_overlap as unknown. Such Trials contribute to overall QA but
not to the strict cross-agent metric.

The Answering Agent answers on behalf of the original Asking User. The prompt
retains asking_user_id, role, and question semantics. Random selection changes
the executing teammate, not the meaning of first-person language in the
question.

Initial economical runs may use one Answerer Seed per Benchmark Case. Product
claims should use at least three seeds or demonstrate that answerer-selection
variance is negligible.

## Experimental arms

### Arm 1: no_memory_team

All Actor Agents participate in history replay, but no persistent memory is
available at answer time.

The Answering Agent starts a fresh session and receives only:

- the official question;
- the original Asking User identity and role context required by the benchmark;
- the common answer instructions.

It receives no historical messages, recalled context, memory provider, or
other agent's session.

This arm measures base-model answering, abstention, and accidental prior
knowledge. It is the end-to-end baseline.

### Arm 2: groupmembench_mem0

This arm is an official-compatible Mem0 baseline, not a PAX-specific shared
Mem0 configuration.

All domain messages are ingested chronologically into one shared, retrievable
Mem0 memory state for that domain. Original speaker and message metadata is
preserved at the ingestion boundary. Questions do not influence which source
messages are ingested.

The compatibility contract is:

~~~yaml
arm: groupmembench_mem0
dataset:
  revision: pinned
  scope: full_domain
ingestion:
  order: chronological
  unit: original_message
  llm: gpt-4o-mini
  embedding: text-embedding-3-large
  mem0_version: pinned
retrieval:
  question: official_question
  asking_user_id: preserved
  top_k: official_or_explicitly_reconstructed
answer:
  model: gpt-5
  prompt: official_reader_prompt
judge:
  model: gpt-5
  prompt: official_judge_prompt
~~~

A domain-level store is built once and reused across all six question
categories. A separate Mem0 store is used for each dataset domain and dataset
revision.

The random Answering Agent remains harness telemetry for this arm. It does not
change the shared Mem0 namespace, official reader prompt, or Asking User
identity. This deliberately gives the flat shared-memory baseline its intended
access model.

If future official Mem0 code reveals a different namespace or payload mapping,
the arm follows the official implementation even when it differs from the
current shared-user reconstruction.

The arm records one of three reproducibility levels:

- exact_reproduction: official runner, version, configuration, and result
  artifacts are available and pinned;
- protocol_reproduction: undisclosed details are reconstructed while all
  published controls are matched;
- comparable_baseline: one or more material controls differ.

Only exact_reproduction may claim exact replication of published Mem0 numbers.
Protocol reproduction reports the deviation from published aggregate and
per-category results. A comparable baseline must not be presented as a
GroupMemBench Mem0 reproduction.

The current public artifacts permit protocol_reproduction at most. Model
substitution, self-hosted DeepSeek extraction, question-conditioned source
selection, or custom top-k behavior downgrades the arm to comparable_baseline.

### Arm 3: private_sqlite_plus_team_note

Each Actor Agent has an independent local SQLite memory store:

~~~text
agent-a -> agent-a.sqlite
agent-b -> agent-b.sqlite
agent-c -> agent-c.sqlite
~~~

No agent may query another agent's SQLite store.

All authorized collaboration Events also enter one shared Team Note
collaboration scope. At answer time, the selected Answering Agent can access
only:

- its own SQLite memory;
- the shared Team Note memory;
- the same question and Asking User context used in other arms.

This arm represents the PAX product architecture:

- private SQLite preserves what the individual agent experienced;
- Team Note preserves short-lived state the team needs to coordinate;
- private history is not flattened into one shared RAG namespace.

The Answering Agent identity therefore affects which SQLite database is
available, while the shared Team Note scope remains the same for every eligible
teammate.

## Primary protocol

For each dataset domain:

1. pin the conversation, question, prompt, model, provider, and judge revisions;
2. create stable Actor Agent identities for every User_n;
3. replay the full source history chronologically without question-conditioned
   selection;
4. build the memory state required by each arm;
5. wait for and record memory readiness;
6. select the paired Answering Agent from the Answerer Seed;
7. start a fresh answer session;
8. provide the original question and Asking User identity;
9. expose only the memory allowed by the arm;
10. capture recall context, provenance, answer, cost, latency, and errors;
11. judge all completed answers with the same pinned semantic judge;
12. compare the three arms by paired Benchmark Case and Answerer Seed.

Memory construction is shared across questions when the architecture permits
it. A per-question store is prohibited for the primary protocol because it
changes ingestion cost, permits question-conditioned leakage, and diverges
from the full-domain benchmark.

## Scoring

### Primary answer metrics

Report:

- semantic-judge accuracy by arm and category;
- groupmembench_mem0 lift over no_memory_team;
- private_sqlite_plus_team_note lift over no_memory_team;
- private_sqlite_plus_team_note paired wins, losses, and ties against
  groupmembench_mem0;
- strict cross-agent accuracy;
- abstention accuracy.

Token F1 remains diagnostic and is not an answer-quality claim.

### Team-memory narrative metrics

Report:

- whether the Answering Agent authored any supporting Event;
- number of source agents required by the gold answer;
- number of distinct source agents represented in delivered evidence;
- correct speaker and role attribution;
- multi-agent and multi-hop success;
- handoff, update, temporal, and disagreement accuracy;
- answerer_source_overlap status.

A result is not labeled strict cross-agent success unless supporting evidence
annotations establish that the Answering Agent was not a source author.

### Safety metrics

Report:

- unsupported-answer rate;
- stale and superseded-fact leakage;
- wrong-speaker attribution;
- cross-domain, cross-team, and cross-case leakage;
- private-SQLite leakage;
- Mem0 provenance loss;
- Team Note evidence-provenance coverage.

### Operational metrics

Report:

- memory ingestion cost;
- memory storage size;
- extraction and readiness latency;
- recall and consumer latency;
- answer input and output tokens;
- total cost per domain and answered question;
- failed and unscored Trials separately from quality denominators.

## Mem0 reproduction validation

Before using the three-arm result for a product comparison, the report must
state:

- reproduction level;
- official repository and dataset revisions;
- Mem0 version and configuration;
- ingestion, embedding, answer, and judge models;
- namespace and author-metadata mapping;
- retrieval limit and score semantics;
- full-domain message count accepted;
- deviations from the paper's protocol;
- observed aggregate and per-category accuracy versus the published values.

Published figures are comparison targets rather than hard equality assertions
unless exact official artifacts are available. Protocol reproduction must
explain material deviations before PAX-versus-Mem0 conclusions are drawn.

A Mem0 ingest with zero writes, incomplete full-domain coverage, mismatched
identity scope, or a question-conditioned source subset fails preflight and
does not produce a comparable score.

## Relationship to stage-local evaluation

Eval v3 uses only three end-to-end arms. It does not remove the existing
stage-local control loops.

The end-to-end evaluation answers:

> Does this team memory architecture help a randomly assigned teammate answer?

The fixed replay evaluation separately answers:

- was supporting evidence present in the source;
- was it extracted and admitted;
- was it a recall candidate;
- did relation expansion find it;
- was it selected and packed;
- why was an available candidate rejected?

Gold Team Notes, retrieval variants, Hint Recall, and ranking experiments remain
stage-local arms unless a later ADR promotes one into the product architecture.
They do not expand the three-arm public narrative benchmark.

## Acceptance

Eval v3 is ready for product use when:

- all three arms consume the same pinned full-domain source revision;
- Answering Agent selection is paired and reproducible;
- Asking User semantics remain unchanged;
- strict cross-agent Trials are identifiable;
- no-memory isolation and private-SQLite isolation pass leakage checks;
- the Mem0 arm declares and satisfies its reproduction level;
- source coverage, ingest health, and failed Trials are visible;
- semantic-judge accuracy is the primary answer metric;
- stage traces can attribute Team Note failures without relying on end-to-end
  guesses.

## Consequences

The evaluation directly demonstrates the PAX collaboration-memory narrative
instead of only producer-to-consumer transfer. The Mem0 arm becomes externally
recognizable and can be compared with published GroupMemBench behavior. The
PAX arm tests a meaningful architectural distinction between agent-private
history and team-shared collaboration state.

The protocol is substantially more expensive than bounded per-question
ingestion because it builds full-domain memory and runs multiple agent
identities. Reusing immutable domain stores, pinning Answerer Seeds, and
separating stage replay from end-to-end arms control that cost.

The three arms compare complete memory architectures, not isolated ranking
algorithms. Results may support product-level claims but must not be phrased as
a causal comparison of Team Note ranking against Mem0 retrieval.

## Rejected alternatives

### Keep one producer that reads all authors

Rejected because it gives one process an omniscient view and does not establish
multi-agent identity, private memory, or cross-agent handoff.

### Randomize the Answering Agent independently per arm

Rejected because answerer variance would be confounded with memory architecture.

### Replace asking_user_id with the random Answering Agent

Rejected because GroupMemBench questions are user-bound and the gold answer may
change with the asking identity.

### Ingest a question-conditioned source subset

Rejected for the primary protocol because it combines upstream retrieval with
memory construction, permits source-coverage failures, and cannot reproduce the
published full-domain baseline.

### Use a custom self-hosted Mem0 setup as the official baseline

Rejected because model, version, namespace, and retrieval changes can materially
alter the result. Such a run may be useful but must be labeled
comparable_baseline.

### Give every agent access to every SQLite database

Rejected because it turns private agent memory into another shared store and
removes the architectural distinction Team Note is intended to test.

### Add more end-to-end ablation arms

Rejected for Eval v3. The public narrative remains a three-arm architectural
comparison. Internal recall optimization belongs in fixed, stage-local replay.
