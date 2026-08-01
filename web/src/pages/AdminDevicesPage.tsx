// Devices list + device enrollment issuance (doc sections 5.9, 6.6).
// A device enrollment onboards a whole machine: agents on it self-provision
// through the resulting Device Credential, which always carries exactly the
// `agent_provision` permission — the form therefore has no permission matrix.

import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { apiError } from "../api/client";
import { createDeviceEnrollment } from "../api/actions";
import { listAllMembers, listDevices } from "../api/queries";
import type { DeviceEnrollmentSecret, Member } from "../api/types";
import { deviceConnectCommand, isSelfDescribingEnrollmentToken } from "../lib/enrollment";
import { copyTextToClipboard } from "../lib/clipboard";
import { usePagedList } from "../lib/usePagedList";
import { useErrorHandler } from "../lib/useErrorHandler";
import { formatTime } from "../lib/format";
import { validateDeviceName } from "../lib/validation";
import { Badge } from "../components/Badge";
import { Button } from "../components/Button";
import { Modal } from "../components/Modal";
import { PagedListCard } from "../components/PagedListCard";
import { SecretCard } from "../components/SecretCard";
import { useToast } from "../components/Toasts";

const STATUS_FILTERS = ["all", "active", "revoked"] as const;
const EXPIRY_OPTIONS = [
  { value: 300, label: "5 minutes" },
  { value: 900, label: "15 minutes" },
  { value: 1800, label: "30 minutes" },
] as const;

function CreateDeviceEnrollmentModal({
  onClose,
  onCreated,
  onMaybeCreated,
}: {
  onClose: () => void;
  onCreated: (secret: DeviceEnrollmentSecret) => void;
  onMaybeCreated: () => void;
}) {
  const toast = useToast();
  const [deviceName, setDeviceName] = useState("");
  const [expiresIn, setExpiresIn] = useState<number>(900);
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | undefined>();

  const submit = async () => {
    const nameError = validateDeviceName(deviceName);
    if (nameError) return setFormError(nameError);
    setFormError(undefined);
    setBusy(true);
    try {
      const secret = await createDeviceEnrollment({
        device_name: deviceName.trim(),
        expires_in_seconds: expiresIn,
      });
      onCreated(secret);
    } catch (err) {
      if (apiError(err) && err.status < 500) {
        // Client-side rejection: keep the form so the user can correct it.
        setFormError(`Request rejected (HTTP ${err.status}); please check the input`);
      } else {
        // One-time secret, no Idempotency-Key (doc 3.3): never blind retry;
        // refresh the list and let the user revoke duplicates.
        toast("warn", "Request failed; no automatic retry. The list has been refreshed: if the Device already appears in it, use it or revoke it and create a new one");
        onMaybeCreated();
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal title="Create Device Enrollment" onClose={onClose}>
      <label htmlFor="de-name">device_name (required)</label>
      <input
        id="de-name"
        type="text"
        placeholder="todd-macbook-air"
        maxLength={200}
        value={deviceName}
        onChange={(e) => setDeviceName(e.target.value)}
      />
      <label htmlFor="de-exp">Token lifetime</label>
      <select id="de-exp" value={expiresIn} onChange={(e) => setExpiresIn(Number(e.target.value))}>
        {EXPIRY_OPTIONS.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
      {formError && <div className="note bad">{formError}</div>}
      <div className="note small">
        A Device Credential always carries only the <code>agent_provision</code> permission; no
        permission matrix is offered. The permission ceiling of the Agents it provisions is set by
        the deployment&apos;s grantable configuration.
      </div>
      <div className="note small">
        The create response contains a one-time token and <b>does not support Idempotency-Key</b>:
        timeouts are not retried automatically. Refresh the Devices list first to check whether a
        record was created.
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
        columns={["Device", "Creator", "Agents", "Last activity", "Status"]}
        emptyText={filter === "all" ? "No devices yet" : "No matching records."}
        renderRow={(d) => (
          <tr key={d.credential_id}>
            <td>
              <Link to={`/admin/devices/${encodeURIComponent(d.credential_id)}`}>
                {d.device_name}
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
