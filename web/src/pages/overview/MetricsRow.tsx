import { MetricTile } from "../../components/MetricTile";
import type { OverviewMetrics } from "../../api/types";

function seconds(ms: number | undefined): string {
  return ms === undefined ? "—" : `${(ms / 1000).toFixed(1)}`;
}

export function MetricsRow({ metrics }: { metrics: OverviewMetrics }) {
  const acceptPct = Math.round(metrics.recall_accept_rate * 100);
  return (
    <div className="ov-metrics">
      <MetricTile label="Evidence captured" value={metrics.evidence_captured} note="evidence events written to the lake" />
      <MetricTile label="Facts in circulation" value={metrics.live_notes} note={`live team notes · ${metrics.notes_expiring_today} expiring in 24h`} />
      <MetricTile label="Context handed to agents" value={metrics.recalls_served} note={`recalls served · ${acceptPct}% with evidence`} />
      <MetricTile label="Time to remember" value={seconds(metrics.p95_ms)} unit="s" note={`p95 · p50 ${seconds(metrics.p50_ms)}s`} />
      <MetricTile label="Needs a person" value={metrics.attention_count} note="items in the queue below" />
    </div>
  );
}
