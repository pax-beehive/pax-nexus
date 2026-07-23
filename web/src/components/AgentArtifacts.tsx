// Enrollment issuance + enrollment/credential metadata lists, shared between
// the owner view (/v1/me/agents/...) and the admin governance view
// (/v1/admin/agents/...).

import { useState } from "react";
import {
  beginAction,
  createEnrollment,
  revokeCredential,
  revokeEnrollment,
  type AgentScope,
} from "../api/actions";
import { listCredentials, listEnrollments } from "../api/queries";
import { ApiError } from "../api/client";
import type { CredentialMetadata, EnrollmentSecret } from "../api/types";
import { GRANTABLE_PERMISSIONS } from "../api/types";
import { deriveCredentialStatus } from "../lib/credentials";
import { copyTextToClipboard } from "../lib/clipboard";
import { enrollmentConnectCommand, isSelfDescribingEnrollmentToken } from "../lib/enrollment";
import { usePagedList } from "../lib/usePagedList";
import { useErrorHandler } from "../lib/useErrorHandler";
import { formatTime } from "../lib/format";
import { validateFutureTime } from "../lib/validation";
import { Badge } from "./Badge";
import { ConfirmDialog } from "./ConfirmDialog";
import { Countdown } from "./Countdown";
import { Modal } from "./Modal";
import { SecretCard } from "./SecretCard";
import { useToast } from "./Toasts";

const ENROLLMENT_STATUSES = ["all", "pending", "consumed", "revoked", "expired"] as const;
const CREDENTIAL_STATUSES = ["all", "active", "expired", "revoked"] as const;
const ENROLLMENT_EXPIRY_OPTIONS = [
  { value: 300, label: "5 分钟" },
  { value: 900, label: "15 分钟" },
  { value: 1800, label: "30 分钟" },
] as const;

function Tabs({
  label,
  options,
  value,
  onChange,
}: {
  /** Accessible group name, e.g. "enrollment status". */
  label: string;
  options: readonly string[];
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <div className="tabs" role="group" aria-label={label}>
      {options.map((o) => (
        <button
          key={o}
          className={o === value ? "on" : ""}
          aria-pressed={o === value}
          onClick={() => onChange(o)}
        >
          {o}
        </button>
      ))}
    </div>
  );
}

function LoadMore({
  nextCursor,
  loadingMore,
  onLoadMore,
}: {
  nextCursor?: string;
  loadingMore: boolean;
  onLoadMore: () => void;
}) {
  if (!nextCursor) return null;
  return (
    <div style={{ marginTop: 10, textAlign: "center" }}>
      <button className="btn sm" disabled={loadingMore} onClick={onLoadMore}>
        {loadingMore ? "加载中…" : "加载更多"}
      </button>
    </div>
  );
}

