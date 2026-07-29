import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { apiError } from "../api/client";
import { beginAction, createAgent } from "../api/actions";
import { listMyAgents } from "../api/queries";
import type { AgentProfile } from "../api/types";
import { usePagedList } from "../lib/usePagedList";
import { useErrorHandler } from "../lib/useErrorHandler";
import { validateAgentId, validateDisplayName } from "../lib/validation";
import { Badge, ProvisionedByBadge } from "../components/Badge";
import { Modal } from "../components/Modal";
import { useToast } from "../components/Toasts";

const STATUS_FILTERS = ["all", "active", "suspended", "retired"] as const;

function CreateAgentModal({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (agent: AgentProfile) => void;
}) {
  const toast = useToast();
  const handleError = useErrorHandler();
  const [agentId, setAgentId] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [description, setDescription] = useState("");
  const [agentType, setAgentType] = useState("codex");
  const [visible, setVisible] = useState(true);
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | undefined>();
  // One Idempotency-Key per modal instance (= one user action). A network
  // retry reuses it; reopening the modal generates a fresh key (doc 3.3).
  const actionKeyRef = useRef(beginAction());

  const submit = async () => {
    const idError = validateAgentId(agentId.trim());
    if (idError) return setFormError(idError);
    const nameError = validateDisplayName(displayName);
    if (nameError) return setFormError(nameError);
    setFormError(undefined);
    setBusy(true);
    try {
      const agent = await createAgent(
        {
          agent_id: agentId.trim(),
          display_name: displayName.trim(),
          description,
          agent_type: agentType,
          // Always send the user's explicit choice; never rely on the
          // server default (doc 5.4).
          directory_visible: visible,
        },
        actionKeyRef.current,
      );
      toast("ok", "Agent created");
      onCreated(agent);
    } catch (err) {
      if (apiError(err, undefined, "agent_id_conflict")) {
        setFormError("agent_id already exists; choose a different ID");
      } else if (apiError(err, undefined, "idempotency_conflict")) {
        setFormError("This action's Idempotency-Key was already used for a different request; close the dialog and start over");
      } else {
        handleError(err);
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal title="Create Agent" onClose={onClose}>
      <label htmlFor="ca-id">agent_id (immutable after creation, globally unique)</label>
      <input
        id="ca-id"
        type="text"
        placeholder="alice-codex"
        maxLength={128}
        value={agentId}
        onChange={(e) => setAgentId(e.target.value)}
      />
      <label htmlFor="ca-name">display_name</label>
      <input
        id="ca-name"
        type="text"
        placeholder="Alice Codex"
        maxLength={200}
        value={displayName}
        onChange={(e) => setDisplayName(e.target.value)}
      />
      <label htmlFor="ca-desc">description</label>
      <textarea id="ca-desc" rows={2} value={description} onChange={(e) => setDescription(e.target.value)} />
      <div className="field-row">
        <div>
          <label htmlFor="ca-type">agent_type</label>
          <select id="ca-type" value={agentType} onChange={(e) => setAgentType(e.target.value)}>
            <option value="codex">codex</option>
            <option value="claude">claude</option>
            <option value="custom">custom</option>
          </select>
        </div>
        <div>
          <label>&nbsp;</label>
          <label className="ck">
            <input type="checkbox" checked={visible} onChange={(e) => setVisible(e.target.checked)} />
            directory_visible (discoverable in the directory)
          </label>
        </div>
      </div>
      {formError && <div className="note bad">{formError}</div>}
      <div className="note small">
        Idempotency-Key for this action: <code>{actionKeyRef.current.slice(0, 13)}…</code>.
        Network retries reuse the same key; reopening the form generates a new key.
      </div>
      <div className="row" style={{ justifyContent: "flex-end" }}>
        <button className="btn ghost" onClick={onClose} disabled={busy}>
          Cancel
        </button>
        <button className="btn primary" disabled={busy} onClick={() => void submit()}>
          {busy ? "Creating…" : "Create"}
        </button>
      </div>
    </Modal>
  );
}

export function MyAgentsPage() {
  const navigate = useNavigate();
  const handleError = useErrorHandler();
  const [filter, setFilter] = useState<string>("all");
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
      <div className="page-head">
        <div>
          <h1>My Agents</h1>
          <p className="muted" style={{ margin: 0 }}>
            Register and manage the Agent identities you own
          </p>
        </div>
        <button className="btn primary" onClick={() => setCreateOpen(true)}>
          + Create Agent
        </button>
      </div>
      <div className="tabs" role="group" aria-label="agent status">
        {STATUS_FILTERS.map((s) => (
          <button
            key={s}
            className={s === filter ? "on" : ""}
            aria-pressed={s === filter}
            onClick={() => setFilter(s)}
          >
            {s}
          </button>
        ))}
      </div>
      {list.loading ? (
        <p className="muted">Loading…</p>
      ) : list.items.length === 0 ? (
        <div className="card flat muted">No agents yet — click + Create Agent to get started.</div>
      ) : (
        <div className="grid">
          {list.items.map((a) => (
            <div key={a.agent_id} className="card agent-card" onClick={() => navigate(`/agents/${encodeURIComponent(a.agent_id)}`)}>
              <div className="row between">
                <strong>{a.display_name}</strong>
                <span className="row">
                  <ProvisionedByBadge agent={a} />
                  <Badge status={a.status} />
                </span>
              </div>
              <div className="small mono muted" style={{ margin: "6px 0" }}>
                {a.agent_id}
              </div>
              <div className="small muted">{a.description}</div>
              <div className="small faint" style={{ marginTop: 8 }}>
                {a.agent_type} · {a.directory_visible ? "directory visible" : "directory hidden"} · v{a.resource_version}
              </div>
            </div>
          ))}
        </div>
      )}
      {list.nextCursor && (
        <div style={{ marginTop: 10, textAlign: "center" }}>
          <button className="btn sm" disabled={list.loadingMore} onClick={() => void list.loadMore()}>
            {list.loadingMore ? "Loading…" : "Load more"}
          </button>
        </div>
      )}
      {createOpen && (
        <CreateAgentModal
          onClose={() => setCreateOpen(false)}
          onCreated={(agent) => {
            setCreateOpen(false);
            navigate(`/agents/${encodeURIComponent(agent.agent_id)}`);
          }}
        />
      )}
    </>
  );
}
