// Lifecycle：暂停 / 恢复 / 退役 / 移交，四张（互斥后最多三张）动作卡。
//
// 暂停与恢复占同一个位置，按 agent.status 互斥；owner 看自己的活跃 Agent
// 只会看到暂停 + 退役，看不到恢复（未 suspended）也看不到移交（没有
// govern.any-agent）。全部四种可见性判断都来自 access（agentScope.ts 的
// 单一真源），组件自己不再手搓角色判断。
//
// 销毁类确认框（暂停 / 退役）里的密钥与待认领令牌清单，直接渲染调用方
// 传入的 pendingEnrollments / activeCredentials 这两个数组本身——不重取、
// 不自己数。这样「预览计数 = 页面行数」在结构上就不可能分叉，因为页面上
// 那两张列表卡渲染的正是同一个数组。数组是 undefined 表示那条取数腿失败
// 了，此时必须说「没取到」，绝不能显示成 0（在销毁确认框里把「没取到」
// 误报成「0 把」会让人以为可以安全销毁）。
//
// 移交是唯一的例外端点：POST /v1/admin/agents/:id/transfer 是移交唯一的
// 入口，没有 /v1/me/... 对应物，所以即使 access.actScope 是 "me"（owner
// 移交自己的 Agent 就是这种情况），移交也恒走 admin scope。

import { useEffect, useState } from "react";
import { beginAction, retireAgent, transferAgent, updateAgent } from "../../api/actions";
import { apiError } from "../../api/client";
import { listAllMembers } from "../../api/queries";
import type { AgentProfile, CredentialMetadata, EnrollmentMetadata, Member } from "../../api/types";
import { Button } from "../../components/Button";
import { Card } from "../../components/Card";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { useToast } from "../../components/Toasts";
import { noticeForError } from "../../lib/statusMessage";
import type { AgentAccess } from "./agentScope";

type PendingKind = "suspend" | "resume" | "retire" | "transfer";
type PendingAction = { kind: PendingKind; key: string };

const DIALOG_META: Record<PendingKind, { title: string; consequence: string; confirmLabel: string }> = {
  suspend: {
    title: "暂停这个 Agent",
    consequence: "它会立刻停止读写团队记忆。密钥被销毁，不是暂存。",
    confirmLabel: "暂停并销毁密钥",
  },
  resume: {
    title: "恢复运行",
    consequence: "恢复后它能重新读写，但旧密钥不会回来——你要重新发一次接入令牌。",
    confirmLabel: "恢复运行",
  },
  retire: {
    title: "永久退役",
    consequence: "终局。这个身份永远无法再启用，ID 也不能重用。",
    confirmLabel: "永久退役",
  },
  transfer: {
    title: "移交给别人",
    consequence: "把身份交给另一个人，并吊销当前所有者签发的每一把密钥。",
    confirmLabel: "移交并吊销密钥",
  },
};

/** 暂停与退役共用的销毁预览：直接渲染传入数组，undefined 说明那条腿没取到。 */
function DestructionPreview({
  enrollments,
  credentials,
}: {
  enrollments?: EnrollmentMetadata[];
  credentials?: CredentialMetadata[];
}) {
  if (enrollments === undefined || credentials === undefined) {
    return (
      <p className="small muted">
        这台 Agent 的密钥清单没取到，销毁范围可能比这里显示的多。
      </p>
    );
  }
  return (
    <div className="small">
      <p>
        这会销毁 <b>{credentials.length} 把活跃密钥</b> 和{" "}
        <b>{enrollments.length} 张未认领令牌</b>：
      </p>
      <ul>
        {credentials.map((c) => (
          <li key={c.credential_id}>{c.label}</li>
        ))}
        {enrollments.map((e) => (
          <li key={e.enrollment_id}>{e.credential_label}（待认领）</li>
        ))}
      </ul>
    </div>
  );
}

