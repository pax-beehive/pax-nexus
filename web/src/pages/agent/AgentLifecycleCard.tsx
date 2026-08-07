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
import { useErrorHandler } from "../../lib/useErrorHandler";
import type { AgentAccess } from "./agentScope";

type PendingKind = "suspend" | "resume" | "retire" | "transfer";
type PendingAction = { kind: PendingKind; key: string };

const DIALOG_META: Record<PendingKind, { title: string; consequence: string; confirmLabel: string }> = {
  suspend: {
    title: "Pause this agent",
    consequence: "It stops writing and reading team memory immediately. Keys are destroyed, not parked.",
    confirmLabel: "Pause and revoke keys",
  },
  resume: {
    title: "Resume running",
    consequence:
      "It can read and write again, but old keys won't come back — you'll need to issue a new enrollment token.",
    confirmLabel: "Resume running",
  },
  retire: {
    title: "Retire permanently",
    consequence: "Final. The identity can never be reactivated and the ID can't be reused.",
    confirmLabel: "Retire permanently",
  },
  transfer: {
    title: "Hand to another owner",
    consequence: "Transfers the identity and revokes every key the current owner issued.",
    confirmLabel: "Transfer and revoke keys",
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
        This agent's key list couldn't be loaded — the destruction scope may be larger than shown here.
      </p>
    );
  }
  return (
    <div className="small">
      <p>
        This will destroy{" "}
        <b>
          {credentials.length} active {credentials.length === 1 ? "key" : "keys"}
        </b>{" "}
        and{" "}
        <b>
          {enrollments.length} unclaimed {enrollments.length === 1 ? "token" : "tokens"}
        </b>
        :
      </p>
      <ul>
        {credentials.map((c) => (
          <li key={c.credential_id}>{c.label}</li>
        ))}
        {enrollments.map((e) => (
          <li key={e.enrollment_id}>{e.credential_label} (unclaimed)</li>
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
  const handleError = useErrorHandler();
  const [pending, setPending] = useState<PendingAction | undefined>();
  const [busy, setBusy] = useState(false);
  const [members, setMembers] = useState<Member[] | undefined>();
  const [targetMembershipId, setTargetMembershipId] = useState("");

  // 移交弹窗打开时才取成员列表；一个弹窗一次，选中项落本地 state。
  useEffect(() => {
    if (pending?.kind !== "transfer") return;
    setMembers(undefined);
    setTargetMembershipId("");
    listAllMembers()
      .then((list) => {
        setMembers(list.filter((m) => m.membership_id !== agent.owner_membership_id));
      })
      .catch((err: unknown) => handleError(err));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pending?.kind, pending?.key]);

  const onConflict = async () => {
    setPending(undefined);
    const fresh = await refetch();
    onChanged(fresh);
    toast("warn", "Someone saved changes before you — refreshed to the latest data.");
  };

  const runAction = async () => {
    if (!pending || busy) return;
    setBusy(true);
    try {
      if (pending.kind === "retire") {
        const updated = await retireAgent(access.actScope, agent.agent_id, agent.resource_version, pending.key);
        onChanged(updated);
        toast("warn", "Retired permanently — final, it can't be recovered.");
      } else if (pending.kind === "transfer") {
        if (!targetMembershipId) {
          toast("warn", "Choose a transfer target first");
          setBusy(false);
          return;
        }
        // 移交没有 /v1/me/... 端点，恒走 admin scope（见文件头注释）。
        const updated = await transferAgent(agent.agent_id, targetMembershipId, agent.resource_version);
        onChanged(updated);
        toast("ok", "Transferred — every key issued by the previous owner has been revoked.");
      } else {
        const status = pending.kind === "suspend" ? "suspended" : "active";
        const updated = await updateAgent(access.actScope, agent.agent_id, { status }, agent.resource_version);
        onChanged(updated);
        toast(
          pending.kind === "suspend" ? "warn" : "ok",
          pending.kind === "suspend"
            ? "Paused — keys and pending tokens have been revoked."
            : "Resumed — old keys won't come back; issue a new enrollment token.",
        );
      }
      setPending(undefined);
    } catch (err) {
      if (apiError(err, 409, "resource_version_conflict")) {
        await onConflict();
      } else {
        handleError(err);
      }
    } finally {
      setBusy(false);
    }
  };

  if (access.retired) {
    return (
      <Card title="Lifecycle">
        <p className="small muted">This identity is retired — final, it can't be recovered.</p>
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
              <div className="ag-action-name">Pause this agent</div>
              <div className="ag-action-why">It stops writing and reading team memory immediately. Keys are destroyed, not parked.</div>
            </div>
            <Button size="sm" onClick={() => setPending({ kind: "suspend", key: beginAction() })}>
              Pause
            </Button>
          </div>
        )}
        {showResume && (
          <div className="ag-action">
            <div>
              <div className="ag-action-name">Resume running</div>
              <div className="ag-action-why">
                It can read and write again, but old keys won't come back — you'll need to issue a new enrollment token.
              </div>
            </div>
            <Button size="sm" onClick={() => setPending({ kind: "resume", key: beginAction() })}>
              Resume
            </Button>
          </div>
        )}
        {access.canRetire && (
          <div className="ag-action">
            <div>
              <div className="ag-action-name">Retire permanently</div>
              <div className="ag-action-why">Final. The identity can never be reactivated and the ID can't be reused.</div>
            </div>
            <Button variant="danger" size="sm" onClick={() => setPending({ kind: "retire", key: beginAction() })}>
              Retire
            </Button>
          </div>
        )}
        {access.canTransfer && (
          <div className="ag-action">
            <div>
              <div className="ag-action-name">Hand to another owner</div>
              <div className="ag-action-why">
                Transfers the identity and revokes every key the current owner issued.
              </div>
            </div>
            <Button size="sm" onClick={() => setPending({ kind: "transfer", key: beginAction() })}>
              Transfer
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
              <label htmlFor="lc-transfer-target">Hand to</label>
              <select
                id="lc-transfer-target"
                value={targetMembershipId}
                onChange={(e) => setTargetMembershipId(e.target.value)}
              >
                <option value="">Choose a member…</option>
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
