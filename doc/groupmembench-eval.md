# GroupMemBench evaluation note

## What the benchmark measures

GroupMemBench evaluates memory over multi-author, multi-channel conversations.
Each query is bound to an asking user, so speaker identity is part of both the
evidence and the query. Its six categories are multi-hop reasoning, knowledge
update, term ambiguity, user-implicit reasoning, temporal reasoning, and
abstention.

The official Finance conversation contains 30,000 messages. The released
question files contain 214 questions across the six categories. The local
runner pins the official repository revision and uses deterministic seeds so a
run can be reproduced.

## Local protocol

Each 12-case run selects two questions per category. For each question, BM25
selects source messages from the Finance conversation and expands immediate
neighbors and reply ancestors, with a maximum of 32 messages. The producer does
not receive the question or gold answer. It reads the selected messages and
produces a factual handoff through OpenCode and paxm.

A separate OpenCode consumer receives only the question and runs in two arms:

- `control`: Team Note recall disabled
- `extracted_notes`: Team Note recall enabled

Both arms use the question's asking user and distinct agent identities. Each
case has an isolated API-key scope. The runner executes four cases concurrently
against one PostgreSQL and one team-memory service, then records per-case model
output, service logs, database counts, exact match, and token F1.

This is a bounded-context extraction and transfer diagnostic. It tests whether
Team Note helps after relevant conversation regions have been selected. It does
not test retrieval from the raw 30,000-message domain and must not be reported
as an official GroupMemBench score. The official benchmark also uses a semantic
equivalence judge; local exact match is stricter, while token F1 is only a
lexical proxy.

## July 14, 2026 results

Three deterministic 12-case runs used different seeds:

- `runs/groupmembench-20260715T011528Z`, seed `team-memory-v1`
- `runs/groupmembench-20260715T012257Z`, seed `team-memory-v2`
- `runs/groupmembench-20260715T013836Z`, seed `team-memory-v3`

The runs contain 36 paired executions covering 35 unique questions. One
abstention question appeared under two seeds.

| Metric | Control | Team Note |
| --- | ---: | ---: |
| Paired executions | 36 | 36 |
| Exact matches | 0 | 0 |
| Mean token F1 | 0.0803 | 0.1091 |

The absolute token-F1 lift was 0.0288 and the relative lift was 35.9 percent.
Team Note won 15 paired executions, lost 7, and tied 14. All three seeds showed
a positive aggregate lift, but the lift narrowed from 0.0524 to 0.0217 to
0.0125 across the three runs.

| Category | Runs | Unique | Control F1 | Team Note F1 | Wins / losses / ties |
| --- | ---: | ---: | ---: | ---: | ---: |
| abstention | 6 | 5 | 0.3072 | 0.2741 | 2 / 4 / 0 |
| knowledge update | 6 | 6 | 0.1114 | 0.1454 | 4 / 2 / 0 |
| multi-hop | 6 | 6 | 0.0000 | 0.0175 | 1 / 0 / 5 |
| temporal | 6 | 6 | 0.0000 | 0.0000 | 0 / 0 / 6 |
| term ambiguity | 6 | 6 | 0.0111 | 0.0518 | 2 / 1 / 3 |
| user implicit | 6 | 6 | 0.0518 | 0.1659 | 6 / 0 / 0 |

The strongest and most consistent signal was user-implicit recall. Temporal
reasoning showed no benefit, and multi-hop remained weak. Abstention was
approximately neutral, which matters because lexical overlap can reward generic
refusals without proving grounded behavior.

The first two recorded 12-case runs predate an evaluation-image identity fix. Their
producer events retained the correct asking user and isolated scope but used
`unknown` as the origin agent ID because `agents.opencode.agent_id` was absent
from the generated paxm configuration. This does not change the paired quality
scores, but those runs are not evidence for agent-level provenance. The runner
now sets the agent ID and fails a case unless PostgreSQL contains the expected
producer user and agent IDs. The third run passed this assertion for all 12
cases and recorded no `unknown` producer agents.

Across the three completed runs, 36 durable extraction jobs completed without
stuck jobs or retries. Mean extraction latency was 17.4 seconds, ranging from
5.9 to 35.7 seconds. Recall stayed on the request path and averaged 23
milliseconds, with one 500-millisecond outlier. The runs persisted 202 notes and
produced 190 deliveries.

A post-fix two-OpenCode smoke run at `runs/20260715T013336Z` verified the full
identity path. PostgreSQL recorded `eval-owner/opencode-producer` for ingestion,
recall recorded `eval-owner/opencode-consumer`, the control could not answer,
and the Team Note arm exactly returned `ORBIT-731`.

## Next evaluation slices

The next quality gate should replace token F1 with a semantic equivalence judge
and add citation-grounding checks. The highest-priority strategy work is
temporal representation and multi-hop note linking. A later full-domain run
should ingest the Finance conversation without question-conditioned source
selection and evaluate the session-lake retrieval stage separately from Team
Note extraction and delivery.

## Temporal experiment after Zep-style fact linking

The temporal implementation added bitemporal note fields, revision
invalidation, query-aware ranking, and one-hop `related_subjects`. A focused run
at `runs/groupmembench-20260715T023711Z` confirmed that DeepSeek can extract
dependency edges such as `Legal validation of frozen snapshot` depending on
`User_7 final Ops rows posting`, and PostgreSQL recall delivered the linked
facts. Both control and Team Note still scored zero on the two selected cases.

That zero is not evidence that temporal linking is ineffective. The producer
source for `temporal_30` did not contain the gold `2025-07-24`, and the source
for `temporal_40` did not contain the gold `2025-07-20`. The local BM25
preselection chose semantically similar messages from different project
threads, so extraction could not recover either answer. This exposes a protocol
confound: question-conditioned, bounded source selection measures an upstream
retriever and Team Note together, while the producer is intentionally blind to
the question.

Future temporal evaluation must report an evidence-coverage gate before scoring
memory quality. The preferred protocol is to ingest complete project/channel
histories, then test extraction and recall. If a bounded diagnostic remains
necessary, cases whose gold-supporting messages are absent must be marked
`source_not_covered` and excluded from extraction-quality conclusions.