export function AgentLifecycleCard({
  agent,
  access,
  pendingEnrollments,
  activeCredentials,
  onChanged,
  refetch,
}: {
  agent: AgentProfile;
  access: AgentAccess;
  pendingEnrollments: EnrollmentMetadata[] | undefined;
  activeCredentials: CredentialMetadata[] | undefined;
  onChanged: (agent: AgentProfile) => void;
  refetch: () => Promise<AgentProfile>;
}) {
  const toast = useToast();
  const [pending, setPending] = useState<PendingAction | undefined>();
  const [busy, setBusy] = useState(false);
  const [members, setMembers] = useState<Member[] | undefined>();
  const [targetMembershipId, setTargetMembershipId] = useState("");

  // 这张卡不用 useErrorHandler：它经 useAuth 依赖 AuthContext，会把 401/403
  // 特判成登出/重取 /v1/me。那两种恢复动作属于页面外壳的职责（已经在更外
  // 层处理过一次），这里只管把非 409 错误映射成一条 toast，避免这张纯受控
  // 组件额外要求调用方套一层 AuthProvider 才能渲染。
  const reportError = (err: unknown) => {
    const notice = noticeForError(err);
    toast(notice.kind, notice.message);
  };

  // 移交弹窗打开时才取成员列表；一个弹窗一次，选中项落本地 state。
  useEffect(() => {
    if (pending?.kind !== "transfer") return;
    setMembers(undefined);
    setTargetMembershipId("");
    listAllMembers()
      .then((list) => {
        setMembers(list.filter((m) => m.membership_id !== agent.owner_membership_id));
      })
      .catch((err: unknown) => reportError(err));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pending?.kind, pending?.key]);

  const onConflict = async () => {
    setPending(undefined);
    const fresh = await refetch();
    onChanged(fresh);
    toast("warn", "有人在你之前改过它，已刷新到最新数据。");
  };

  const runAction = async () => {
    if (!pending || busy) return;
    setBusy(true);
    try {
      if (pending.kind === "retire") {
        const updated = await retireAgent(access.actScope, agent.agent_id, agent.resource_version, pending.key);
        onChanged(updated);
        toast("warn", "已永久退役——终态，无法恢复。");
      } else if (pending.kind === "transfer") {
        if (!targetMembershipId) {
          toast("warn", "请先选择移交目标");
          setBusy(false);
          return;
        }
        // 移交没有 /v1/me/... 端点，恒走 admin scope（见文件头注释）。
        const updated = await transferAgent(agent.agent_id, targetMembershipId, agent.resource_version);
        onChanged(updated);
        toast("ok", "已移交，原所有者签发的密钥已全部吊销。");
      } else {
        const status = pending.kind === "suspend" ? "suspended" : "active";
        const updated = await updateAgent(access.actScope, agent.agent_id, { status }, agent.resource_version);
        onChanged(updated);
        toast(
          pending.kind === "suspend" ? "warn" : "ok",
          pending.kind === "suspend"
            ? "已暂停；密钥与待认领令牌已被吊销。"
            : "已恢复运行；旧密钥不会回来，需重新发放接入令牌。",
        );
      }
      setPending(undefined);
    } catch (err) {
      if (apiError(err, 409, "resource_version_conflict")) {
        await onConflict();
      } else {
        reportError(err);
      }
    } finally {
      setBusy(false);
    }
  };

  if (access.retired) {
    return (
      <Card title="Lifecycle">
        <p className="small muted">这个身份已退役——终态，无法恢复。</p>
      </Card>
    );
  }

  const showSuspend = access.canSuspend && agent.status === "active";
  const showResume = access.canResume && agent.status === "suspended";
  const meta = pending ? DIALOG_META[pending.kind] : undefined;

  return (
    <Card title="Lifecycle">
      <div className="ag-actions">
        {showSuspend && (
          <div className="ag-action">
            <div>
              <div className="ag-action-name">暂停这个 Agent</div>
              <div className="ag-action-why">它会立刻停止读写团队记忆。密钥被销毁，不是暂存。</div>
            </div>
            <Button size="sm" onClick={() => setPending({ kind: "suspend", key: beginAction() })}>
              暂停
            </Button>
          </div>
        )}
        {showResume && (
          <div className="ag-action">
            <div>
              <div className="ag-action-name">恢复运行</div>
              <div className="ag-action-why">
                恢复后它能重新读写，但旧密钥不会回来——你要重新发一次接入令牌。
              </div>
            </div>
            <Button size="sm" onClick={() => setPending({ kind: "resume", key: beginAction() })}>
              恢复
            </Button>
          </div>
        )}
        {access.canRetire && (
          <div className="ag-action">
            <div>
              <div className="ag-action-name">永久退役</div>
              <div className="ag-action-why">终局。这个身份永远无法再启用，ID 也不能重用。</div>
            </div>
            <Button variant="danger" size="sm" onClick={() => setPending({ kind: "retire", key: beginAction() })}>
              退役
            </Button>
          </div>
        )}
        {access.canTransfer && (
          <div className="ag-action">
            <div>
              <div className="ag-action-name">移交给别人</div>
              <div className="ag-action-why">
                把身份交给另一个人，并吊销当前所有者签发的每一把密钥。
              </div>
            </div>
            <Button size="sm" onClick={() => setPending({ kind: "transfer", key: beginAction() })}>
              移交
            </Button>
          </div>
        )}
      </div>

      {pending && meta && (
        <ConfirmDialog
          title={meta.title}
          consequences={[meta.consequence]}
          confirmLabel={meta.confirmLabel}
          busy={busy}
          onConfirm={() => void runAction()}
          onClose={() => setPending(undefined)}
        >
          {(pending.kind === "suspend" || pending.kind === "retire") && (
            <DestructionPreview enrollments={pendingEnrollments} credentials={activeCredentials} />
          )}
          {pending.kind === "transfer" && (
            <div>
              <label htmlFor="lc-transfer-target">交给谁</label>
              <select
                id="lc-transfer-target"
                value={targetMembershipId}
                onChange={(e) => setTargetMembershipId(e.target.value)}
              >
                <option value="">选择成员…</option>
                {members?.map((m) => (
                  <option key={m.membership_id} value={m.membership_id}>
                    {m.display_name}
                  </option>
                ))}
              </select>
            </div>
          )}
        </ConfirmDialog>
      )}
    </Card>
  );
}
