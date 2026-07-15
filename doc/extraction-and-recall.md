# Extraction and recall contract

## Session slicing

Extraction operates on one actor session at a time. A slice contains up to 25
new events, is bounded to approximately 8,192 input tokens, and may prepend up
to three already processed events for continuity. New events take priority when
the token budget is tight. An oversized first new event is still processed so
the cursor cannot become stuck.

Every candidate must cite at least one event from `new_event_ids`. Overlap
events may provide context but cannot be the candidate's only evidence. The
runtime also rejects unknown evidence IDs and model responses with more than 10
candidates per slice.

An explicitly complete input batch schedules extraction after the worker
debounce. An incomplete batch schedules extraction after the batch inactivity
timeout. The timeout job captures the session cursor; if a later batch has
advanced that cursor by execution time, the stale job exits and the later job
becomes the effective timer.

## Extracted candidate fields

| Field | Meaning | Participation after extraction |
| --- | --- | --- |
| `id` | Runtime-generated idempotency key for the candidate. | Prevents the same candidate from being applied twice. It does not rank recall. |
| `action` | `create`, `update`, or `resolve`. | Controls the canonical note state transition. Resolved notes are excluded from recall. |
| `kind` | `handoff`, `blocker`, `status`, or `artifact_reference`. | Selects TTL policy and recall priority in that order. |
| `subject` | Stable, short identity of the fact. | Combines with `task_ref`, `thread_ref`, and `kind` to form the canonical note key. It is not emitted directly. |
| `body` | Concise factual collaboration note. | Becomes the recalled text, rendered as `[kind] body`, and consumes the token budget. |
| `task_ref` | Optional task identity. | A nonempty value must equal the recall request's `task_ref`. It is also part of the canonical key. |
| `thread_ref` | Optional thread identity. | A nonempty value must equal the recall request's `thread_ref`. It is also part of the canonical key. |
| `audience_agent_ids` | Optional recipient allowlist. Empty means all agents in scope. | Filters recall by the requesting `agent_id`. |
| `evidence_event_ids` | Source event IDs supporting the candidate. | Required for grounding, origin validation, audit, and revision evidence. It does not rank recall. |
| `origin` | Source `user_id`, `agent_id`, and `session_id`. | Set by the runtime from the authenticated session slice and used for evidence validation and provenance. It does not rank recall. |

The HTTP envelope retains the legacy string `items` and also returns one
structured `details` entry per item with `note_id`, `revision`, `text`, and
`origin`. The paxm bridge maps that origin onto each JSON-RPC memory hit, so a
consumer can identify the source user, agent, and session. The bridge does not
advertise full paxm attribution capability yet because collaboration scope is
assigned by the API key rather than faithfully round-tripping a caller-supplied
scope object.

## paxm batch forwarding

The bridge implements `paxm.putBatch` as a real batch operation. It validates
all items before sending any request, resolves origin from the top-level paxm
origin with metadata and provider identity as fallbacks, and groups items by
resolved user, agent, and session. Each group becomes one complete Team Memory
session batch. Groups and events retain first-seen order, while returned memory
references always retain the original paxm input order.

## Recall pipeline

Recall first resolves the collaboration scope from the API key or provider
configuration. Client metadata cannot override it. Within that scope, a note
is eligible only when all of these conditions hold:

1. State is active and both TTL deadlines are still in the future.
2. Nonempty `task_ref` and `thread_ref` values match the request.
3. The requesting agent is included by `audience_agent_ids`.
4. The current note revision has not already been delivered to the requesting
   session.

Eligible notes are ordered by `handoff`, `blocker`, `status`, then
`artifact_reference`; ties use most recently updated first. Notes that fit the
request token budget are atomically claimed and returned as `[kind] body`.

The current MVP does not perform semantic or lexical matching on query text.
The paxm bridge uses query metadata for session, task, and thread identity plus
the requested limit for a token-budget cap; query text itself does not
participate in filtering or ranking. This is intentional so the eval can first
measure deterministic team-note policy before adding retrieval heuristics.
