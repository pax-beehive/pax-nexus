// Operations console (operations doc section 3): five independent regions --
// activity summary, pipeline health, storage, recent activity and the recall
// drawer. Each region owns its loading/ready/error state so a storage 503
// never clears summary or events. Operations responses stay in React memory:
// never written to the URL, localStorage, analytics or the console (doc 9/13).

import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { ApiError } from "../api/client";
import {
  getOperationsStorage,
  getOperationsSummary,
  getRecallDiagnostic,
  listAdminAgents,
  listOperationEvents,
  listOperationsStorageHistory,
  type OperationEventFilter,
} from "../api/queries";
import type {
  AgentProfile,
  OperationEvent,
  OperationKind,
  OperationOutcome,
  OperationsStorageSnapshot,
  OperationsSummary,
  RecallDiagnostic,
} from "../api/types";
import { formatBytes, formatTime } from "../lib/format";
import {
  componentAvailability,
  isEmptyRecall,
  isSnapshotStale,
  KNOWN_STORAGE_SCHEMA_VERSION,
  OPERATION_KINDS,
  OPERATION_OUTCOMES,
  operationKindLabel,
  operationOutcomeTone,
  recallObservationId,
  storageComponentLabel,
  timeWindow,
  type OutcomeTone,
  type TimeWindowPreset,
} from "../lib/operations";
import { noticeForError } from "../lib/statusMessage";
import { useErrorHandler } from "../lib/useErrorHandler";
import { isAbortError, usePolling } from "../lib/usePolling";

// Summary and the first events page poll every 15s while visible (doc 4.3).
const POLL_INTERVAL_MS = 15_000;
const PAGE_SIZE = 50;

const TONE_BADGE: Record<OutcomeTone, string> = {
  ok: "b-active",
  warn: "b-suspended",
  bad: "b-retired",
  muted: "b-expired",
};

// ---------------------------------------------------------------------------
// Region hooks
// ---------------------------------------------------------------------------

interface SummaryRegion {
  summary?: OperationsSummary;
  status: "loading" | "ready" | "error";
  error?: unknown;
}

function useSummaryRegion(
  preset: TimeWindowPreset,
  agentId: string,
  onAuthError: (err: unknown) => void,
): SummaryRegion & { retry: () => void } {
  const [state, setState] = useState<SummaryRegion>({ status: "loading" });
  const [epoch, setEpoch] = useState(0);

  usePolling(
    async (signal) => {
      const summary = await getOperationsSummary(
        { ...timeWindow(preset), agent_id: agentId || undefined },
        signal,
      );
      setState({ status: "ready", summary });
    },
    POLL_INTERVAL_MS,
    [preset, agentId, epoch],
    useCallback(
      (err: unknown) => {
        onAuthError(err);
        // Keep the last good data and mark it possibly stale (doc 11).
        setState((prev) => ({
          ...prev,
          status: prev.status === "ready" ? "ready" : "error",
          error: err,
        }));
      },
      [onAuthError],
    ),
  );

  return { ...state, retry: () => setEpoch((e) => e + 1) };
}

interface EventsFilter {
  preset: TimeWindowPreset;
  agentId: string;
  kind: "" | OperationKind;
  outcome: "" | OperationOutcome;
}

interface EventsRegion {
  items: OperationEvent[];
  nextCursor?: string;
  generatedAt?: string;
  status: "loading" | "ready" | "error";
  error?: unknown;
  loadingMore: boolean;
  /** True once the user has paged beyond the first page. */
  paged: boolean;
  /** A poll saw newer first-page events while the user reads later pages. */
  newActivity: boolean;
}

function eventsQuery(filter: EventsFilter, cursor?: string): OperationEventFilter {
  return {
    ...timeWindow(filter.preset),
    agent_id: filter.agentId || undefined,
    operation_kind: filter.kind || undefined,
    outcome: filter.outcome || undefined,
    limit: PAGE_SIZE,
    cursor,
  };
}

