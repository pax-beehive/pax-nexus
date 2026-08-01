// Team Pulse: a live per-agent activity view on top of the read-only
// operations aggregate (GET /v1/admin/operations/agents) and the existing
// operation events feed. Regions own their loading/ready/error state like the
// Operations console; responses stay in React memory only -- never written to
// the URL, localStorage, analytics or the console. Animations are pure CSS
// keyframes plus the rAF count-up hook; no new dependencies.

import { useCallback, useEffect, useRef, useState } from "react";
import { apiError } from "../api/client";
import {
  listAdminAgents,
  listOperationEvents,
  listOperationsAgentStats,
} from "../api/queries";
import type {
  AgentProfile,
  OperationEvent,
  OperationsAgentStats,
} from "../api/types";
import { timeWindow } from "../lib/operations";
import { useErrorHandler } from "../lib/useErrorHandler";
import { usePolling } from "../lib/usePolling";
import { usePolledRegion } from "../lib/useRegion";
import { useAuth } from "../auth/AuthContext";
import { hasServerCapability } from "../lib/capabilities";
import { AgentGrid } from "./pulse/AgentGrid";
import { KnowledgeFlow } from "./pulse/KnowledgeFlow";
import { LiveEventsFeed } from "./pulse/LiveEventsFeed";

// Stats and the event feed poll every 10s while the page is visible.
const POLL_INTERVAL_MS = 10_000;
const FEED_SIZE = 20;

// ---------------------------------------------------------------------------
// Region hooks
// ---------------------------------------------------------------------------

interface StatsSnapshot {
  agents: OperationsAgentStats[];
  fromTime?: string;
  toTime?: string;
  generatedAt?: string;
  /** Note ids first seen in the latest poll; they render with a fade-in. */
  freshNotes: Set<string>;
}

interface StatsRegion extends StatsSnapshot {
  status: "loading" | "ready" | "error";
  error?: unknown;
}

function useStatsRegion(
  onAuthError: (err: unknown) => void,
): StatsRegion & { retry: () => void } {
  const seenNotesRef = useRef<Set<string> | undefined>();
  const region = usePolledRegion<StatsSnapshot>(
    async (signal, prev) => {
      const res = await listOperationsAgentStats(timeWindow("1h"), signal);
      const seen = seenNotesRef.current;
      const fresh = new Set<string>();
      const nowSeen = new Set<string>();
      for (const agent of res.items) {
        for (const note of agent.recent_notes) {
          nowSeen.add(note.note_id);
          if (seen !== undefined && !seen.has(note.note_id)) fresh.add(note.note_id);
        }
      }
      seenNotesRef.current = nowSeen;
      return {
        agents: res.items,
        fromTime: res.fromTime,
        toTime: res.toTime,
        generatedAt: res.generatedAt,
        // The first successful load highlights nothing: everything is "new".
        freshNotes: prev.status === "loading" ? new Set() : fresh,
      };
    },
    POLL_INTERVAL_MS,
    [],
    onAuthError,
  );

  return {
    agents: region.data?.agents ?? [],
    fromTime: region.data?.fromTime,
    toTime: region.data?.toTime,
    generatedAt: region.data?.generatedAt,
    freshNotes: region.data?.freshNotes ?? new Set(),
    status: region.status,
    error: region.error,
    retry: region.retry,
  };
}

interface FeedRegion {
  items: OperationEvent[];
  /** attempt_ids that appeared in the latest applied poll (slide-in + flash). */
  freshIds: Set<string>;
  status: "loading" | "ready" | "error";
  error?: unknown;
  /** New events arrived while the user was scrolled down the feed. */
  pending?: { items: OperationEvent[]; freshIds: Set<string> };
}

function useFeedRegion(
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
    POLL_INTERVAL_MS,
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

/** Ticking clock for relative last-active labels and status-dot freshness. */
function useNow(intervalMs: number): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(timer);
  }, [intervalMs]);
  return now;
}

// ---------------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------------

export function AdminPulsePage() {
  const { state: authState } = useAuth();
  const canExplore =
    authState.kind === "active" && hasServerCapability(authState.me, "view.team-memory");
  const handleError = useErrorHandler();
  // Only auth transitions go through the global handler; region failures stay
  // region-local so a failing poll never spams toasts.
  const onAuthError = useCallback(
    (err: unknown) => {
      if (apiError(err) && (err.status === 401 || err.status === 403)) {
        handleError(err);
      }
    },
    [handleError],
  );

  const stats = useStatsRegion(onAuthError);
  const feedScrolledRef = useRef(false);
  const feed = useFeedRegion(feedScrolledRef, onAuthError);
  const now = useNow(1000);

  // Non-authoritative agent-type enrichment for the glyphs; the raw agent id
  // always stays visible and unknown types get the generic glyph.
  const [agentTypes, setAgentTypes] = useState<Map<string, AgentProfile>>(new Map());
  useEffect(() => {
    listAdminAgents({ limit: 100 })
      .then((page) => setAgentTypes(new Map(page.items.map((a) => [a.agent_id, a]))))
      .catch(() => {});
  }, []);

  const agentNames = new Map(stats.agents.map((a) => [a.agent_id, a.display_name]));

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Team Pulse</h1>
          <p className="muted flush">
            Real-time per-agent activity: writes, notes produced, recalls, and capsule traffic
          </p>
        </div>
      </div>

      <KnowledgeFlow
        status={stats.status}
        error={stats.error}
        onRetry={stats.retry}
        generatedAt={stats.generatedAt}
        agents={stats.agents}
      />

      <AgentGrid
        status={stats.status}
        error={stats.error}
        onRetry={stats.retry}
        agents={stats.agents}
        freshNotes={stats.freshNotes}
        agentTypes={agentTypes}
        now={now}
        canExplore={canExplore}
      />

      <LiveEventsFeed
        status={feed.status}
        error={feed.error}
        onRetry={feed.retry}
        items={feed.items}
        freshIds={feed.freshIds}
        pending={feed.pending}
        onApplyPending={feed.applyPending}
        agentNames={agentNames}
        scrolledRef={feedScrolledRef}
      />
    </>
  );
}
