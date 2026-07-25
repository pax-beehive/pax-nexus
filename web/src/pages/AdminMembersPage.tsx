import { useEffect, useState } from "react";
import { ApiError } from "../api/client";
import { updateMember } from "../api/actions";
import { getMember, listMembers } from "../api/queries";
import type { HumanMe, Member, MembershipStatus, Role } from "../api/types";
import { canManageTargetRole } from "../lib/capabilities";
import { usePagedList } from "../lib/usePagedList";
import { useErrorHandler } from "../lib/useErrorHandler";
import { Badge, RoleBadge } from "../components/Badge";
import { Modal } from "../components/Modal";
import { useToast } from "../components/Toasts";

const STATUS_FILTERS = ["all", "active", "suspended", "removed"] as const;
const ROLE_FILTERS = ["all", "owner", "admin", "member"] as const;
const ROLES: Role[] = ["owner", "admin", "member"];
const STATUSES: MembershipStatus[] = ["active", "suspended", "removed"];

function EditMemberModal({
  actor,
  member,
  onClose,
  onSaved,
}: {
  actor: HumanMe;
  member: Member;
  onClose: () => void;
  onSaved: (m: Member) => void;
}) {
  const toast = useToast();
  const handleError = useErrorHandler();
  const [role, setRole] = useState<Role>(member.role);
  const [status, setStatus] = useState<MembershipStatus>(member.status);
  const [busy, setBusy] = useState(false);
  const actorRole = actor.role ?? "member";

  const save = async () => {
    if (busy) return;
    setBusy(true);
    try {
      const updated = await updateMember(
        member.membership_id,
        { role, status },
        member.resource_version,
      );
      toast("ok", "Member updated");
      onSaved(updated);
    } catch (err) {
      if (err instanceof ApiError && err.code === "last_active_owner") {
        toast("bad", "You must promote another active Owner first");
      } else if (err instanceof ApiError && err.code === "resource_version_conflict") {
        const fresh = await getMember(member.membership_id);
        onSaved(fresh);
        toast("warn", "Modified by someone else; refreshed with the latest data");
      } else {
        handleError(err);
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal title={`Edit ${member.email ?? member.membership_id}`} onClose={onClose}>
      <div className="field-row">
        <div>
          <label htmlFor="em-role">role</label>
          <select id="em-role" value={role} onChange={(e) => setRole(e.target.value as Role)}>
            {ROLES.map((r) => (
              <option key={r} value={r} disabled={!canManageTargetRole(actorRole, r) && r !== member.role}>
                {r}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label htmlFor="em-status">status</label>
          <select
            id="em-status"
            value={status}
            onChange={(e) => setStatus(e.target.value as MembershipStatus)}
          >
            {STATUSES.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
        </div>
      </div>
      <div className="note small">
        resource_version: <code>{member.resource_version}</code> (sent in both the body and If-Match).
        Suspending or removing revokes this user&apos;s Human Sessions, all Agent Credentials, and pending
        Enrollments; reactivation does not restore old keys. removed is terminal and can only be re-invited.
      </div>
      <div className="row" style={{ justifyContent: "flex-end" }}>
        <button className="btn ghost" onClick={onClose} disabled={busy}>
          Cancel
        </button>
        <button className="btn primary" disabled={busy} onClick={() => void save()}>
          {busy ? "Saving…" : "Save"}
        </button>
      </div>
    </Modal>
  );
}

export function AdminMembersPage({ me }: { me: HumanMe }) {
  const handleError = useErrorHandler();
  const [filter, setFilter] = useState<string>("all");
  const [roleFilter, setRoleFilter] = useState<string>("all");
  const [editing, setEditing] = useState<Member | undefined>();
  const list = usePagedList(
    (cursor) =>
      listMembers({
        role: roleFilter === "all" ? undefined : roleFilter,
        status: filter === "all" ? undefined : filter,
        cursor,
      }),
    [filter, roleFilter],
  );

  useEffect(() => {
    if (list.error) handleError(list.error);
  }, [list.error, handleError]);

  const actorRole = me.role ?? "member";

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Members</h1>
          <p className="muted" style={{ margin: 0 }}>
            Owners can manage all roles; Admins can only manage Members
          </p>
        </div>
      </div>
      <div className="row wrap" style={{ gap: 10, marginBottom: 14, alignItems: "center" }}>
        <label className="filter-label" htmlFor="member-status-filter">
          Status
        </label>
        <select
          id="member-status-filter"
          style={{ width: 150 }}
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        >
          {STATUS_FILTERS.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
        <label className="filter-label" htmlFor="member-role-filter">
          Role
        </label>
        <select
          id="member-role-filter"
          style={{ width: 150 }}
          value={roleFilter}
          onChange={(e) => setRoleFilter(e.target.value)}
        >
          {ROLE_FILTERS.map((r) => (
            <option key={r} value={r}>
              {r}
            </option>
          ))}
        </select>
      </div>
      <div className="card">
        {list.loading ? (
          <p className="muted small">Loading…</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Email</th>
                <th>Role</th>
                <th>Status</th>
                <th>Version</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {list.items.map((m) => {
                const manageable = m.status !== "removed" && canManageTargetRole(actorRole, m.role);
                return (
                  <tr key={m.membership_id}>
                    <td>
                      {m.email ?? m.user_id}
                      {m.membership_id === me.membership_id && <span className="faint small">(you)</span>}
                    </td>
                    <td>
                      <RoleBadge role={m.role} />
                    </td>
                    <td>
                      <Badge status={m.status} />
                    </td>
                    <td className="small mono">v{m.resource_version}</td>
                    <td>
                      {manageable ? (
                        <button className="btn sm" onClick={() => setEditing(m)}>
                          Edit
                        </button>
                      ) : (
                        <span className="faint small">{m.status === "removed" ? "Terminal" : "No permission"}</span>
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
            {list.loadingMore ? "Loading…" : "Load more"}
          </button>
        </div>
      )}
      <div className="note small">
        The system always keeps at least one active Owner; the server rejects changes that break this
        constraint with <code>last_active_owner</code>. removed is terminal and requires a new invitation.
      </div>
      {editing && (
        <EditMemberModal
          actor={me}
          member={editing}
          onClose={() => setEditing(undefined)}
          onSaved={() => {
            setEditing(undefined);
            list.reload();
          }}
        />
      )}
    </>
  );
}
