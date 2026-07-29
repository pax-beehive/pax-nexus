// Operations console (operations doc section 3): five independent regions --
// activity summary, pipeline health, storage, recent activity and the recall
// drawer. Each region owns its loading/ready/error state so a storage 503
// never clears summary or events. Operations responses stay in React memory:
// never written to the URL, localStorage, analytics or the console (doc 9/13).

import { useCallback, useEffect, useRef, useState } from "react";
import { apiError } from "../api/client";
import { listAdminAgents } from "../api/queries";
import type {
  AgentProfile,
  OperationKind,
  OperationOutcome,
} from "../api/types";
import { formatTime } from "../lib/format";
import {
  isEmptyRecall,
  OPERATION_KINDS,
  OPERATION_OUTCOMES,
  operationKindLabel,
  operationOutcomeTone,
  recallObservationId,
  TONE_BADGE,
  type TimeWindowPreset,
} from "../lib/operations";
import { useErrorHandler } from "../lib/useErrorHandler";
import { RegionError } from "../components/RegionError";
import { ExplorerDiagnosticDrawer } from "./operations/ExplorerDiagnosticDrawer";
import { explorerTargetFromLocation, type ExplorerDrawerTarget } from "./operations/explorerTarget";
import {
  useEventsRegion,
  useStorageHistory,
  useStorageRegion,
  useSummaryRegion,
} from "./operations/hooks";
import { PipelineHealthCard } from "./operations/PipelineHealthCard";
import { RecallDrawer } from "./operations/RecallDrawer";
import { StorageHistoryReady } from "./operations/StorageHistoryView";
import { StorageSnapshotView } from "./operations/StorageSnapshotView";
import { SummaryCards } from "./operations/SummaryCards";

