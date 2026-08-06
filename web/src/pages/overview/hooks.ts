// Region hooks for the Overview landing page (portal-modernist phase 2b):
// the aggregate (metrics + throughput series + note mix) and the writers
// list poll independently every 10s, mirroring the Operations console /
// Team Pulse region pattern (usePolledRegion) so a failure in one never
// clears the other.

import { getOverview, listOperationsAgentStats } from "../../api/queries";
import type { OperationsAgentStats, OverviewResponse } from "../../api/types";
import { timeWindow, type TimeWindowPreset } from "../../lib/operations";
import { usePolledRegion } from "../../lib/useRegion";

export const OVERVIEW_POLL_MS = 10_000;

export function useOverviewRegion(window: TimeWindowPreset, onAuthError: (err: unknown) => void) {
  return usePolledRegion<OverviewResponse>(
    (signal) => getOverview(window, signal),
    OVERVIEW_POLL_MS,
    [window],
    onAuthError,
  );
}

export function useWritersRegion(window: TimeWindowPreset, onAuthError: (err: unknown) => void) {
  return usePolledRegion<OperationsAgentStats[]>(
    async (signal) => {
      const page = await listOperationsAgentStats(timeWindow(window), signal);
      return page.items;
    },
    OVERVIEW_POLL_MS,
    [window],
    onAuthError,
  );
}