function IssueEnrollmentModal({
  agentId,
  onClose,
  onCreated,
  onMaybeCreated,
}: {
  agentId: string;
  onClose: () => void;
  onCreated: (secret: EnrollmentSecret) => void;
  onMaybeCreated: () => void;
}) {
  const toast = useToast();
  const [label, setLabel] = useState("");
  const [permissions, setPermissions] = useState<string[]>(["observe", "search"]);
  const [expiresIn, setExpiresIn] = useState<number>(900);
  const [credExpiresAt, setCredExpiresAt] = useState("");
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | undefined>();

  const submit = async () => {
    if (!label.trim()) return setFormError("credential_label 必填");
    if (permissions.length === 0) return setFormError("permissions 必须显式选择且非空");
    const timeError = validateFutureTime(credExpiresAt);
    if (timeError) return setFormError(timeError);
    setFormError(undefined);
    setBusy(true);
    try {
      const secret = await createEnrollment(agentId, {
        credential_label: label.trim(),
        permissions,
        expires_in_seconds: expiresIn,
        credential_expires_at: credExpiresAt ? new Date(credExpiresAt).toISOString() : undefined,
      });
      onCreated(secret);
    } catch (err) {
      if (err instanceof ApiError && err.status < 500) {
        // Client-side rejection: keep the form so the user can correct it.
        setFormError(`请求被拒绝（HTTP ${err.status}），请检查输入`);
      } else {
        // Timeout/5xx: never blind-retry a one-time-secret creation. Close,
        // refresh the pending list, and let the user decide (doc 3.3).
        toast("warn", "请求失败，未自动重试。已刷新列表：若出现新的 pending 记录，可使用或吊销后重建");
        onMaybeCreated();
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal title="签发一次性 Enrollment" onClose={onClose}>
        <label htmlFor="en-label">credential_label（必填）</label>
        <input
          id="en-label"
          type="text"
          placeholder="Alice MacBook"
          value={label}
          onChange={(e) => setLabel(e.target.value)}
        />
        <label>permissions（显式选择，受部署 grantable 限制）</label>
        {GRANTABLE_PERMISSIONS.map((p) => (
          <label key={p} className="ck">
            <input
              type="checkbox"
              checked={permissions.includes(p)}
              onChange={(e) =>
                setPermissions((prev) => (e.target.checked ? [...prev, p] : prev.filter((x) => x !== p)))
              }
            />
            {p}
          </label>
        ))}
        <div className="field-row">
          <div>
            <label htmlFor="en-exp">token 有效期</label>
            <select id="en-exp" value={expiresIn} onChange={(e) => setExpiresIn(Number(e.target.value))}>
              {ENROLLMENT_EXPIRY_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label htmlFor="en-credexp">credential 过期时间（可选）</label>
            <input
              id="en-credexp"
              type="datetime-local"
              value={credExpiresAt}
              onChange={(e) => setCredExpiresAt(e.target.value)}
            />
          </div>
        </div>
        {formError && <div className="note bad">{formError}</div>}
        <div className="note small">
          此接口返回一次性 secret 且<b>不支持 Idempotency-Key</b>：网络超时不会自动重试，先刷新 pending 列表确认。
        </div>
        <div className="row" style={{ justifyContent: "flex-end" }}>
          <button className="btn ghost" onClick={onClose} disabled={busy}>
            取消
          </button>
          <button className="btn primary" disabled={busy} onClick={() => void submit()}>
            {busy ? "签发中…" : "签发"}
          </button>
        </div>
    </Modal>
  );
}

export function AgentArtifacts({
  scope,
  agentId,
  agentStatus,
  canIssue,
}: {
  scope: AgentScope;
  agentId: string;
  agentStatus: string;
  canIssue: boolean;
}) {
  const toast = useToast();
  const handleError = useErrorHandler();
  const [secret, setSecret] = useState<EnrollmentSecret | undefined>();
  const [issueOpen, setIssueOpen] = useState(false);
  const [enrollmentFilter, setEnrollmentFilter] = useState<string>("all");
  const [credentialFilter, setCredentialFilter] = useState<string>("all");
  // Pending revoke confirmation; the Idempotency-Key is bound to the action
  // instance (one per opened dialog, reused if the confirm is retried).
  const [revokeTarget, setRevokeTarget] = useState<
    | { kind: "enrollment"; id: string; key: string }
    | { kind: "credential"; id: string; key: string }
    | undefined
  >();
  const [busy, setBusy] = useState(false);

  const enrollments = usePagedList(
    (cursor) =>
      listEnrollments(scope, agentId, {
        status: enrollmentFilter === "all" ? undefined : enrollmentFilter,
        cursor,
      }),
    [scope, agentId, enrollmentFilter],
  );

  const credentials = usePagedList(
    (cursor) =>
      listCredentials(scope, agentId, {
        status: credentialFilter === "all" ? undefined : credentialFilter,
        cursor,
      }),
    [scope, agentId, credentialFilter],
  );

  const confirmRevoke = async () => {
    if (!revokeTarget || busy) return;
    setBusy(true);
    try {
      if (revokeTarget.kind === "enrollment") {
        await revokeEnrollment(scope, agentId, revokeTarget.id, revokeTarget.key);
        toast("ok", "Enrollment 已吊销");
        enrollments.reload();
      } else {
        await revokeCredential(scope, agentId, revokeTarget.id, revokeTarget.key);
        toast("ok", "Credential 已吊销，对应 API key 立即失效");
        credentials.reload();
      }
      setRevokeTarget(undefined);
    } catch (err) {
      handleError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      {secret && (
        <SecretCard
          title="一次性 Enrollment token（仅此一次显示）"
          value={secret.token}
          valueLabel=" token"
          expiresAt={secret.expires_at}
          note={
            isSelfDescribingEnrollmentToken(secret.token)
              ? "token 不会写入持久存储、日志或埋点。丢失请吊销后重新签发；token 已内嵌接入地址，客户端可直接解析；exchange 由客户端完成，Portal 永远看不到 API key。"
              : "token 不会写入持久存储、日志或埋点。丢失请吊销后重新签发；exchange 由客户端完成，Portal 永远看不到 API key。"
          }
          extraActions={
            <button
              className="btn sm"
              onClick={() => {
                const command = enrollmentConnectCommand(secret.token, window.location.origin);
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

      <div className="card">
        <div className="row between">
          <h2 style={{ margin: 0 }}>Enrollments</h2>
          {canIssue && (
            <button className="btn primary sm" onClick={() => setIssueOpen(true)}>
              + 签发一次性 Enrollment
            </button>
          )}
        </div>
        {agentStatus !== "active" && (
          <div className="note warn small">
            Agent 非 active：暂停 / retire 会立即吊销全部 Credential 和 pending Enrollment，恢复 active 不会还原旧 key。
          </div>
        )}
        <Tabs
          label="enrollment status"
          options={ENROLLMENT_STATUSES}
          value={enrollmentFilter}
          onChange={setEnrollmentFilter}
        />
        {enrollments.loading ? (
          <p className="muted small">加载中…</p>
        ) : enrollments.items.length === 0 ? (
          <p className="muted small">暂无 Enrollment。</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Label</th>
                <th>Permissions</th>
                <th>Status</th>
                <th>过期/创建</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {enrollments.items.map((e) => (
                <tr key={e.enrollment_id}>
                  <td>{e.credential_label}</td>
                  <td className="small mono">{e.permissions.join(", ")}</td>
                  <td>
                    <Badge status={e.status} />
                  </td>
                  <td className="small">
                    {e.status === "pending" ? <Countdown to={e.expires_at} /> : formatTime(e.created_at)}
                  </td>
                  <td>
                    {e.status === "pending" && (
                      <button
                        className="btn sm danger"
                        onClick={() =>
                          setRevokeTarget({ kind: "enrollment", id: e.enrollment_id, key: beginAction() })
                        }
                      >
                        吊销
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <LoadMore
          nextCursor={enrollments.nextCursor}
          loadingMore={enrollments.loadingMore}
          onLoadMore={() => void enrollments.loadMore()}
        />
      </div>

      <div className="card">
        <h2 style={{ margin: "0 0 8px" }}>Credentials（仅元数据，永不含 API key）</h2>
        <Tabs
          label="credential status"
          options={CREDENTIAL_STATUSES}
          value={credentialFilter}
          onChange={setCredentialFilter}
        />
        {credentials.loading ? (
          <p className="muted small">加载中…</p>
        ) : credentials.items.length === 0 ? (
          <p className="muted small">暂无 Credential。</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Label</th>
                <th>Permissions</th>
                <th>Status</th>
                <th>Last used</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {credentials.items.map((c: CredentialMetadata) => {
                const derived = deriveCredentialStatus(c);
                return (
                  <tr key={c.credential_id}>
                    <td>{c.label}</td>
                    <td className="small mono">{c.permissions.join(", ")}</td>
                    <td>
                      <Badge status={derived} />
                    </td>
                    <td className="small">{formatTime(c.last_used_at)}</td>
                    <td>
                      {derived === "active" && (
                        <button
                          className="btn sm danger"
                          onClick={() =>
                            setRevokeTarget({ kind: "credential", id: c.credential_id, key: beginAction() })
                          }
                        >
                          吊销
                        </button>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
        <LoadMore
          nextCursor={credentials.nextCursor}
          loadingMore={credentials.loadingMore}
          onLoadMore={() => void credentials.loadMore()}
        />
      </div>

      {issueOpen && (
        <IssueEnrollmentModal
          agentId={agentId}
          onClose={() => setIssueOpen(false)}
          onCreated={(s) => {
            setIssueOpen(false);
            setSecret(s);
            enrollments.reload();
          }}
          onMaybeCreated={() => {
            setIssueOpen(false);
            enrollments.reload();
          }}
        />
      )}

      {revokeTarget && (
        <ConfirmDialog
          title={revokeTarget.kind === "enrollment" ? "吊销 Enrollment" : "吊销 Credential"}
          consequences={
            revokeTarget.kind === "enrollment"
              ? ["该一次性 token 立即失效，未完成的客户端接入将失败", "此操作不可恢复；需要时重新签发新的 Enrollment"]
              : ["对应 API key 立即失效，持有它的 Agent 客户端将失去访问", "此操作不可恢复；需要时重新签发 Enrollment"]
          }
          confirmLabel="确认吊销"
          busy={busy}
          onConfirm={() => void confirmRevoke()}
          onClose={() => setRevokeTarget(undefined)}
        />
      )}
    </>
  );
}
