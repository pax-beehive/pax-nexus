import { Link } from "react-router-dom";
import type { AgentProfile, DeviceSummary, Member } from "../../api/types";
import { Button } from "../../components/Button";
import { EmptyState } from "../../components/EmptyState";
import { Tag } from "../../components/Tag";
import { formatTime } from "../../lib/format";

/**
 * 第 2 层：某人的机器 + 散装 Agent。「Connect a machine」「+ Create Agent」只在
 * `isSelf` 为真时渲染——注册端点是 /v1/me/device-enrollments，永远为调用者
 * 本人注册，别人的头部渲染这两个按钮就是承诺一件做不到的事（设计 §5.2）。
 *
 * 这一层没有吊销动作，这是有意的：机器行是 <button>，往里塞 <Button> 就是
 * 嵌套按钮（无效 HTML）；而且这一层只有 provisioned_agent_count 一个数，
 * 没有 Agent 明细，就地吊销的级联预览会与第 3 层的展示分成两个数据源。
 * 吊销统一在第 3 层的机器头部。
 */
export function MachinesLevel({
  person,
  devices,
  looseAgents,
  isSelf,
  onDrill,
  onCreateAgent,
  onConnectMachine,
}: {
  person: Member;
  devices: DeviceSummary[];
  looseAgents: AgentProfile[];
  isSelf: boolean;
  onDrill: (credentialId: string) => void;
  onCreateAgent: () => void;
  onConnectMachine: () => void;
}) {
  return (
    <>
      <div className="at-head">
        <div>
          <div className="row">
            <span className="at-row-name">{person.display_name}</span>
            <Tag tone="outline">{person.role}</Tag>
          </div>
          <div className="small muted">
            {person.email} · joined {formatTime(person.joined_at)}
          </div>
        </div>
        <div className="row">
          <Link to="/management/members" className="btn btn-ghost">
            Change access
          </Link>
          {/* 注册端点是 /v1/me/device-enrollments：永远为调用者本人注册，
              所以别人的头部不渲染这两个按钮。 */}
          {isSelf && <Button onClick={onConnectMachine}>Connect a machine</Button>}
          {isSelf && (
            <Button variant="primary" onClick={onCreateAgent}>
              + Create Agent
            </Button>
          )}
        </div>
      </div>

      {devices.length === 0 ? (
        <EmptyState
          title="No machines yet"
          body={
            isSelf
              ? "Connect a machine and the agents running on it register themselves."
              : `${person.display_name} has not connected a machine.`
          }
        />
      ) : (
        devices.map((device) => (
          <button
            key={device.credential_id}
            type="button"
            className="at-row at-machines"
            onClick={() => onDrill(device.credential_id)}
          >
            <span>
              <span className="at-row-name">{device.device_name}</span>
              <span className="small mono faint"> {device.credential_id}</span>
            </span>
            <span>
              <Tag tone={device.status === "active" ? "neutral" : "attention"}>
                {device.status === "active" ? "connected" : "revoked"}
              </Tag>
            </span>
            <span className="small">{device.provisioned_agent_count}</span>
            <span className="small muted">{formatTime(device.last_used_at)}</span>
            <span className="at-row-go" aria-hidden="true">
              →
            </span>
          </button>
        ))
      )}

      {looseAgents.length > 0 && (
        <>
          <p className="at-group-note">
            Registered by hand, without a machine — these keys were issued one at a time.
          </p>
          {looseAgents.map((agent) => (
            <Link
              key={agent.agent_id}
              to={`/management/agents/${encodeURIComponent(agent.agent_id)}`}
              className="at-row at-agents"
            >
              <span className="at-row-name">{agent.display_name}</span>
              <span className="small mono faint">{agent.agent_id}</span>
              <span className="small">{agent.agent_type}</span>
              <span className="small muted">{agent.status}</span>
              <span className="at-row-go" aria-hidden="true">
                →
              </span>
            </Link>
          ))}
        </>
      )}
    </>
  );
}
