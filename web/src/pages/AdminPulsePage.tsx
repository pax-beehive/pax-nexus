// Team Pulse: a live per-agent activity view on top of the read-only
// operations aggregate (GET /v1/admin/operations/agents) and the existing
// operation events feed. Regions own their loading/ready/error state like the
// Operations console; responses stay in React memory only -- never written to
// the URL, localStorage, analytics or the console. Animations are pure CSS
// keyframes plus the rAF count-up hook; no new dependencies.

import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
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
import { formatTime } from "../lib/format";
import {
  agentActivity,
  operationKindLabel,
  operationOutcomeTone,
  relativeAge,
  timeWindow,
  TONE_BADGE,
} from "../lib/operations";
import { useCountUp } from "../lib/useCountUp";
import { useErrorHandler } from "../lib/useErrorHandler";
import { usePolling } from "../lib/usePolling";
import { usePolledRegion } from "../lib/useRegion";
import { RegionError } from "../components/RegionError";
import { useAuth } from "../auth/AuthContext";
import { hasServerCapability } from "../lib/capabilities";

// Stats and the event feed poll every 10s while the page is visible.
const POLL_INTERVAL_MS = 10_000;
const FEED_SIZE = 20;

const ACTIVITY_LABEL = {
  active: "Active (within 1 min)",
  recent: "Recently active (within 10 min)",
  idle: "Idle",
} as const;

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
// Small presentational pieces
// ---------------------------------------------------------------------------

/** Inline agent-type glyph; unknown types get the generic mark. */
function AgentGlyph({ type }: { type?: string }) {
  const common = {
    width: 26,
    height: 26,
    viewBox: "0 0 24 24",
    fill: "none",
    stroke: "currentColor",
    strokeWidth: 1.6,
    strokeLinecap: "round" as const,
    strokeLinejoin: "round" as const,
    "aria-hidden": true,
  };
  switch (type) {
    case "codex":
      return (
        <svg {...common} className="pulse-glyph">
          <rect x="2.5" y="4" width="19" height="16" rx="3" />
          <path d="M7 9l3 3-3 3" />
          <line x1="12.5" y1="15" x2="17" y2="15" />
        </svg>
      );
    case "claude":
      return (
        <svg {...common} className="pulse-glyph">
          <path d="M12 3v18M3.5 12h17M6 6l12 12M18 6L6 18" />
        </svg>
      );
    case "pi":
      return (
        <svg {...common} className="pulse-glyph">
          <path d="M5 8h14M9 8c0 5-1 8-2.5 10M15 8c0 5 1 8 2.5 10" />
        </svg>
      );
    default:
      return (
        <svg {...common} className="pulse-glyph">
          <path d="M12 3l7.8 4.5v9L12 21l-7.8-4.5v-9L12 3z" />
          <circle cx="12" cy="12" r="2.4" />
        </svg>
      );
  }
}

function PulseDot({ activity }: { activity: keyof typeof ACTIVITY_LABEL }) {
  return (
    <span
      className={`pulse-dot s-${activity}`}
      title={ACTIVITY_LABEL[activity]}
      aria-label={ACTIVITY_LABEL[activity]}
    />
  );
}

