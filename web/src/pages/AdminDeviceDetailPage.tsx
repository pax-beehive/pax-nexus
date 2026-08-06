// Device detail: provisioned-agent attribution and cascade revocation
// (doc section 5.10). The detail response's `agents` array is the cascade
// preview — the revoke dialog reuses it directly, no extra request.

import { useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { apiError } from "../api/client";
import { getDevice } from "../api/queries";
import type { DeviceDetail } from "../api/types";
import { aliveProvisionedAgents } from "../lib/devices";
import { useErrorHandler } from "../lib/useErrorHandler";
import { formatTime } from "../lib/format";
import { Badge } from "../components/Badge";
import { Button } from "../components/Button";
import { RevokeDeviceModal } from "../components/RevokeDeviceModal";

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
          {/* Modernist 的 ghost 类名（Button.tsx 的 ghost variant 发的就是
              这一对）。旧的点式 `.btn.ghost` 是阶段 1 留下的兼容别名，新代码
              禁用；这里是 <Link>，所以手写类名而不是套 <Button>——保住
              中键新开、复制链接这些链接语义。 */}
          <Link to="/admin/devices" className="btn btn-ghost">
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
