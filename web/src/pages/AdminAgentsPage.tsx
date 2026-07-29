import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { apiError } from "../api/client";
import { transferAgent, updateAgent } from "../api/actions";
import { getAdminAgent, listAdminAgents, listAllMembers } from "../api/queries";
import type { AgentProfile, HumanMe, Member } from "../api/types";
import { can } from "../lib/capabilities";
import { usePagedList } from "../lib/usePagedList";
import { useErrorHandler } from "../lib/useErrorHandler";
import { Badge, ProvisionedByBadge } from "../components/Badge";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { Modal } from "../components/Modal";
import { PagedListCard } from "../components/PagedListCard";
import { useToast } from "../components/Toasts";

const STATUS_FILTERS = ["all", "active", "suspended", "retired"] as const;

type PendingGovernance =
  | { kind: "suspend"; agent: AgentProfile }
  | { kind: "resume"; agent: AgentProfile }
  | { kind: "transfer"; agent: AgentProfile };

function TransferModal({
  agent,
  members,
  onClose,
  onDone,
}: {
  agent: AgentProfile;
  members: Member[];
  onClose: () => void;
  onDone: (agent: AgentProfile) => void;
}) {
  const toast = useToast();
  const handleError = useErrorHandler();
  const targets = members.filter(
    (m) => m.status === "active" && m.membership_id !== agent.owner_membership_id,
  );
  const [target, setTarget] = useState(targets[0]?.membership_id ?? "");
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    if (!target || busy) return;
    setBusy(true);
    try {
      const updated = await transferAgent(agent.agent_id, target, agent.resource_version);
      toast("ok", "Transferred; the old Credentials and Enrollments were revoked");
      onDone(updated);
    } catch (err) {
      if (apiError(err, undefined, "resource_version_conflict")) {
        toast("warn", "The data was modified by someone else; refresh and try again");
        onClose();
      } else {
        handleError(err);
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal title={`Transfer ${agent.display_name}`} onClose={onClose}>
      <label htmlFor="tf-target">Target Owner (active Memberships only)</label>
      {targets.length === 0 ? (
        <div className="note warn">No active Membership is available as a transfer target.</div>
      ) : (
        <select id="tf-target" value={target} onChange={(e) => setTarget(e.target.value)}>
          {targets.map((m) => (
            <option key={m.membership_id} value={m.membership_id}>
              {m.email ?? m.membership_id}
            </option>
          ))}
        </select>
      )}
      <div className="note warn small">
        Transfer revokes all Credentials and pending Enrollments under the old Owner; the new Owner must issue a new Enrollment.
      </div>
      <div className="row" style={{ justifyContent: "flex-end" }}>
        <button className="btn ghost" onClick={onClose} disabled={busy}>
          Cancel
        </button>
        <button className="btn danger" disabled={!target || busy} onClick={() => void submit()}>
          {busy ? "Transferring…" : "Confirm transfer"}
        </button>
      </div>
    </Modal>
  );
}

export function AdminAgentsPage({ me }: { me: HumanMe }) {
  const toast = useToast();
  const handleError = useErrorHandler();
  const [status, setStatus] = useState<string>("all");
  const [q, setQ] = useState("");
  const [qInput, setQInput] = useState("");
  const [ownerFilter, setOwnerFilter] = useState("");
  const [members, setMembers] = useState<Member[]>([]);
  const [pending, setPending] = useState<PendingGovernance | undefined>();
  const [busy, setBusy] = useState(false);

  // Load every member page for owner labels, transfer targets and filtering.
  // The raw membership ID remains a fallback while this best-effort
  // enrichment is loading.
  useEffect(() => {
    let cancelled = false;
    listAllMembers({ limit: 100 })
      .then((loaded) => {
        if (!cancelled) setMembers(loaded);
      })
      .catch((err: unknown) => {
        if (!cancelled) handleError(err);
      });
    return () => {
      cancelled = true;
    };
  }, [handleError]);

  const list = usePagedList(
    (cursor) =>
      listAdminAgents({
        status: status === "all" ? undefined : status,
        q: q || undefined,
        owner_membership_id: ownerFilter || undefined,
        cursor,
      }),
    [status, q, ownerFilter],
  );

  useEffect(() => {
    if (list.error) handleError(list.error);
  }, [list.error, handleError]);

  const memberLabel = useMemo(() => {
    const map = new Map(members.map((m) => [m.membership_id, m.email ?? m.membership_id]));
    return (id: string | undefined) => (id ? (map.get(id) ?? id) : "—");
  }, [members]);

  const actorRole = me.role ?? "member";
  const maySuspend = can(actorRole, "suspend.any-agent");
  const mayGovern = can(actorRole, "govern.any-agent");

  const runGovernance = async () => {
    if (!pending || pending.kind === "transfer" || busy) return;
    setBusy(true);
    const { agent } = pending;
    const nextStatus = pending.kind === "suspend" ? "suspended" : "active";
    try {
      const updated = await updateAgent("admin", agent.agent_id, { status: nextStatus }, agent.resource_version);
      toast(
        pending.kind === "suspend" ? "warn" : "ok",
        pending.kind === "suspend"
          ? "Suspended; Credentials and pending Enrollments were cascade-revoked"
          : "Resumed to active (old Credentials are not restored; a new Enrollment is required)",
      );
      setPending(undefined);
      list.reload();
      void updated;
    } catch (err) {
      if (apiError(err, undefined, "resource_version_conflict")) {
        await getAdminAgent(agent.agent_id).catch(() => undefined);
        list.reload();
        toast("warn", "Modified by someone else; the latest data has been loaded");
        setPending(undefined);
      } else {
        handleError(err);
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <div className="page-head">
        <div>
          <h1>All Agents</h1>
          <p className="muted" style={{ margin: 0 }}>
            Admins can only suspend; edit, resume, retire, and transfer are Owner-only
          </p>
        </div>
      </div>
      <div className="row wrap" style={{ marginBottom: 14, gap: 10 }}>
        <input
          type="text"
          style={{ width: 240 }}
          placeholder="Search by name or ID (q)"
          value={qInput}
          onChange={(e) => setQInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") setQ(qInput.trim());
          }}
        />
        <button className="btn sm" onClick={() => setQ(qInput.trim())}>
          Search
        </button>
        <select
          style={{ width: 220 }}
          aria-label="Owner filter"
          value={ownerFilter}
          onChange={(e) => setOwnerFilter(e.target.value)}
        >
          <option value="">All Owners</option>
          {members.map((m) => (
            <option key={m.membership_id} value={m.membership_id}>
              {m.email ?? m.membership_id}
            </option>
          ))}
        </select>
      </div>
      <div className="tabs" role="group" aria-label="agent status">
        {STATUS_FILTERS.map((s) => (
          <button
            key={s}
            className={s === status ? "on" : ""}
            aria-pressed={s === status}
            onClick={() => setStatus(s)}
          >
            {s}
          </button>
        ))}
      </div>
      <PagedListCard
        list={list}
        columns={["Agent", "Owner", "Status", "Governance"]}
        emptyText="No agents yet"
        renderRow={(a) => {
          const retired = a.status === "retired";
          return (
            <tr key={a.agent_id}>
              <td>
                <Link to={`/admin/agents/${encodeURIComponent(a.agent_id)}`}>{a.display_name}</Link>
                <div className="small mono faint">{a.agent_id}</div>
              </td>
              <td className="small">{memberLabel(a.owner_membership_id)}</td>
              <td>
                <span className="row">
                  <Badge status={a.status} />
                  <ProvisionedByBadge agent={a} />
                </span>
              </td>
              <td>
                {retired ? (
                  <span className="faint small">Terminal</span>
                ) : (
                  <span className="row wrap">
                    {a.status === "active" && maySuspend && (
                      <button
                        className="btn sm danger"
                        onClick={() => setPending({ kind: "suspend", agent: a })}
                      >
                        Suspend
                      </button>
                    )}
                    {a.status === "suspended" && mayGovern && (
                      <button className="btn sm" onClick={() => setPending({ kind: "resume", agent: a })}>
                        Resume
                      </button>
                    )}
                    {mayGovern && (
                      <button className="btn sm" onClick={() => setPending({ kind: "transfer", agent: a })}>
                        Transfer
                      </button>
                    )}
                  </span>
                )}
              </td>
            </tr>
          );
        }}
      />

      {pending?.kind === "transfer" && (
        <TransferModal
          agent={pending.agent}
          members={members}
          onClose={() => setPending(undefined)}
          onDone={() => {
            setPending(undefined);
            list.reload();
          }}
        />
      )}
      {pending && pending.kind !== "transfer" && (
        <ConfirmDialog
          title={pending.kind === "suspend" ? "Suspend Agent" : "Resume Agent"}
          consequences={
            pending.kind === "suspend"
              ? [
                  `Immediately revoke all Credentials and pending Enrollments for ${pending.agent.display_name}`,
                  "Resuming to active does not restore old keys; a new Enrollment must be issued",
                ]
              : ["Old Credentials stay revoked; the Owner must issue a new Enrollment before clients can connect"]
          }
          confirmLabel={pending.kind === "suspend" ? "Confirm suspend" : "Confirm resume"}
          busy={busy}
          onConfirm={() => void runGovernance()}
          onClose={() => setPending(undefined)}
        />
      )}
    </>
  );
}
