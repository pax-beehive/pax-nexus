import { useEffect, useState } from "react";
import { apiError } from "../api/client";
import { createInvitation, revokeInvitation } from "../api/actions";
import { listInvitations } from "../api/queries";
import type { HumanMe, Invitation, Role } from "../api/types";
import { can, canRevokeInvitation } from "../lib/capabilities";
import { usePagedList } from "../lib/usePagedList";
import { useErrorHandler } from "../lib/useErrorHandler";
import { formatTime } from "../lib/format";
import { validateEmail } from "../lib/validation";
import { Badge, RoleBadge } from "../components/Badge";
import { Button } from "../components/Button";
import { Countdown } from "../components/Countdown";
import { Modal } from "../components/Modal";
import { PagedListCard } from "../components/PagedListCard";
import { SecretCeremony } from "../components/SecretCeremony";
import { useToast } from "../components/Toasts";
import { PageHeader } from "../components/PageHeader";

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
      if (apiError(err) && err.status < 500) {
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
      <PageHeader
        kicker="MANAGEMENT · INVITATIONS"
        title="Outstanding invitations"
        lede="Each invitation produces one link, shown once. Failures never say why — expired, revoked, already used and wrong address all read the same, so a link can't be used to probe the team."
        actions={
          <Button variant="primary" onClick={() => setCreateOpen(true)}>
            + Create invitation
          </Button>
        }
      />

      {secretUrl && (
        <SecretCeremony
          title="One-time invitation link · shown once, stored nowhere"
          headline="Send the link out now. We can't show it to you again."
          body="The token lives in the URL fragment (#invite=), so it never reaches server access logs or Referer headers."
          value={secretUrl}
          valueLabel="Invitation link"
          steps={[
            "Send the link to them (IM or email both work).",
            "They open the link, sign in, and the join is done.",
            "The person then appears in the team's Members list.",
          ]}
          recovery="Nothing breaks. Revoke this invitation and create a new one."
          onClose={() => setSecretUrl(undefined)}
        />
      )}

      <div className="seg" role="group" aria-label="invitation status">
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

      <PagedListCard
        list={list}
        columns={["Email", "Role", "Status", "Expires/Created", ""]}
        emptyText="No matching records."
        renderRow={(i) => (
          <tr key={i.invitation_id}>
            <td>
              <span className="at-row-name">{i.target_email}</span>
            </td>
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
                <Button variant="danger" size="sm" onClick={() => void revoke(i)}>
                  Revoke
                </Button>
              )}
            </td>
          </tr>
        )}
      />

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
