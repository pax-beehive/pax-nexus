# Rolling Extraction Context

Status: Accepted

Date: 2026-07-16

## Context

The original extractor handled each producer session independently. It saw a
bounded event slice plus a small overlap and relied on model-generated subjects
and canonical keys to reconcile updates later. Stage evaluation showed that the
largest loss occurred before recall: facts requiring multiple sessions or a
later correction were often missed, duplicated, or left superseded.

The extraction control loop should therefore observe the knowledge evolution it
is expected to control. Input cost can remain bounded when an OpenAI-compatible
provider reuses an identical append-only prompt prefix, but cache hits are an
optimization rather than a correctness dependency.

## Decision

Maintain a durable **Extraction Episode** for each:

```text
scope_id + task_ref + thread_ref
```

Events from different producer sessions advance the same episode. Each request
contains the unchanged episode prefix plus only the new event slice. The model
returns candidate deltas rather than regenerating the complete Team Note state.
An explicit `identity_ref` is a shared memory identity: when present it allows a
later candidate from another agent to update or resolve the same Team Note.

When estimated episode context reaches 100 Ki tokens, start one asynchronous
singleflight structured compaction. Extraction may continue appending session
events while this speculative flight runs. Before the next append would cross
128 Ki tokens, the episode writer waits for the flight. Installing its checkpoint
removes only the prefix included in the compaction snapshot; messages appended
while it ran remain as a tail after the checkpoint. A failed or stale speculative
result is recomputed synchronously at the hard threshold.

The prompt follows the handoff shape of Codex's public
compaction prompt: preserve progress/current state, decisions, constraints,
remaining unresolved work, and the evidence needed for another model to resume.
Team Memory strengthens that shape by requiring:

- active, resolved, and open knowledge collections;
- stable memory IDs;
- exact dates, values, owners, and replacement reasoning;
- evidence event IDs and an evidence index;
- server-owned source cursors.

The public Codex prompt is a design reference, not a copied runtime dependency:
<https://github.com/openai/codex/blob/main/codex-rs/prompts/templates/compact/prompt.md>.

Session Events remain factual authority. Previous assistant outputs and
checkpoints provide continuity but cannot serve as evidence for new candidates.
The server rejects checkpoint items that cite unknown event IDs. Episode writes
use optimistic versions; same-process advances are serialized per episode.
Saved extraction responses are keyed by slice checksum so a queue retry can
replay the same candidates without another model call.

## Configuration

- `TEAM_MEMORY_EXTRACTION_CONTEXT_MODE=rolling` enables this decision and is the
  default application mode.
- `TEAM_MEMORY_EXTRACTION_CONTEXT_MODE=slice` preserves the previous extractor
  as an evaluation baseline.
- `TEAM_MEMORY_EXTRACTION_COMPACTION_ENABLED=false` disables speculative
  compaction and hard-limit waiting while preserving append-only rolling
  extraction. This is the default until the compaction stage passes its own
  retention and schema evaluation.
- `TEAM_MEMORY_EXTRACTION_SUMMARY_ENABLED=true` enables the default non-blocking
  sliding-window summary.
- `TEAM_MEMORY_EXTRACTION_SUMMARY_TRIGGER_TOKENS=8192` summarizes after another
  8,192 raw message tokens are ready to slide past the retained tail.
- `TEAM_MEMORY_EXTRACTION_SUMMARY_TAIL_TOKENS=16384` retains the newest raw
  message window after a successful summary.
- `TEAM_MEMORY_EXTRACTION_COMPACT_START_TOKENS=12288` starts speculative
  asynchronous compaction when compaction is enabled.
- `TEAM_MEMORY_EXTRACTION_COMPACT_TOKENS=16384` is the hard singleflight wait
  threshold when compaction is enabled.

The initial 102,400/131,072 thresholds were reduced after the rolling 10-case
evaluation. Episode p95 was already 15,924 estimated tokens, the maximum was
20,867, and completed extractor calls had 104,330 ms p95 latency despite an
85.2 percent prompt-cache hit rate. The 12,288 soft threshold starts compaction
before that observed tail, while the 16,384 hard threshold bounds further
growth. These values are evaluation-derived defaults, not a model context-window
limit.

The follow-up 10-case evaluation installed six checkpoints but produced five
hard-wait timeouts, invalid checkpoint collection shapes, and evidence-index
mismatches. End-to-end Team Note accuracy remained 0.20 and hybrid accuracy
remained 0.10. Compaction is therefore disabled by default so rolling extraction
and compaction can be controlled and evaluated as separate loops.

A subsequent no-compaction rolling run completed all 20 Team Note and hybrid
trials and restored end-to-end accuracy to 0.40 and 0.30 respectively. It also
made more required facts available than the slice baseline, but recall returned
only 7 of 23 required atoms in each arm. The largest episode was 19,143
estimated tokens, so neither compaction nor the 24 Ki-token summary sidecar was
exercised. This does not establish that compaction is intrinsically harmful or
helpful: it establishes that compaction added loss and blocking to workloads
that did not yet require context reduction. Compaction remains off while recall
is optimized. Its value must be tested separately on a long-horizon fixture
that crosses the summary and compaction boundaries and measures retention at
each boundary.

The replacement is a periodic summary sidecar, not a hard compaction boundary.
It starts only after raw message history reaches the tail plus trigger budget,
never blocks extraction, retains complete recent user/assistant turns, and
installs only a valid `{"summary":"..."}` result. Failure keeps the previous
summary and raw history. The summary is derived continuity context and cannot be
used as factual evidence for a new candidate.

## Consequences

The model can now observe cross-session changes directly, and its prompt prefix
is compatible with provider context caching. Extraction becomes stateful, so
episode persistence, ordering, idempotent retry, compaction fidelity, and cache
telemetry are part of the module's interface.

Compaction is a new possible loss point. The asynchronous result is process-local
until the next episode advance installs and persists it; a restart can discard
speculative work without changing factual state. Evaluation must separately report gold
atom coverage, superseded-fact leakage, unsupported knowledge, checkpoint
retention, prompt cache hit ratio, input cache hit/miss tokens, output tokens,
and downstream judge accuracy. Rolling mode should not replace the slice
baseline until these measurements show an improvement.
