// Agent profile edit form + lifecycle actions (suspend / resume / retire),
// shared by the owner view and the admin governance view.
//
// Optimistic locking: every update sends resource_version in the body AND
// If-Match. On resource_version_conflict the parent refetches and the form
// resets to fresh data — stale edits are never silently overwritten (doc 3.3).

import { useEffect, useState } from "react";
import { apiError } from "../api/client";
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
    toast("warn", "Someone else modified this; refreshed to the latest data");
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
      toast("ok", `Saved (v${updated.resource_version})`);
    } catch (err) {
      if (apiError(err, 409, "resource_version_conflict")) {
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
        toast("warn", "Agent retired (terminal state, cannot be recovered)");
      } else {
        const status = pending.kind === "suspend" ? "suspended" : "active";
        const updated = await updateAgent(scope, agent.agent_id, { status }, agent.resource_version);
        onChanged(updated);
        toast(
          pending.kind === "suspend" ? "warn" : "ok",
          pending.kind === "suspend"
            ? "Suspended; Credentials and pending Enrollments have been revoked"
            : "Resumed to active (old Credentials are not restored; a new Enrollment is required)",
        );
      }
      setPending(undefined);
    } catch (err) {
      if (apiError(err, 409, "resource_version_conflict")) {
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
        <h2 style={{ margin: 0 }}>Profile</h2>
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
        directory_visible (discoverable in the directory)
      </label>
      <div className="row between" style={{ marginTop: 10 }}>
        <span className="small muted">
          resource_version: <code>{agent.resource_version}</code> (sent in both body and <code>If-Match</code> on submit)
        </span>
        {!retired && canEdit && (
          <button className="btn primary sm" disabled={busy} onClick={() => void save()}>
            Save
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
            Suspend Agent
          </button>
        )}
        {!retired && canResume && agent.status === "suspended" && (
          <button className="btn sm" onClick={() => setPending({ kind: "resume", key: beginAction() })}>
            Resume to active
          </button>
        )}
        {!retired && canRetire && (
          <button
            className="btn sm danger"
            onClick={() => setPending({ kind: "retire", key: beginAction() })}
          >
            Retire (irreversible)
          </button>
        )}
        {retired && <span className="badge b-retired">retired is a terminal state and cannot be recovered</span>}
      </div>

      {pending && (
        <ConfirmDialog
          title={
            pending.kind === "suspend"
              ? "Suspend Agent"
              : pending.kind === "resume"
                ? "Resume Agent"
                : "Retire Agent"
          }
          consequences={
            pending.kind === "suspend"
              ? [
                  "Immediately revokes all Credentials and pending Enrollments of this Agent",
                  "Resuming to active does not restore old keys; a new Enrollment must be issued",
                ]
              : pending.kind === "resume"
                ? ["Old Credentials stay revoked; a new Enrollment must be issued before clients can connect"]
                : [
                    "Retire is terminal and irreversible; the Agent cannot be recovered",
                    "All Credentials and Enrollments are revoked immediately",
                  ]
          }
          confirmLabel={
            pending.kind === "suspend" ? "Confirm suspend" : pending.kind === "resume" ? "Confirm resume" : "Confirm retire"
          }
          busy={busy}
          onConfirm={() => void runAction()}
          onClose={() => setPending(undefined)}
        />
      )}
    </div>
  );
}
