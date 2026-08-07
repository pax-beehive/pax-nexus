// 发放一次性接入令牌。
//
// 权限用人类标签呈现，但原始名以小字并排显示——API 文档与审计日志用的是
// 原始名，藏起来会让用户没法把界面和文档对上。
//
// 这个端点返回一次性密钥且不支持 Idempotency-Key（doc 3.3）：
// 4xx 保留表单让用户改；超时/5xx 关闭表单、刷新待认领列表，绝不自动重试。

import { useState } from "react";
import { createEnrollment } from "../api/actions";
import { apiError } from "../api/client";
import type { EnrollmentSecret } from "../api/types";
import { GRANTABLE_PERMISSIONS } from "../api/types";
import { Button } from "./Button";
import { Modal } from "./Modal";
import { Seg } from "./Seg";
import { useToast } from "./Toasts";

/** 原始权限名 → 人类标签。键必须覆盖 GRANTABLE_PERMISSIONS 全集。 */
const PERMISSION_LABELS: Record<string, string> = {
  observe: "Record its sessions",
  search: "Search team memory",
  get: "Read specific notes",
  channel_send: "Send to other agents",
  channel_receive: "Receive from other agents",
};

type ClaimWindow = "300" | "900" | "1800";
type KeyLifetime = "30" | "90" | "365" | "none";

const CLAIM_WINDOWS: { value: ClaimWindow; label: string }[] = [
  { value: "300", label: "5m" },
  { value: "900", label: "15m" },
  { value: "1800", label: "30m" },
];

const KEY_LIFETIMES: { value: KeyLifetime; label: string }[] = [
  { value: "30", label: "30d" },
  { value: "90", label: "90d" },
  { value: "365", label: "1y" },
  { value: "none", label: "Never" },
];

const DAY_MS = 24 * 60 * 60 * 1000;

function credentialExpiry(lifetime: KeyLifetime): string | undefined {
  if (lifetime === "none") return undefined;
  return new Date(Date.now() + Number(lifetime) * DAY_MS).toISOString();
}

export function IssueAccessModal({
  agentId,
  agentName,
  onClose,
  onCreated,
  onMaybeCreated,
}: {
  agentId: string;
  agentName: string;
  onClose: () => void;
  onCreated: (secret: EnrollmentSecret) => void;
  onMaybeCreated: () => void;
}) {
  const toast = useToast();
  const [label, setLabel] = useState("");
  const [permissions, setPermissions] = useState<string[]>(["observe", "search"]);
  const [claimWindow, setClaimWindow] = useState<ClaimWindow>("900");
  const [lifetime, setLifetime] = useState<KeyLifetime>("90");
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | undefined>();

  const submit = async () => {
    if (!label.trim()) return setFormError("Say which machine it will run on first.");
    if (permissions.length === 0) {
      return setFormError("Pick at least one thing it may do with team memory.");
    }
    setFormError(undefined);
    setBusy(true);
    try {
      const secret = await createEnrollment(agentId, {
        credential_label: label.trim(),
        // 保持 GRANTABLE_PERMISSIONS 的顺序，请求体稳定、便于比对。
        permissions: [...GRANTABLE_PERMISSIONS].filter((p) => permissions.includes(p)),
        expires_in_seconds: Number(claimWindow),
        credential_expires_at: credentialExpiry(lifetime),
      });
      onCreated(secret);
    } catch (err) {
      if (apiError(err) && err.status < 500) {
        setFormError(`Request rejected (HTTP ${err.status}); check what you entered.`);
      } else {
        toast(
          "warn",
          "Request failed and was not retried. The list has been refreshed: if a new unclaimed token appeared, use it or cancel it before issuing another one.",
        );
        onMaybeCreated();
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal title={`Issue access for ${agentName}`} onClose={onClose}>
      <label htmlFor="ia-label">Where will it run?</label>
      <input
        id="ia-label"
        type="text"
        placeholder="mac-studio-01"
        value={label}
        onChange={(e) => setLabel(e.target.value)}
      />

      <label>What may it do with team memory?</label>
      {GRANTABLE_PERMISSIONS.map((p) => (
        <label key={p} className="ck">
          <input
            type="checkbox"
            checked={permissions.includes(p)}
            onChange={(e) =>
              setPermissions((prev) => (e.target.checked ? [...prev, p] : prev.filter((x) => x !== p)))
            }
          />
          {PERMISSION_LABELS[p] ?? p} <span className="small muted mono">{p}</span>
        </label>
      ))}

      <div className="field-row">
        <div>
          <label>Claim window</label>
          <Seg
            label="Claim window"
            options={CLAIM_WINDOWS}
            value={claimWindow}
            onChange={setClaimWindow}
          />
        </div>
        <div>
          <label>Key lifetime</label>
          <Seg label="Key lifetime" options={KEY_LIFETIMES} value={lifetime} onChange={setLifetime} />
        </div>
      </div>

      {formError && <div className="note bad">{formError}</div>}
      <div className="note small">
        The token appears only once, on the next screen. This endpoint has{" "}
        <b>no retry protection</b>: if the request times out, don&apos;t resend — refresh the
        unclaimed list and make sure nothing new appeared before trying again.
      </div>
      <div className="row" style={{ justifyContent: "flex-end" }}>
        <Button variant="ghost" onClick={onClose} disabled={busy}>
          Cancel
        </Button>
        <Button variant="primary" disabled={busy} onClick={() => void submit()}>
          {busy ? "Issuing…" : "Issue one-time token"}
        </Button>
      </div>
    </Modal>
  );
}
