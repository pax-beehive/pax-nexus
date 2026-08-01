// Device detail: provisioned-agent attribution and cascade revocation
// (doc section 5.10). The detail response's `agents` array is the cascade
// preview — the revoke dialog reuses it directly, no extra request.

import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { apiError } from "../api/client";
import { beginAction, revokeDevice } from "../api/actions";
import { getDevice } from "../api/queries";
import type { DeviceDetail, DeviceProvisionedAgent, DeviceSummary } from "../api/types";
import { aliveProvisionedAgents } from "../lib/devices";
import { useErrorHandler } from "../lib/useErrorHandler";
import { formatTime } from "../lib/format";
import { Badge } from "../components/Badge";
import { Button } from "../components/Button";
import { Modal } from "../components/Modal";
import { useToast } from "../components/Toasts";

function RevokeDeviceModal({
  device,
  cascade,
  onClose,
  onDone,
}: {
  device: DeviceSummary;
  /** Live provisioned agents whose credentials will be revoked with the Device. */
  cascade: DeviceProvisionedAgent[];
  onClose: () => void;
  onDone: (device: DeviceSummary) => void;
}) {
  const toast = useToast();
  const handleError = useErrorHandler();
  const [busy, setBusy] = useState(false);
  // One Idempotency-Key per dialog instance; Device revocation has no
  // resource_version / If-Match (doc section 5.10).
  const actionKeyRef = useRef(beginAction());

  const submit = async () => {
    if (busy) return;
    setBusy(true);
    try {
      const updated = await revokeDevice(device.credential_id, actionKeyRef.current);
      toast("ok", "Device revoked; the Agent Credentials it provisioned have been cascade-revoked");
      onDone(updated);
    } catch (err) {
      handleError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal title={`Revoke Device ${device.device_name}`} onClose={onClose}>
      <div className="note bad">
        Revocation cascade-revokes every active Agent Credential provisioned by this Device in the
        same transaction; the corresponding Agents&apos; existing keys stop working immediately
        (401). This action cannot be undone.
      </div>
      {cascade.length === 0 ? (
        <div className="note small">This Device currently has no active Agent Credentials.</div>
      ) : (
        <>
          <div className="note warn small" style={{ marginBottom: 4 }}>
            Cascade preview: the following {cascade.length} Agent Credentials will be revoked
            together with the Device
          </div>
          <table>
            <thead>
              <tr>
                <th>Agent</th>
                <th>Credential</th>
                <th>Last used</th>
              </tr>
            </thead>
            <tbody>
              {cascade.map((a) => (
                <tr key={a.credential_id}>
                  <td>
                    {a.display_name}
                    <div className="small mono faint">{a.agent_id}</div>
                  </td>
                  <td className="small mono">{a.credential_id}</td>
                  <td className="small">{formatTime(a.last_used_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
      <div className="row" style={{ justifyContent: "flex-end" }}>
        <Button variant="ghost" onClick={onClose} disabled={busy}>
          Cancel
        </Button>
        <Button variant="danger" disabled={busy} onClick={() => void submit()}>
          {busy ? "Revoking…" : "Confirm revocation"}
        </Button>
      </div>
    </Modal>
  );
}

export function AdminDeviceDetailPage() {
  const { credentialId = "" } = useParams();
  const handleError = useErrorHandler();
  const [detail, setDetail] = useState<DeviceDetail | undefined>();
  const [notFound, setNotFound] = useState(false);
  const [revokeOpen, setRevokeOpen] = useState(false);

  const refetch = useCallback(async () => {
    const fresh = await getDevice(credentialId);
    setDetail(fresh);
    return fresh;
  }, [credentialId]);

  useEffect(() => {
    let cancelled = false;
    getDevice(credentialId)
      .then((d) => {
        if (!cancelled) setDetail(d);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        if (apiError(err, 404)) setNotFound(true);
        else handleError(err);
      });
    return () => {
      cancelled = true;
    };
  }, [credentialId, handleError]);

  if (notFound) {
    return (
      <div className="card">
        <h2>404</h2>
        <p className="muted">
          The Device does not exist or is not of type device.{" "}
          <Link to="/admin/devices">Back to the list</Link>
        </p>
      </div>
    );
  }
  if (!detail) return <p className="muted">Loading…</p>;

  const { device } = detail;
  const alive = aliveProvisionedAgents(detail.agents);

  return (
    <>
      <div className="page-head">
        <div>
          <h1>{device.device_name}</h1>
          <div className="row small muted">
            <code>{device.credential_id}</code>
            <Badge status={device.status} />
            <span>creator: {device.created_by_user_id}</span>
          </div>
        </div>
        <div className="row">
          {device.status === "active" && (
            <Button variant="danger" onClick={() => setRevokeOpen(true)}>
              Revoke Device
            </Button>
          )}
          <Link to="/admin/devices" className="btn ghost">
            ← Back
          </Link>
        </div>
      </div>

      <div className="card">
        <h2 style={{ margin: "0 0 8px" }}>Device</h2>
        <div className="small muted">
          Created {formatTime(device.created_at)}
          {device.revoked_at && <> · Revoked {formatTime(device.revoked_at)}</>}
          {" · Last activity "}
          {formatTime(device.last_used_at)}
        </div>
        <div className="small muted" style={{ marginTop: 4 }}>
          Permissions: <code>agent_provision</code> (fixed) · grantable ceiling:{" "}
          <span className="mono">{device.grantable_permissions.join(", ") || "—"}</span>
        </div>
      </div>

      <div className="card">
        <h2 style={{ margin: "0 0 8px" }}>Provisioned Agents ({alive.length})</h2>
        {alive.length === 0 ? (
          <p className="muted small">This Device has not provisioned any active Agent Credentials yet.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Agent</th>
                <th>Type</th>
                <th>Status</th>
                <th>Last used</th>
              </tr>
            </thead>
            <tbody>
              {alive.map((a) => (
                <tr key={a.credential_id}>
                  <td>
                    <Link to={`/admin/agents/${encodeURIComponent(a.agent_id)}`}>
                      {a.display_name}
                    </Link>
                    <div className="small mono faint">{a.agent_id}</div>
                  </td>
                  <td className="small">{a.agent_type}</td>
                  <td>
                    <Badge status={a.agent_status} />
                  </td>
                  <td className="small">{formatTime(a.last_used_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {revokeOpen && (
        <RevokeDeviceModal
          device={device}
          cascade={alive}
          onClose={() => setRevokeOpen(false)}
          onDone={() => {
            setRevokeOpen(false);
            void refetch();
          }}
        />
      )}
    </>
  );
}
