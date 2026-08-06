import { useState } from "react";
import { Link } from "react-router-dom";
import type { DeviceProvisionedAgent, DeviceSummary, Member } from "../../api/types";
import { Button } from "../../components/Button";
import { EmptyState } from "../../components/EmptyState";
import { RevokeDeviceModal } from "../../components/RevokeDeviceModal";
import { Tag } from "../../components/Tag";
import { formatTime } from "../../lib/format";

/**
 * 第 3 层：某机器的 Agent。吊销动作在这里的机器头部——展示行与吊销预览
 * 都来自同一次 `getDevice` + 同一个 `aliveProvisionedAgents`（调用方传入的
 * `agents` 已经过滤好），所以「预览行数 = 实际级联数」是结构性的。
 */
export function DeviceAgentsLevel({
  person,
  device,
  agents,
  onRevoked,
}: {
  person: Member;
  device: DeviceSummary;
  /** 已经过 aliveProvisionedAgents 过滤——与级联预览同源。 */
  agents: DeviceProvisionedAgent[];
  onRevoked: () => void;
}) {
  const [revoking, setRevoking] = useState(false);

  return (
    <>
      <div className="at-head">
        <div>
          <div className="row">
            <span className="at-row-name">{device.device_name}</span>
            <Tag tone={device.status === "active" ? "neutral" : "attention"}>
              {device.status === "active" ? "connected" : "revoked"}
            </Tag>
          </div>
          <div className="small muted">
            <span className="mono">{device.credential_id}</span> · last used{" "}
            {formatTime(device.last_used_at)} · trusted by {person.display_name}
          </div>
        </div>
        {device.status === "active" && (
          <Button variant="danger" onClick={() => setRevoking(true)}>
            Revoke this machine
          </Button>
        )}
      </div>

      {agents.length === 0 ? (
        <EmptyState
          title="No agents registered yet"
          body="This machine has not registered any agent that still holds a live key."
        />
      ) : (
        agents.map((agent) => (
          <Link
            key={agent.credential_id}
            to={`/management/agents/${encodeURIComponent(agent.agent_id)}`}
            className="at-row at-agents"
          >
            <span className="at-row-name">{agent.display_name}</span>
            <span className="small mono faint">{agent.agent_id}</span>
            <span className="small">{agent.agent_type}</span>
            <span className="small muted">last used {formatTime(agent.last_used_at)}</span>
            <span className="at-row-go" aria-hidden="true">
              →
            </span>
          </Link>
        ))
      )}

      {revoking && (
        <RevokeDeviceModal
          device={device}
          cascade={agents}
          onClose={() => setRevoking(false)}
          onDone={() => {
            setRevoking(false);
            onRevoked();
          }}
        />
      )}
    </>
  );
}
