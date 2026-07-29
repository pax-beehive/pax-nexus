// Storage snapshot view (operations doc section 10): partial captures,
// staleness and schema mismatches get explicit warnings so zeroed values are
// never read as a healthy empty database.

import type { OperationsStorageSnapshot } from "../../api/types";
import { formatBytes, formatTime } from "../../lib/format";
import {
  componentAvailability,
  isSnapshotStale,
  KNOWN_STORAGE_SCHEMA_VERSION,
  storageComponentLabel,
} from "../../lib/operations";
import { Stat } from "./Stat";

export function StorageSnapshotView({ snapshot }: { snapshot: OperationsStorageSnapshot }) {
  const partial = snapshot.status === "partial";
  const unknownStatus = snapshot.status !== "complete" && snapshot.status !== "partial";
  const stale = isSnapshotStale(snapshot.captured_at);
  const schemaMismatch = snapshot.schema_version !== KNOWN_STORAGE_SCHEMA_VERSION;
  const statusBadge =
    snapshot.status === "complete"
      ? "b-active"
      : snapshot.status === "partial"
        ? "b-suspended"
        : "b-expired";

  return (
    <>
      {partial && (
        <div className="note warn">
          This capture is incomplete (partial), captured at{" "}
          <span title={snapshot.captured_at}>{formatTime(snapshot.captured_at)}</span>
          {snapshot.warning_codes.length > 0 && (
            <>
              ; warning codes:{" "}
              {snapshot.warning_codes.map((code) => (
                <code key={code} style={{ marginRight: 6 }}>
                  {code}
                </code>
              ))}
            </>
          )}
          . Zero values from failed components do not mean the database is truly empty.
        </div>
      )}
      {unknownStatus && (
        <div className="note warn">
          Unknown capture status <code>{snapshot.status}</code>; the returned data is shown below.
        </div>
      )}
      {stale && (
        <div className="note warn">
          Snapshot captured at{" "}
          <span title={snapshot.captured_at}>{formatTime(snapshot.captured_at)}</span>{" "}
          and may be stale (captured hourly by default; deployments can adjust the interval;
          this does not indicate a database failure).
        </div>
      )}
      {schemaMismatch && (
        <div className="note warn">
          schema_version <code>{snapshot.schema_version}</code> differs from the frontend's known version{" "}
          <code>{KNOWN_STORAGE_SCHEMA_VERSION}</code>; only database totals and raw component names are shown.
        </div>
      )}
      <div className="stat-grid" style={{ marginBottom: 12 }}>
        <Stat
          label="database physical"
          value={formatBytes(snapshot.database_physical_bytes)}
          title="Total size of the entire database"
        />
        <Stat
          label="other physical"
          value={formatBytes(snapshot.other_physical_bytes)}
          title="Allocation not attributed to a known component"
        />
        <Stat
          label="captured at"
          value={
            <span className="small" title={snapshot.captured_at}>
              {formatTime(snapshot.captured_at)}
            </span>
          }
        />
        <Stat label="status" value={<span className={`badge ${statusBadge}`}>{snapshot.status}</span>} />
      </div>
      {schemaMismatch ? (
        <p className="small muted">
          components: {snapshot.components.map((c) => c.component).join(", ") || "—"}
        </p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Component</th>
              <th>Counts</th>
              <th>Logical</th>
              <th>Physical</th>
              <th>Reclaimable</th>
              <th>Data time range</th>
            </tr>
          </thead>
          <tbody>
            {snapshot.components.map((component) => {
              const availability = componentAvailability(snapshot, component);
              const label = storageComponentLabel(component.component);
              return (
                <tr key={component.component}>
                  <td>
                    {label}
                    {label !== component.component && (
                      <span className="faint small"> ({component.component})</span>
                    )}
                  </td>
                  <td className="small">
                    {Object.entries(component.counts)
                      .map(([key, value]) =>
                        key.endsWith("_bytes") ? `${key} ${formatBytes(value)}` : `${key} ${value}`,
                      )
                      .join(" · ") || "—"}
                  </td>
                  <td className="small">
                    {availability.logical ? formatBytes(component.logical_bytes) : "unavailable"}
                  </td>
                  <td className="small">
                    {availability.physical ? formatBytes(component.physical_bytes) : "unavailable"}
                  </td>
                  <td className="small">
                    {component.estimated_reclaimable_bytes !== undefined
                      ? formatBytes(component.estimated_reclaimable_bytes)
                      : "—"}
                  </td>
                  <td className="small">
                    {component.oldest_at || component.newest_at ? (
                      <span title={`${component.oldest_at ?? "—"} → ${component.newest_at ?? "—"}`}>
                        {formatTime(component.oldest_at)} → {formatTime(component.newest_at)}
                      </span>
                    ) : (
                      "—"
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
      <p className="faint small">
        logical is the interpretable size of the domain payload; physical is the current
        allocation of the PostgreSQL relation. After deletion, logical/count may drop while
        physical does not, which is expected behavior.
      </p>
    </>
  );
}
