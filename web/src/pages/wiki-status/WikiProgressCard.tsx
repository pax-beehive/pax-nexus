import type { WikiIngestionStatus } from "../../api/wiki";
import { formatTime } from "../../lib/format";

export interface WikiProgressCardProps {
  status: WikiIngestionStatus | undefined;
  statusError: boolean;
}

export function WikiProgressCard({ status, statusError }: WikiProgressCardProps) {
  const progressAvailable = !statusError && status?.pending_sessions !== undefined;
  return (
    <section className="card wiki-progress" aria-label="Extraction progress">
      <div className="wiki-ingestion-copy">
        <span className="wiki-eyebrow">Extraction</span>
        <strong>Progress</strong>
      </div>
      {progressAvailable ? (
        <div className="wiki-progress-stats">
          <div>
            <span className="muted small">Pending sessions</span>
            <strong className="wiki-progress-figure">{status?.pending_sessions}</strong>
          </div>
          <div>
            <span className="muted small">Last processed</span>
            <strong className="wiki-progress-figure">
              {status?.last_processed_at ? formatTime(status.last_processed_at) : "Never"}
            </strong>
          </div>
        </div>
      ) : (
        <p className="muted small">Progress is unavailable.</p>
      )}
    </section>
  );
}
