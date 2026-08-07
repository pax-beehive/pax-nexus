// Agent 详情页的密钥两卡：待认领的一次性令牌（Waiting to be claimed）与
// 活跃的长期密钥（Active keys）。
//
// `keys` 由调用方通过 useAgentKeys(scope, agentId) 取得并作为 prop 传入——
// 本组件自己不发起初始取数请求，只在三处主动发请求：吊销 / 取消（走
// access.actScope）、历史翻页（懒加载，同样走 access.actScope——见下方
// EnrollmentHistory/CredentialHistory 调用点的注释）、发放接入令牌
// （IssueAccessModal 内部固定走 me scope，因为发放端点没有 admin 对应物）。
//
// 权限判断全部来自 access（agentScope.ts 的单一真源），组件内不出现
// role === 之类的自搓判断。
//
// 历史区是两个独立的懒加载子组件：只有点开「显示历史」之后才挂载，
// usePagedList 因此只在那时才第一次调用——hook 不能条件调用，所以懒加载
// 必须拆成子组件，而不是在同一个组件里 if (showHistory) 才调 hook。
//
// 一次性密钥只活在 SecretCeremony 的 props 与渲染树里；关闭仪式不作废那条
// 待认领记录——令牌在有效期内仍可兑换，只是不再可见，toast 里说清楚这一点。

import { useState } from "react";
import { beginAction, revokeCredential, revokeEnrollment, type AgentScope } from "../../api/actions";
import { listCredentials, listEnrollments } from "../../api/queries";
import type {
  AgentProfile,
  CredentialMetadata,
  EnrollmentMetadata,
  EnrollmentSecret,
} from "../../api/types";
import { Badge } from "../../components/Badge";
import { Button } from "../../components/Button";
import { Card } from "../../components/Card";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { Countdown } from "../../components/Countdown";
import { EmptyState } from "../../components/EmptyState";
import { IssueAccessModal } from "../../components/IssueAccessModal";
import { RegionError } from "../../components/RegionError";
import { SecretCeremony } from "../../components/SecretCeremony";
import { useToast } from "../../components/Toasts";
import { deriveCredentialStatus } from "../../lib/credentials";
import { enrollmentConnectCommand } from "../../lib/enrollment";
import { formatTime } from "../../lib/format";
import { useErrorHandler } from "../../lib/useErrorHandler";
import { usePagedList } from "../../lib/usePagedList";
import type { AgentAccess } from "./agentScope";
import type { AgentKeys, KeyLeg } from "./useAgentKeys";

// ---- Waiting to be claimed ----

function EnrollmentRows({
  items,
  access,
  onCancel,
}: {
  items: EnrollmentMetadata[];
  access: AgentAccess;
  onCancel: (enrollment: EnrollmentMetadata) => void;
}) {
  return (
    <div>
      {items.map((e) => (
        <div className="ag-enrollment" key={e.enrollment_id}>
          <div>
            <div className="ag-action-name">{e.credential_label}</div>
            <div className="ag-action-why">{e.permissions.join(", ")}</div>
          </div>
          <span className="ag-enrollment-id">{e.enrollment_id}</span>
          <div className="row">
            <Countdown to={e.expires_at} />
            {access.canRevoke && (
              <Button size="sm" variant="ghost" onClick={() => onCancel(e)}>
                Cancel
              </Button>
            )}
          </div>
        </div>
      ))}
    </div>
  );
}

