// Device detail: provisioned-agent attribution and cascade revocation
// (doc section 5.10). The detail response's `agents` array is the cascade
// preview — the revoke dialog reuses it directly, no extra request.

import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { ApiError } from "../api/client";
import { beginAction, revokeDevice } from "../api/actions";
import { getDevice } from "../api/queries";
import type { DeviceDetail, DeviceProvisionedAgent, DeviceSummary } from "../api/types";
import { aliveProvisionedAgents } from "../lib/devices";
import { useErrorHandler } from "../lib/useErrorHandler";
import { formatTime } from "../lib/format";
import { Badge } from "../components/Badge";
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
      toast("ok", "Device 已吊销，其铸发的 Agent Credential 已级联失效");
      onDone(updated);
    } catch (err) {
      handleError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal title={`吊销 Device ${device.device_name}`} onClose={onClose}>
      <div className="note bad">
        吊销会在同一事务内级联吊销该 Device 铸发的全部活跃 Agent Credential；对应 Agent
        的旧 key 立即失效（401）。此操作不可恢复。
      </div>
      {cascade.length === 0 ? (
        <div className="note small">该 Device 当前没有仍活跃的 Agent Credential。</div>
      ) : (
        <>
          <div className="note warn small" style={{ marginBottom: 4 }}>
            级联预览：以下 {cascade.length} 个 Agent Credential 将随 Device 一起被吊销
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
        <button className="btn ghost" onClick={onClose} disabled={busy}>
          取消
        </button>
        <button className="btn danger" disabled={busy} onClick={() => void submit()}>
          {busy ? "吊销中…" : "确认吊销"}
        </button>
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
        if (err instanceof ApiError && err.status === 404) setNotFound(true);
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
          Device 不存在或不是 device 类型。<Link to="/admin/devices">返回列表</Link>
        </p>
      </div>
    );
  }
  if (!detail) return <p className="muted">加载中…</p>;

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
            <button className="btn danger" onClick={() => setRevokeOpen(true)}>
              吊销 Device
            </button>
          )}
          <Link to="/admin/devices" className="btn ghost">
            ← 返回
          </Link>
        </div>
      </div>

      <div className="card">
        <h2 style={{ margin: "0 0 8px" }}>Device</h2>
        <div className="small muted">
          创建于 {formatTime(device.created_at)}
          {device.revoked_at && <> · 吊销于 {formatTime(device.revoked_at)}</>}
          {" · 最近活动 "}
          {formatTime(device.last_used_at)}
        </div>
        <div className="small muted" style={{ marginTop: 4 }}>
          权限：<code>agent_provision</code>（固定）· grantable 上限：
          <span className="mono">{device.grantable_permissions.join(", ") || "—"}</span>
        </div>
      </div>

      <div className="card">
        <h2 style={{ margin: "0 0 8px" }}>Provisioned Agents（{alive.length}）</h2>
        {alive.length === 0 ? (
          <p className="muted small">该 Device 尚未铸发仍活跃的 Agent Credential。</p>
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
