// Activity summary cards (operations doc section 7).

import type { OperationsSummary } from "../../api/types";
import { Stat } from "./Stat";

export function SummaryCards({ summary }: { summary: OperationsSummary }) {
  const { observations: obs, recalls, latency } = summary;
  return (
    <div className="grid">
      <div className="card">
        <h2>Observations</h2>
        <div className="stat-grid">
          <Stat label="requests" value={obs.requests} />
          <Stat label="succeeded" value={obs.succeeded} />
          <Stat label="input events" value={obs.input_events} />
          <Stat label="events written" value={obs.events_written} />
          <Stat label="duplicates" value={obs.duplicate_events} />
        </div>
        {obs.duplicate_events > 0 && (
          <p className="faint small" style={{ marginBottom: 0 }}>
            duplicates are legitimate idempotent replays (events_written=0 with duplicate&gt;0 still counts as success), not failures.
          </p>
        )}
      </div>
      <div className="card">
        <h2>Recalls</h2>
        <div className="stat-grid">
          <Stat label="requests" value={recalls.requests} />
          <Stat label="succeeded" value={recalls.succeeded} />
          <Stat label="with evidence" value={recalls.with_evidence} />
          <Stat label="empty" value={recalls.empty} title="A correct zero-result response is still succeeded" />
          <Stat label="memory hits" value={recalls.memory_hits} title="Counts only Memory Search hits" />
          <Stat label="notes delivered" value={recalls.team_notes_delivered} />
        </div>
        <p className="faint small" style={{ marginBottom: 0 }}>
          memory.search {recalls.memory_search_requests} · memory.get {recalls.memory_get_requests} ·
          team_note.recall {recalls.team_note_recall_requests} ｜ hits: evidence{" "}
          {recalls.evidence_hits} · hint {recalls.hint_hits} · reference {recalls.reference_hits}
          <br />
          with_evidence does not imply answer correctness (correctness belongs to Evaluation).
        </p>
      </div>
      <div className="card">
        <h2>Latency &amp; Errors</h2>
        <div className="stat-grid">
          <Stat label="samples" value={latency.sample_count} />
          <Stat
            label="p50"
            value={latency.p50_ms !== undefined ? `${latency.p50_ms} ms` : "insufficient samples"}
          />
          <Stat
            label="p95"
            value={latency.p95_ms !== undefined ? `${latency.p95_ms} ms` : "insufficient samples"}
          />
          <Stat label="errors" value={summary.errors} title="failed / timed_out / cancelled, excluding rejected" />
        </div>
        <p className="faint small" style={{ marginBottom: 0 }}>
          Samples cover complete external calls to memory.search / memory.get / team_note.recall.
        </p>
      </div>
    </div>
  );
}
