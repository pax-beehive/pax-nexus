// Agent 详情页：单一元素，按「这个 Agent 是不是你的」自适应 scope（阶段 4，
// 取代旧的 AgentDetailPage/AdminAgentDetailPage 两页 + AgentDetailView 共享
// 布局，见 spec 5.6-5.7）。
//
// 外层只做三态分派：加载中 / 404 / 就绪。就绪态整块交给 LoadedAgent——
// useAgentKeys 需要 access.actScope，而 access 要等 agent 到手才算得出，
// 不能把这个 hook 放在 `if (!agent) return` 之后（那违反 hooks 规则），
// 所以拆成两层组件，把所有依赖 access 的 hook 都留在只在 agent 就绪后才
// 挂载的 LoadedAgent 里。

import { useMemo } from "react";
import { Link, useParams } from "react-router-dom";
import { getAdminAgent, getOwnAgent } from "../api/queries";
import type { AgentProfile, HumanMe } from "../api/types";
import { can } from "../lib/capabilities";
import { useAgentDetail } from "../lib/useAgentDetail";
import { AgentBehaviourCard } from "./agent/AgentBehaviourCard";
import { AgentHeader } from "./agent/AgentHeader";
import { AgentIdentityCard } from "./agent/AgentIdentityCard";
import { AgentKeysSection } from "./agent/AgentKeysSection";
import { AgentLifecycleCard } from "./agent/AgentLifecycleCard";
import { readScopeFor, resolveAgentAccess } from "./agent/agentScope";
import { useAgentKeys } from "./agent/useAgentKeys";

/** 就绪态整页：所有依赖 access 的 hook 全在这里，只在 agent 到手后挂载。 */
function LoadedAgent({
  agent,
  me,
  onChanged,
  refetch,
}: {
  agent: AgentProfile;
  me: HumanMe;
  onChanged: (agent: AgentProfile) => void;
  refetch: () => Promise<AgentProfile>;
}) {
  const access = resolveAgentAccess(me, agent);
  const keys = useAgentKeys(access.actScope, agent.agent_id);

  return (
    <>
      <AgentHeader agent={agent} access={access} me={me} />
      <div className="split">
        <div className="stack">
          <AgentIdentityCard agent={agent} access={access} onChanged={onChanged} refetch={refetch} />
          <AgentLifecycleCard
            agent={agent}
            access={access}
            pendingEnrollments={keys.enrollments.items}
            activeCredentials={keys.credentials.items}
            onChanged={onChanged}
            refetch={refetch}
          />
        </div>
        <div className="stack">
          <AgentKeysSection agent={agent} access={access} keys={keys} />
          {can(me.role, "view.audit") && <AgentBehaviourCard agentId={agent.agent_id} />}
        </div>
      </div>
    </>
  );
}

export function AgentDetailPage({ me }: { me: HumanMe }) {
  const { agentId = "" } = useParams();
  // 取详情用的 scope 只看「查看者能不能看全体」，不看这个 Agent 是不是自己
  // 的——那要等 agent 到手才知道（resolveAgentAccess，在 LoadedAgent 里）。
  const readScope = readScopeFor(me);
  // fetcher 必须 memo：useAgentDetail 把它放进 effect deps。
  const fetcher = useMemo(
    () => (readScope === "admin" ? getAdminAgent : getOwnAgent),
    [readScope],
  );
  const { agent, setAgent, notFound, refetch } = useAgentDetail(fetcher, agentId);

  if (notFound) {
    return (
      <div className="card">
        <h2>没找到</h2>
        <p className="muted">
          这个 Agent 不存在，或者你看不到它。<Link to="/management">回到访问树</Link>
        </p>
      </div>
    );
  }

  // 不用字面量 "Loading…"：renderApp 测试助手等的就是全局 boot 占位符那个
  // 字符串消失，同名会让深链测试挂死。
  if (!agent) return <p className="muted">正在打开这个 Agent…</p>;

  return <LoadedAgent agent={agent} me={me} onChanged={setAgent} refetch={refetch} />;
}
