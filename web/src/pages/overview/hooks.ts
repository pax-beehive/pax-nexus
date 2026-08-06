// Region hooks for the Overview landing page (portal-modernist phase 2b):
// the aggregate (metrics + throughput series + note mix), the writers list,
// and the held-events feed all poll independently every 10s, mirroring the
// Operations console / Team Pulse region pattern (usePolledRegion /
// usePolling) so a failure in one never clears another.

import { useCallback, useRef, useState } from "react";
import { getOverview, listOperationEvents, listOperationsAgentStats } from "../../api/queries";
import type { OperationEvent, OperationsAgentStats, OverviewResponse } from "../../api/types";
import { timeWindow, type TimeWindowPreset } from "../../lib/operations";
import { usePolling } from "../../lib/usePolling";
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

// ---------------------------------------------------------------------------
// Held-events feed (ported from AdminPulsePage.tsx's useFeedRegion — same
// scrolledRef / pending-buffer / freshIds mechanics, only the poll interval
// constant is shared with the rest of this page. AdminPulsePage.tsx and
// pulse/LiveEventsFeed.tsx are deleted in Task 8 once this is the only copy.
// ---------------------------------------------------------------------------

/** Events per page (doc: Overview event feed). */
export const FEED_SIZE = 20;

export interface FeedRegion {
  items: OperationEvent[];
  /** attempt_ids that appeared in the latest applied poll (slide-in + flash). */
  freshIds: Set<string>;
  status: "loading" | "ready" | "error";
  error?: unknown;
  /** New events arrived while the user was scrolled down the feed. */
  pending?: { items: OperationEvent[]; freshIds: Set<string> };
}

export function useFeedRegion(
  scrolledRef: { current: boolean },
  onAuthError: (err: unknown) => void,
): FeedRegion & { applyPending: () => void; retry: () => void } {
  const [state, setState] = useState<FeedRegion>({
    items: [],
    freshIds: new Set(),
    status: "loading",
  });
  const [epoch, setEpoch] = useState(0);
  const knownIdsRef = useRef<Set<string> | undefined>();

  usePolling(
    async (signal) => {
      const page = await listOperationEvents({ limit: FEED_SIZE }, signal);
      const known = knownIdsRef.current;
      const fresh = new Set(
        known === undefined
          ? []
          : page.items.filter((e) => !known.has(e.attempt_id)).map((e) => e.attempt_id),
      );
      if (scrolledRef.current && known !== undefined) {
        // The user is reading further down: hold the update behind a notice
        // instead of shifting the list under their eyes.
        setState((prev) => ({
          ...prev,
          pending: { items: page.items, freshIds: fresh },
          error: undefined,
        }));
        return;
      }
      knownIdsRef.current = new Set(page.items.map((e) => e.attempt_id));
      setState((prev) => ({
        items: page.items,
        freshIds: prev.status === "loading" ? new Set() : fresh,
        status: "ready",
        error: undefined,
        pending: undefined,
      }));
    },
    OVERVIEW_POLL_MS,
    [epoch, scrolledRef],
    useCallback(
      (err: unknown) => {
        onAuthError(err);
        setState((prev) => ({
          ...prev,
          status: prev.status === "ready" ? "ready" : "error",
          error: err,
        }));
      },
      [onAuthError],
    ),
  );

  const applyPending = useCallback(() => {
    setState((prev) => {
      if (!prev.pending) return prev;
      knownIdsRef.current = new Set(prev.pending.items.map((e) => e.attempt_id));
      return {
        ...prev,
        items: prev.pending.items,
        freshIds: prev.pending.freshIds,
        pending: undefined,
      };
    });
  }, []);

  return { ...state, applyPending, retry: () => setEpoch((e) => e + 1) };
}
