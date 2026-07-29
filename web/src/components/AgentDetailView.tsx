// Shared agent detail layout for the owner view (/agents/:agentId) and the
// admin governance view (/admin/agents/:agentId). Loading, the 404 card, and
// the governance + artifacts composition are identical in both; the scope,
// back link, governance permissions, and the admin-only owner row come in as
// props.

import { Link } from "react-router-dom";
import type { AgentScope } from "../api/actions";
import type { AgentProfile } from "../api/types";
import { AgentArtifacts } from "./AgentArtifacts";
import { AgentGovernanceCard } from "./AgentGovernanceCard";
import { Badge, ProvisionedByBadge } from "./Badge";

export function AgentDetailView({
  scope,
  backTo,
  agent,
  notFound,
  showOwner = false,
  canEdit,
  canSuspend,
  canResume,
  canRetire,
  canIssue,
  onChanged,
  refetch,
}: {
  scope: AgentScope;
  /** Target of the header back button and the 404 card link. */
  backTo: string;
  agent: AgentProfile | undefined;
  notFound: boolean;
  /** Admin scope also shows the owning membership id in the header row. */
  showOwner?: boolean;
  canEdit: boolean;
  canSuspend: boolean;
  canResume: boolean;
  canRetire: boolean;
  canIssue: boolean;
  onChanged: (agent: AgentProfile) => void;
  refetch: () => Promise<AgentProfile>;
}) {
  if (notFound) {
    return (
      <div className="card">
        <h2>404</h2>
        <p className="muted">
          Agent does not exist or is not visible. <Link to={backTo}>Back to list</Link>
        </p>
      </div>
    );
  }
  if (!agent) return <p className="muted">Loading…</p>;

  return (
    <>
      <div className="page-head">
        <div>
          <h1>{agent.display_name}</h1>
          <div className="row small muted">
            <code>{agent.agent_id}</code>
            <Badge status={agent.status} />
            <ProvisionedByBadge agent={agent} />
            {showOwner && <span>owner: {agent.owner_membership_id ?? "—"}</span>}
          </div>
        </div>
        <Link to={backTo} className="btn ghost">
          ← Back
        </Link>
      </div>
      <AgentGovernanceCard
        scope={scope}
        agent={agent}
        canEdit={canEdit}
        canSuspend={canSuspend}
        canResume={canResume}
        canRetire={canRetire}
        onChanged={onChanged}
        refetch={refetch}
      />
      <AgentArtifacts
        scope={scope}
        agentId={agent.agent_id}
        agentStatus={agent.status}
        canIssue={canIssue}
      />
    </>
  );
}
