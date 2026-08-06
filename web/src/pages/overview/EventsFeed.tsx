// "What just happened" block: the held-events feed, ported from
// pulse/LiveEventsFeed.tsx (portal-modernist phase 2b task 7 -- the original
// and AdminPulsePage.tsx are deleted in task 8). Mechanics are unchanged
// (scrolledRef / pending buffer / freshIds, all owned by
// overview/hooks.ts's useFeedRegion); only the chrome changed: banner copy,
// the "Show" button, the #overview-feed container id, no more .pulse-feed*
// classes (replaced by .note / utility classes + the new .ov-feed-item rules
// in overview-chart.css), and the bottom links now point at the governance
// pages instead of nothing.

import { Link } from "react-router-dom";
import type { OperationEvent } from "../../api/types";
import { Button } from "../../components/Button";
import { RegionError } from "../../components/RegionError";
import { formatTime } from "../../lib/format";
import { operationKindLabel, operationOutcomeTone, TONE_BADGE } from "../../lib/operations";

interface PendingFeedUpdate {
  items: OperationEvent[];
  freshIds: Set<string>;
}

interface EventsFeedProps {
  status: "loading" | "ready" | "error";
  error?: unknown;
  onRetry: () => void;
  items: OperationEvent[];
  freshIds: Set<string>;
  pending?: PendingFeedUpdate;
  onApplyPending: () => void;
  agentNames: ReadonlyMap<string, string>;
  scrolledRef: { current: boolean };
}

export function EventsFeed({
  status,
  error,
  onRetry,
  items,
  freshIds,
  pending,
  onApplyPending,
  agentNames,
  scrolledRef,
}: EventsFeedProps) {
  return (
    <>
      <div className="row between section">
        <h2 className="flush">What just happened</h2>
        <Button size="sm" aria-label="Refresh event feed" onClick={onRetry}>
          Refresh
        </Button>
      </div>
      {pending && (
        <div className="note row">
          {`${pending.freshIds.size} new events held while you read`}
          <Button
            size="sm"
            onClick={() => {
              onApplyPending();
              const list = document.getElementById("overview-feed");
              // jsdom (unit tests) doesn't implement scrollTo; real browsers do.
              if (list && typeof list.scrollTo === "function") list.scrollTo({ top: 0 });
            }}
          >
            Show
          </Button>
        </div>
      )}
      <div className="card">
        {status === "loading" ? (
          <p className="muted small">Loading…</p>
        ) : status === "error" ? (
          <RegionError error={error} onRetry={onRetry} />
        ) : (
          <>
            {error && <div className="note warn">Auto-refresh failed; the list may be stale.</div>}
            {items.length === 0 ? (
              <p className="muted small">No events yet.</p>
            ) : (
              <ul
                id="overview-feed"
                className="ov-feed"
                onScroll={(e) => {
                  scrolledRef.current = e.currentTarget.scrollTop > 24;
                }}
              >
                {items.map((event) => (
                  <li
                    key={event.attempt_id}
                    className={`ov-feed-item${freshIds.has(event.attempt_id) ? " new" : ""}`}
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
      <div className="row between" style={{ marginTop: 10 }}>
        <Link to="/governance/audit">Full activity stream →</Link>
        <Link to="/governance/pipeline">Pipeline health →</Link>
      </div>
    </>
  );
}
