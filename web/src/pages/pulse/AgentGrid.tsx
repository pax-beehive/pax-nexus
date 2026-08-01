// Agents region: the heading plus the loading / error / empty states and the
// card wall of per-agent pulse cards.

import { Link } from "react-router-dom";
import type { AgentProfile, OperationsAgentStats } from "../../api/types";
import { RegionError } from "../../components/RegionError";
import { AgentPulseCard } from "./AgentPulseCard";

interface AgentGridProps {
  status: "loading" | "ready" | "error";
  error?: unknown;
  onRetry: () => void;
  agents: OperationsAgentStats[];
  freshNotes: Set<string>;
  agentTypes: ReadonlyMap<string, AgentProfile>;
  now: number;
  canExplore: boolean;
}

export function AgentGrid({
  status,
  error,
  onRetry,
  agents,
  freshNotes,
  agentTypes,
  now,
  canExplore,
}: AgentGridProps) {
  return (
    <>
      <h2>Agents</h2>
      {status === "loading" && (
        <div className="card">
          <p className="muted small">Loading…</p>
        </div>
      )}
      {status === "error" && (
        <div className="card">
          <RegionError error={error} onRetry={onRetry} />
        </div>
      )}
      {status === "ready" && agents.length === 0 && (
        <div className="card flat">
          <h3 style={{ marginTop: 0 }}>No agent activity yet</h3>
          <p className="muted small">
            Once agents are registered and connected, this page shows each agent's event
            writes, notes produced, recalls, and capsule traffic in real time.
          </p>
          <Link className="btn sm" to="/admin/agents">
            Go to All Agents to register an agent
          </Link>
        </div>
      )}
      {status === "ready" && agents.length > 0 && (
        <div className="grid">
          {agents.map((agent) => (
            <AgentPulseCard
              key={agent.agent_id}
              agent={agent}
              agentType={agentTypes.get(agent.agent_id)?.agent_type}
              freshNotes={freshNotes}
              now={now}
              canExplore={canExplore}
            />
          ))}
        </div>
      )}
    </>
  );
}
