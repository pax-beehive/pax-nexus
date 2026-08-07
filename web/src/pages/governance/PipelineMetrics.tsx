// Pipeline health top bar: six numbers distilled from `OperationsSummary`
// (design spec §2.3). Three subtitles are deliberate departures from the
// mockup's literal copy, recorded in the design doc's §8 accounting:
//
//   - "空手而归" (格6) uses `recalls.empty` — the closest honest count to
//     the mockup's "refused for budget or a hard gate", which
//     `OperationsSummary` has no direct counter for. The subtitle says so
//     rather than asserting a specific cause.
//   - "排队中" (格3) carries the `oldest_unextracted_at` relative time,
//     labeled "最老的未抽取事件" — that timestamp is the oldest event not
//     yet extracted, not the oldest item held for review (格1); the mockup
//     conflated the two.
//   - "失败" (格2) is `extraction.failed` (extraction runs only) with
//     `summary.errors` folded into the subtitle. `errors` is "failed /
//     timed_out / cancelled, excluding rejected" across *every* operation
//     kind (recall, observation, extraction) -- a different, broader count
//     than the headline number. Dropping the old SummaryCards/
//     PipelineHealthCard cards left it with no home anywhere on this page;
//     this reuses the subtitle slot that used to be a bare "—" rather than
//     adding a seventh cell (which would break the 6-column grid).
//
// This component only reads `summary`; it never touches the region hooks
// in pages/operations/hooks.ts.
//
// I2 (final-fix wave): this used to hand-roll `.gv-metric-label` /
// `.gv-metric-value` / `.gv-metric-sub` in parallel with the shared
// `MetricTile` primitive (label + big number + note) that AgentBehaviourCard
// and AccessSummary already use. The hand-rolled label reintroduced the exact
// contrast anti-pattern task 8 had just fixed elsewhere: `.card-kicker`
// (components.css) is 10px/uppercase/`color: var(--color-accent)` and does
// NOT fade; `.gv-metric-label` was 10px/uppercase/`opacity: 0.55` on top of
// the *body* text color, which measured 3.65:1 in beige -- below AA. Reusing
// MetricTile fixes that for free instead of adding another one-off arcade
// override.

import type { OperationsSummary } from "../../api/types";
import { MetricTile } from "../../components/MetricTile";
import { formatRelativeFrom } from "../../lib/format";

interface MetricCell {
  label: string;
  value: string;
  sub: string;
}

function latencyValue(ms: number | undefined): string {
  return ms !== undefined ? `${ms} ms` : "insufficient samples";
}

function queuedSub(extraction: OperationsSummary["extraction"], generatedAt: string): string {
  if (extraction.unextracted_events === 0) return "—";
  if (!extraction.oldest_unextracted_at) return "Oldest unextracted event: time unknown";
  return `Oldest unextracted event: ${formatRelativeFrom(extraction.oldest_unextracted_at, generatedAt)}`;
}

export function PipelineMetrics({ summary }: { summary: OperationsSummary }) {
  const { extraction, latency, recalls, errors, generated_at } = summary;

  const cells: MetricCell[] = [
    { label: "Held for review", value: String(extraction.quarantined), sub: "—" },
    {
      label: "Failed",
      value: String(extraction.failed),
      sub: `extraction failed · ${errors} errors across all operations`,
    },
    {
      label: "Waiting",
      value: String(extraction.unextracted_events),
      sub: queuedSub(extraction, generated_at),
    },
    { label: "Typical delay", value: latencyValue(latency.p50_ms), sub: "p50" },
    { label: "Worst case", value: latencyValue(latency.p95_ms), sub: "p95" },
    { label: "Recalls refused", value: String(recalls.empty), sub: "insufficient evidence or budget" },
  ];

  return (
    <div className="gv-metrics">
      {cells.map((cell) => (
        <MetricTile key={cell.label} label={cell.label} value={cell.value} note={cell.sub} />
      ))}
    </div>
  );
}
