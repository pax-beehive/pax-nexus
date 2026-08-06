import type { OverviewSeriesPoint } from "../../api/types";
import type { TimeWindowPreset } from "../../lib/operations";

const ROWS = [
  { key: "evidence", label: "Evidence in", token: "var(--color-neutral-400)" },
  { key: "facts", label: "Facts kept", token: "var(--color-accent)" },
  { key: "recalls", label: "Recalls served", token: "var(--color-neutral-700)" },
] as const;

function bucketLabel(iso: string, window: TimeWindowPreset): string {
  const d = new Date(iso);
  if (window === "7d") {
    return `${d.getMonth() + 1}/${d.getDate()}`;
  }
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  return `${hh}:${mm}`;
}

/** Three small-multiple bar strips — one per measure, each normalized to its
 * own peak (shown at the right), so three different scales never share an
 * axis. Row swatch + name carry series identity; color is never the only cue. */
export function ThroughputChart({
  series,
  window,
}: {
  series: OverviewSeriesPoint[];
  window: TimeWindowPreset;
}) {
  return (
    <div className="ov-chart">
      <div className="ov-chart-note">
        Three separate scales — each row is drawn against its own peak, shown at the right.
      </div>
      {ROWS.map((row) => {
        const values = series.map((p) => p[row.key]);
        const peak = Math.max(1, ...values);
        return (
          <div key={row.key} data-row={row.key}>
            <div className="ov-chart-rowhead">
              <span className="ov-chart-series">
                <span className="ov-chart-swatch" style={{ background: row.token }} />
                {row.label}
              </span>
              <span className="ov-chart-peak">peak {Math.max(0, ...values)}</span>
            </div>
            <div className="ov-chart-track">
              {series.map((p) => (
                <div key={p.bucket_at} className="ov-chart-cell" title={`${bucketLabel(p.bucket_at, window)} · ${p[row.key]}`}>
                  <div
                    className="ov-chart-bar"
                    style={{ height: `${(p[row.key] / peak) * 100}%`, background: row.token }}
                  />
                </div>
              ))}
            </div>
          </div>
        );
      })}
      <div className="ov-chart-ticks">
        {series.map((p) => (
          <span key={p.bucket_at} className="ov-chart-tick">
            {bucketLabel(p.bucket_at, window)}
          </span>
        ))}
      </div>
    </div>
  );
}
