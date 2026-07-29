// Enrollment issuance + enrollment/credential metadata lists, shared between
// the owner view (/v1/me/agents/...) and the admin governance view
// (/v1/admin/agents/...).

import { useState } from "react";
import {
  beginAction,
  createEnrollment,
  revokeCredential,
  revokeEnrollment,
  type AgentScope,
} from "../api/actions";
import { listCredentials, listEnrollments } from "../api/queries";
import { apiError } from "../api/client";
import type { CredentialMetadata, EnrollmentSecret } from "../api/types";
import { GRANTABLE_PERMISSIONS } from "../api/types";
import { deriveCredentialStatus } from "../lib/credentials";
import { copyTextToClipboard } from "../lib/clipboard";
import { enrollmentConnectCommand, isSelfDescribingEnrollmentToken } from "../lib/enrollment";
import { usePagedList } from "../lib/usePagedList";
import { useErrorHandler } from "../lib/useErrorHandler";
import { formatTime } from "../lib/format";
import { validateFutureTime } from "../lib/validation";
import { Badge } from "./Badge";
import { ConfirmDialog } from "./ConfirmDialog";
import { Countdown } from "./Countdown";
import { Modal } from "./Modal";
import { SecretCard } from "./SecretCard";
import { useToast } from "./Toasts";

const ENROLLMENT_STATUSES = ["all", "pending", "consumed", "revoked", "expired"] as const;
const CREDENTIAL_STATUSES = ["all", "active", "expired", "revoked"] as const;
const ENROLLMENT_EXPIRY_OPTIONS = [
  { value: 300, label: "5 minutes" },
  { value: 900, label: "15 minutes" },
  { value: 1800, label: "30 minutes" },
] as const;

function Tabs({
  label,
  options,
  value,
  onChange,
}: {
  /** Accessible group name, e.g. "enrollment status". */
  label: string;
  options: readonly string[];
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <div className="tabs" role="group" aria-label={label}>
      {options.map((o) => (
        <button
          key={o}
          className={o === value ? "on" : ""}
          aria-pressed={o === value}
          onClick={() => onChange(o)}
        >
          {o}
        </button>
      ))}
    </div>
  );
}

function LoadMore({
  nextCursor,
  loadingMore,
  onLoadMore,
}: {
  nextCursor?: string;
  loadingMore: boolean;
  onLoadMore: () => void;
}) {
  if (!nextCursor) return null;
  return (
    <div style={{ marginTop: 10, textAlign: "center" }}>
      <button className="btn sm" disabled={loadingMore} onClick={onLoadMore}>
        {loadingMore ? "Loading…" : "Load more"}
      </button>
    </div>
  );
}

function IssueEnrollmentModal({
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
          <button className="btn ghost" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button className="btn primary" disabled={busy} onClick={() => void submit()}>
            {busy ? "Issuing…" : "Issue"}
          </button>
        </div>
    </Modal>
  );
}

