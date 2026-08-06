// AgentHeader：面包屑 + 标题 + 归属行 + 两个跳转按钮。
//
// 机器归属解析是可有可无的锦上添花：只有 admin 视角（access.readScope ===
// "admin"）且 agent 确实是被 Device 自助注册的（provisioned_by 存在）才
// 发起 getDevice 请求换机器名；member 视角绝不能碰 /v1/admin/devices，
// 请求失败时也只静默退回既有的 ProvisionedByBadge，不调 useErrorHandler
// 弹全局错误打扰用户。

import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { getDevice } from "../../api/queries";
import type { AgentProfile, HumanMe } from "../../api/types";
import { Badge, ProvisionedByBadge } from "../../components/Badge";
import { Crumbs } from "../../components/Crumbs";
import { Tag } from "../../components/Tag";
import { can, hasServerCapability } from "../../lib/capabilities";
import type { AgentAccess } from "./agentScope";

export function AgentHeader({
  agent,
  access,
  me,
}: {
  agent: AgentProfile;
  access: AgentAccess;
  me: HumanMe;
}) {
  const [deviceName, setDeviceName] = useState<string | undefined>();

  useEffect(() => {
    setDeviceName(undefined);
    if (agent.provisioned_by === undefined || access.readScope !== "admin") return;
    let cancelled = false;
    getDevice(agent.provisioned_by)
      .then((detail) => {
        if (!cancelled) setDeviceName(detail.device.device_name);
      })
      .catch(() => {
        // 静默降级：机器名只是锦上添花，取不到就退回 ProvisionedByBadge。
        if (!cancelled) setDeviceName(undefined);
      });
    return () => {
      cancelled = true;
    };
  }, [agent.provisioned_by, access.readScope]);

  const showSessions = can(me.role, "view.audit");
  const showMemory = hasServerCapability(me, "view.team-memory");
  const resolvedMachine =
    agent.provisioned_by !== undefined && access.readScope === "admin" && deviceName !== undefined;

  return (
    <>
      <Crumbs items={[{ label: "访问树", to: "/management" }, { label: agent.display_name }]} />
      <div className="ag-head">
        <div>
          <div className="row">
            <h1>{agent.display_name}</h1>
            <Badge status={agent.status} />
            <Tag tone="outline">{agent.agent_type}</Tag>
          </div>
          <div className="ag-head-facts">
            <code>{agent.agent_id}</code>
            <span>归 {agent.owner_membership_id ?? "—"} 所有</span>
            {resolvedMachine ? (
              <span>在 {deviceName} 上自助注册 · 继承那台机器的授权</span>
            ) : (
              <ProvisionedByBadge agent={agent} />
            )}
          </div>
        </div>
        <div className="row">
          {showSessions && (
            <Link to={`/governance/sessions?agent=${agent.agent_id}`} className="btn btn-ghost">
              查看它的会话
            </Link>
          )}
          {showMemory && (
            <Link to={`/governance/memory?agent=${agent.agent_id}`} className="btn btn-ghost">
              查看它的记忆
            </Link>
          )}
        </div>
      </div>
    </>
  );
}
