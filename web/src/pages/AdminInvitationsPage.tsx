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
  { value: 86400, label: "24 小时" },
  { value: 172800, label: "2 天" },
  { value: 604800, label: "7 天（最大）" },
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
        toast("warn", "请求失败，未自动重试。已刷新列表：若出现新的 pending 邀请，可使用或吊销后重建");
        onMaybeCreated();
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal title="创建邀请" onClose={onClose}>
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
          <label htmlFor="iv-exp">有效期</label>
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
        创建响应包含一次性 token，且<b>不支持 Idempotency-Key</b>：超时不会自动重试，先刷新列表确认是否已产生 pending 记录。
      </div>
      <div className="row" style={{ justifyContent: "flex-end" }}>
        <button className="btn ghost" onClick={onClose} disabled={busy}>
          取消
        </button>
        <button className="btn primary" disabled={busy} onClick={() => void submit()}>
          {busy ? "创建中…" : "创建"}
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
      toast("ok", "邀请已吊销");
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
            Owner 可邀请 admin/member；Admin 只能邀请 member
          </p>
        </div>
        <button className="btn primary" onClick={() => setCreateOpen(true)}>
          + 创建邀请
        </button>
      </div>

      {secretUrl && (
        <SecretCard
          title="邀请已创建——token 仅此一次显示"
          value={secretUrl}
          valueLabel=" Join URL"
          note="token 位于 URL fragment（#invite=），不会进入服务端 access log 或 Referer。token 丢失只能 revoke 后重新邀请。"
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
          <p className="muted small">加载中…</p>
        ) : list.items.length === 0 ? (
          <p className="muted small">无匹配记录。</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Email</th>
                <th>Role</th>
                <th>Status</th>
                <th>过期/创建</th>
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
                        吊销
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
            {list.loadingMore ? "加载中…" : "加载更多"}
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
