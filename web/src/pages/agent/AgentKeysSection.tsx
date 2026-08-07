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
                取消
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

  if (list.loading) return <p className="small muted">加载中…</p>;
  if (list.error) return <RegionError error={list.error} onRetry={list.reload} />;
  if (list.items.length === 0) {
    return <EmptyState title="还没有历史记录" body="取消过的令牌和已认领的记录会出现在这里。" />;
  }

  return (
    <div>
      <table>
        <thead>
          <tr>
            <th>标签</th>
            <th>权限</th>
            <th>状态</th>
            <th>发放于</th>
            <th>认领窗口到期</th>
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
            {list.loadingMore ? "加载中…" : "加载更多"}
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
      toast("warn", "已取消这张待认领令牌。");
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
            发放接入权限
          </Button>
        )}
        <span className="ag-keys-note">一次性令牌 · 从不存储，也不会再次发送</span>
      </div>

      {leg.loading ? (
        <p className="small muted">加载中…</p>
      ) : leg.error ? (
        <RegionError error={leg.error} onRetry={reload} />
      ) : (leg.items ?? []).length === 0 ? (
        <EmptyState
          title="还没有待认领的令牌"
          body="发放一张接入令牌，Agent 认领后这里会消失，Active keys 里会多一把。"
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
          {showHistory ? "隐藏令牌历史" : "显示令牌历史"}
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
          title="取消这张待认领令牌"
          consequences={[
            "这张一次性令牌立刻失效，正在进行的客户端接入会失败",
            "无法撤销；需要的话重新发一张",
          ]}
          confirmLabel="确认取消"
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
          <th>在哪运行</th>
          <th>能做什么</th>
          <th>最近使用</th>
          <th>到期</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {items.map((c) => (
          <tr key={c.credential_id}>
            <td>{c.label}</td>
            <td className="small mono">{c.permissions.join(", ")}</td>
            <td className="small">{formatTime(c.last_used_at)}</td>
            <td className="small">{c.expires_at ? formatTime(c.expires_at) : "不过期"}</td>
            <td>
              {access.canRevoke && (
                <Button variant="danger" size="sm" onClick={() => onRevoke(c)}>
                  吊销
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

  if (list.loading) return <p className="small muted">加载中…</p>;
  if (list.error) return <RegionError error={list.error} onRetry={list.reload} />;
  if (list.items.length === 0) {
    return <EmptyState title="还没有历史记录" body="吊销过的密钥会出现在这里。" />;
  }

  return (
    <div>
      <table>
        <thead>
          <tr>
            <th>在哪运行</th>
            <th>能做什么</th>
            <th>状态</th>
            <th>最近使用</th>
            <th>到期</th>
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
              <td className="small">{c.expires_at ? formatTime(c.expires_at) : "不过期"}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {list.nextCursor && (
        <div className="row" style={{ marginTop: "var(--space-2)" }}>
          <Button size="sm" disabled={list.loadingMore} onClick={() => void list.loadMore()}>
            {list.loadingMore ? "加载中…" : "加载更多"}
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
      toast("warn", "已吊销这把密钥，持有它的客户端立即失去访问。");
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
        <p className="small muted">加载中…</p>
      ) : leg.error ? (
        <RegionError error={leg.error} onRetry={reload} />
      ) : (leg.items ?? []).length === 0 ? (
        <EmptyState title="还没有任何密钥" body="认领令牌后，Agent 会长出第一把 API 密钥。" />
      ) : (
        <CredentialRows
          items={leg.items ?? []}
          access={access}
          onRevoke={(credential) => setRevoking({ credential, key: beginAction() })}
        />
      )}

      <div className="row" style={{ marginTop: "var(--space-3)" }}>
        <Button variant="ghost" size="sm" onClick={() => setShowHistory((v) => !v)}>
          {showHistory ? "隐藏密钥历史" : "显示密钥历史"}
        </Button>
      </div>
      {/* 同上（见 EnrollmentHistory 调用点注释）：历史区跟实时区
          （keys.credentials）同源，用 access.actScope，不用 access.readScope。 */}
      {showHistory && <CredentialHistory scope={access.actScope} agentId={agent.agent_id} />}

      {revoking && (
        <ConfirmDialog
          title="吊销这把密钥"
          consequences={[
            "这把 API 密钥立刻失效，持有它的客户端立即失去访问",
            "无法撤销；需要的话重新发一次接入权限",
          ]}
          confirmLabel="确认吊销"
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
          title="一次性接入令牌 · 只展示一次，不存任何地方"
          headline="现在就复制。我们没法再给你看一次。"
          body="把接入命令交给要跑这个 Agent 的机器；令牌只在这一屏出现一次。"
          value={secret.token}
          valueLabel="令牌"
          expiresAt={secret.expires_at}
          steps={[
            "在目标机器上跑接入命令",
            "客户端要在认领窗口关闭前完成认领",
            "认领后令牌失效，Active keys 里会多出一把长期密钥",
          ]}
          recovery="没保存也不用慌——去「待认领」里取消这条记录，再发一次新的。"
          command={enrollmentConnectCommand(secret.token, window.location.origin)}
          onClose={() => {
            setSecret(undefined);
            toast(
              "ok",
              "令牌不再可见。它仍在有效期内可被兑换——要作废就在「待认领」里取消它。",
            );
          }}
        />
      )}
    </>
  );
}
