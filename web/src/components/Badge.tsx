import type { AgentProfile } from "../api/types";

export function Badge({ status }: { status: string }) {
  return <span className={`badge b-${status}`}>{status}</span>;
}

export function RoleBadge({ role }: { role: string }) {
  return <span className="badge b-role">{role}</span>;
}

/**
 * Doc section 8.1: `provisioned_by` is present only when a Device
 * self-registered the agent. The badge is decided by field presence
 * (undefined when absent), never by truthiness of the value.
 */
export function ProvisionedByBadge({
  agent,
}: {
  agent: Pick<AgentProfile, "provisioned_by">;
}) {
  if (agent.provisioned_by === undefined) {
    return <span className="badge b-human">human-registered</span>;
  }
  return (
    <span className="badge b-device" title={`provisioned by device ${agent.provisioned_by}`}>
      device-provisioned
    </span>
  );
}
