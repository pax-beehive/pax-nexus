# Team Note

The Team Note context owns short-lived, evidence-backed collaboration state for passive recall.

## Language

**Recall Observation**:
A seven-day record of one successful recall's request controls (with the query
stored as a digest), current extraction snapshot, exact delivered envelope, and
Recall Trace, including a zero-hit response. It supports diagnostics and
evaluation without changing recall decisions.

**Recall Trace**:
The per-candidate record of how shared recall policy handled every available
candidate: compiled Recall Intent, retrieval lanes and reasons, hard-gate
results, temporal resolution, query-safe matched-term counts, scorecard contributions, relation path, final
disposition, budget drop, and delivery-claim loss. It attributes recall losses
to a stage without re-running retrieval.

**Recall Intent**:
The deterministic interpretation of a recall query as current, as-of,
changes-since, history, or discovery work, plus requested fact types, scope,
time boundary, relation budget, and token budget. Unrecognized text does not
become a hard constraint.

**Retrieval Lane**:
An inspectable candidate path such as exact scope, lexical terms, temporal
state, one-hop relation, coordination act, or agent routing. Semantic retrieval
is a compatibility fallback and never contributes points to the General Recall
v3 evidence scorecard.

**Evidence Scorecard**:
The monotonic General Recall v3 point breakdown for direct scope or entity
match, required-fact coverage, lexical coverage, temporal validity, source
evidence, and coordination relevance. Version 1 is intentionally uncalibrated;
the score is evidence ordering, not a probability or answer-quality claim.

**Candidate**:
A model-proposed collaboration fact grounded in one or more Session Events.

**Claim**:
An extraction v2 internal product: a source-faithful assertion found in one or
more new Session Events. Claims are emitted only when separating the source
assertion changes the outcome, such as a conflict, ambiguous correction,
unresolved time, or corroboration. They are diagnostic and are never admitted
or delivered directly.

**State Decision**:
An extraction v2 internal product proposing how directly cited Events and,
when needed, Claims affect one canonical memory identity: create, update,
resolve, no_change, or keep_conflict_open. Ordinary atomic facts cite Events
directly instead of duplicating their content in a Claim. Deterministic code
validates and admits decisions as one atomic Extraction Run.

**Extraction Coverage**:
The explicit accounting of every new Session Event as evidence for a State
Decision or Claim, or as a reviewed `no_state` Event. Unreviewed Events,
invalid `no_state` declarations, and Claims with no State Decision are recorded
in the Extraction Trace.

**Extraction Trace**:
The per-slice diagnostic record of extraction v2 products: claims, state
decisions, Extraction Coverage, interaction observations, deterministic
rejections, and would-verify triggers. It attributes extraction losses to
observation coverage, claim detection, identity resolution, state transition,
temporal validation, or admission. It never enters passive agent context.

**Source Clause Citation**:
The shortest exact, contiguous Event text that supports one State Decision.
It narrows evidence authority inside an Event without becoming a new memory
rendering or replacing the Event as factual evidence.

**Extraction Observation Time**:
The latest source time among the new Events in one extraction slice. It is the
fixed instant used by deterministic temporal admission and remains unchanged
when a saved extraction response is replayed.

**Extraction Execution Envelope**:
The validated serial budget shared by provider execution, the durable worker,
runtime configuration, and extraction evaluation. It relates attempt timeout,
retry backoff, calls per Slice, Slices per job, background persistence margin,
and the worker deadline so no inner provider budget can outlive its job.

**Candidate Rejection**:
A Candidate dropped before admission with a deterministic grounding or policy
reason, recorded so extraction evaluation can attribute lost facts without
failing the whole slice.

**Quarantined Extraction Run**:
An Extraction Run whose candidates failed deterministic admission. It is
durably recorded with its reason so the stream advances instead of retrying a
result that can never be admitted. A replay of the same input observes the
quarantine.

**Extraction Replay**:
The idempotent re-application of one Extraction Run. Replays are matched on
deterministic inputs (actor, sequence range, input checksum, model, prompt
version); token usage and recomputed candidate batches never conflict with the
durable result, which always wins.

**Extraction Episode**:
The durable, append-only LLM context that incrementally maintains knowledge for
one collaboration scope, task, and thread across producer sessions.

**Extraction Checkpoint**:
A structured handoff containing active, resolved, and unresolved knowledge with
stable memory IDs, evidence IDs, and source cursors. It replaces an older
Extraction Episode prefix after compaction but is never factual evidence itself.

**Continuity Summary**:
A derived, non-blocking summary of the history that slid past an Extraction
Episode's recent raw window. It helps the extractor maintain state but cannot be
candidate evidence.

**Team Note**:
The current admitted revision of a short-lived collaboration fact.
_Avoid_: Wiki page, raw memory

**Historical Team Note Revision**:
An immutable recorded revision selected only by explicit `as_of`, `history`, or
`changes_since` Recall Intent. Its validity interval, recorded time, audience,
task/thread scope, evidence, and delivery revision are evaluated as recorded;
future-recorded revisions cannot leak into an earlier Recall Observation.

**Delivery**:
A recorded insertion of one Team Note revision into an agent session.

## Relationships

- A **Candidate** cites one or more Session Events.
- A **State Decision** cites both its supporting Events and, when it changes
  state, the exact **Source Clause Citation** that authorizes that change.
- An **Extraction Episode** consumes Session Events from multiple sessions.
- When compaction is enabled, an **Extraction Checkpoint** resumes one
  **Extraction Episode** after compaction. Rolling extraction does not require a
  checkpoint.
- A **Continuity Summary** plus the recent raw window provides the default
  rolling context; summary failure never blocks episode advancement.
- A **Candidate** creates, updates, or resolves one **Team Note**.
- A **Team Note** may produce one **Delivery** per revision and recipient session.
- An explicit historical **Recall Intent** selects **Historical Team Note
  Revisions**; passive current recall continues to select only active current
  **Team Notes**.

## Example dialogue

> **Dev:** "Should this long project history become a **Team Note**?"
> **Domain expert:** "Only the current handoff should; durable history belongs in LLM Wiki."

## Flagged ambiguities

- "note" means **Team Note** only inside this context; LLM Wiki stores pages.