function EnrollmentHistory({ scope, agentId }: { scope: AgentScope; agentId: string }) {
  const list = usePagedList<EnrollmentMetadata>(
    (cursor) => listEnrollments(scope, agentId, { cursor }),
    [scope, agentId],
  );

  if (list.loading) return <p className="small muted">Loading…</p>;
  if (list.error) return <RegionError error={list.error} onRetry={list.reload} />;
  if (list.items.length === 0) {
    return <EmptyState title="No history yet" body="Cancelled tokens and claimed records will show up here." />;
  }

  return (
    <div>
      <table>
        <thead>
          <tr>
            <th>Label</th>
            <th>Permissions</th>
            <th>Status</th>
            <th>Issued</th>
            <th>Claim window ends</th>
          </tr>
        </thead>
        <tbody>
          {list.items.map((e) => (
            <tr key={e.enrollment_id}>
              <td>{e.credential_label}</td>
              <td className="small mono">{e.permissions.join(", ")}</td>
              <td>
                <Badge status={e.status} />
              </td>
              <td className="small">{formatTime(e.created_at)}</td>
              <td className="small">{formatTime(e.expires_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {list.nextCursor && (
        <div className="row" style={{ marginTop: "var(--space-2)" }}>
          <Button size="sm" disabled={list.loadingMore} onClick={() => void list.loadMore()}>
            {list.loadingMore ? "Loading…" : "Load more"}
          </Button>
        </div>
      )}
    </div>
  );
}

function EnrollmentCard({
  agent,
  access,
  leg,
  reload,
  onIssue,
}: {
  agent: AgentProfile;
  access: AgentAccess;
  leg: KeyLeg<EnrollmentMetadata>;
  reload: () => void;
  onIssue: () => void;
}) {
  const toast = useToast();
  const handleError = useErrorHandler();
  const [cancelling, setCancelling] = useState<{ enrollment: EnrollmentMetadata; key: string }>();
  const [busy, setBusy] = useState(false);
  const [showHistory, setShowHistory] = useState(false);

  const confirmCancel = async () => {
    if (!cancelling) return;
    setBusy(true);
    try {
      await revokeEnrollment(
        access.actScope,
        agent.agent_id,
        cancelling.enrollment.enrollment_id,
        cancelling.key,
      );
      toast("warn", "Cancelled this pending token.");
      setCancelling(undefined);
      reload();
    } catch (err) {
      handleError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card title="Waiting to be claimed">
      <div className="row">
        {access.canIssue && (
          <Button size="sm" onClick={onIssue}>
            Issue access
          </Button>
        )}
        <span className="ag-keys-note">One-time tokens · never stored, never re-sent</span>
      </div>

      {leg.loading ? (
        <p className="small muted">Loading…</p>
      ) : leg.error ? (
        <RegionError error={leg.error} onRetry={reload} />
      ) : (leg.items ?? []).length === 0 ? (
        <EmptyState
          title="Nothing waiting to be claimed"
          body="Issue an enrollment token — once the agent claims it, it leaves this list and a key appears under Active keys."
        />
      ) : (
        <EnrollmentRows
          items={leg.items ?? []}
          access={access}
          onCancel={(enrollment) => setCancelling({ enrollment, key: beginAction() })}
        />
      )}

      <div className="row" style={{ marginTop: "var(--space-3)" }}>
        <Button variant="ghost" size="sm" onClick={() => setShowHistory((v) => !v)}>
          {showHistory ? "Hide token history" : "Show token history"}
        </Button>
      </div>
      {/* enrollments/credentials 整域按「这个 Agent 是不是你的」定 scope，
          不是按查看者的全局权限——历史区必须和实时区（keys.enrollments，
          调用方用 access.actScope 取的）同源，否则同一张卡的两个区域会
          悄悄走两条不同的鉴权路径。不要为了「历史是读操作」的直觉改回
          access.readScope。 */}
      {showHistory && <EnrollmentHistory scope={access.actScope} agentId={agent.agent_id} />}

      {cancelling && (
        <ConfirmDialog
          title="Cancel this pending token"
          consequences={[
            "This one-time token dies immediately — any client onboarding in progress will fail.",
            "This can't be undone; issue a new token if needed.",
          ]}
          confirmLabel="Cancel token"
          busy={busy}
          onConfirm={() => void confirmCancel()}
          onClose={() => setCancelling(undefined)}
        />
      )}
    </Card>
  );
}

// ---- Active keys ----

function CredentialRows({
  items,
  access,
  onRevoke,
}: {
  items: CredentialMetadata[];
  access: AgentAccess;
  onRevoke: (credential: CredentialMetadata) => void;
}) {
  return (
    <table>
      <thead>
        <tr>
          <th>Where it runs</th>
          <th>Can do</th>
          <th>Last used</th>
          <th>Expires</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {items.map((c) => (
          <tr key={c.credential_id}>
            <td>{c.label}</td>
            <td className="small mono">{c.permissions.join(", ")}</td>
            <td className="small">{formatTime(c.last_used_at)}</td>
            <td className="small">{c.expires_at ? formatTime(c.expires_at) : "Never"}</td>
            <td>
              {access.canRevoke && (
                <Button variant="danger" size="sm" onClick={() => onRevoke(c)}>
                  Revoke
                </Button>
              )}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function CredentialHistory({ scope, agentId }: { scope: AgentScope; agentId: string }) {
  const list = usePagedList<CredentialMetadata>(
    (cursor) => listCredentials(scope, agentId, { cursor }),
    [scope, agentId],
  );

  if (list.loading) return <p className="small muted">Loading…</p>;
  if (list.error) return <RegionError error={list.error} onRetry={list.reload} />;
  if (list.items.length === 0) {
    return <EmptyState title="No history yet" body="Revoked keys will show up here." />;
  }

  return (
    <div>
      <table>
        <thead>
          <tr>
            <th>Where it runs</th>
            <th>Can do</th>
            <th>Status</th>
            <th>Last used</th>
            <th>Expires</th>
          </tr>
        </thead>
        <tbody>
          {list.items.map((c) => (
            <tr key={c.credential_id}>
              <td>{c.label}</td>
              <td className="small mono">{c.permissions.join(", ")}</td>
              <td>
                <Badge status={deriveCredentialStatus(c)} />
              </td>
              <td className="small">{formatTime(c.last_used_at)}</td>
              <td className="small">{c.expires_at ? formatTime(c.expires_at) : "Never"}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {list.nextCursor && (
        <div className="row" style={{ marginTop: "var(--space-2)" }}>
          <Button size="sm" disabled={list.loadingMore} onClick={() => void list.loadMore()}>
            {list.loadingMore ? "Loading…" : "Load more"}
          </Button>
        </div>
      )}
    </div>
  );
}

function CredentialCard({
  agent,
  access,
  leg,
  reload,
}: {
  agent: AgentProfile;
  access: AgentAccess;
  leg: KeyLeg<CredentialMetadata>;
  reload: () => void;
}) {
  const toast = useToast();
  const handleError = useErrorHandler();
  const [revoking, setRevoking] = useState<{ credential: CredentialMetadata; key: string }>();
  const [busy, setBusy] = useState(false);
  const [showHistory, setShowHistory] = useState(false);

  const confirmRevoke = async () => {
    if (!revoking) return;
    setBusy(true);
    try {
      await revokeCredential(
        access.actScope,
        agent.agent_id,
        revoking.credential.credential_id,
        revoking.key,
      );
      toast("warn", "Key revoked — the client holding it lost access immediately.");
      setRevoking(undefined);
      reload();
    } catch (err) {
      handleError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card title="Active keys">
      {leg.loading ? (
        <p className="small muted">Loading…</p>
      ) : leg.error ? (
        <RegionError error={leg.error} onRetry={reload} />
      ) : (leg.items ?? []).length === 0 ? (
        <EmptyState title="No keys yet" body="Once a token is claimed, the agent gets its first API key." />
      ) : (
        <CredentialRows
          items={leg.items ?? []}
          access={access}
          onRevoke={(credential) => setRevoking({ credential, key: beginAction() })}
        />
      )}

      <div className="row" style={{ marginTop: "var(--space-3)" }}>
        <Button variant="ghost" size="sm" onClick={() => setShowHistory((v) => !v)}>
          {showHistory ? "Hide key history" : "Show key history"}
        </Button>
      </div>
      {/* 同上（见 EnrollmentHistory 调用点注释）：历史区跟实时区
          （keys.credentials）同源，用 access.actScope，不用 access.readScope。 */}
      {showHistory && <CredentialHistory scope={access.actScope} agentId={agent.agent_id} />}

      {revoking && (
        <ConfirmDialog
          title="Revoke this key"
          consequences={[
            "This API key dies immediately — the client holding it loses access at once.",
            "This can't be undone; issue access again if needed.",
          ]}
          confirmLabel="Revoke key"
          busy={busy}
          onConfirm={() => void confirmRevoke()}
          onClose={() => setRevoking(undefined)}
        />
      )}
    </Card>
  );
}

// ---- Section ----

export function AgentKeysSection({
  agent,
  access,
  keys,
}: {
  agent: AgentProfile;
  access: AgentAccess;
  keys: AgentKeys;
}) {
  const toast = useToast();
  const [issuing, setIssuing] = useState(false);
  const [secret, setSecret] = useState<EnrollmentSecret>();

  return (
    <>
      <EnrollmentCard
        agent={agent}
        access={access}
        leg={keys.enrollments}
        reload={keys.reload}
        onIssue={() => setIssuing(true)}
      />
      <CredentialCard agent={agent} access={access} leg={keys.credentials} reload={keys.reload} />

      {issuing && (
        <IssueAccessModal
          agentId={agent.agent_id}
          agentName={agent.display_name}
          onClose={() => setIssuing(false)}
          onCreated={(s) => {
            setIssuing(false);
            setSecret(s);
            keys.reload();
          }}
          onMaybeCreated={() => {
            setIssuing(false);
            keys.reload();
          }}
        />
      )}

      {secret && (
        <SecretCeremony
          title="One-time enrollment token · shown once, stored nowhere"
          headline="Copy this now. We can't show it again."
          body="Hand the connect command to the machine that will run this agent — the token appears only on this screen."
          value={secret.token}
          valueLabel="Token"
          expiresAt={secret.expires_at}
          steps={[
            "Run the connect command on the target machine",
            "The client must finish claiming before the claim window closes",
            "Once claimed, the token burns itself and a long-lived key appears under Active keys",
          ]}
          recovery="Nothing is recoverable and nothing is broken — cancel the record under “Waiting to be claimed” and issue a new one."
          command={enrollmentConnectCommand(secret.token, window.location.origin)}
          onClose={() => {
            setSecret(undefined);
            toast(
              "ok",
              "The token is no longer visible. It can still be redeemed until it expires — cancel it under “Waiting to be claimed” to void it.",
            );
          }}
        />
      )}
    </>
  );
}
