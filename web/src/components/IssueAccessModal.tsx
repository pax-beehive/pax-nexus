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
  observe: "记录它的会话",
  search: "检索团队记忆",
  get: "读取指定笔记",
  channel_send: "发送给其他 Agent",
  channel_receive: "接收其他 Agent 的消息",
};

type ClaimWindow = "300" | "900" | "1800";
type KeyLifetime = "30" | "90" | "365" | "none";

const CLAIM_WINDOWS: { value: ClaimWindow; label: string }[] = [
  { value: "300", label: "5 分钟" },
  { value: "900", label: "15 分钟" },
  { value: "1800", label: "30 分钟" },
];

const KEY_LIFETIMES: { value: KeyLifetime; label: string }[] = [
  { value: "30", label: "30 天" },
  { value: "90", label: "90 天" },
  { value: "365", label: "1 年" },
  { value: "none", label: "不过期" },
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
    if (!label.trim()) return setFormError("得先说清楚它在哪台机器上跑。");
    if (permissions.length === 0) {
      return setFormError("至少要选一项它能对团队记忆做的事。");
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
        setFormError(`请求被拒绝（HTTP ${err.status}），检查一下填的内容。`);
      } else {
        toast(
          "warn",
          "请求失败，没有自动重试。列表已刷新：如果多出一条待认领记录，用它或取消它再重发一次。",
        );
        onMaybeCreated();
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal title={`给 ${agentName} 发放接入权限`} onClose={onClose}>
      <label htmlFor="ia-label">它会在哪台机器上跑？</label>
      <input
        id="ia-label"
        type="text"
        placeholder="mac-studio-01"
        value={label}
        onChange={(e) => setLabel(e.target.value)}
      />

      <label>它可以对团队记忆做什么？</label>
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
          <label>认领窗口</label>
          <Seg
            label="认领窗口"
            options={CLAIM_WINDOWS}
            value={claimWindow}
            onChange={setClaimWindow}
          />
        </div>
        <div>
          <label>密钥有效期</label>
          <Seg label="密钥有效期" options={KEY_LIFETIMES} value={lifetime} onChange={setLifetime} />
        </div>
      </div>

      {formError && <div className="note bad">{formError}</div>}
      <div className="note small">
        令牌只在下一屏出现一次。这个端点<b>不支持重试保护</b>：如果请求超时，别重发——
        刷新待认领列表，确认没多出记录再操作。
      </div>
      <div className="row" style={{ justifyContent: "flex-end" }}>
        <Button variant="ghost" onClick={onClose} disabled={busy}>
          取消
        </Button>
        <Button variant="primary" disabled={busy} onClick={() => void submit()}>
          {busy ? "发放中…" : "发放一次性令牌"}
        </Button>
      </div>
    </Modal>
  );
}