function useEventsRegion(
  filter: EventsFilter,
  onAuthError: (err: unknown) => void,
): EventsRegion & { loadMore: () => Promise<void>; backToFirstPage: () => void } {
  const [state, setState] = useState<EventsRegion>({
    items: [],
    status: "loading",
    loadingMore: false,
    paged: false,
    newActivity: false,
  });
  const [epoch, setEpoch] = useState(0);
  const cursorRef = useRef<string | undefined>();
  const pagedRef = useRef(false);
  const firstIdRef = useRef<number | undefined>();
  const epochRef = useRef(0);
  const moreAbortRef = useRef<AbortController | null>(null);

  // Any filter change drops the cursor and every appended page (doc 4.1),
  // and aborts an in-flight "load more".
  useEffect(() => {
    epochRef.current += 1;
    cursorRef.current = undefined;
    pagedRef.current = false;
    firstIdRef.current = undefined;
    moreAbortRef.current?.abort();
    setState({
      items: [],
      status: "loading",
      loadingMore: false,
      paged: false,
      newActivity: false,
    });
  }, [filter.preset, filter.agentId, filter.kind, filter.outcome, epoch]);

  usePolling(
    async (signal) => {
      const page = await listOperationEvents(eventsQuery(filter), signal);
      if (signal.aborted) return;
      if (pagedRef.current) {
        // Later pages stay untouched; flag new activity instead (doc 4.3).
        if (page.items[0]?.operation_event_id !== firstIdRef.current) {
          setState((prev) => ({ ...prev, newActivity: true }));
        }
        return;
      }
      cursorRef.current = page.nextCursor;
      firstIdRef.current = page.items[0]?.operation_event_id;
      setState((prev) => ({
        ...prev,
        items: page.items,
        nextCursor: page.nextCursor,
        generatedAt: page.generatedAt,
        status: "ready",
        error: undefined,
        newActivity: false,
      }));
    },
    POLL_INTERVAL_MS,
    [filter.preset, filter.agentId, filter.kind, filter.outcome, epoch],
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

  const backToFirstPage = useCallback(() => setEpoch((e) => e + 1), []);

  const loadMore = useCallback(async () => {
    const cursor = cursorRef.current;
    if (!cursor) return;
    moreAbortRef.current?.abort();
    const controller = new AbortController();
    moreAbortRef.current = controller;
    const myEpoch = epochRef.current;
    setState((prev) => ({ ...prev, loadingMore: true, error: undefined }));
    try {
      // Next pages must carry exactly the first page's filters (doc 4.2).
      const page = await listOperationEvents(eventsQuery(filter, cursor), controller.signal);
      if (epochRef.current !== myEpoch) return;
      cursorRef.current = page.nextCursor;
      pagedRef.current = true;
      setState((prev) => ({
        ...prev,
        items: [...prev.items, ...page.items],
        nextCursor: page.nextCursor,
        loadingMore: false,
        paged: true,
      }));
    } catch (err) {
      if (isAbortError(err) || epochRef.current !== myEpoch) return;
      if (err instanceof ApiError && err.status === 400) {
        // Invalid or retention-expired cursor: restart from page 1 (doc 4.2).
        backToFirstPage();
        return;
      }
      onAuthError(err);
      setState((prev) => ({ ...prev, loadingMore: false, error: err }));
    }
  }, [filter, onAuthError, backToFirstPage]);

  return { ...state, loadMore, backToFirstPage };
}

type StorageRegion =
  | { status: "loading" }
  | { status: "ready"; snapshot: OperationsStorageSnapshot; refreshError?: unknown }
  | { status: "unavailable" }
  | { status: "error"; error: unknown };

function useStorageRegion(onAuthError: (err: unknown) => void): {
  region: StorageRegion;
  refresh: () => void;
} {
  const [region, setRegion] = useState<StorageRegion>({ status: "loading" });
  const [epoch, setEpoch] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    getOperationsStorage(controller.signal)
      .then((snapshot) => setRegion({ status: "ready", snapshot }))
      .catch((err: unknown) => {
        if (isAbortError(err)) return;
        if (err instanceof ApiError && err.status === 503 && err.code === "storage_not_available") {
          // Storage gets its own empty state; other regions keep working (doc 11).
          setRegion({ status: "unavailable" });
          return;
        }
        onAuthError(err);
        setRegion((prev) =>
          prev.status === "ready" ? { ...prev, refreshError: err } : { status: "error", error: err },
        );
      });
    return () => controller.abort();
  }, [epoch, onAuthError]);

  return { region, refresh: () => setEpoch((e) => e + 1) };
}

