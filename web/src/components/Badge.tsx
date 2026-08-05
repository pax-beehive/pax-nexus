import type { AgentProfile } from "../api/types";

/**
 * 两色制：需要人处理的状态用 attention（朱红），其余一律 neutral。
 * 终态（retired / removed / revoked / expired）不需要人处理，因此是 neutral。
 */
const ATTENTION_STATUSES = new Set(["suspended", "pending"]);

function toneFor(status: string): string {
  return ATTENTION_STATUSES.has(status) ? "tag-attention" : "tag-neutral";
}

export function Badge({ status }: { status: string }) {
  return <span className={`tag ${toneFor(status)}`}>{status}</span>;
}

/** owner / admin 用描边强调，member 是常态。 */
export function RoleBadge({ role }: { role: string }) {
  const tone = role === "member" ? "tag-neutral" : "tag-outline";
  return <span className={`tag ${tone}`}>{role}</span>;
}

/**
 * Doc section 8.1: `provisioned_by` 只有在 Device 自助注册该 Agent 时才存在。
 * 徽标由字段是否存在决定（缺失即 undefined），绝不由值的真假决定。
 */
export function ProvisionedByBadge({
  agent,
}: {
  agent: Pick<AgentProfile, "provisioned_by">;
}) {
  if (agent.provisioned_by === undefined) {
    return <span className="tag tag-neutral">human-registered</span>;
  }
  return (
    <span className="tag tag-neutral" title={`provisioned by device ${agent.provisioned_by}`}>
      device-provisioned
    </span>
  );
}
