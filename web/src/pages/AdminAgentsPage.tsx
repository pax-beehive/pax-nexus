import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { ApiError } from "../api/client";
import { transferAgent, updateAgent } from "../api/actions";
import { getAdminAgent, listAdminAgents, listAllMembers } from "../api/queries";
import type { AgentProfile, HumanMe, Member } from "../api/types";
import { can } from "../lib/capabilities";
import { usePagedList } from "../lib/usePagedList";
import { useErrorHandler } from "../lib/useErrorHandler";
import { Badge } from "../components/Badge";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { Modal } from "../components/Modal";
import { useToast } from "../components/Toasts";

const STATUS_FILTERS = ["all", "active", "suspended", "retired"] as const;

type PendingGovernance =
  | { kind: "suspend"; agent: AgentProfile }
  | { kind: "resume"; agent: AgentProfile }
  | { kind: "transfer"; agent: AgentProfile };

function TransferModal({
  agent,
  members,
  onClose,
  onDone,
}: {
  agent: AgentProfile;
  members: Member[];
  onClose: () => void;
  onDone: (agent: AgentProfile) => void;
}) {
  const toast = useToast();
  const handleError = useErrorHandler();
  const targets = members.filter(
    (m) => m.status === "active" && m.membership_id !== agent.owner_membership_id,
  );
  const [target, setTarget] = useState(targets[0]?.membership_id ?? "");
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    if (!target || busy) return;
    setBusy(true);
    try {
      const updated = await transferAgent(agent.agent_id, target, agent.resource_version);
      toast("ok", "已转移；旧 Credential 与 Enrollment 已吊销");
      onDone(updated);
    } catch (err) {
      if (err instanceof ApiError && err.code === "resource_version_conflict") {
        toast("warn", "数据已被他人修改，请刷新后重试");
        onClose();
      } else {
        handleError(err);
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal title={`转移 ${agent.display_name}`} onClose={onClose}>
      <label htmlFor="tf-target">目标 Owner（仅 active Membership）</label>
      {targets.length === 0 ? (
        <div className="note warn">没有可转移的 active Membership。</div>
      ) : (
        <select id="tf-target" value={target} onChange={(e) => setTarget(e.target.value)}>
          {targets.map((m) => (
            <option key={m.membership_id} value={m.membership_id}>
              {m.email ?? m.membership_id}
            </option>
          ))}
        </select>
      )}
      <div className="note warn small">
        Transfer 会吊销旧 Owner 下的全部 Credential 与 pending Enrollment；新 Owner 必须重新签发 Enrollment。
      </div>
      <div className="row" style={{ justifyContent: "flex-end" }}>
        <button className="btn ghost" onClick={onClose} disabled={busy}>
          取消
        </button>
        <button className="btn danger" disabled={!target || busy} onClick={() => void submit()}>
          {busy ? "转移中…" : "确认转移"}
        </button>
      </div>
    </Modal>
  );
}

export function AdminAgentsPage({ me }: { me: HumanMe }) {
  const toast = useToast();
  const handleError = useErrorHandler();
  const [status, setStatus] = useState<string>("all");
  const [q, setQ] = useState("");
  const [qInput, setQInput] = useState("");
  const [ownerFilter, setOwnerFilter] = useState("");
  const [members, setMembers] = useState<Member[]>([]);
  const [pending, setPending] = useState<PendingGovernance | undefined>();
  const [busy, setBusy] = useState(false);

  // Load every member page for owner labels, transfer targets and filtering.
  // The raw membership ID remains a fallback while this best-effort
  // enrichment is loading.
  useEffect(() => {
    let cancelled = false;
    listAllMembers({ limit: 100 })
      .then((loaded) => {
        if (!cancelled) setMembers(loaded);
      })
      .catch((err: unknown) => {
        if (!cancelled) handleError(err);
      });
    return () => {
      cancelled = true;
    };
  }, [handleError]);

  const list = usePagedList(
    (cursor) =>
      listAdminAgents({
        status: status === "all" ? undefined : status,
        q: q || undefined,
        owner_membership_id: ownerFilter || undefined,
        cursor,
      }),
    [status, q, ownerFilter],
  );

  useEffect(() => {
    if (list.error) handleError(list.error);
  }, [list.error, handleError]);

  const memberLabel = useMemo(() => {
    const map = new Map(members.map((m) => [m.membership_id, m.email ?? m.membership_id]));
    return (id: string | undefined) => (id ? (map.get(id) ?? id) : "—");
  }, [members]);

  const actorRole = me.role ?? "member";
  const maySuspend = can(actorRole, "suspend.any-agent");
  const mayGovern = can(actorRole, "govern.any-agent");

  const runGovernance = async () => {
    if (!pending || pending.kind === "transfer" || busy) return;
    setBusy(true);
    const { agent } = pending;
    const nextStatus = pending.kind === "suspend" ? "suspended" : "active";
    try {
      const updated = await updateAgent("admin", agent.agent_id, { status: nextStatus }, agent.resource_version);
      toast(
        pending.kind === "suspend" ? "warn" : "ok",
        pending.kind === "suspend"
          ? "已暂停并级联吊销 Credential 与 pending Enrollment"
          : "已恢复 active（旧 Credential 不会还原，需要新 Enrollment）",
      );
      setPending(undefined);
      list.reload();
      void updated;
    } catch (err) {
      if (err instanceof ApiError && err.code === "resource_version_conflict") {
        await getAdminAgent(agent.agent_id).catch(() => undefined);
        list.reload();
        toast("warn", "已被他人修改，已刷新最新数据");
        setPending(undefined);
      } else {
        handleError(err);
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <div className="page-head">
        <div>
          <h1>All Agents</h1>
          <p className="muted" style={{ margin: 0 }}>
            Admin 只能暂停；编辑、恢复、retire、转移仅 Owner
          </p>
        </div>
      </div>
      <div className="row wrap" style={{ marginBottom: 14, gap: 10 }}>
        <input
          type="text"
          style={{ width: 240 }}
          placeholder="按名称或 ID 搜索（q）"
          value={qInput}
          onChange={(e) => setQInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") setQ(qInput.trim());
          }}
        />
        <button className="btn sm" onClick={() => setQ(qInput.trim())}>
          搜索
        </button>
        <select
          style={{ width: 220 }}
          aria-label="Owner 过滤"
          value={ownerFilter}
          onChange={(e) => setOwnerFilter(e.target.value)}
        >
          <option value="">全部 Owner</option>
          {members.map((m) => (
            <option key={m.membership_id} value={m.membership_id}>
              {m.email ?? m.membership_id}
            </option>
          ))}
        </select>
      </div>
      <div className="tabs" role="group" aria-label="agent status">
        {STATUS_FILTERS.map((s) => (
          <button
            key={s}
            className={s === status ? "on" : ""}
            aria-pressed={s === status}
            onClick={() => setStatus(s)}
          >
            {s}
          </button>
        ))}
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
                <th>Agent</th>
                <th>Owner</th>
                <th>Status</th>
                <th>治理操作</th>
              </tr>
            </thead>
            <tbody>
              {list.items.map((a) => {
                const retired = a.status === "retired";
                return (
                  <tr key={a.agent_id}>
                    <td>
                      <Link to={`/admin/agents/${encodeURIComponent(a.agent_id)}`}>{a.display_name}</Link>
                      <div className="small mono faint">{a.agent_id}</div>
                    </td>
                    <td className="small">{memberLabel(a.owner_membership_id)}</td>
                    <td>
                      <Badge status={a.status} />
                    </td>
                    <td>
                      {retired ? (
                        <span className="faint small">终态</span>
                      ) : (
                        <span className="row wrap">
                          {a.status === "active" && maySuspend && (
                            <button
                              className="btn sm danger"
                              onClick={() => setPending({ kind: "suspend", agent: a })}
                            >
                              暂停
                            </button>
                          )}
                          {a.status === "suspended" && mayGovern && (
                            <button className="btn sm" onClick={() => setPending({ kind: "resume", agent: a })}>
                              恢复
                            </button>
                          )}
                          {mayGovern && (
                            <button className="btn sm" onClick={() => setPending({ kind: "transfer", agent: a })}>
                              转移
                            </button>
                          )}
                        </span>
                      )}
                    </td>
                  </tr>
                );
              })}
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

      {pending?.kind === "transfer" && (
        <TransferModal
          agent={pending.agent}
          members={members}
          onClose={() => setPending(undefined)}
          onDone={() => {
            setPending(undefined);
            list.reload();
          }}
        />
      )}
      {pending && pending.kind !== "transfer" && (
        <ConfirmDialog
          title={pending.kind === "suspend" ? "暂停 Agent" : "恢复 Agent"}
          consequences={
            pending.kind === "suspend"
              ? [
                  `立即吊销 ${pending.agent.display_name} 的全部 Credential 和 pending Enrollment`,
                  "恢复 active 不会还原旧 key，必须重新签发 Enrollment",
                ]
              : ["旧 Credential 保持 revoked，Owner 需要重新签发 Enrollment 才能接入客户端"]
          }
          confirmLabel={pending.kind === "suspend" ? "确认暂停" : "确认恢复"}
          busy={busy}
          onConfirm={() => void runGovernance()}
          onClose={() => setPending(undefined)}
        />
      )}
    </>
  );
}
