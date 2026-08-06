// Pipeline health top bar: six numbers distilled from `OperationsSummary`
// (design spec §2.3). Two subtitles are deliberate departures from the
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
//
// This component only reads `summary`; it never touches the region hooks
// in pages/operations/hooks.ts.

import type { OperationsSummary } from "../../api/types";
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
  if (!extraction.oldest_unextracted_at) return "最老的未抽取事件：时间未知";
  return `最老的未抽取事件：${formatRelativeFrom(extraction.oldest_unextracted_at, generatedAt)}`;
}

export function PipelineMetrics({ summary }: { summary: OperationsSummary }) {
  const { extraction, latency, recalls, generated_at } = summary;

  const cells: MetricCell[] = [
    { label: "扣下待查", value: String(extraction.quarantined), sub: "—" },
    { label: "失败", value: String(extraction.failed), sub: "—" },
    {
      label: "排队中",
      value: String(extraction.unextracted_events),
      sub: queuedSub(extraction, generated_at),
    },
    { label: "典型延迟", value: latencyValue(latency.p50_ms), sub: "p50" },
    { label: "最坏情况", value: latencyValue(latency.p95_ms), sub: "p95" },
    { label: "空手而归", value: String(recalls.empty), sub: "证据不足或预算不够" },
  ];

  return (
    <div className="gv-metrics">
      {cells.map((cell) => (
        <div className="gv-metric" key={cell.label}>
          <div className="gv-metric-label">{cell.label}</div>
          <div className="gv-metric-value">{cell.value}</div>
          <div className="gv-metric-sub">{cell.sub}</div>
        </div>
      ))}
    </div>
  );
}
