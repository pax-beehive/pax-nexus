// Live events region: the polled operation-event feed with slide-in
// highlights for fresh rows, a held-back "new events" notice while the user
// is scrolled down, and a manual refresh.

import type { OperationEvent } from "../../api/types";
import { Button } from "../../components/Button";
import { RegionError } from "../../components/RegionError";
import { formatTime } from "../../lib/format";
import {
  operationKindLabel,
  operationOutcomeTone,
  TONE_BADGE,
} from "../../lib/operations";

interface PendingFeedUpdate {
  items: OperationEvent[];
  freshIds: Set<string>;
}

interface LiveEventsFeedProps {
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

export function LiveEventsFeed({
  status,
  error,
  onRetry,
  items,
  freshIds,
  pending,
  onApplyPending,
  agentNames,
  scrolledRef,
}: LiveEventsFeedProps) {
  return (
    <>
      <div className="row between section">
        <h2 className="flush">Live events</h2>
        <Button size="sm" aria-label="Refresh event feed" onClick={onRetry}>
          Refresh
        </Button>
      </div>
      {pending && (
        <div className="note row">
          {pending.freshIds.size > 0
            ? `${pending.freshIds.size} new events arrived.`
            : "New events arrived."}
          <Button
            size="sm"
            onClick={() => {
              onApplyPending();
              document.getElementById("pulse-feed")?.scrollTo({ top: 0 });
            }}
          >
            View latest
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
                id="pulse-feed"
                className="pulse-feed"
                onScroll={(e) => {
                  scrolledRef.current = e.currentTarget.scrollTop > 24;
                }}
              >
                {items.map((event) => (
                  <li
                    key={event.attempt_id}
                    className={`pulse-feed-item${
                      freshIds.has(event.attempt_id) ? " new" : ""
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
