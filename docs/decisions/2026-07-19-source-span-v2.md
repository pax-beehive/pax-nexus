# Source Span v2 retrieval shards

Date: 2026-07-19

## Decision

Add `source-span-v2` as an opt-in extraction candidate. It retains the
`source-span-v1` contract that model output is limited to a neutral `subject`
and literal `topics`, while deterministic extraction turns each new event into
ordered raw-text shards sized for the shared recall budget.

The Session Lake remains the immutable full-session archive. Every byte of a
new event appears, in order, in exactly one or more persisted source shards;
an oversized event is split into consecutive raw fragments with the same event
identifier. A source shard carries event ID, occurred-at time, actor identity,
event type, task, and thread. It is still source material, not canonical state.

Shards target 180 extraction-token units, which leaves room for generic recall
formatting within the existing 500-token shared recall budget. Small source
events are not padded. The normal model-output candidate cap does not apply to
the deterministic shards, because truncating them would lose retained source
content from an otherwise bounded Session Slice.

## Boundaries

- `source-span-v2` has its own extraction-run and episode protocol version.
  It cannot replay either a v1 source span or a state-normalizing v2 result.
- The change is confined to extraction candidate construction. It adds no
  candidate-name branch to `RecallNotes`, `PlanRecall`, ranking, relation
  expansion, authorization, or token-budget packing.
- Recall consumes these notes through the same generic `source_span` note kind
  and ordinary Candidate/NoteStore seams as before.
- The model still does not summarize, normalize, infer, or resolve state.

## Evaluation gate

Run the existing end-to-end cohort with `source-span-v2` and the fixed
`passive-v1` recall build. Compare source retention, budget-drop count,
candidate recall over available atoms, context precision, and judge accuracy
to the recorded `source-span-v1 + passive-v1` baseline. If v2 does not reduce
budget drops and improve positive-case judge correctness, stop further
investment in the source-span extraction path.
