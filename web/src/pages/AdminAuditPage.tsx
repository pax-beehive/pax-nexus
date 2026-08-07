import { useEffect, useMemo, useState } from "react";
import { listAdminAgents, listAuditEvents, listMembers } from "../api/queries";
import type { AgentProfile, Member } from "../api/types";
import { usePagedList } from "../lib/usePagedList";
import { useErrorHandler } from "../lib/useErrorHandler";
import { Button } from "../components/Button";
import { PageHeader } from "../components/PageHeader";
import { Seg } from "../components/Seg";
import { AuditRow, type LabelDirectory } from "./governance/AuditRow";

// Fixed kind vocabulary from the backend audit schema (migration 017).
const TARGET_KINDS = ["membership", "invitation", "agent", "enrollment", "credential"] as const;

// The backend's actor_kind carries four values — bootstrap / human / agent /
// system — but bootstrap fires exactly once, at first install, so design
// phase 5 §2.1 folds it into "系统" rather than giving it its own Seg slot.
type ActorKindFilter = "" | "human" | "agent" | "system";
const ACTOR_KIND_OPTIONS: { value: ActorKindFilter; label: string }[] = [
  { value: "", label: "全部" },
  { value: "human", label: "人" },
  { value: "agent", label: "Agent" },
  { value: "system", label: "系统" },
];

export function AdminAuditPage() {
  const handleError = useErrorHandler();
  const [actionInput, setActionInput] = useState("");
  const [action, setAction] = useState("");
  const [targetInput, setTargetInput] = useState("");
  const [targetId, setTargetId] = useState("");
  const [actorKind, setActorKind] = useState<ActorKindFilter>("");
  const [targetKind, setTargetKind] = useState("");
  const [expandedId, setExpandedId] = useState<number | null>(null);
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
      <PageHeader
        variant="bleed"
        kicker="Governance · 审计流水"
        title="发生过的一切，未经编辑"
        lede={
          <p className="lede-dim">
            只追加。名字是我们替你查出来的方便——原始标识符留在行上，
            所以人、Agent 或机器消失之后，条目仍然读得通。
          </p>
        }
      />
      <div className="toolbar" style={{ marginBottom: 14, padding: "0 var(--space-4)" }}>
        <Seg
          label="Filter by actor_kind"
          options={ACTOR_KIND_OPTIONS}
          value={actorKind}
          onChange={setActorKind}
        />
        <select
          aria-label="Filter by target_kind"
          value={targetKind}
          onChange={(e) => setTargetKind(e.target.value)}
        >
          <option value="">target_kind: all</option>
          {TARGET_KINDS.map((k) => (
            <option key={k} value={k}>
              {k}
            </option>
          ))}
        </select>
        <input
          type="text"
          placeholder="action (e.g. agent.create)"
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
          placeholder="target_id"
          value={targetInput}
          onChange={(e) => setTargetInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") applyFilters();
          }}
        />
        <Button size="sm" onClick={applyFilters}>
          Apply filters
        </Button>
      </div>
      <div className="card">
        {list.loading ? (
          <p className="muted small">Loading…</p>
        ) : list.error && list.items.length === 0 ? (
          <div className="note bad row between" role="alert">
            <span>Failed to load the list.</span>
            <Button size="sm" onClick={list.reload}>
              Retry
            </Button>
          </div>
        ) : list.items.length === 0 ? (
          <p className="muted small">No matching audit events.</p>
        ) : (
          <>
            {list.items.map((e) => (
              <AuditRow
                key={e.audit_event_id}
                event={e}
                directory={directory}
                expanded={expandedId === e.audit_event_id}
                onToggle={() =>
                  setExpandedId(expandedId === e.audit_event_id ? null : e.audit_event_id)
                }
              />
            ))}
            {list.error ? (
              <div className="note bad row between" role="alert">
                <span>Failed to load more.</span>
                <Button size="sm" onClick={() => void list.loadMore()}>
                  Retry
                </Button>
              </div>
            ) : null}
          </>
        )}
        {list.nextCursor && !(list.error && list.items.length > 0) ? (
          <div className="load-more">
            <Button size="sm" disabled={list.loadingMore} onClick={() => void list.loadMore()}>
              {list.loadingMore ? "Loading…" : "Load more"}
            </Button>
          </div>
        ) : null}
      </div>
    </>
  );
}
