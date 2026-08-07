import { useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { listMyAgents } from "../../api/queries";
import { useAuth } from "../../auth/AuthContext";
import { currentTeam } from "../../lib/teams";
import { usePagedList } from "../../lib/usePagedList";
import { useErrorHandler } from "../../lib/useErrorHandler";
import { Badge, ProvisionedByBadge } from "../../components/Badge";
import { Button } from "../../components/Button";
import { Seg } from "../../components/Seg";
import { CreateAgentModal } from "./CreateAgentModal";
import { PageHeader } from "../../components/PageHeader";

const STATUS_FILTERS = ["all", "active", "suspended", "retired"] as const;
type StatusFilter = (typeof STATUS_FILTERS)[number];

/**
 * Management 根节点的 member 分叉：本人 Agent 列表 + 创建入口。owner/admin
 * 的对应入口在访问树「人员层点自己 → 本人机器层」（AccessTreePage 里的
 * AdminAccessTree），"+ Create Agent" 在全站只有这两处。
 */
export function MyAgentsLevel() {
  const navigate = useNavigate();
  const handleError = useErrorHandler();
  const { state } = useAuth();
  // SaaS profile: the page (and every other scoped view) follows the
  // session's current team (design/m3-teams 04).
  const teamName = state.kind === "active" ? currentTeam(state.me)?.name : undefined;
  const [filter, setFilter] = useState<StatusFilter>("all");
  const [createOpen, setCreateOpen] = useState(false);
  const list = usePagedList(
    (cursor) => listMyAgents({ status: filter === "all" ? undefined : filter, cursor }),
    [filter],
  );

  useEffect(() => {
    if (list.error) handleError(list.error);
  }, [list.error, handleError]);

  return (
    <>
      <PageHeader
        title="My Agents"
        lede={
          teamName ? `${teamName} team scope` : "Register and manage the Agent identities you own"
        }
        actions={
          <Button variant="primary" onClick={() => setCreateOpen(true)}>
            + Create Agent
          </Button>
        }
      />
      <Seg
        label="agent status"
        options={STATUS_FILTERS.map((s) => ({ value: s, label: s }))}
        value={filter}
        onChange={setFilter}
      />
      {list.loading ? (
        <p className="muted">Loading…</p>
      ) : list.items.length === 0 ? (
        <div className="card flat muted">No agents yet — click + Create Agent to get started.</div>
      ) : (
        list.items.map((a) => (
          <Link
            key={a.agent_id}
            to={`/management/agents/${encodeURIComponent(a.agent_id)}`}
            className="at-row at-agents"
          >
            <span className="at-row-name">{a.display_name}</span>
            <span className="small mono faint">{a.agent_id}</span>
            <span className="small">{a.agent_type}</span>
            <span className="row">
              <ProvisionedByBadge agent={a} />
              <Badge status={a.status} />
            </span>
            <span className="at-row-go" aria-hidden="true">
              →
            </span>
          </Link>
        ))
      )}
      {list.nextCursor && (
        <div className="load-more">
          <Button size="sm" disabled={list.loadingMore} onClick={() => void list.loadMore()}>
            {list.loadingMore ? "Loading…" : "Load more"}
          </Button>
        </div>
      )}
      {createOpen && (
        <CreateAgentModal
          onClose={() => setCreateOpen(false)}
          onCreated={(agent) => {
            setCreateOpen(false);
            navigate(`/management/agents/${encodeURIComponent(agent.agent_id)}`);
          }}
        />
      )}
    </>
  );
}
