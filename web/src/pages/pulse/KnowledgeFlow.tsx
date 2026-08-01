// Knowledge flow region: heading with the window timestamp plus the
// agents -> Team Notes -> recall SVG strip, stroke widths scaled by volume.

import type { OperationsAgentStats } from "../../api/types";
import { formatTime } from "../../lib/format";
import { RegionError } from "../../components/RegionError";

/** Stroke width scales with recent volume but stays within 1.5..7.5px. */
function flowWidth(volume: number): number {
  return 1.5 + Math.min(6, Math.sqrt(Math.max(0, volume)));
}

function FlowStrip({ agents }: { agents: OperationsAgentStats[] }) {
  const written = agents.reduce((sum, a) => sum + a.events_written, 0);
  const notes = agents.reduce((sum, a) => sum + a.notes_authored, 0);
  const recalled = agents.reduce((sum, a) => sum + a.recall_requests, 0);
  return (
    <svg
      className="pulse-flow"
      viewBox="0 0 640 110"
      role="img"
      aria-label={`Knowledge flow over the last hour: ${written} events written, ${notes} notes produced, ${recalled} recalls`}
    >
      <path
        className="flow-line"
        d="M132 55 C 200 55, 240 55, 292 55"
        strokeWidth={flowWidth(written)}
      />
      <path
        className="flow-line"
        d="M348 55 C 400 55, 440 55, 508 55"
        strokeWidth={flowWidth(recalled)}
      />
      <g className="flow-node">
        <circle cx="76" cy="55" r="34" />
        <text x="76" y="51" textAnchor="middle" className="flow-title">
          Agents
        </text>
        <text x="76" y="67" textAnchor="middle" className="flow-sub">
          {agents.length}
        </text>
      </g>
      <g className="flow-node">
        <circle cx="320" cy="55" r="34" />
        <text x="320" y="51" textAnchor="middle" className="flow-title">
          Team Notes
        </text>
        <text x="320" y="67" textAnchor="middle" className="flow-sub">
          +{notes}
        </text>
      </g>
      <g className="flow-node">
        <circle cx="564" cy="55" r="34" />
        <text x="564" y="51" textAnchor="middle" className="flow-title">
          Recall
        </text>
        <text x="564" y="67" textAnchor="middle" className="flow-sub">
          {recalled}
        </text>
      </g>
    </svg>
  );
}

interface KnowledgeFlowProps {
  status: "loading" | "ready" | "error";
  error?: unknown;
  onRetry: () => void;
  generatedAt?: string;
  agents: OperationsAgentStats[];
}

export function KnowledgeFlow({
  status,
  error,
  onRetry,
  generatedAt,
  agents,
}: KnowledgeFlowProps) {
  return (
    <>
      <div className="row between">
        <h2 className="flush">Knowledge flow</h2>
        {generatedAt && (
          <p className="faint small flush">
            Last 1 hour · generated at{" "}
            <span title={generatedAt}>{formatTime(generatedAt)}</span>
          </p>
        )}
      </div>
      <div className="card">
        {status === "loading" && <p className="muted small">Loading…</p>}
        {status === "error" && <RegionError error={error} onRetry={onRetry} />}
        {status === "ready" && (
          <>
            {error && (
              <div className="note warn">Auto-refresh failed; the data shown may be stale.</div>
            )}
            <FlowStrip agents={agents} />
          </>
        )}
      </div>
    </>
  );
}