type HistoryRegion =
  | { status: "idle" }
  | { status: "loading" }
  | {
      status: "ready";
      items: OperationsStorageSnapshot[];
      nextCursor?: string;
      loadingMore: boolean;
    }
  | { status: "error"; error: unknown };

function useStorageHistory(onAuthError: (err: unknown) => void): {
  region: HistoryRegion;
  load: (cursor?: string) => Promise<void>;
} {
  const [region, setRegion] = useState<HistoryRegion>({ status: "idle" });
  const cursorRef = useRef<string | undefined>();
  const abortRef = useRef<AbortController | null>(null);
  const generationRef = useRef(0);

  const load = useCallback(
    async (cursor?: string): Promise<void> => {
      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;
      const generation = generationRef.current;
      if (!cursor) {
        setRegion({ status: "loading" });
      } else {
        setRegion((prev) => (prev.status === "ready" ? { ...prev, loadingMore: true } : prev));
      }
      try {
        const page = await listOperationsStorageHistory(
          { limit: PAGE_SIZE, cursor },
          controller.signal,
        );
        if (generationRef.current !== generation) return;
        cursorRef.current = page.nextCursor;
        setRegion((prev) => ({
          status: "ready",
          items: cursor && prev.status === "ready" ? [...prev.items, ...page.items] : page.items,
          nextCursor: page.nextCursor,
          loadingMore: false,
        }));
      } catch (err) {
        if (isAbortError(err) || generationRef.current !== generation) return;
        if (err instanceof ApiError && err.status === 400 && cursor) {
          // Expired cursor: restart history from the first page (doc 4.2).
          generationRef.current += 1;
          await load();
          return;
        }
        onAuthError(err);
        setRegion({ status: "error", error: err });
      }
    },
    [onAuthError],
  );

  return { region, load };
}

// ---------------------------------------------------------------------------
// Small presentational pieces
// ---------------------------------------------------------------------------

function Stat({ label, value, title }: { label: string; value: ReactNode; title?: string }) {
  return (
    <div className="stat" title={title}>
      <div className="stat-value">{value}</div>
      <div className="stat-label">{label}</div>
    </div>
  );
}

