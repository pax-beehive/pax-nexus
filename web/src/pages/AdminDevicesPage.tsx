// Devices list + device enrollment issuance (doc sections 5.9, 6.6).
// A device enrollment onboards a whole machine: agents on it self-provision
// through the resulting Device Credential, which always carries exactly the
// `agent_provision` permission — the form therefore has no permission matrix.

import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { listAllMembers, listDevices } from "../api/queries";
import type { DeviceEnrollmentSecret, Member } from "../api/types";
import { deviceConnectCommand, isSelfDescribingEnrollmentToken } from "../lib/enrollment";
import { copyTextToClipboard } from "../lib/clipboard";
import { usePagedList } from "../lib/usePagedList";
import { useErrorHandler } from "../lib/useErrorHandler";
import { formatTime } from "../lib/format";
import { Badge } from "../components/Badge";
import { Button } from "../components/Button";
import { CreateDeviceEnrollmentModal } from "../components/CreateDeviceEnrollmentModal";
import { PagedListCard } from "../components/PagedListCard";
import { SecretCard } from "../components/SecretCard";
import { useToast } from "../components/Toasts";

const STATUS_FILTERS = ["all", "active", "revoked"] as const;

export function AdminDevicesPage() {
  const toast = useToast();
  const handleError = useErrorHandler();
  const [filter, setFilter] = useState<string>("all");
  const [createOpen, setCreateOpen] = useState(false);
  const [secret, setSecret] = useState<DeviceEnrollmentSecret | undefined>();
  const [members, setMembers] = useState<Member[]>([]);
  const list = usePagedList(
    (cursor) => listDevices({ status: filter === "all" ? undefined : filter, cursor }),
    [filter],
  );

  useEffect(() => {
    if (list.error) handleError(list.error);
  }, [list.error, handleError]);

  // Best-effort creator label enrichment; the raw ID remains the fallback.
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

  const creatorLabel = useMemo(() => {
    const map = new Map(members.map((m) => [m.membership_id, m.email ?? m.membership_id]));
    return (membershipId: string, userId: string) => map.get(membershipId) ?? userId;
  }, [members]);

  return (
    <>
      <div className="page-head">
        <div>
          <p className="card-kicker">MANAGEMENT · DEVICES</p>
          <h1>Devices</h1>
          <p className="muted flush">
            Machine-level onboarding: one Device Enrollment provisions an entire machine, and the
            Agents on it self-mint Credentials
          </p>
        </div>
        <Button variant="primary" onClick={() => setCreateOpen(true)}>
          + Create Device Enrollment
        </Button>
      </div>

      {secret && (
        <SecretCard
          title="One-time Device Enrollment token (shown only once)"
          value={secret.token}
          valueLabel=" token"
          expiresAt={secret.expires_at}
          note={
            isSelfDescribingEnrollmentToken(secret.token)
              ? "The token is never written to durable storage, logs, or analytics. If it is lost, you can only revoke the Device and create a new enrollment; the token embeds the connect address — run paxl device connect on the target machine to finish onboarding."
              : "The token is never written to durable storage, logs, or analytics. If it is lost, you can only revoke the Device and create a new enrollment; run paxl device connect on the target machine to finish onboarding."
          }
          extraActions={
            <Button
              size="sm"
              onClick={() => {
                const command = deviceConnectCommand(
                  secret.token,
                  window.location.origin,
                  secret.device_name,
                );
                void copyTextToClipboard(command).then((ok) => {
                  if (ok) toast("ok", "Connect command copied");
                  else window.prompt("Copy manually:", command);
                });
              }}
            >
              Copy client command
            </Button>
          }
          onClose={() => setSecret(undefined)}
        />
      )}

      <div className="seg" role="group" aria-label="device status">
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
        columns={["Device", "Creator", "Agents", "Last activity", "Status", ""]}
        emptyText={filter === "all" ? "No devices yet" : "No matching records."}
        renderRow={(d) => (
          <tr key={d.credential_id}>
            <td>
              <Link to={`/admin/devices/${encodeURIComponent(d.credential_id)}`}>
                <span className="at-row-name">{d.device_name}</span>
              </Link>
              <div className="small mono faint">{d.credential_id}</div>
            </td>
            <td className="small">
              {creatorLabel(d.created_by_membership_id, d.created_by_user_id)}
            </td>
            <td className="small">{d.provisioned_agent_count}</td>
            <td className="small">{formatTime(d.last_used_at)}</td>
            <td>
              <Badge status={d.status} />
            </td>
            <td className="at-row-go" aria-hidden="true">
              →
            </td>
          </tr>
        )}
      />

      {createOpen && (
        <CreateDeviceEnrollmentModal
          onClose={() => setCreateOpen(false)}
          onCreated={(s) => {
            setCreateOpen(false);
            // The one-time token is held in memory only, never persisted.
            setSecret(s);
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