export function AgentArtifacts({
  scope,
  agentId,
  agentStatus,
  canIssue,
}: {
  scope: AgentScope;
  agentId: string;
  agentStatus: string;
  canIssue: boolean;
}) {
  const toast = useToast();
  const handleError = useErrorHandler();
  const [secret, setSecret] = useState<EnrollmentSecret | undefined>();
  const [issueOpen, setIssueOpen] = useState(false);
  const [enrollmentFilter, setEnrollmentFilter] = useState<string>("all");
  const [credentialFilter, setCredentialFilter] = useState<string>("all");
  // Pending revoke confirmation; the Idempotency-Key is bound to the action
  // instance (one per opened dialog, reused if the confirm is retried).
  const [revokeTarget, setRevokeTarget] = useState<
    | { kind: "enrollment"; id: string; key: string }
    | { kind: "credential"; id: string; key: string }
    | undefined
  >();
  const [busy, setBusy] = useState(false);

  const enrollments = usePagedList(
    (cursor) =>
      listEnrollments(scope, agentId, {
        status: enrollmentFilter === "all" ? undefined : enrollmentFilter,
        cursor,
      }),
    [scope, agentId, enrollmentFilter],
  );

  const credentials = usePagedList(
    (cursor) =>
      listCredentials(scope, agentId, {
        status: credentialFilter === "all" ? undefined : credentialFilter,
        cursor,
      }),
    [scope, agentId, credentialFilter],
  );

  const confirmRevoke = async () => {
    if (!revokeTarget || busy) return;
    setBusy(true);
    try {
      if (revokeTarget.kind === "enrollment") {
        await revokeEnrollment(scope, agentId, revokeTarget.id, revokeTarget.key);
        toast("ok", "Enrollment revoked");
        enrollments.reload();
      } else {
        await revokeCredential(scope, agentId, revokeTarget.id, revokeTarget.key);
        toast("ok", "Credential revoked; the API key stops working immediately");
        credentials.reload();
      }
      setRevokeTarget(undefined);
    } catch (err) {
      handleError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      {secret && (
        <SecretCard
          title="One-time Enrollment token (shown only once)"
          value={secret.token}
          valueLabel=" token"
          expiresAt={secret.expires_at}
          note={
            isSelfDescribingEnrollmentToken(secret.token)
              ? "The token is never written to durable storage, logs, or analytics. If lost, revoke it and issue a new one; the token embeds the connect address so clients can parse it directly; the client performs the exchange, and the Portal never sees the API key."
              : "The token is never written to durable storage, logs, or analytics. If lost, revoke it and issue a new one; the client performs the exchange, and the Portal never sees the API key."
          }
          extraActions={
            <button
              className="btn sm"
              onClick={() => {
                const command = enrollmentConnectCommand(secret.token, window.location.origin);
                void copyTextToClipboard(command).then((ok) => {
                  if (ok) toast("ok", "Connect command copied");
                  else window.prompt("Copy manually:", command);
                });
              }}
            >
              Copy client command
            </button>
          }
          onClose={() => setSecret(undefined)}
        />
      )}

      <div className="card">
        <div className="row between">
          <h2 style={{ margin: 0 }}>Enrollments</h2>
          {canIssue && (
            <button className="btn primary sm" onClick={() => setIssueOpen(true)}>
              + Issue one-time Enrollment
            </button>
          )}
        </div>
        {agentStatus !== "active" && (
          <div className="note warn small">
            Agent is not active: suspend / retire immediately revokes all Credentials and pending Enrollments, and returning to active does not restore old keys.
          </div>
        )}
        <Tabs
          label="enrollment status"
          options={ENROLLMENT_STATUSES}
          value={enrollmentFilter}
          onChange={setEnrollmentFilter}
        />
        {enrollments.loading ? (
          <p className="muted small">Loading…</p>
        ) : enrollments.items.length === 0 ? (
          <p className="muted small">No Enrollments yet.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Label</th>
                <th>Permissions</th>
                <th>Status</th>
                <th>Expires/Created</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {enrollments.items.map((e) => (
                <tr key={e.enrollment_id}>
                  <td>{e.credential_label}</td>
                  <td className="small mono">{e.permissions.join(", ")}</td>
                  <td>
                    <Badge status={e.status} />
                  </td>
                  <td className="small">
                    {e.status === "pending" ? <Countdown to={e.expires_at} /> : formatTime(e.created_at)}
                  </td>
                  <td>
                    {e.status === "pending" && (
                      <button
                        className="btn sm danger"
                        onClick={() =>
                          setRevokeTarget({ kind: "enrollment", id: e.enrollment_id, key: beginAction() })
                        }
                      >
                        Revoke
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <LoadMore
          nextCursor={enrollments.nextCursor}
          loadingMore={enrollments.loadingMore}
          onLoadMore={() => void enrollments.loadMore()}
        />
      </div>

      <div className="card">
        <h2 style={{ margin: "0 0 8px" }}>Credentials (metadata only, never contains API keys)</h2>
        <Tabs
          label="credential status"
          options={CREDENTIAL_STATUSES}
          value={credentialFilter}
          onChange={setCredentialFilter}
        />
        {credentials.loading ? (
          <p className="muted small">Loading…</p>
        ) : credentials.items.length === 0 ? (
          <p className="muted small">No Credentials yet.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Label</th>
                <th>Permissions</th>
                <th>Status</th>
                <th>Last used</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {credentials.items.map((c: CredentialMetadata) => {
                const derived = deriveCredentialStatus(c);
                return (
                  <tr key={c.credential_id}>
                    <td>{c.label}</td>
                    <td className="small mono">{c.permissions.join(", ")}</td>
                    <td>
                      <Badge status={derived} />
                    </td>
                    <td className="small">{formatTime(c.last_used_at)}</td>
                    <td>
                      {derived === "active" && (
                        <button
                          className="btn sm danger"
                          onClick={() =>
                            setRevokeTarget({ kind: "credential", id: c.credential_id, key: beginAction() })
                          }
                        >
                          Revoke
                        </button>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
        <LoadMore
          nextCursor={credentials.nextCursor}
          loadingMore={credentials.loadingMore}
          onLoadMore={() => void credentials.loadMore()}
        />
      </div>

      {issueOpen && (
        <IssueEnrollmentModal
          agentId={agentId}
          onClose={() => setIssueOpen(false)}
          onCreated={(s) => {
            setIssueOpen(false);
            setSecret(s);
            enrollments.reload();
          }}
          onMaybeCreated={() => {
            setIssueOpen(false);
            enrollments.reload();
          }}
        />
      )}

      {revokeTarget && (
        <ConfirmDialog
          title={revokeTarget.kind === "enrollment" ? "Revoke Enrollment" : "Revoke Credential"}
          consequences={
            revokeTarget.kind === "enrollment"
              ? ["The one-time token stops working immediately and any in-progress client onboarding will fail", "This cannot be undone; issue a new Enrollment if needed"]
              : ["The API key stops working immediately and the Agent client holding it loses access", "This cannot be undone; issue a new Enrollment if needed"]
          }
          confirmLabel="Confirm revoke"
          busy={busy}
          onConfirm={() => void confirmRevoke()}
          onClose={() => setRevokeTarget(undefined)}
        />
      )}
    </>
  );
}