function RegionError({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const notice = noticeForError(error);
  return (
    <div className={`note ${notice.kind === "ok" ? "" : notice.kind}`}>
      {notice.message}
      {onRetry && (
        <button className="btn sm" style={{ marginLeft: 10 }} onClick={onRetry}>
          重试
        </button>
      )}
    </div>
  );
}

/** Count maps render generically so unknown reason/lane codes survive (doc 5). */
function CountMap({ title, counts }: { title: string; counts: Record<string, number> }) {
  const entries = Object.entries(counts);
  return (
    <div>
      <h3>{title}</h3>
      {entries.length === 0 ? (
        <p className="faint small">无</p>
      ) : (
        <div className="chips">
          {entries.map(([key, value]) => (
            <code key={key}>
              {key}: {value}
            </code>
          ))}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Summary regions (doc section 7)
// ---------------------------------------------------------------------------

function SummaryCards({ summary }: { summary: OperationsSummary }) {
  const { observations: obs, recalls, latency } = summary;
  return (
    <div className="grid">
      <div className="card">
        <h2>Observations</h2>
        <div className="stat-grid">
          <Stat label="requests" value={obs.requests} />
          <Stat label="succeeded" value={obs.succeeded} />
          <Stat label="input events" value={obs.input_events} />
          <Stat label="events written" value={obs.events_written} />
          <Stat label="duplicates" value={obs.duplicate_events} />
        </div>
        {obs.duplicate_events > 0 && (
          <p className="faint small" style={{ marginBottom: 0 }}>
            duplicates 为合法幂等 replay（events_written=0 且 duplicate&gt;0 仍是成功），不计入失败。
          </p>
        )}
      </div>
      <div className="card">
        <h2>Recalls</h2>
        <div className="stat-grid">
          <Stat label="requests" value={recalls.requests} />
          <Stat label="succeeded" value={recalls.succeeded} />
          <Stat label="with evidence" value={recalls.with_evidence} />
          <Stat label="empty" value={recalls.empty} title="正确返回零结果仍是 succeeded" />
          <Stat label="memory hits" value={recalls.memory_hits} title="仅统计 Memory Search hit" />
          <Stat label="notes delivered" value={recalls.team_notes_delivered} />
        </div>
        <p className="faint small" style={{ marginBottom: 0 }}>
          memory.search {recalls.memory_search_requests} · memory.get {recalls.memory_get_requests} ·
          team_note.recall {recalls.team_note_recall_requests} ｜ hits: evidence{" "}
          {recalls.evidence_hits} · hint {recalls.hint_hits} · reference {recalls.reference_hits}
          <br />
          with_evidence 不代表答案正确性（正确性属于 Evaluation）。
        </p>
      </div>
      <div className="card">
        <h2>Latency &amp; Errors</h2>
        <div className="stat-grid">
          <Stat label="samples" value={latency.sample_count} />
          <Stat
            label="p50"
            value={latency.p50_ms !== undefined ? `${latency.p50_ms} ms` : "样本不足"}
          />
          <Stat
            label="p95"
            value={latency.p95_ms !== undefined ? `${latency.p95_ms} ms` : "样本不足"}
          />
          <Stat label="errors" value={summary.errors} title="failed / timed_out / cancelled，不含 rejected" />
        </div>
        <p className="faint small" style={{ marginBottom: 0 }}>
          样本含 memory.search / memory.get / team_note.recall 完整外部调用。
        </p>
      </div>
    </div>
  );
}

function PipelineHealthCard({ summary }: { summary: OperationsSummary }) {
  const ex = summary.extraction;
  return (
    <div className="card">
      <div className="stat-grid">
        <Stat label="extraction runs" value={ex.runs} />
        <Stat label="completed" value={ex.completed} />
        <Stat
          label="quarantined"
          value={ex.quarantined}
          title="deterministic rejection，不计入 failed/errors"
        />
        <Stat label="failed" value={ex.failed} />
        <Stat label="admitted revisions" value={ex.admitted_revisions} />
        <Stat label="unextracted backlog" value={ex.unextracted_events} />
        <Stat
          label="oldest pending"
          value={
            ex.oldest_unextracted_at !== undefined ? (
              <span title={ex.oldest_unextracted_at}>{formatTime(ex.oldest_unextracted_at)}</span>
            ) : ex.unextracted_events > 0 ? (
              "age unavailable"
            ) : (
              "—"
            )
          }
        />
      </div>
      <p className="faint small" style={{ marginBottom: 0 }}>
        Observation accepted 不代表 extraction 完成；backlog 与 extraction 是另一条异步链。
      </p>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Storage region (doc section 10)
// ---------------------------------------------------------------------------

function StorageSnapshotView({ snapshot }: { snapshot: OperationsStorageSnapshot }) {
  const partial = snapshot.status === "partial";
  const unknownStatus = snapshot.status !== "complete" && snapshot.status !== "partial";
  const stale = isSnapshotStale(snapshot.captured_at);
  const schemaMismatch = snapshot.schema_version !== KNOWN_STORAGE_SCHEMA_VERSION;
  const statusBadge =
    snapshot.status === "complete"
      ? "b-active"
      : snapshot.status === "partial"
        ? "b-suspended"
        : "b-expired";

  return (
    <>
      {partial && (
        <div className="note warn">
          本次采集不完整（partial），采集于{" "}
          <span title={snapshot.captured_at}>{formatTime(snapshot.captured_at)}</span>
          {snapshot.warning_codes.length > 0 && (
            <>
              ；warning codes:{" "}
              {snapshot.warning_codes.map((code) => (
                <code key={code} style={{ marginRight: 6 }}>
                  {code}
                </code>
              ))}
            </>
          )}
          。失败 component 的零值不代表真实空库。
        </div>
      )}
      {unknownStatus && (
        <div className="note warn">
          未知采集状态 <code>{snapshot.status}</code>；以下为已返回的数据。
        </div>
      )}
      {stale && (
        <div className="note warn">
          快照采集于 <span title={snapshot.captured_at}>{formatTime(snapshot.captured_at)}</span>
          ，可能已过时（默认每小时采集一次；部署可调整间隔，不代表数据库故障）。
        </div>
      )}
      {schemaMismatch && (
        <div className="note warn">
          schema_version <code>{snapshot.schema_version}</code> 与前端已知版本{" "}
          <code>{KNOWN_STORAGE_SCHEMA_VERSION}</code> 不一致，仅显示数据库总量与原始 component 名称。
        </div>
      )}
      <div className="stat-grid" style={{ marginBottom: 12 }}>
        <Stat
          label="database physical"
          value={formatBytes(snapshot.database_physical_bytes)}
          title="整个数据库大小"
        />
        <Stat
          label="other physical"
          value={formatBytes(snapshot.other_physical_bytes)}
          title="未归到已知 component 的 allocation"
        />
        <Stat
          label="captured at"
          value={
            <span className="small" title={snapshot.captured_at}>
              {formatTime(snapshot.captured_at)}
            </span>
          }
        />
        <Stat label="status" value={<span className={`badge ${statusBadge}`}>{snapshot.status}</span>} />
      </div>
      {schemaMismatch ? (
        <p className="small muted">
          components: {snapshot.components.map((c) => c.component).join(", ") || "—"}
        </p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Component</th>
              <th>Counts</th>
              <th>Logical</th>
              <th>Physical</th>
              <th>Reclaimable</th>
              <th>数据时间范围</th>
            </tr>
          </thead>
          <tbody>
            {snapshot.components.map((component) => {
              const availability = componentAvailability(snapshot, component);
              const label = storageComponentLabel(component.component);
              return (
                <tr key={component.component}>
                  <td>
                    {label}
                    {label !== component.component && (
                      <span className="faint small"> ({component.component})</span>
                    )}
                  </td>
                  <td className="small">
                    {Object.entries(component.counts)
                      .map(([key, value]) =>
                        key.endsWith("_bytes") ? `${key} ${formatBytes(value)}` : `${key} ${value}`,
                      )
                      .join(" · ") || "—"}
                  </td>
                  <td className="small">
                    {availability.logical ? formatBytes(component.logical_bytes) : "不可用"}
                  </td>
                  <td className="small">
                    {availability.physical ? formatBytes(component.physical_bytes) : "不可用"}
                  </td>
                  <td className="small">
                    {component.estimated_reclaimable_bytes !== undefined
                      ? formatBytes(component.estimated_reclaimable_bytes)
                      : "—"}
                  </td>
                  <td className="small">
                    {component.oldest_at || component.newest_at ? (
                      <span title={`${component.oldest_at ?? "—"} → ${component.newest_at ?? "—"}`}>
                        {formatTime(component.oldest_at)} → {formatTime(component.newest_at)}
                      </span>
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
      <p className="faint small">
        logical 为领域 payload 的可解释大小；physical 为 PostgreSQL relation 当前
        allocation。删除后 logical/count 可下降而 physical 暂不下降，属正常行为。
      </p>
    </>
  );
}

// ---------------------------------------------------------------------------
// Storage history trend (doc section 10)
// ---------------------------------------------------------------------------

type HistoryReady = Extract<HistoryRegion, { status: "ready" }>;

/** Trend only, sorted by captured_at (doc section 10); never a backup view. */
function StorageHistoryReady({
  region,
  onLoadMore,
}: {
  region: HistoryReady;
  onLoadMore: (cursor: string) => void;
}) {
  const cursor = region.nextCursor;
  if (region.items.length === 0) {
    return <p className="muted small">暂无历史快照。</p>;
  }
  return (
    <>
      <table>
        <thead>
          <tr>
            <th>采集时间</th>
            <th>数据库总量</th>
            <th>状态</th>
            <th>告警</th>
          </tr>
        </thead>
        <tbody>
          {[...region.items]
            .sort((a, b) => a.captured_at.localeCompare(b.captured_at))
            .map((snap) => (
              <tr key={snap.snapshot_id}>
                <td className="small">
                  <span title={snap.captured_at}>{formatTime(snap.captured_at)}</span>
                </td>
                <td className="small">{formatBytes(snap.database_physical_bytes)}</td>
                <td className="small">{snap.status}</td>
                <td className="small">
                  {snap.warning_codes.length > 0 ? snap.warning_codes.join(", ") : "—"}
                </td>
              </tr>
            ))}
        </tbody>
      </table>
      {cursor !== undefined && (
        <div style={{ marginTop: 10, textAlign: "center" }}>
          <button
            className="btn sm"
            disabled={region.loadingMore}
            onClick={() => onLoadMore(cursor)}
          >
            {region.loadingMore ? "加载中…" : "加载更多"}
          </button>
        </div>
      )}
    </>
  );
}

// ---------------------------------------------------------------------------
// Recall detail drawer (doc section 9); state lives in React memory only.
// ---------------------------------------------------------------------------

type DrawerState =
  | { status: "loading" }
  | { status: "ready"; recall: RecallDiagnostic }
  | { status: "not-found" }
  | { status: "expired" }
  | { status: "error"; error: unknown };

function RecallDrawer({
  observationId,
  onClose,
  onAuthError,
}: {
  observationId: number;
  onClose: () => void;
  onAuthError: (err: unknown) => void;
}) {
  const [state, setState] = useState<DrawerState>({ status: "loading" });
  const [epoch, setEpoch] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setState({ status: "loading" });
    getRecallDiagnostic(observationId, controller.signal)
      .then((recall) => setState({ status: "ready", recall }))
      .catch((err: unknown) => {
        if (isAbortError(err)) return;
        // 404/410 are terminal lifecycle states and are never retried (doc 9).
        if (err instanceof ApiError && err.status === 404) {
          setState({ status: "not-found" });
        } else if (err instanceof ApiError && err.status === 410) {
          setState({ status: "expired" });
        } else {
          onAuthError(err);
          setState({ status: "error", error: err });
        }
      });
    return () => controller.abort();
  }, [observationId, epoch, onAuthError]);

  return (
    <>
      <div className="drawer-backdrop" onClick={onClose} />
      <aside className="drawer">
        <div className="row between" style={{ marginBottom: 12 }}>
          <h2 style={{ margin: 0 }}>Recall #{observationId}</h2>
          <button className="btn ghost sm" onClick={onClose}>
            关闭
          </button>
        </div>
        {state.status === "loading" && <p className="muted small">加载中…</p>}
        {state.status === "not-found" && (
          <div className="note warn">
            诊断不存在：从未记录，或 Operation Event 与诊断均已过 retention。列表中的事件仍然有效。
          </div>
        )}
        {state.status === "expired" && (
          <div className="note warn">
            诊断已过期或被清理（diagnostic_expired）；列表中的安全事件仍然有效。
          </div>
        )}
        {state.status === "error" && (
          <RegionError error={state.error} onRetry={() => setEpoch((e) => e + 1)} />
        )}
        {state.status === "ready" && <RecallView recall={state.recall} />}
      </aside>
    </>
  );
}

function RecallView({ recall }: { recall: RecallDiagnostic }) {
  return (
    <>
      <div className="stat-grid" style={{ marginBottom: 14 }}>
        <Stat
          label="occurred at"
          value={
            <span className="small" title={recall.occurred_at}>
              {formatTime(recall.occurred_at)}
            </span>
          }
        />
        <Stat label="agent" value={<code>{recall.agent_id}</code>} />
        <Stat label="session" value={<code>{recall.session_id}</code>} />
        <Stat label="duration" value={`${recall.duration_ms} ms`} />
        <Stat label="token budget" value={recall.token_budget} />
        <Stat label="planned tokens" value={recall.planned_tokens} />
        <Stat label="max items" value={recall.max_items} />
        <Stat
          label="evidence sufficient"
          value={
            <span className={`badge ${recall.evidence_sufficient ? "b-active" : "b-suspended"}`}>
              {recall.evidence_sufficient ? "是" : "否"}
            </span>
          }
        />
      </div>
      <h3>Delivery funnel</h3>
      <div className="funnel">
        <Stat label="candidates" value={recall.candidates} />
        <span className="funnel-arrow">→</span>
        <Stat label="fusion kept" value={recall.fusion_kept} />
        <span className="funnel-arrow">→</span>
        <Stat label="planned notes" value={recall.planned_notes} />
        <span className="funnel-arrow">→</span>
        <Stat label="delivered" value={recall.delivered_items} />
      </div>
      <h3>Lanes executed</h3>
      {recall.lanes_executed.length === 0 ? (
        <p className="faint small">无</p>
      ) : (
        <div className="chips">
          {recall.lanes_executed.map((lane) => (
            <code key={lane}>{lane}</code>
          ))}
        </div>
      )}
      <h3>Reason codes</h3>
      {recall.reason_codes.length === 0 ? (
        <p className="faint small">无</p>
      ) : (
        <div className="chips">
          {recall.reason_codes.map((code) => (
            <code key={code}>{code}</code>
          ))}
        </div>
      )}
      <CountMap title="Disposition counts" counts={recall.disposition_counts} />
      <CountMap title="Rejection counts" counts={recall.rejection_counts} />
      <CountMap title="Budget drop counts" counts={recall.budget_drop_counts} />
      <CountMap title="Hard gate failure counts" counts={recall.hard_gate_failure_counts} />
    </>
  );
}

// ---------------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------------

export function AdminOperationsPage() {
  const handleError = useErrorHandler();
  // Only auth transitions go through the global handler; region failures stay
  // region-local so a failing poll never spams toasts (doc 11).
  const onAuthError = useCallback(
    (err: unknown) => {
      if (err instanceof ApiError && (err.status === 401 || err.status === 403)) {
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

  // Non-authoritative agent label enrichment (doc section 8): the raw agent
  // id always stays visible; retired agents keep rendering as raw ids.
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
            只读运行面；不展示 query、正文、hit text 或原始错误信息
          </p>
        </div>
      </div>

      <div className="row wrap" style={{ marginBottom: 14, gap: 10 }}>
        <div className="tabs" style={{ marginBottom: 0 }}>
          {(["1h", "24h", "7d"] as TimeWindowPreset[]).map((p) => (
            <button
              key={p}
              className={preset === p ? "on" : ""}
              onClick={() => setPreset(p)}
              title="超出部署 retention 的窗口会被后端拒绝"
            >
              {p}
            </button>
          ))}
        </div>
        <input
          type="text"
          style={{ width: 220 }}
          placeholder="Agent ID 过滤"
          value={agentInput}
          onChange={(e) => setAgentInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") applyAgent();
          }}
        />
        <button className="btn sm" onClick={applyAgent}>
          应用过滤
        </button>
      </div>

      <h2>Activity summary</h2>
      {summary.status === "loading" && (
        <div className="card">
          <p className="muted small">加载中…</p>
        </div>
      )}
      {summary.status === "error" && (
        <div className="card">
          <RegionError error={summary.error} onRetry={summary.retry} />
        </div>
      )}
      {summary.status === "ready" && summary.summary && (
        <>
          {summary.error && (
            <div className="note warn">自动刷新失败，显示的数据可能已过期。</div>
          )}
          <p className="faint small">
            窗口{" "}
            <span title={summary.summary.from_time}>{formatTime(summary.summary.from_time)}</span> —{" "}
            <span title={summary.summary.to_time}>{formatTime(summary.summary.to_time)}</span> · 生成于{" "}
            <span title={summary.summary.generated_at}>
              {formatTime(summary.summary.generated_at)}
            </span>
          </p>
          <SummaryCards summary={summary.summary} />
          <h2>Pipeline health</h2>
          <PipelineHealthCard summary={summary.summary} />
        </>
      )}

      <div className="row between" style={{ marginTop: 18 }}>
        <h2 style={{ margin: 0 }}>Storage</h2>
        <div className="row">
          <button className="btn ghost sm" onClick={toggleHistory}>
            {historyOpen ? "收起历史趋势" : "历史趋势"}
          </button>
          <button className="btn sm" onClick={storage.refresh}>
            刷新
          </button>
        </div>
      </div>
      <div className="card">
        {storage.region.status === "loading" && <p className="muted small">加载中…</p>}
        {storage.region.status === "unavailable" && (
          <p className="muted small" style={{ margin: 0 }}>
            Storage 统计暂不可用（storage_not_available）；summary 与事件区域不受影响。
          </p>
        )}
        {storage.region.status === "error" && (
          <RegionError error={storage.region.error} onRetry={storage.refresh} />
        )}
        {storage.region.status === "ready" && (
          <>
            {storage.region.refreshError && (
              <div className="note warn">刷新失败，显示的快照可能已过期。</div>
            )}
            <StorageSnapshotView snapshot={storage.region.snapshot} />
          </>
        )}
      </div>
      {historyOpen && (
        <div className="card">
          <h3 style={{ marginTop: 0 }}>Storage history</h3>
          <p className="faint small">
            history 仅用于趋势，不是 backup；backup/restore 操作见部署文档
            deployment-instruction.md。
          </p>
          {history.region.status === "loading" && <p className="muted small">加载中…</p>}
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
            value={kind}
            onChange={(e) => setKind(e.target.value as "" | OperationKind)}
          >
            <option value="">全部 operation</option>
            {OPERATION_KINDS.map((k) => (
              <option key={k} value={k}>
                {operationKindLabel(k)}
              </option>
            ))}
          </select>
          <select
            style={{ width: 150 }}
            value={outcome}
            onChange={(e) => setOutcome(e.target.value as "" | OperationOutcome)}
          >
            <option value="">全部 outcome</option>
            {OPERATION_OUTCOMES.map((o) => (
              <option key={o} value={o}>
                {o}
              </option>
            ))}
          </select>
          <button className="btn sm" onClick={events.backToFirstPage}>
            刷新
          </button>
        </div>
      </div>
      {events.newActivity && (
        <div className="note">
          有新活动。
          <button className="btn sm" style={{ marginLeft: 10 }} onClick={events.backToFirstPage}>
            回到第一页
          </button>
        </div>
      )}
      <div className="card">
        {events.status === "loading" ? (
          <p className="muted small">加载中…</p>
        ) : events.status === "error" ? (
          <RegionError error={events.error} onRetry={events.backToFirstPage} />
        ) : (
          <>
            {events.error && <div className="note warn">自动刷新失败，列表可能已过期。</div>}
            {events.items.length === 0 ? (
              <p className="muted small">无匹配事件。</p>
            ) : (
              <table>
                <thead>
                  <tr>
                    <th>时间</th>
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
                生成于 <span title={events.generatedAt}>{formatTime(events.generatedAt)}</span>
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
            {events.loadingMore ? "加载中…" : "加载更多"}
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
    </>
  );
}
