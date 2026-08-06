// Overview landing page (portal-modernist phase 2b): a team-scoped snapshot
// -- five headline metrics, throughput, note mix, and who is writing -- each
// block owning its own loading/ready/error state so a failing region never
// unmounts an unrelated one (mirrors AdminOperationsPage.tsx). The metrics
// row, throughput chart and note mix share the overview aggregate region;
// "who is writing" polls a separate, independent region. Responses stay in
// React memory only -- never written to the URL, localStorage, analytics or
// the console. `.ov-bottom` renders empty placeholder sections here; Task 7
// fills the attention queue and event feed.

import { useCallback, useState } from "react";
import { apiError } from "../api/client";
import { useAuth } from "../auth/AuthContext";
import { RegionError } from "../components/RegionError";
import { Seg } from "../components/Seg";
import { Tag } from "../components/Tag";
import { TIME_WINDOW_PRESETS, type TimeWindowPreset } from "../lib/operations";
import { currentTeam } from "../lib/teams";
import { useErrorHandler } from "../lib/useErrorHandler";
import { useOverviewRegion, useWritersRegion } from "./overview/hooks";
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

  const heading = (me ? currentTeam(me)?.name : undefined) ?? "Overview";
  const today = new Date().toLocaleDateString();

  return (
    <>
      <div className="page-head">
        <div>
          <p className="card-kicker">{`TODAY · ${today}`}</p>
          <h1>{heading}</h1>
        </div>
        <div className="row">
          <Tag tone="outline">Live</Tag>
          <Seg
            label="Time window"
            options={TIME_WINDOW_PRESETS.map((p) => ({ value: p, label: p }))}
            value={window}
            onChange={setWindow}
          />
        </div>
      </div>

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
              <ThroughputChart series={overview.data.series} window={window} />
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
        {/* Task 7 fills these with the attention queue and the event feed. */}
        <section className="ov-block" aria-label="Needs your attention" />
        <section className="ov-block" aria-label="What just happened" />
      </div>
    </>
  );
}
