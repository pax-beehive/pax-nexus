// 访问树的纯派生：三个列表快照 → 各层需要的分组与计数。
// 全部是无副作用的纯函数，React 一概不进来。

import type { AgentProfile, DeviceSummary, Member, Role } from "../../api/types";

/**
 * 手工注册（散装）的 Agent：后端对人工注册的 Agent 整个省略 provisioned_by
 * （api/types.ts:100-106），所以判断的是字段存在性，不是真值。
 */
export function isLooseAgent(agent: AgentProfile): boolean {
  return agent.provisioned_by === undefined;
}

export function devicesOf(devices: DeviceSummary[], membershipId: string): DeviceSummary[] {
  return devices.filter((device) => device.created_by_membership_id === membershipId);
}

export function agentsOf(agents: AgentProfile[], membershipId: string): AgentProfile[] {
  return agents.filter((agent) => agent.owner_membership_id === membershipId);
}

export function looseAgentsOf(agents: AgentProfile[], membershipId: string): AgentProfile[] {
  return agentsOf(agents, membershipId).filter(isLooseAgent);
}

export interface PeopleCounts {
  total: number;
  owners: number;
  admins: number;
  members: number;
}

export function summarizePeople(members: Member[]): PeopleCounts {
  const byRole = (role: Role) => members.filter((member) => member.role === role).length;
  return {
    total: members.length,
    owners: byRole("owner"),
    admins: byRole("admin"),
    members: byRole("member"),
  };
}

export interface MachineCounts {
  total: number;
  connected: number;
  revoked: number;
  peopleWithoutMachine: number;
}

export function summarizeMachines(devices: DeviceSummary[], members: Member[]): MachineCounts {
  // 「没有机器」问的是有没有登记过，不是有没有能用的：把吊销过机器的人
  // 混进这个数会让 admin 以为那个人从没连过。
  const withMachine = new Set(devices.map((device) => device.created_by_membership_id));
  return {
    total: devices.length,
    connected: devices.filter((device) => device.status === "active").length,
    revoked: devices.filter((device) => device.status === "revoked").length,
    peopleWithoutMachine: members.filter((member) => !withMachine.has(member.membership_id)).length,
  };
}

export interface AgentCounts {
  total: number;
  active: number;
  suspended: number;
  retired: number;
}

export function summarizeAgents(agents: AgentProfile[]): AgentCounts {
  const byStatus = (status: AgentProfile["status"]) =>
    agents.filter((agent) => agent.status === status).length;
  return {
    total: agents.length,
    active: byStatus("active"),
    suspended: byStatus("suspended"),
    retired: byStatus("retired"),
  };
}
