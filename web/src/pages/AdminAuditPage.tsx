import { Fragment, useEffect, useMemo, useState, type ReactNode } from "react";
import { getAuditEvent, listAdminAgents, listAuditEvents, listMembers } from "../api/queries";
import type { AgentProfile, AuditEvent, Member } from "../api/types";
import { usePagedList } from "../lib/usePagedList";
import { useErrorHandler } from "../lib/useErrorHandler";
import { formatTime } from "../lib/format";

// Fixed kind vocabularies from the backend audit schema (migration 017).
const ACTOR_KINDS = ["bootstrap", "human", "agent", "system"] as const;
const TARGET_KINDS = ["membership", "invitation", "agent", "enrollment", "credential"] as const;

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

function DetailField({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <span className="faint">{label}: </span>
      {children}
    </div>
  );
}

export function AdminAuditPage() {
  const handleError = useErrorHandler();
  const [actionInput, setActionInput] = useState("");
  const [action, setAction] = useState("");
  const [targetInput, setTargetInput] = useState("");
  const [targetId, setTargetId] = useState("");
  const [actorKind, setActorKind] = useState("");
  const [targetKind, setTargetKind] = useState("");
  const [expandedId, setExpandedId] = useState<number | null>(null);
  const [detail, setDetail] = useState<AuditEvent | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
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
        actor_kind: actorKind || undefined,
        action: action || undefined,
        target_kind: targetKind || undefined,
        target_id: targetId || undefined,
        cursor,
      }),
    [actorKind, action, targetKind, targetId],
  );

  useEffect(() => {
    if (list.error) handleError(list.error);
  }, [list.error, handleError]);

  // Detail endpoint (doc section 6.2): fetched on demand for the expanded row.
  useEffect(() => {
    if (expandedId === null) {
      setDetail(null);
      return;
    }
    let cancelled = false;
    setDetail(null);
    setDetailLoading(true);
    getAuditEvent(expandedId)
      .then((event) => {
        if (!cancelled) setDetail(event);
      })
      .catch((err: unknown) => {
        if (!cancelled) handleError(err);
      })
      .finally(() => {
        if (!cancelled) setDetailLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [expandedId, handleError]);

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
        <select
          aria-label="actor_kind 过滤"
          value={actorKind}
          onChange={(e) => setActorKind(e.target.value)}
        >
          <option value="">actor_kind: 全部</option>
          {ACTOR_KINDS.map((k) => (
            <option key={k} value={k}>
              {k}
            </option>
          ))}
        </select>
        <select
          aria-label="target_kind 过滤"
          value={targetKind}
          onChange={(e) => setTargetKind(e.target.value)}
        >
          <option value="">target_kind: 全部</option>
          {TARGET_KINDS.map((k) => (
            <option key={k} value={k}>
              {k}
            </option>
          ))}
        </select>
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
                <th></th>
              </tr>
            </thead>
            <tbody>
              {list.items.map((e) => (
                <Fragment key={e.audit_event_id}>
                  <tr>
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
                    <td>
                      <button
                        className="btn sm"
                        onClick={() =>
                          setExpandedId(expandedId === e.audit_event_id ? null : e.audit_event_id)
                        }
                      >
                        {expandedId === e.audit_event_id ? "收起" : "详情"}
                      </button>
                    </td>
                  </tr>
                  {expandedId === e.audit_event_id && (
                    <tr>
                      <td colSpan={5}>
                        {detailLoading ? (
                          <p className="muted small">加载中…</p>
                        ) : detail ? (
                          <div className="small" style={{ display: "grid", gap: 4, padding: "4px 0" }}>
                            <DetailField label="audit_event_id">
                              <code>{detail.audit_event_id}</code>
                            </DetailField>
                            <DetailField label="occurred_at">
                              {formatTime(detail.occurred_at)}
                            </DetailField>
                            <DetailField label="action">
                              <span className="mono">{detail.action}</span>
                            </DetailField>
                            <DetailField label="actor_kind">{detail.actor_kind}</DetailField>
                            {detail.actor_user_id && (
                              <DetailField label="actor_user_id">
                                <Label id={detail.actor_user_id} directory={directory} />
                              </DetailField>
                            )}
                            {detail.actor_membership_id && (
                              <DetailField label="actor_membership_id">
                                <Label id={detail.actor_membership_id} directory={directory} />
                              </DetailField>
                            )}
                            {detail.actor_agent_id && (
                              <DetailField label="actor_agent_id">
                                <Label id={detail.actor_agent_id} directory={directory} />
                              </DetailField>
                            )}
                            {detail.actor_credential_id && (
                              <DetailField label="actor_credential_id">
                                <Label id={detail.actor_credential_id} directory={directory} />
                              </DetailField>
                            )}
                            <DetailField label="target_kind">{detail.target_kind}</DetailField>
                            <DetailField label="target_id">
                              <Label id={detail.target_id} directory={directory} />
                            </DetailField>
                          </div>
                        ) : null}
                      </td>
                    </tr>
                  )}
                </Fragment>
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
