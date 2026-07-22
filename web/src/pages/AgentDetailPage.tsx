import { useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { ApiError } from "../api/client";
import { getOwnAgent } from "../api/queries";
import type { AgentProfile } from "../api/types";
import { useErrorHandler } from "../lib/useErrorHandler";
import { AgentArtifacts } from "../components/AgentArtifacts";
import { AgentGovernanceCard } from "../components/AgentGovernanceCard";
import { Badge } from "../components/Badge";

export function AgentDetailPage() {
  const { agentId = "" } = useParams();
  const handleError = useErrorHandler();
  const [agent, setAgent] = useState<AgentProfile | undefined>();
  const [notFound, setNotFound] = useState(false);

  const refetch = useCallback(async () => {
    const fresh = await getOwnAgent(agentId);
    setAgent(fresh);
    return fresh;
  }, [agentId]);

  useEffect(() => {
    let cancelled = false;
    getOwnAgent(agentId)
      .then((a) => {
        if (!cancelled) setAgent(a);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        // 404: do not distinguish hidden from nonexistent (doc section 9).
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
          Agent 不存在或不可见。<Link to="/agents">返回列表</Link>
        </p>
      </div>
    );
  }
  if (!agent) return <p className="muted">加载中…</p>;

  return (
    <>
      <div className="page-head">
        <div>
          <h1>{agent.display_name}</h1>
          <div className="row small muted">
            <code>{agent.agent_id}</code>
            <Badge status={agent.status} />
          </div>
        </div>
        <Link to="/agents" className="btn ghost">
          ← 返回
        </Link>
      </div>
      <AgentGovernanceCard
        scope="me"
        agent={agent}
        canEdit={!agent.retired_at}
        canSuspend
        canResume
        canRetire
        onChanged={setAgent}
        refetch={refetch}
      />
      <AgentArtifacts
        scope="me"
        agentId={agent.agent_id}
        agentStatus={agent.status}
        canIssue={agent.status === "active"}
      />
    </>
  );
}
