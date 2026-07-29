// Region hooks for the Operations console. Each region owns its
// loading/ready/error state so a storage 503 never clears summary or events
// (operations doc section 11). Regions without extra bookkeeping delegate to
// the shared usePolledRegion skeleton; events, storage and history keep
// bespoke hooks for their cursor, availability and manual-load semantics.

import { useCallback, useEffect, useRef, useState } from "react";
import { apiError } from "../../api/client";
import {
  getOperationsStorage,
  getOperationsSummary,
  listOperationEvents,
  listOperationsStorageHistory,
  type OperationEventFilter,
} from "../../api/queries";
import type {
  OperationEvent,
  OperationKind,
  OperationOutcome,
  OperationsStorageSnapshot,
  OperationsSummary,
} from "../../api/types";
import { timeWindow, type TimeWindowPreset } from "../../lib/operations";
import { isAbortError, usePolling } from "../../lib/usePolling";
import { usePolledRegion, type PolledRegionState } from "../../lib/useRegion";

// Summary and the first events page poll every 15s while visible (doc 4.3).
const POLL_INTERVAL_MS = 15_000;
const PAGE_SIZE = 50;

type SummaryRegion = PolledRegionState<OperationsSummary>;

export function useSummaryRegion(
  preset: TimeWindowPreset,
  agentId: string,
  onAuthError: (err: unknown) => void,
): SummaryRegion & { retry: () => void } {
  return usePolledRegion<OperationsSummary>(
    (signal) =>
      getOperationsSummary(
        { ...timeWindow(preset), agent_id: agentId || undefined },
        signal,
      ),
    POLL_INTERVAL_MS,
    [preset, agentId],
    onAuthError,
  );
}

export interface EventsFilter {
  preset: TimeWindowPreset;
  agentId: string;
  kind: "" | OperationKind;
  outcome: "" | OperationOutcome;
}

export interface EventsRegion {
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

// Deliberately a bespoke fork of usePagedList's cursor pagination (see
// lib/usePagedList.ts), not a shared abstraction: this hook also layers in
// 15s polling of the first page and newest-first "new activity" detection
// when the user has paged past it, neither of which usePagedList models.
// Invariant: any race-guard fix here (epochRef/cursorRef/abort generation)
// must be mirrored in usePagedList.ts's generationRef/epoch guards, and
// vice versa.
export function useEventsRegion(
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
      if (apiError(err, 400)) {
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

export type StorageRegion =
  | { status: "loading" }
  | { status: "ready"; snapshot: OperationsStorageSnapshot; refreshError?: unknown }
  | { status: "unavailable" }
  | { status: "error"; error: unknown };

export function useStorageRegion(onAuthError: (err: unknown) => void): {
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
        if (apiError(err, 503, "storage_not_available")) {
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

export type HistoryRegion =
  | { status: "idle" }
  | { status: "loading" }
  | {
      status: "ready";
      items: OperationsStorageSnapshot[];
      nextCursor?: string;
      loadingMore: boolean;
    }
  | { status: "error"; error: unknown };

export function useStorageHistory(onAuthError: (err: unknown) => void): {
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
        if (apiError(err, 400) && cursor) {
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
