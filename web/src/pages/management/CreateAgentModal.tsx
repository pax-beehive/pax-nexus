import { useRef, useState } from "react";
import { apiError } from "../../api/client";
import { beginAction, createAgent } from "../../api/actions";
import type { AgentProfile } from "../../api/types";
import { useErrorHandler } from "../../lib/useErrorHandler";
import { validateAgentId, validateDisplayName } from "../../lib/validation";
import { Button } from "../../components/Button";
import { Modal } from "../../components/Modal";
import { useToast } from "../../components/Toasts";

export function CreateAgentModal({
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
        <Button variant="ghost" onClick={onClose} disabled={busy}>
          Cancel
        </Button>
        <Button variant="primary" disabled={busy} onClick={() => void submit()}>
          {busy ? "Creating…" : "Create"}
        </Button>
      </div>
    </Modal>
  );
}
