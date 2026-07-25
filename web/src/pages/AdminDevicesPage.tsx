// Devices list + device enrollment issuance (doc sections 5.9, 6.6).
// A device enrollment onboards a whole machine: agents on it self-provision
// through the resulting Device Credential, which always carries exactly the
// `agent_provision` permission — the form therefore has no permission matrix.

import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { ApiError } from "../api/client";
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
import { Modal } from "../components/Modal";
import { SecretCard } from "../components/SecretCard";
import { useToast } from "../components/Toasts";

const STATUS_FILTERS = ["all", "active", "revoked"] as const;
const EXPIRY_OPTIONS = [
  { value: 300, label: "5 分钟" },
  { value: 900, label: "15 分钟" },
  { value: 1800, label: "30 分钟" },
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
      if (err instanceof ApiError && err.status < 500) {
        // Client-side rejection: keep the form so the user can correct it.
        setFormError(`请求被拒绝（HTTP ${err.status}），请检查输入`);
      } else {
        // One-time secret, no Idempotency-Key (doc 3.3): never blind retry;
        // refresh the list and let the user revoke duplicates.
        toast("warn", "请求失败，未自动重试。已刷新列表：若列表中已出现该 Device，可使用或吊销后重建");
        onMaybeCreated();
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal title="创建 Device Enrollment" onClose={onClose}>
      <label htmlFor="de-name">device_name（必填）</label>
      <input
        id="de-name"
        type="text"
        placeholder="todd-macbook-air"
        maxLength={200}
        value={deviceName}
        onChange={(e) => setDeviceName(e.target.value)}
      />
      <label htmlFor="de-exp">token 有效期</label>
      <select id="de-exp" value={expiresIn} onChange={(e) => setExpiresIn(Number(e.target.value))}>
        {EXPIRY_OPTIONS.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
      {formError && <div className="note bad">{formError}</div>}
      <div className="note small">
        Device Credential 固定只有 <code>agent_provision</code> 权限，不提供权限矩阵；它铸发的 Agent
        的权限上限由部署 grantable 配置决定。
      </div>
      <div className="note small">
        创建响应包含一次性 token，且<b>不支持 Idempotency-Key</b>：超时不会自动重试，先刷新 Devices 列表确认是否已产生记录。
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
          <p className="muted" style={{ margin: 0 }}>
            机器级接入：一个 Device Enrollment 供应整台机器，机器上的 Agent 自助铸发 Credential
          </p>
        </div>
        <button className="btn primary" onClick={() => setCreateOpen(true)}>
          + 创建 Device Enrollment
        </button>
      </div>

      {secret && (
        <SecretCard
          title="一次性 Device Enrollment token（仅此一次显示）"
          value={secret.token}
          valueLabel=" token"
          expiresAt={secret.expires_at}
          note={
            isSelfDescribingEnrollmentToken(secret.token)
              ? "token 不会写入持久存储、日志或埋点。丢失只能吊销 Device 后重新创建；token 已内嵌接入地址，在目标机器执行 paxl device connect 完成接入。"
              : "token 不会写入持久存储、日志或埋点。丢失只能吊销 Device 后重新创建；在目标机器执行 paxl device connect 完成接入。"
          }
          extraActions={
            <button
              className="btn sm"
              onClick={() => {
                const command = deviceConnectCommand(
                  secret.token,
                  window.location.origin,
                  secret.device_name,
                );
                void copyTextToClipboard(command).then((ok) => {
                  if (ok) toast("ok", "接入命令 已复制");
                  else window.prompt("手动复制：", command);
                });
              }}
            >
              复制客户端命令
            </button>
          }
          onClose={() => setSecret(undefined)}
        />
      )}

      <div className="tabs" role="group" aria-label="device status">
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
                <th>Device</th>
                <th>Creator</th>
                <th>Agents</th>
                <th>Last activity</th>
                <th>Status</th>
              </tr>
            </thead>
            <tbody>
              {list.items.map((d) => (
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