function PulseStat({ label, value, title }: { label: string; value: number; title?: string }) {
  const display = useCountUp(value);
  return (
    <div className="stat" title={title}>
      <div className="stat-value">{display}</div>
      <div className="stat-label">{label}</div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Agent card wall
// ---------------------------------------------------------------------------

function AgentCard({
  agent,
  agentType,
  freshNotes,
  now,
  canExplore,
}: {
  agent: OperationsAgentStats;
  agentType?: string;
  freshNotes: Set<string>;
  now: number;
  canExplore: boolean;
}) {
  const activity = agentActivity(agent.last_active_at, now);
  return (
    <div className="card pulse-card">
      <div className="row between" style={{ alignItems: "flex-start" }}>
        <div className="row">
          <AgentGlyph type={agentType} />
          <div>
            <div className="pulse-name">{agent.display_name || agent.agent_id}</div>
            <code className="small">{agent.agent_id}</code>
          </div>
        </div>
        <PulseDot activity={activity} />
      </div>
      <div className="stat-grid pulse-stats">
        <PulseStat label="events written" value={agent.events_written} />
        <PulseStat label="notes authored" value={agent.notes_authored} />
        <PulseStat label="recalls" value={agent.recall_requests} />
        <PulseStat
          label="capsules"
          value={agent.channel_sent + agent.channel_received_accepted}
          title={`sent ${agent.channel_sent} · accepted ${agent.channel_received_accepted}`}
        />
      </div>
      <p className="faint small" style={{ marginBottom: 0 }}>
        Last active:{" "}
        {agent.last_active_at ? (
          <span title={agent.last_active_at}>{relativeAge(agent.last_active_at, now)}</span>
        ) : (
          "no activity"
        )}
        {" · "}extraction {agent.extraction_runs} runs · tokens{" "}
        {agent.extraction_input_tokens}/{agent.extraction_output_tokens}
      </p>
      {agent.recent_notes.length > 0 && (
        <ul className="pulse-notes">
          {agent.recent_notes.map((note) => (
            <li
              key={note.note_id}
              className={freshNotes.has(note.note_id) ? "fade-in" : undefined}
            >
              <span className="badge b-role">{note.kind}</span>
              {canExplore ? (
                <Link
                  className="small"
                  title={formatTime(note.created_at)}
                  to={`/admin/explorer/notes/${encodeURIComponent(note.note_id)}`}
                >
                  {note.subject}
                </Link>
              ) : (
                <span className="small" title={formatTime(note.created_at)}>
                  {note.subject}
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Knowledge flow strip: agents -> Team Notes -> recall side
// ---------------------------------------------------------------------------

/** Stroke width scales with recent volume but stays within 1.5..7.5px. */
function flowWidth(volume: number): number {
  return 1.5 + Math.min(6, Math.sqrt(Math.max(0, volume)));
}

function FlowStrip({ agents }: { agents: OperationsAgentStats[] }) {
  const written = agents.reduce((sum, a) => sum + a.events_written, 0);
  const notes = agents.reduce((sum, a) => sum + a.notes_authored, 0);
  const recalled = agents.reduce((sum, a) => sum + a.recall_requests, 0);
  return (
    <svg
      className="pulse-flow"
      viewBox="0 0 640 110"
      role="img"
      aria-label={`Knowledge flow over the last hour: ${written} events written, ${notes} notes produced, ${recalled} recalls`}
    >
      <path
        className="flow-line"
        d="M132 55 C 200 55, 240 55, 292 55"
        strokeWidth={flowWidth(written)}
      />
      <path
        className="flow-line"
        d="M348 55 C 400 55, 440 55, 508 55"
        strokeWidth={flowWidth(recalled)}
      />
      <g className="flow-node">
        <circle cx="76" cy="55" r="34" />
        <text x="76" y="51" textAnchor="middle" className="flow-title">
          Agents
        </text>
        <text x="76" y="67" textAnchor="middle" className="flow-sub">
          {agents.length}
        </text>
      </g>
      <g className="flow-node">
        <circle cx="320" cy="55" r="34" />
        <text x="320" y="51" textAnchor="middle" className="flow-title">
          Team Notes
        </text>
        <text x="320" y="67" textAnchor="middle" className="flow-sub">
          +{notes}
        </text>
      </g>
      <g className="flow-node">
        <circle cx="564" cy="55" r="34" />
        <text x="564" y="51" textAnchor="middle" className="flow-title">
          Recall
        </text>
        <text x="564" y="67" textAnchor="middle" className="flow-sub">
          {recalled}
        </text>
      </g>
    </svg>
  );
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
          <p className="muted" style={{ margin: 0 }}>
            Real-time per-agent activity: writes, notes produced, recalls, and capsule traffic
          </p>
        </div>
      </div>

      <div className="row between">
        <h2 style={{ margin: 0 }}>Knowledge flow</h2>
        {stats.generatedAt && (
          <p className="faint small" style={{ margin: 0 }}>
            Last 1 hour · generated at{" "}
            <span title={stats.generatedAt}>{formatTime(stats.generatedAt)}</span>
          </p>
        )}
      </div>
      <div className="card">
        {stats.status === "loading" && <p className="muted small">Loading…</p>}
        {stats.status === "error" && <RegionError error={stats.error} onRetry={stats.retry} />}
        {stats.status === "ready" && (
          <>
            {stats.error && (
              <div className="note warn">Auto-refresh failed; the data shown may be stale.</div>
            )}
            <FlowStrip agents={stats.agents} />
          </>
        )}
      </div>

      <h2>Agents</h2>
      {stats.status === "loading" && (
        <div className="card">
          <p className="muted small">Loading…</p>
        </div>
      )}
      {stats.status === "error" && (
        <div className="card">
          <RegionError error={stats.error} onRetry={stats.retry} />
        </div>
      )}
      {stats.status === "ready" && stats.agents.length === 0 && (
        <div className="card flat">
          <h3 style={{ marginTop: 0 }}>No agent activity yet</h3>
          <p className="muted small">
            Once agents are registered and connected, this page shows each agent's event
            writes, notes produced, recalls, and capsule traffic in real time.
          </p>
          <Link className="btn sm" to="/admin/agents">
            Go to All Agents to register an agent
          </Link>
        </div>
      )}
      {stats.status === "ready" && stats.agents.length > 0 && (
        <div className="grid">
          {stats.agents.map((agent) => (
            <AgentCard
              key={agent.agent_id}
              agent={agent}
              agentType={agentTypes.get(agent.agent_id)?.agent_type}
              freshNotes={stats.freshNotes}
              now={now}
              canExplore={canExplore}
            />
          ))}
        </div>
      )}

      <div className="row between" style={{ marginTop: 18 }}>
        <h2 style={{ margin: 0 }}>Live events</h2>
        <button className="btn sm" aria-label="Refresh event feed" onClick={feed.retry}>
          Refresh
        </button>
      </div>
      {feed.pending && (
        <div className="note">
          {feed.pending.freshIds.size > 0
            ? `${feed.pending.freshIds.size} new events arrived.`
            : "New events arrived."}
          <button
            className="btn sm"
            style={{ marginLeft: 10 }}
            onClick={() => {
              feed.applyPending();
              document.getElementById("pulse-feed")?.scrollTo({ top: 0 });
            }}
          >
            View latest
          </button>
        </div>
      )}
      <div className="card">
        {feed.status === "loading" ? (
          <p className="muted small">Loading…</p>
        ) : feed.status === "error" ? (
          <RegionError error={feed.error} onRetry={feed.retry} />
        ) : (
          <>
            {feed.error && <div className="note warn">Auto-refresh failed; the list may be stale.</div>}
            {feed.items.length === 0 ? (
              <p className="muted small">No events yet.</p>
            ) : (
              <ul
                id="pulse-feed"
                className="pulse-feed"
                onScroll={(e) => {
                  feedScrolledRef.current = e.currentTarget.scrollTop > 24;
                }}
              >
                {feed.items.map((event) => (
                  <li
                    key={event.attempt_id}
                    className={`pulse-feed-item${
                      feed.freshIds.has(event.attempt_id) ? " new" : ""
                    }`}
                  >
                    <span className="faint small" title={event.started_at}>
                      {formatTime(event.started_at)}
                    </span>
                    <span className="small">
                      {event.actor_agent_id
                        ? (agentNames.get(event.actor_agent_id) ?? event.actor_agent_id)
                        : "—"}
                    </span>
                    <span className="small" title={event.operation_kind}>
                      {operationKindLabel(event.operation_kind)}
                    </span>
                    <span
                      className={`badge ${TONE_BADGE[operationOutcomeTone(event.outcome)]}`}
                    >
                      {event.outcome}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </>
        )}
      </div>
    </>
  );
}
