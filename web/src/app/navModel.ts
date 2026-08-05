// 顶栏与二级导航的可见性规则。纯函数，不碰 React、不碰路由。
//
// 两类门控并存：`can()` 是客户端角色矩阵（只用于隐藏入口，后端逐请求鉴权），
// `hasServerCapability()` 是服务端下发的能力位。Overview 与 Pipeline health
// 走后者，因为它们的数据源本身就由服务端能力决定。

import type { HumanMe } from "../api/types";
import { can, hasServerCapability } from "../lib/capabilities";
import { hasTeams } from "../lib/teams";

export interface NavItem {
  to: string;
  label: string;
}

export interface NavSection {
  id: string;
  label: string;
  /** 点击顶栏项时前往的路径，等于第一个可见子项。 */
  to: string;
  items: NavItem[];
}

export function navSections(me: HumanMe): NavSection[] {
  const sections: NavSection[] = [];
  const adminLike = can(me.role, "view.members");

  if (hasServerCapability(me, "view.operations")) {
    sections.push({ id: "overview", label: "Overview", to: "/overview", items: [] });
  }

  // Management 对所有人可见。member 只看到访问树本身，根节点是他自己。
  const management: NavItem[] = [{ to: "/management", label: "Access tree" }];
  if (adminLike) {
    management.push(
      { to: "/management/members", label: "Members" },
      { to: "/management/devices", label: "Devices" },
      { to: "/management/agents", label: "Agents" },
    );
  }
  if (can(me.role, "invite.member")) {
    management.push({ to: "/management/invitations", label: "Invitations" });
  }
  sections.push({ id: "management", label: "Management", to: "/management", items: management });

  const governance: NavItem[] = [];
  if (can(me.role, "view.audit")) {
    governance.push(
      { to: "/governance/audit", label: "Audit trail" },
      { to: "/governance/sessions", label: "Session audit" },
    );
  }
  if (hasServerCapability(me, "view.operations")) {
    governance.push({ to: "/governance/pipeline", label: "Pipeline health" });
  }
  if (hasServerCapability(me, "view.team-memory")) {
    governance.push({ to: "/governance/memory", label: "Memory explorer" });
  }
  if (governance.length > 0) {
    sections.push({
      id: "governance",
      label: "Governance",
      to: governance[0].to,
      items: governance,
    });
  }

  sections.push({
    id: "apps",
    label: "Apps",
    to: "/apps/wiki",
    items: [
      { to: "/apps/wiki", label: "Wiki" },
      { to: "/apps/todos", label: "Todos" },
    ],
  });

  const settings: NavItem[] = [];
  // on-prem 没有团队概念，Team 面板整条不存在。
  if (hasTeams(me)) settings.push({ to: "/settings/team", label: "Team" });
  settings.push(
    { to: "/settings/memory", label: "Memory rules" },
    { to: "/settings/usage", label: "Model usage" },
    { to: "/settings/appearance", label: "Appearance" },
  );
  sections.push({ id: "settings", label: "Settings", to: settings[0].to, items: settings });

  return sections;
}

/** 登录后的默认落地路径：能看 Overview 就落 Overview，否则落 Management。 */
export function landingPath(me: HumanMe): string {
  return hasServerCapability(me, "view.operations") ? "/overview" : "/management";
}

/**
 * 当前路径归属哪个顶栏分区。用最长前缀匹配，这样 /management/devices/dev_01
 * 这类深层路径也能正确点亮顶栏。
 */
export function sectionForPath(
  sections: NavSection[],
  pathname: string,
): NavSection | undefined {
  let best: NavSection | undefined;
  let bestLength = 0;
  for (const section of sections) {
    const prefixes = section.items.length > 0 ? section.items.map((i) => i.to) : [section.to];
    for (const prefix of prefixes) {
      if ((pathname === prefix || pathname.startsWith(`${prefix}/`)) && prefix.length > bestLength) {
        best = section;
        bestLength = prefix.length;
      }
    }
  }
  return best;
}