export function AdminOperationsPage({ canInspectTeamMemory = false }: { canInspectTeamMemory?: boolean }) {
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

  const [preset, setPreset] = useState<TimeWindowPreset>("24h");
  const [agentInput, setAgentInput] = useState("");
  const [agentId, setAgentId] = useState("");
  const [kind, setKind] = useState<"" | OperationKind>("");
  const [outcome, setOutcome] = useState<"" | OperationOutcome>("");

  const summary = useSummaryRegion(preset, agentId, onAuthError);
  const events = useEventsRegion({ preset, agentId, kind, outcome }, onAuthError);
  const storage = useStorageRegion(onAuthError);
  const history = useStorageHistory(onAuthError);
  const [historyOpen, setHistoryOpen] = useState(false);
  const historyLoaded = useRef(false);
  const [drawerId, setDrawerId] = useState<number | null>(null);
  const [explorerDrawer, setExplorerDrawer] = useState<ExplorerDrawerTarget | null>(() =>
    explorerTargetFromLocation(canInspectTeamMemory),
  );

  // Non-authoritative agent label enrichment (doc section 8): the raw agent
  // id always stays visible; retired agents keep rendering as raw ids. The
  // fetch is best-effort, so a failure is swallowed -- labels simply stay
  // raw ids rather than degrading the whole page.
  const [agentLabels, setAgentLabels] = useState<Map<string, AgentProfile>>(new Map());
  useEffect(() => {
    listAdminAgents({ limit: 100 })
      .then((page) => setAgentLabels(new Map(page.items.map((a) => [a.agent_id, a]))))
      .catch(() => {});
  }, []);

  const applyAgent = () => setAgentId(agentInput.trim());

  const toggleHistory = () => {
    const open = !historyOpen;
    setHistoryOpen(open);
    if (open && !historyLoaded.current) {
      historyLoaded.current = true;
      void history.load();
    }
  };

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Operations</h1>
          <p className="muted" style={{ margin: 0 }}>
            {canInspectTeamMemory
              ? "Read-only view; raw queries, credentials, idempotency keys and raw errors are never shown. Owner diagnostics may include Team Memory content."
              : "Read-only operational view; queries, content, hit text and raw error details are never shown"}
          </p>
        </div>
      </div>

      <div className="row wrap" style={{ marginBottom: 14, gap: 10 }}>
        <div className="tabs" style={{ marginBottom: 0 }} role="group" aria-label="time window">
          {(["1h", "24h", "7d"] as TimeWindowPreset[]).map((p) => (
            <button
              key={p}
              className={preset === p ? "on" : ""}
              aria-pressed={preset === p}
              onClick={() => setPreset(p)}
              title="Windows beyond the deployment retention are rejected by the backend"
            >
              {p}
            </button>
          ))}
        </div>
        <input
          type="text"
          style={{ width: 220 }}
          placeholder="Filter by Agent ID"
          value={agentInput}
          onChange={(e) => setAgentInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") applyAgent();
          }}
        />
        <button className="btn sm" onClick={applyAgent}>
          Apply filter
        </button>
      </div>

      <h2>Activity summary</h2>
      {summary.status === "loading" && (
        <div className="card">
          <p className="muted small">Loading…</p>
        </div>
      )}
      {summary.status === "error" && (
        <div className="card">
          <RegionError error={summary.error} onRetry={summary.retry} />
        </div>
      )}
      {summary.status === "ready" && summary.data && (
        <>
          {summary.error && (
            <div className="note warn">Auto-refresh failed; the displayed data may be stale.</div>
          )}
          <p className="faint small">
            Window{" "}
            <span title={summary.data.from_time}>{formatTime(summary.data.from_time)}</span> —{" "}
            <span title={summary.data.to_time}>{formatTime(summary.data.to_time)}</span> · generated at{" "}
            <span title={summary.data.generated_at}>
              {formatTime(summary.data.generated_at)}
            </span>
          </p>
          <SummaryCards summary={summary.data} />
          <h2>Pipeline health</h2>
          <PipelineHealthCard summary={summary.data} />
        </>
      )}

      <div className="row between" style={{ marginTop: 18 }}>
        <h2 style={{ margin: 0 }}>Storage</h2>
        <div className="row">
          <button className="btn ghost sm" onClick={toggleHistory}>
            {historyOpen ? "Hide history trend" : "History trend"}
          </button>
          <button className="btn sm" aria-label="Refresh storage" onClick={storage.refresh}>
            Refresh
          </button>
        </div>
      </div>
      <div className="card">
        {storage.region.status === "loading" && <p className="muted small">Loading…</p>}
        {storage.region.status === "unavailable" && (
          <p className="muted small" style={{ margin: 0 }}>
            Storage statistics are temporarily unavailable (storage_not_available); the summary and events regions are unaffected.
          </p>
        )}
        {storage.region.status === "error" && (
          <RegionError error={storage.region.error} onRetry={storage.refresh} />
        )}
        {storage.region.status === "ready" && (
          <>
            {storage.region.refreshError && (
              <div className="note warn">Refresh failed; the displayed snapshot may be stale.</div>
            )}
            <StorageSnapshotView snapshot={storage.region.snapshot} />
          </>
        )}
      </div>
      {historyOpen && (
        <div className="card">
          <h3 style={{ marginTop: 0 }}>Storage history</h3>
          <p className="faint small">
            history is for trends only, not a backup; see the deployment doc
            deployment-instruction.md for backup/restore operations.
          </p>
          {history.region.status === "loading" && <p className="muted small">Loading…</p>}
          {history.region.status === "error" && (
            <RegionError error={history.region.error} onRetry={() => void history.load()} />
          )}
          {history.region.status === "ready" && (
            <StorageHistoryReady region={history.region} onLoadMore={(cursor) => void history.load(cursor)} />
          )}
        </div>
      )}

      <div className="row between" style={{ marginTop: 18 }}>
        <h2 style={{ margin: 0 }}>Recent activity</h2>
        <div className="row">
          <select
            style={{ width: 190 }}
            aria-label="Filter by operation"
            value={kind}
            onChange={(e) => setKind(e.target.value as "" | OperationKind)}
          >
            <option value="">All operations</option>
            {OPERATION_KINDS.map((k) => (
              <option key={k} value={k}>
                {operationKindLabel(k)}
              </option>
            ))}
          </select>
          <select
            style={{ width: 150 }}
            aria-label="Filter by outcome"
            value={outcome}
            onChange={(e) => setOutcome(e.target.value as "" | OperationOutcome)}
          >
            <option value="">All outcomes</option>
            {OPERATION_OUTCOMES.map((o) => (
              <option key={o} value={o}>
                {o}
              </option>
            ))}
          </select>
          <button className="btn sm" aria-label="Refresh recent activity" onClick={events.backToFirstPage}>
            Refresh
          </button>
        </div>
      </div>
      {events.newActivity && (
        <div className="note">
          New activity available.
          <button className="btn sm" style={{ marginLeft: 10 }} onClick={events.backToFirstPage}>
            Back to first page
          </button>
        </div>
      )}
      <div className="card">
        {events.status === "loading" ? (
          <p className="muted small">Loading…</p>
        ) : events.status === "error" ? (
          <RegionError error={events.error} onRetry={events.backToFirstPage} />
        ) : (
          <>
            {events.error && <div className="note warn">Auto-refresh failed; the list may be stale.</div>}
            {events.items.length === 0 ? (
              <p className="muted small">No matching events.</p>
            ) : (
              <table>
                <thead>
                  <tr>
                    <th>Time</th>
                    <th>Agent</th>
                    <th>Operation</th>
                    <th>Outcome</th>
                    <th>Duration</th>
                    <th>Items</th>
                    <th>Error</th>
                    <th>Detail</th>
                  </tr>
                </thead>
                <tbody>
                  {events.items.map((event) => {
                    const recallId = recallObservationId(event);
                    const extractionId =
                      canInspectTeamMemory && event.detail_kind === "extraction_run"
                        ? event.detail_id
                        : undefined;
                    const channelId =
                      canInspectTeamMemory && event.detail_kind === "channel_envelope"
                        ? event.detail_id
                        : undefined;
                    const agentIdRaw = event.actor_agent_id;
                    const agent = agentIdRaw ? agentLabels.get(agentIdRaw) : undefined;
                    const humanActor = event.actor_user_id ?? event.actor_membership_id;
                    return (
                      <tr key={event.operation_event_id}>
                        <td className="small">
                          <span title={event.started_at}>{formatTime(event.started_at)}</span>
                        </td>
                        <td className="small">
                          {agentIdRaw ? (
                            agent ? (
                              <span>
                                {agent.display_name}{" "}
                                <span className="faint small">({agentIdRaw})</span>
                              </span>
                            ) : (
                              <code>{agentIdRaw}</code>
                            )
                          ) : humanActor ? (
                            <code>{humanActor}</code>
                          ) : (
                            <span className="faint">—</span>
                          )}
                        </td>
                        <td className="small" title={event.operation_kind}>
                          {operationKindLabel(event.operation_kind)}
                        </td>
                        <td>
                          <span
                            className={`badge ${TONE_BADGE[operationOutcomeTone(event.outcome)]}`}
                          >
                            {event.outcome}
                          </span>
                          {isEmptyRecall(event) && (
                            <span className="badge b-pending" style={{ marginLeft: 6 }}>
                              empty
                            </span>
                          )}
                        </td>
                        <td className="small">{event.duration_ms} ms</td>
                        <td
                          className="small"
                          title={`input ${event.input_items} · accepted ${event.accepted_items} · duplicate ${event.duplicate_items} · evidence ${event.evidence_items} · hint ${event.hint_items} · reference ${event.reference_items}`}
                        >
                          {event.result_items}→{event.delivered_items}
                        </td>
                        <td className="small">
                          {event.error_code ? <code>{event.error_code}</code> : "—"}
                        </td>
                        <td className="small">
                          {recallId !== undefined ? (
                            <button className="btn ghost sm" onClick={() => setDrawerId(recallId)}>
                              Inspect recall
                            </button>
                          ) : extractionId ? (
                            <button
                              className="btn ghost sm"
                              onClick={() => setExplorerDrawer({ kind: "extraction", id: extractionId })}
                            >
                              Inspect extraction
                            </button>
                          ) : channelId ? (
                            <button
                              className="btn ghost sm"
                              onClick={() => setExplorerDrawer({ kind: "channel", id: channelId })}
                            >
                              Inspect capsule
                            </button>
                          ) : (
                            "—"
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            )}
            {events.generatedAt && (
              <p className="faint small" style={{ marginBottom: 0 }}>
                Generated at <span title={events.generatedAt}>{formatTime(events.generatedAt)}</span>
              </p>
            )}
          </>
        )}
      </div>
      {events.nextCursor && events.status === "ready" && (
        <div style={{ marginTop: 10, textAlign: "center" }}>
          <button
            className="btn sm"
            disabled={events.loadingMore}
            onClick={() => void events.loadMore()}
          >
            {events.loadingMore ? "Loading…" : "Load more"}
          </button>
        </div>
      )}

      {drawerId !== null && (
        <RecallDrawer
          observationId={drawerId}
          onClose={() => setDrawerId(null)}
          onAuthError={onAuthError}
        />
      )}
      {explorerDrawer !== null && (
        <ExplorerDiagnosticDrawer
          target={explorerDrawer}
          onClose={() => setExplorerDrawer(null)}
          onAuthError={onAuthError}
        />
      )}
    </>
  );
}
