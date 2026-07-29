// Pipeline health card: the asynchronous extraction chain behind accepted
// observations (operations doc section 7).

import type { OperationsSummary } from "../../api/types";
import { formatTime } from "../../lib/format";
import { Stat } from "./SummaryCards";

export function PipelineHealthCard({ summary }: { summary: OperationsSummary }) {
  const ex = summary.extraction;
  return (
    <div className="card">
      <div className="stat-grid">
        <Stat label="extraction runs" value={ex.runs} />
        <Stat label="completed" value={ex.completed} />
        <Stat
          label="quarantined"
          value={ex.quarantined}
          title="deterministic rejection, not counted in failed/errors"
        />
        <Stat label="failed" value={ex.failed} />
        <Stat label="admitted revisions" value={ex.admitted_revisions} />
        <Stat label="unextracted backlog" value={ex.unextracted_events} />
        <Stat
          label="oldest pending"
          value={
            ex.oldest_unextracted_at !== undefined ? (
              <span title={ex.oldest_unextracted_at}>{formatTime(ex.oldest_unextracted_at)}</span>
            ) : ex.unextracted_events > 0 ? (
              "age unavailable"
            ) : (
              "—"
            )
          }
        />
      </div>
      <p className="faint small" style={{ marginBottom: 0 }}>
        Observation accepted does not mean extraction finished; backlog and extraction are a separate asynchronous chain.
      </p>
    </div>
  );
}
