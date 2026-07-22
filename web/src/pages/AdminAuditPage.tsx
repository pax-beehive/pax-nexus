import { useEffect, useMemo, useState } from "react";
import { listAdminAgents, listAuditEvents, listMembers } from "../api/queries";
import type { AgentProfile, AuditEvent, Member } from "../api/types";
import { usePagedList } from "../lib/usePagedList";
import { useErrorHandler } from "../lib/useErrorHandler";
import { formatTime } from "../lib/format";

interface LabelDirectory {
  members: Map<string, Member>;
  agents: Map<string, AgentProfile>;
}

/**
 * Non-authoritative label enrichment (doc section 5.8): audit events carry
 * only IDs, so we resolve labels from already-loaded member/agent data and
 * always keep the raw ID visible as fallback for deleted objects.
 */
function Label({ id, directory }: { id: string; directory: LabelDirectory }) {
  const member = directory.members.get(id);
  if (member) {
    return (
      <span>
        {member.email ?? member.display_name} <span className="faint small">({id})</span>
      </span>
    );
  }
  const agent = directory.agents.get(id);
  if (agent) {
    return (
      <span>
        {agent.display_name} <span className="faint small">({id})</span>
      </span>
    );
  }
  return <code>{id}</code>;
}

function actorId(event: AuditEvent): string {
  return (
    event.actor_membership_id ??
    event.actor_user_id ??
    event.actor_agent_id ??
    event.actor_credential_id ??
    "—"
  );
}

export function AdminAuditPage() {
  const handleError = useErrorHandler();
  const [actionInput, setActionInput] = useState("");
  const [action, setAction] = useState("");
  const [targetInput, setTargetInput] = useState("");
  const [targetId, setTargetId] = useState("");
  const [directory, setDirectory] = useState<LabelDirectory>({
    members: new Map(),
    agents: new Map(),
  });

  // Enrichment source data; failures just mean raw IDs are shown.
  useEffect(() => {
    Promise.all([listMembers({ limit: 100 }), listAdminAgents({ limit: 100 })])
      .then(([members, agents]) => {
        const memberMap = new Map<string, Member>();
        for (const m of members.items) {
          memberMap.set(m.membership_id, m);
          memberMap.set(m.user_id, m);
        }
        const agentMap = new Map<string, AgentProfile>(agents.items.map((a) => [a.agent_id, a]));
        setDirectory({ members: memberMap, agents: agentMap });
      })
      .catch(() => {});
  }, []);

  const list = usePagedList(
    (cursor) =>
      listAuditEvents({
        action: action || undefined,
        target_id: targetId || undefined,
        cursor,
      }),
    [action, targetId],
  );

  useEffect(() => {
    if (list.error) handleError(list.error);
  }, [list.error, handleError]);

  const knownActions = useMemo(
    () => [...new Set(list.items.map((e) => e.action))].sort(),
    [list.items],
  );

  const applyFilters = () => {
    setAction(actionInput.trim());
    setTargetId(targetInput.trim());
  };

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Audit Events</h1>
          <p className="muted" style={{ margin: 0 }}>
            immutable 审计；label enrichment 为前端非权威映射，对象删除后保留原始 ID
          </p>
        </div>
      </div>
      <div className="row wrap" style={{ marginBottom: 14, gap: 10 }}>
        <input
          type="text"
          style={{ width: 220 }}
          placeholder="action（如 agent.create）"
          value={actionInput}
          onChange={(e) => setActionInput(e.target.value)}
          list="audit-actions"
          onKeyDown={(e) => {
            if (e.key === "Enter") applyFilters();
          }}
        />
        <datalist id="audit-actions">
          {knownActions.map((a) => (
            <option key={a} value={a} />
          ))}
        </datalist>
        <input
          type="text"
          style={{ width: 220 }}
          placeholder="target_id"
          value={targetInput}
          onChange={(e) => setTargetInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") applyFilters();
          }}
        />
        <button className="btn sm" onClick={applyFilters}>
          应用过滤
        </button>
      </div>
      <div className="card">
        {list.loading ? (
          <p className="muted small">加载中…</p>
        ) : list.items.length === 0 ? (
          <p className="muted small">无匹配记录。</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>时间</th>
                <th>Actor</th>
                <th>Action</th>
                <th>Target</th>
              </tr>
            </thead>
            <tbody>
              {list.items.map((e) => (
                <tr key={e.audit_event_id}>
                  <td className="small">{formatTime(e.occurred_at)}</td>
                  <td>
                    <span className="faint small">{e.actor_kind}: </span>
                    <Label id={actorId(e)} directory={directory} />
                  </td>
                  <td className="mono small">{e.action}</td>
                  <td>
                    <span className="faint small">{e.target_kind}: </span>
                    <Label id={e.target_id} directory={directory} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      {list.nextCursor && (
        <div style={{ marginTop: 10, textAlign: "center" }}>
          <button className="btn sm" disabled={list.loadingMore} onClick={() => void list.loadMore()}>
            {list.loadingMore ? "加载中…" : "加载更多"}
          </button>
        </div>
      )}
    </>
  );
}
