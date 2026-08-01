// Issue one-time Enrollment form: label, explicit permission selection, token
// lifetime, and optional credential expiration. The endpoint returns a
// one-time secret and does not support Idempotency-Key, so failures are never
// blind-retried (doc 3.3).

import { useState } from "react";
import { createEnrollment } from "../../api/actions";
import { apiError } from "../../api/client";
import type { EnrollmentSecret } from "../../api/types";
import { GRANTABLE_PERMISSIONS } from "../../api/types";
import { validateFutureTime } from "../../lib/validation";
import { Button } from "../Button";
import { Modal } from "../Modal";
import { useToast } from "../Toasts";

const ENROLLMENT_EXPIRY_OPTIONS = [
  { value: 300, label: "5 minutes" },
  { value: 900, label: "15 minutes" },
  { value: 1800, label: "30 minutes" },
] as const;

export function IssueEnrollmentModal({
  agentId,
  onClose,
  onCreated,
  onMaybeCreated,
}: {
  agentId: string;
  onClose: () => void;
  onCreated: (secret: EnrollmentSecret) => void;
  onMaybeCreated: () => void;
}) {
  const toast = useToast();
  const [label, setLabel] = useState("");
  const [permissions, setPermissions] = useState<string[]>(["observe", "search"]);
  const [expiresIn, setExpiresIn] = useState<number>(900);
  const [credExpiresAt, setCredExpiresAt] = useState("");
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | undefined>();

  const submit = async () => {
    if (!label.trim()) return setFormError("credential_label is required");
    if (permissions.length === 0) return setFormError("permissions must be explicitly selected and non-empty");
    const timeError = validateFutureTime(credExpiresAt);
    if (timeError) return setFormError(timeError);
    setFormError(undefined);
    setBusy(true);
    try {
      const secret = await createEnrollment(agentId, {
        credential_label: label.trim(),
        permissions,
        expires_in_seconds: expiresIn,
        credential_expires_at: credExpiresAt ? new Date(credExpiresAt).toISOString() : undefined,
      });
      onCreated(secret);
    } catch (err) {
      if (apiError(err) && err.status < 500) {
        // Client-side rejection: keep the form so the user can correct it.
        setFormError(`Request rejected (HTTP ${err.status}); check your input`);
      } else {
        // Timeout/5xx: never blind-retry a one-time-secret creation. Close,
        // refresh the pending list, and let the user decide (doc 3.3).
        toast("warn", "Request failed; not retried automatically. The list has been refreshed: if a new pending record appears, use it or revoke it and issue a new one");
        onMaybeCreated();
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal title="Issue one-time Enrollment" onClose={onClose}>
      <label htmlFor="en-label">credential_label (required)</label>
      <input
        id="en-label"
        type="text"
        placeholder="Alice MacBook"
        value={label}
        onChange={(e) => setLabel(e.target.value)}
      />
      <label>permissions (explicit selection, limited by deployment grantable)</label>
      {GRANTABLE_PERMISSIONS.map((p) => (
        <label key={p} className="ck">
          <input
            type="checkbox"
            checked={permissions.includes(p)}
            onChange={(e) =>
              setPermissions((prev) => (e.target.checked ? [...prev, p] : prev.filter((x) => x !== p)))
            }
          />
          {p}
        </label>
      ))}
      <div className="field-row">
        <div>
          <label htmlFor="en-exp">Token lifetime</label>
          <select id="en-exp" value={expiresIn} onChange={(e) => setExpiresIn(Number(e.target.value))}>
            {ENROLLMENT_EXPIRY_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label htmlFor="en-credexp">Credential expiration (optional)</label>
          <input
            id="en-credexp"
            type="datetime-local"
            value={credExpiresAt}
            onChange={(e) => setCredExpiresAt(e.target.value)}
          />
        </div>
      </div>
      {formError && <div className="note bad">{formError}</div>}
      <div className="note small">
        This endpoint returns a one-time secret and <b>does not support Idempotency-Key</b>: network timeouts are not retried automatically; refresh the pending list to confirm first.
      </div>
      <div className="row" style={{ justifyContent: "flex-end" }}>
        <Button variant="ghost" onClick={onClose} disabled={busy}>
          Cancel
        </Button>
        <Button variant="primary" disabled={busy} onClick={() => void submit()}>
          {busy ? "Issuing…" : "Issue"}
        </Button>
      </div>
    </Modal>
  );
}
