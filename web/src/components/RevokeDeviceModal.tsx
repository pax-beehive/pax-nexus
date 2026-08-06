// 机器吊销确认弹窗：吊销会在同一事务里级联吊销该机器签发的全部存活 Agent
// 凭证，所以预览表与调用方展示的 Agent 行必须同源（都来自
// aliveProvisionedAgents），否则「预览行数 = 实际级联数」只能靠测试保证。

import { useRef, useState } from "react";
import { beginAction, revokeDevice } from "../api/actions";
import type { DeviceProvisionedAgent, DeviceSummary } from "../api/types";
import { useErrorHandler } from "../lib/useErrorHandler";
import { formatTime } from "../lib/format";
import { Button } from "./Button";
import { Modal } from "./Modal";
import { useToast } from "./Toasts";

export function RevokeDeviceModal({
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
