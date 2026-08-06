// Agent 详情页的权限单一真源。
//
// 关键点：动作 scope 由「这个 Agent 是不是你的」决定，不由角色决定。
// POST /v1/me/agents/:id/enrollments 是发放接入令牌的唯一端点（admin scope
// 没有对应物），所以 admin+ 看自己的 Agent 时动作必须切到 me scope，
// 否则管理员给自己的 Agent 发不了令牌。
//
// 唯一例外是移交：只有 POST /v1/admin/agents/:id/transfer 一个端点，
// 且要求 owner 角色，所以即使 actScope 是 "me"，移交也走 admin scope。
// 调用方在 AgentLifecycleCard 里显式处理这一个动作。

import type { AgentScope } from "../../api/actions";
import type { AgentProfile, HumanMe } from "../../api/types";
import { can } from "../../lib/capabilities";

export interface AgentAccess {
  /** 取详情用的 scope。 */
  readScope: AgentScope;
  /** 编辑、生命周期、吊销、发放用的 scope（移交除外，见文件头注释）。 */
  actScope: AgentScope;
  /** 目标 Agent 归当前用户所有。 */
  isSelf: boolean;
  retired: boolean;
  canEdit: boolean;
  canSuspend: boolean;
  canResume: boolean;
  canRetire: boolean;
  canTransfer: boolean;
  canIssue: boolean;
  canRevoke: boolean;
}

export function readScopeFor(me: HumanMe): AgentScope {
  return can(me.role, "view.all-agents") ? "admin" : "me";
}

export function resolveAgentAccess(me: HumanMe, agent: AgentProfile): AgentAccess {
  const readScope = readScopeFor(me);
  // 任一侧缺失都判为「不是自己的」：宁可少给权限，后端仍是唯一执法者。
  const isSelf =
    me.membership_id !== undefined &&
    agent.owner_membership_id !== undefined &&
    agent.owner_membership_id === me.membership_id;
  const retired = agent.status === "retired" || agent.retired_at !== undefined;
  const govern = can(me.role, "govern.any-agent");
  const suspendAny = can(me.role, "suspend.any-agent");
  const live = !retired;

  return {
    readScope,
    actScope: isSelf ? "me" : readScope,
    isSelf,
    retired,
    canEdit: live && (isSelf || govern),
    canSuspend: live && (isSelf || suspendAny),
    canResume: live && (isSelf || govern),
    canRetire: live && (isSelf || govern),
    // 移交端点只有 admin scope 且 owner-only；自己的 Agent 也不例外。
    canTransfer: live && govern,
    // 发放接入令牌没有 admin scope 端点，只有本人可以。
    canIssue: live && isSelf,
    canRevoke: live && (isSelf || suspendAny),
  };
}
