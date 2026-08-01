// Storage history trend (operations doc section 10): trend only, sorted by
// captured_at; never a backup view.

import { formatBytes, formatTime } from "../../lib/format";
import { Button } from "../../components/Button";
import type { HistoryRegion } from "./hooks";

type HistoryReady = Extract<HistoryRegion, { status: "ready" }>;

export function StorageHistoryReady({
  region,
  onLoadMore,
}: {
  region: HistoryReady;
  onLoadMore: (cursor: string) => void;
}) {
  const cursor = region.nextCursor;
  if (region.items.length === 0) {
    return <p className="muted small">No history snapshots yet.</p>;
  }
  return (
    <>
      <table>
        <thead>
          <tr>
            <th>Captured at</th>
            <th>Database total</th>
            <th>Status</th>
            <th>Warnings</th>
          </tr>
        </thead>
        <tbody>
          {[...region.items]
            .sort((a, b) => a.captured_at.localeCompare(b.captured_at))
            .map((snap) => (
              <tr key={snap.snapshot_id}>
                <td className="small">
                  <span title={snap.captured_at}>{formatTime(snap.captured_at)}</span>
                </td>
                <td className="small">{formatBytes(snap.database_physical_bytes)}</td>
                <td className="small">{snap.status}</td>
                <td className="small">
                  {snap.warning_codes.length > 0 ? snap.warning_codes.join(", ") : "—"}
                </td>
              </tr>
            ))}
        </tbody>
      </table>
      {cursor !== undefined && (
        <div style={{ marginTop: 10, textAlign: "center" }}>
          <Button
            size="sm"
            disabled={region.loadingMore}
            onClick={() => onLoadMore(cursor)}
          >
            {region.loadingMore ? "Loading…" : "Load more"}
          </Button>
        </div>
      )}
    </>
  );
}
