import { useEffect, useState } from "react";
import { ApiError } from "../api/client";
import { createInvitation, revokeInvitation } from "../api/actions";
import { listInvitations } from "../api/queries";
import type { HumanMe, Invitation, Role } from "../api/types";
import { can, canRevokeInvitation } from "../lib/capabilities";
import { usePagedList } from "../lib/usePagedList";
import { useErrorHandler } from "../lib/useErrorHandler";
import { formatTime } from "../lib/format";
import { validateEmail } from "../lib/validation";
import { Badge, RoleBadge } from "../components/Badge";
import { Countdown } from "../components/Countdown";
import { Modal } from "../components/Modal";
import { SecretCard } from "../components/SecretCard";
import { useToast } from "../components/Toasts";

const STATUS_FILTERS = ["all", "pending", "accepted", "revoked", "expired"] as const;
const EXPIRY_OPTIONS = [
  { value: 86400, label: "24 hours" },
  { value: 172800, label: "2 days" },
  { value: 604800, label: "7 days (max)" },
] as const;

function CreateInvitationModal({
  actorRole,
  onClose,
  onCreated,
  onMaybeCreated,
}: {
  actorRole: Role;
  onClose: () => void;
  onCreated: (invitation: Invitation) => void;
  onMaybeCreated: () => void;
}) {
  const toast = useToast();
  const handleError = useErrorHandler();
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<Role>("member");
  const [expiresIn, setExpiresIn] = useState<number>(86400);
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | undefined>();

  const submit = async () => {
    const emailError = validateEmail(email.trim());
    if (emailError) return setFormError(emailError);
    setFormError(undefined);
    setBusy(true);
    try {
      const invitation = await createInvitation({
        target_email: email.trim(),
        role,
        expires_in_seconds: expiresIn,
      });
      onCreated(invitation);
    } catch (err) {
      if (err instanceof ApiError && err.status < 500) {
        handleError(err);
      } else {
        // No Idempotency-Key on invitation creation (doc 3.3): never blind
        // retry; refresh the list and let the user revoke duplicates.
        toast("warn", "Request failed; no automatic retry. The list has been refreshed: if a new pending invitation appears, use it or revoke it and create a new one");
        onMaybeCreated();
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal title="Create invitation" onClose={onClose}>
      <label htmlFor="iv-email">target_email</label>
      <input
        id="iv-email"
        type="email"
        placeholder="bob@example.com"
        value={email}
        onChange={(e) => setEmail(e.target.value)}
      />
      <div className="field-row">
        <div>
          <label htmlFor="iv-role">role</label>
          <select id="iv-role" value={role} onChange={(e) => setRole(e.target.value as Role)}>
            <option value="member">member</option>
            {can(actorRole, "invite.admin") && <option value="admin">admin</option>}
          </select>
        </div>
        <div>
          <label htmlFor="iv-exp">Expiration</label>
          <select id="iv-exp" value={expiresIn} onChange={(e) => setExpiresIn(Number(e.target.value))}>
            {EXPIRY_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </div>
      </div>
      {formError && <div className="note bad">{formError}</div>}
      <div className="note small">
        The create response contains a one-time token and <b>does not support Idempotency-Key</b>: timeouts
        are not retried automatically. Refresh the list first to check whether a pending record was created.
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

export function AdminInvitationsPage({ me }: { me: HumanMe }) {
  const toast = useToast();
  const handleError = useErrorHandler();
  const [filter, setFilter] = useState<string>("all");
  const [createOpen, setCreateOpen] = useState(false);
  const [secretUrl, setSecretUrl] = useState<string | undefined>();
  const list = usePagedList(
    (cursor) => listInvitations({ status: filter === "all" ? undefined : filter, cursor }),
    [filter],
  );

  useEffect(() => {
    if (list.error) handleError(list.error);
  }, [list.error, handleError]);

  const actorRole = me.role ?? "member";

  const revoke = async (invitation: Invitation) => {
    try {
      await revokeInvitation(invitation.invitation_id);
      toast("ok", "Invitation revoked");
      list.reload();
    } catch (err) {
      handleError(err);
    }
  };

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Invitations</h1>
          <p className="muted" style={{ margin: 0 }}>
            Owners can invite admins/members; Admins can only invite members
          </p>
        </div>
        <button className="btn primary" onClick={() => setCreateOpen(true)}>
          + Create invitation
        </button>
      </div>

      {secretUrl && (
        <SecretCard
          title="Invitation created — the token is shown only once"
          value={secretUrl}
          valueLabel=" Join URL"
          note="The token lives in the URL fragment (#invite=) and never reaches server access logs or the Referer header. If the token is lost, revoke the invitation and create a new one."
          onClose={() => setSecretUrl(undefined)}
        />
      )}

      <div className="tabs" role="group" aria-label="invitation status">
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

      <div className="card">
        {list.loading ? (
          <p className="muted small">Loading…</p>
        ) : list.items.length === 0 ? (
          <p className="muted small">No matching records.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Email</th>
                <th>Role</th>
                <th>Status</th>
                <th>Expires/Created</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {list.items.map((i) => (
                <tr key={i.invitation_id}>
                  <td>{i.target_email}</td>
                  <td>
                    <RoleBadge role={i.role} />
                  </td>
                  <td>
                    <Badge status={i.status} />
                  </td>
                  <td className="small">
                    {i.status === "pending" ? <Countdown to={i.expires_at} /> : formatTime(i.created_at)}
                  </td>
                  <td>
                    {i.status === "pending" && canRevokeInvitation(actorRole, i.role) && (
                      <button className="btn sm danger" onClick={() => void revoke(i)}>
                        Revoke
                      </button>
                    )}
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
            {list.loadingMore ? "Loading…" : "Load more"}
          </button>
        </div>
      )}

      {createOpen && (
        <CreateInvitationModal
          actorRole={actorRole}
          onClose={() => setCreateOpen(false)}
          onCreated={(invitation) => {
            setCreateOpen(false);
            // The one-time token becomes a fragment-based join URL; it is
            // held in memory only and never persisted.
            if (invitation.token) {
              setSecretUrl(`${window.location.origin}/join#invite=${invitation.token}`);
            }
            list.reload();
          }}
          onMaybeCreated={() => {
            setCreateOpen(false);
            list.reload();
          }}
        />
      )}
    </>
  );
}
