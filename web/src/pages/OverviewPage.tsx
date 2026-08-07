// Overview landing page (portal-modernist phase 2b): a team-scoped snapshot
// -- five headline metrics, throughput, note mix, who is writing, an
// attention queue and a held-events feed -- each block owning its own
// loading/ready/error state so a failing region never unmounts an unrelated
// one (mirrors AdminOperationsPage.tsx). The metrics row, throughput chart,
// note mix and attention queue share the overview aggregate region; "who is
// writing" and the events feed each poll their own independent region.
// Responses stay in React memory only -- never written to the URL,
// localStorage, analytics or the console.

import { useCallback, useRef, useState } from "react";
import { apiError } from "../api/client";
import { useAuth } from "../auth/AuthContext";
import { RegionError } from "../components/RegionError";
import { PageHeader } from "../components/PageHeader";
import { Seg } from "../components/Seg";
import { Tag } from "../components/Tag";
import { timeWindowOptions, type TimeWindowPreset } from "../lib/operations";
import { currentTeam } from "../lib/teams";
import { useErrorHandler } from "../lib/useErrorHandler";
import { AttentionQueue } from "./overview/AttentionQueue";
import { EventsFeed } from "./overview/EventsFeed";
import { useFeedRegion, useOverviewRegion, useWritersRegion } from "./overview/hooks";
import { MetricsRow } from "./overview/MetricsRow";
import { NoteMixBlock } from "./overview/NoteMixBlock";
import { ThroughputChart } from "./overview/ThroughputChart";
import { WhoIsWriting } from "./overview/WhoIsWriting";

export function OverviewPage() {
  const { state: authState } = useAuth();
  const me = authState.kind === "active" ? authState.me : undefined;
  const handleError = useErrorHandler();
  // Only auth transitions go through the global handler; region failures stay
  // region-local so a failing poll never spams toasts (doc 11).
  const onAuthError = useCallback(
    (err: unknown) => {
      if (apiError(err) && (err.status === 401 || err.status === 403)) {
        handleError(err);
      }
    },
    [handleError],
  );

  const [window, setWindow] = useState<TimeWindowPreset>("24h");
  const overview = useOverviewRegion(window, onAuthError);
  const writers = useWritersRegion(window, onAuthError);
  const feedScrolledRef = useRef(false);
  const feed = useFeedRegion(feedScrolledRef, onAuthError);
  const agentNames = new Map((writers.data ?? []).map((a) => [a.agent_id, a.display_name]));

  const heading = (me ? currentTeam(me)?.name : undefined) ?? "Overview";
  const today = new Date().toLocaleDateString();

  return (
    <>
      <PageHeader
        kicker={`TODAY · ${today}`}
        title={heading}
        actions={
          <div className="row">
            <Tag tone="outline">Live</Tag>
            <Seg
              label="Time window"
              options={timeWindowOptions(overview.data?.event_retention_seconds)}
              value={window}
              onChange={setWindow}
            />
          </div>
        }
      />

      {overview.status === "loading" && (
        <div className="ov-metrics">
          <p className="muted small">Loading…</p>
        </div>
      )}
      {overview.status === "error" && (
        <div className="ov-metrics">
          <RegionError error={overview.error} onRetry={overview.retry} />
        </div>
      )}
      {overview.status === "ready" && overview.data && (
        <>
          {overview.error && (
            <div className="note warn">Auto-refresh failed; the metrics may be stale.</div>
          )}
          <MetricsRow metrics={overview.data.metrics} />
        </>
      )}

      <div className="ov-mid">
        <div className="ov-block">
          {overview.status === "loading" && <p className="muted small">Loading…</p>}
          {overview.status === "error" && (
            <RegionError error={overview.error} onRetry={overview.retry} />
          )}
          {overview.status === "ready" && overview.data && (
            <>
              {overview.error && (
                <div className="note warn">Auto-refresh failed; the throughput chart may be stale.</div>
              )}
              <ThroughputChart
                series={overview.data.series}
                fromTime={overview.data.from_time}
                toTime={overview.data.to_time}
              />
            </>
          )}
        </div>
        <div className="ov-block">
          {overview.status === "loading" && <p className="muted small">Loading…</p>}
          {overview.status === "error" && (
            <RegionError error={overview.error} onRetry={overview.retry} />
          )}
          {overview.status === "ready" && overview.data && (
            <>
              {overview.error && (
                <div className="note warn">Auto-refresh failed; the note mix may be stale.</div>
              )}
              <NoteMixBlock mix={overview.data.note_mix} />
            </>
          )}
        </div>
        <div className="ov-block">
          {writers.status === "loading" && <p className="muted small">Loading…</p>}
          {writers.status === "error" && (
            <RegionError error={writers.error} onRetry={writers.retry} />
          )}
          {writers.status === "ready" && writers.data && (
            <>
              {writers.error && (
                <div className="note warn">Auto-refresh failed; the writers list may be stale.</div>
              )}
              <WhoIsWriting agents={writers.data} />
            </>
          )}
        </div>
      </div>

      <div className="ov-bottom">
        <section className="ov-block" aria-label="Needs your attention">
          {overview.status === "loading" && <p className="muted small">Loading…</p>}
          {overview.status === "error" && (
            <RegionError error={overview.error} onRetry={overview.retry} />
          )}
          {overview.status === "ready" && overview.data && (
            <>
              {overview.error && (
                <div className="note warn">Auto-refresh failed; the attention queue may be stale.</div>
              )}
              <AttentionQueue items={overview.data.attention} />
            </>
          )}
        </section>
        <section className="ov-block" aria-label="What just happened">
          <EventsFeed
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
        </section>
      </div>
    </>
  );
}
