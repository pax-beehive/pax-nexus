// Agent profile edit form + lifecycle actions (suspend / resume / retire),
// shared by the owner view and the admin governance view.
//
// Optimistic locking: every update sends resource_version in the body AND
// If-Match. On resource_version_conflict the parent refetches and the form
// resets to fresh data — stale edits are never silently overwritten (doc 3.3).

import { useEffect, useState } from "react";
import { ApiError } from "../api/client";
import {
  beginAction,
  retireAgent,
  updateAgent,
  type AgentScope,
} from "../api/actions";
import type { AgentProfile } from "../api/types";
import { useErrorHandler } from "../lib/useErrorHandler";
import { validateDisplayName } from "../lib/validation";
import { Badge } from "./Badge";
import { ConfirmDialog } from "./ConfirmDialog";
import { useToast } from "./Toasts";

type PendingAction = { kind: "suspend" | "resume" | "retire"; key: string };

export function AgentGovernanceCard({
  scope,
  agent,
  canEdit,
  canSuspend,
  canResume,
  canRetire,
  onChanged,
  refetch,
}: {
  scope: AgentScope;
  agent: AgentProfile;
  canEdit: boolean;
  /** Owner and Admin may suspend any agent. */
  canSuspend: boolean;
  /** Resume is Owner-only on foreign agents (doc section 2.2). */
  canResume: boolean;
  canRetire: boolean;
  onChanged: (agent: AgentProfile) => void;
  refetch: () => Promise<AgentProfile>;
}) {
  const toast = useToast();
  const handleError = useErrorHandler();
  const retired = agent.status === "retired";

  const [name, setName] = useState(agent.display_name);
  const [type, setType] = useState(agent.agent_type);
  const [description, setDescription] = useState(agent.description);
  const [visible, setVisible] = useState(agent.directory_visible);
  const [busy, setBusy] = useState(false);
  const [pending, setPending] = useState<PendingAction | undefined>();

  // Reset the draft whenever the authoritative agent changes (load, save,
  // or 409 refetch).
  useEffect(() => {
    setName(agent.display_name);
    setType(agent.agent_type);
    setDescription(agent.description);
    setVisible(agent.directory_visible);
  }, [agent]);

  const onConflict = async () => {
    const fresh = await refetch();
    onChanged(fresh);
    toast("warn", "已被他人修改，已刷新最新数据");
  };

  const save = async () => {
    const nameError = validateDisplayName(name);
    if (nameError) return toast("warn", nameError);
    setBusy(true);
    try {
      const updated = await updateAgent(
        scope,
        agent.agent_id,
        {
          display_name: name.trim(),
          description,
          agent_type: type.trim(),
          directory_visible: visible,
        },
        agent.resource_version,
      );
      onChanged(updated);
      toast("ok", `已保存（v${updated.resource_version}）`);
    } catch (err) {
      if (
        err instanceof ApiError &&
        err.status === 409 &&
        err.code === "resource_version_conflict"
      ) {
        await onConflict();
      } else {
        handleError(err);
      }
    } finally {
      setBusy(false);
    }
  };

  const runAction = async () => {
    if (!pending || busy) return;
    setBusy(true);
    try {
      if (pending.kind === "retire") {
        const updated = await retireAgent(
          scope,
          agent.agent_id,
          agent.resource_version,
          pending.key,
        );
        onChanged(updated);
        toast("warn", "Agent 已 retire（终态，不可恢复）");
      } else {
        const status = pending.kind === "suspend" ? "suspended" : "active";
        const updated = await updateAgent(scope, agent.agent_id, { status }, agent.resource_version);
        onChanged(updated);
        toast(
          pending.kind === "suspend" ? "warn" : "ok",
          pending.kind === "suspend"
            ? "已暂停，Credential 与 pending Enrollment 已吊销"
            : "已恢复 active（旧 Credential 不会还原，需要新 Enrollment）",
        );
      }
      setPending(undefined);
    } catch (err) {
      if (
        err instanceof ApiError &&
        err.status === 409 &&
        err.code === "resource_version_conflict"
      ) {
        setPending(undefined);
        await onConflict();
      } else {
        handleError(err);
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="card">
      <div className="row between">
        <h2 style={{ margin: 0 }}>资料</h2>
        <Badge status={agent.status} />
      </div>
      <div className="field-row">
        <div>
          <label htmlFor="ed-name">display_name</label>
          <input
            id="ed-name"
            type="text"
            value={name}
            disabled={retired || !canEdit}
            onChange={(e) => setName(e.target.value)}
          />
        </div>
        <div>
          <label htmlFor="ed-type">agent_type</label>
          <input
            id="ed-type"
            type="text"
            value={type}
            disabled={retired || !canEdit}
            onChange={(e) => setType(e.target.value)}
          />
        </div>
      </div>
      <label htmlFor="ed-desc">description</label>
      <textarea
        id="ed-desc"
        rows={2}
        value={description}
        disabled={retired || !canEdit}
        onChange={(e) => setDescription(e.target.value)}
      />
      <label className="ck">
        <input
          type="checkbox"
          checked={visible}
          disabled={retired || !canEdit}
          onChange={(e) => setVisible(e.target.checked)}
        />
        directory_visible（可被目录发现）
      </label>
      <div className="row between" style={{ marginTop: 10 }}>
        <span className="small muted">
          resource_version: <code>{agent.resource_version}</code>（提交时 body + <code>If-Match</code> 双发）
        </span>
        {!retired && canEdit && (
          <button className="btn primary sm" disabled={busy} onClick={() => void save()}>
            保存
          </button>
        )}
      </div>
      <hr className="divider" />
      <div className="row wrap">
        {!retired && canSuspend && agent.status === "active" && (
          <button
            className="btn sm danger"
            onClick={() => setPending({ kind: "suspend", key: beginAction() })}
          >
            暂停 Agent
          </button>
        )}
        {!retired && canResume && agent.status === "suspended" && (
          <button className="btn sm" onClick={() => setPending({ kind: "resume", key: beginAction() })}>
            恢复 active
          </button>
        )}
        {!retired && canRetire && (
          <button
            className="btn sm danger"
            onClick={() => setPending({ kind: "retire", key: beginAction() })}
          >
            Retire（不可逆）
          </button>
        )}
        {retired && <span className="badge b-retired">retired 为终态，不可恢复</span>}
      </div>

      {pending && (
        <ConfirmDialog
          title={
            pending.kind === "suspend"
              ? "暂停 Agent"
              : pending.kind === "resume"
                ? "恢复 Agent"
                : "Retire Agent"
          }
          consequences={
            pending.kind === "suspend"
              ? [
                  "立即吊销该 Agent 的全部 Credential 和 pending Enrollment",
                  "恢复 active 不会还原旧 key，必须重新签发 Enrollment",
                ]
              : pending.kind === "resume"
                ? ["旧 Credential 保持 revoked，需要重新签发 Enrollment 才能接入客户端"]
                : [
                    "Retire 是终态、不可逆，Agent 不可恢复",
                    "全部 Credential 与 Enrollment 立即吊销",
                  ]
          }
          confirmLabel={
            pending.kind === "suspend" ? "确认暂停" : pending.kind === "resume" ? "确认恢复" : "确认 Retire"
          }
          busy={busy}
          onConfirm={() => void runAction()}
          onClose={() => setPending(undefined)}
        />
      )}
    </div>
  );
}
