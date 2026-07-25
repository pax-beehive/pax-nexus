// Device-scoped agent provisioning helpers (doc sections 5.10, 8.6).

import type { DeviceProvisionedAgent } from "../api/types";

/**
 * The still-live provisioned agents of a Device: credential-history rows
 * filtered to `revoked_at` empty, then deduplicated by agent_id keeping the
 * newest row. This is both the detail-page agent list and the cascade
 * preview shown before revoking the Device.
 */
export function aliveProvisionedAgents(
  agents: DeviceProvisionedAgent[],
): DeviceProvisionedAgent[] {
  const latest = new Map<string, DeviceProvisionedAgent>();
  for (const row of agents) {
    if (row.revoked_at) continue;
    const existing = latest.get(row.agent_id);
    if (!existing || row.created_at > existing.created_at) latest.set(row.agent_id, row);
  }
  return [...latest.values()];
}
