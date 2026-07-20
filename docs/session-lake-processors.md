# Session Lake processor guide

## Purpose

Session Lake is the shared, immutable evidence layer for knowledge products.
It is not a Team Note database, a Wiki database, or a queue whose messages may
be deleted after one consumer handles them. A processor consumes ordered source
events and owns its derived output, lifecycle, and delivery policy.

This guide describes the current Team Note consumer and the contract that a
parallel processor, such as LLM Wiki maintenance, must follow.

## Boundary and terms

| Term | Meaning |
| --- | --- |
| Scope | The authorization and collaboration boundary for source evidence. |
| Actor | The stable `(user_id, agent_id, session_id)` identity on an event. |
| Session Event | Immutable source evidence with an event ID, per-actor sequence, occurrence time, content, and metadata. |
| Session Batch | One ingestion request containing events from exactly one Actor. `complete` is a sticky statement that no more events are expected for that stream. |
| Processor | An independent consumer that derives a product from Session Events. Team Note extraction and LLM Wiki maintenance are different processors. |
| Consumer cursor | A processor-owned high-water mark for one `(scope, actor)` stream. It is never a global “processed” bit. |
| Derived output | A Team Note revision, Wiki Page revision, checkpoint, or another product record. It must retain source-event provenance. |

The source of truth is the Session Event. A derived output is never allowed to
rewrite its source event or make a source event disappear for another
processor.

## Current ingestion contract

`POST /v1/session-batches` is defined in `idl/team_memory.thrift`. The storage
adapter enforces these properties:

- A batch has a non-empty scope and one Actor only; mixed-actor batches are
  rejected.
- An Event has a non-empty `id`, positive `sequence`, and non-zero
  `occurred_at`.
- `(scope_id, event_id)` is idempotent. Replaying the same event increments the
  duplicate count and does not overwrite stored content.
- `(scope_id, agent_id, session_id, sequence)` is unique, so an Actor stream
  has one durable order.
- `complete` only transitions from false to true. It never reopens a stream.

`captured_at` is set by Session Lake persistence. A processor should preserve
`occurred_at` as the factual time and may use `captured_at` to measure ingest
latency. It must not treat either timestamp as a replacement for the other.

Visibility, task, thread, and metadata are source attributes. Each processor
must apply and record its own eligibility and authorization policy; it must not
silently assume that Team Note’s `team_note_eligible` policy authorizes a Wiki
output.

## How Team Note consumes a stream today

The current path is intentionally specific to Team Note:

```text
ObserveSession
  -> append immutable Session Events in one transaction
  -> enqueue one Team Note extraction job for the Actor stream
  -> NextSlice(extraction cursor, limits, overlap)
  -> extractor proposal and deterministic admission
  -> persist Extraction Run and Team Note changes
  -> CommitSlice advances the Team Note extraction cursor
```

`sessionlake.Slice` contains two kinds of events:

- `NewEventIDs` are the events that advance the cursor and define
  `InputChecksum`.
- `OverlapEventIDs` are earlier events supplied only as bounded context. They
  must not advance the cursor and must not be counted as newly processed.

The Team Note runtime persists the extraction result before calling
`CommitSlice`. An admitted run, a recorded rejection, or a quarantined run may
advance the Team Note cursor. A transient model, storage, or cancellation error
must leave the cursor unchanged so the same source range can be retried.

For incomplete batches, the Team Note queue uses `require_current` to avoid
processing an obsolete stream head. Stream ordering is preserved per Actor;
different Actor streams can be processed in parallel.

## Important current limitation

The domain model requires independent consumer cursors, but the current
PostgreSQL implementation has only `session_streams.extraction_cursor`. That
column, `Lake.NextSlice`, and `Lake.CommitSlice` are Team Note extraction
mechanisms today.

**A new processor must not read, advance, reset, or reinterpret
`extraction_cursor`.** Doing so can cause Team Note to skip source events, or
can cause a Wiki processor to skip material already consumed by Team Note.

Similarly, a new processor must not subscribe to the `team_note_extract`
queue and reinterpret its jobs. That queue carries Team Note runtime semantics,
including its debounce, admission, and retry policy.

## Contract for a parallel processor

Before a second processor is implemented, add a generic consumer state seam.
Its durable identity must include at least:

```text
(processor_name, processor_version, scope_id, agent_id, session_id)
```

It should record:

- the committed source sequence or immutable range/checkpoint identifier;
- the processor configuration or schema version that interprets that range;
- a durable output revision or idempotency key;
- status, attempt/error information, and timestamps; and
- source event IDs used by every derived output.

Use the following processing rules:

1. Read source events in sequence after that processor’s own cursor.
2. Bound the range by explicit event and token limits. If overlap is used,
   label it as context rather than new consumption.
3. Build the derived output with source-event citations and the processor’s
   eligibility decision.
4. Atomically persist the output/revision and the processor cursor, or make
   the output idempotent against the same input range before advancing.
5. On retryable failure, do not advance the cursor.
6. On terminal invalid input, record a processor-local quarantine/rejection
   with source IDs, then advance only according to that processor’s policy.
7. Never let a reset of one processor change another processor’s output or
   cursor.

The first generic cursor/outbox design is a cross-product persistence contract
and should be introduced with an ADR and integration tests. Until it exists,
an LLM Wiki implementation should remain an explicitly isolated experiment;
it must not reuse Team Note’s persistence state as a shortcut.

## LLM Wiki application

An LLM Wiki processor differs from Team Note in product intent:

- It may use larger maintenance batches and cross-session synthesis.
- Its output is durable, browsed knowledge such as a Wiki Page revision, not
  passively injected collaboration state.
- It must preserve explicit source-event citations, including actor identity
  and factual time, so a page can be audited or rebuilt.
- Its active-recall/indexing path must be independent of `RecallNotes` and
  Team Note ranking.

For a Wiki page that incorporates multiple streams, keep the consumer cursor
per source stream and persist a page-level provenance set. A page checkpoint is
not permission to discard or compact the source events in Session Lake.

## Implementation and evaluation checklist

Before enabling a processor in production, verify:

- replaying an identical batch is idempotent;
- two processors can consume the same stream without cursor interference;
- a failed run retries the same source range;
- a terminal rejection is inspectable and does not wedge the stream;
- actor, scope, event IDs, occurrence times, and visibility policy survive into
  output provenance;
- a processor reset/rebuild leaves other processor state unchanged; and
- evaluation measures source coverage, derived-output correctness, latency,
  and downstream utility separately.

## Code map

- Shared source contract: `internal/session/contracts.go`
- Lake slicing and Team Note cursor operations: `internal/sessionlake/lake.go`
- PostgreSQL durability and idempotency: `internal/platform/postgres/store.go`
- Team Note orchestration: `internal/teamnote/runtime/app.go`
- Team Note queue: `internal/teamnote/extractionqueue/queue.go`
- Current LLM Wiki domain boundary: `internal/llmwiki/CONTEXT.md`
