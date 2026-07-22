import { useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { ApiError } from "../api/client";
import { getAdminAgent } from "../api/queries";
import type { AgentProfile, HumanMe } from "../api/types";
import { can } from "../lib/capabilities";
import { useErrorHandler } from "../lib/useErrorHandler";
import { AgentArtifacts } from "../components/AgentArtifacts";
import { AgentGovernanceCard } from "../components/AgentGovernanceCard";
import { Badge } from "../components/Badge";

/**
 * Admin governance view of a single agent (doc section 5.7). Admin may only
 * suspend; edit / resume / retire / transfer are Owner-only. Enrollment
 * issuance stays with the owning human — admins can view and revoke only.
 */
export function AdminAgentDetailPage({ me }: { me: HumanMe }) {
  const { agentId = "" } = useParams();
  const handleError = useErrorHandler();
  const [agent, setAgent] = useState<AgentProfile | undefined>();
  const [notFound, setNotFound] = useState(false);

  const refetch = useCallback(async () => {
    const fresh = await getAdminAgent(agentId);
    setAgent(fresh);
    return fresh;
  }, [agentId]);

  useEffect(() => {
    let cancelled = false;
    getAdminAgent(agentId)
      .then((a) => {
        if (!cancelled) setAgent(a);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        if (err instanceof ApiError && err.status === 404) setNotFound(true);
        else handleError(err);
      });
    return () => {
      cancelled = true;
    };
  }, [agentId, handleError]);

  if (notFound) {
    return (
      <div className="card">
        <h2>404</h2>
        <p className="muted">
          Agent 不存在或不可见。<Link to="/admin/agents">返回列表</Link>
        </p>
      </div>
    );
  }
  if (!agent) return <p className="muted">加载中…</p>;

  const actorRole = me.role ?? "member";
  const mayGovern = can(actorRole, "govern.any-agent");

  return (
    <>
      <div className="page-head">
        <div>
          <h1>{agent.display_name}</h1>
          <div className="row small muted">
            <code>{agent.agent_id}</code>
            <Badge status={agent.status} />
            <span>owner: {agent.owner_membership_id ?? "—"}</span>
          </div>
        </div>
        <Link to="/admin/agents" className="btn ghost">
          ← 返回
        </Link>
      </div>
      <AgentGovernanceCard
        scope="admin"
        agent={agent}
        canEdit={mayGovern}
        canSuspend={can(actorRole, "suspend.any-agent")}
        canResume={mayGovern}
        canRetire={mayGovern}
        onChanged={setAgent}
        refetch={refetch}
      />
      <AgentArtifacts scope="admin" agentId={agent.agent_id} agentStatus={agent.status} canIssue={false} />
    </>
  );
}
