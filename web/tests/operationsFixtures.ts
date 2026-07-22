// Shared fixtures and fetch stubbing for the Operations console DOM tests
// (operations doc section 12). Every endpoint override is a factory so a
// polling refresh never re-reads an already consumed Response body.
//
//   await renderApp({
//     route: "/admin/operations",
//     me: opsMe(),
//     fetch: operationsFetch({
//       events: () => jsonResponse({ events: [makeEvent()], generated_at: GEN_AT }),
//     }),
//   });

import type {
  OperationEvent,
  OperationsStorageSnapshot,
  OperationsSummary,
  RecallDiagnostic,
  StorageComponent,
} from "../src/api/types";
import { screen } from "@testing-library/react";
import { jsonResponse, makeMe, renderApp, type FetchHandler } from "./helpers";

export const GEN_AT = "2026-07-22T12:00:01Z";
export const FROM_TIME = "2026-07-21T12:00:00Z";
export const TO_TIME = "2026-07-22T12:00:00Z";

/** Active owner carrying the server-issued Operations capability (doc 2.1). */
export function opsMe(): ReturnType<typeof makeMe> {
  return makeMe({ capabilities: ["view.operations"] });
}

export function makeSummary(overrides: Partial<OperationsSummary> = {}): OperationsSummary {
  return {
    from_time: FROM_TIME,
    to_time: TO_TIME,
    generated_at: GEN_AT,
    observations: {
      requests: 18,
      succeeded: 17,
      input_events: 52,
      events_written: 48,
      duplicate_events: 4,
    },
    extraction: {
      runs: 12,
      completed: 10,
      quarantined: 1,
      failed: 1,
      admitted_revisions: 8,
      unextracted_events: 3,
      oldest_unextracted_at: "2026-07-22T11:58:00Z",
    },
    recalls: {
      requests: 30,
      succeeded: 29,
      with_evidence: 18,
      empty: 5,
      memory_hits: 41,
      team_notes_delivered: 21,
      memory_search_requests: 20,
      memory_get_requests: 4,
      team_note_recall_requests: 6,
      evidence_hits: 23,
      hint_hits: 11,
      reference_hits: 7,
    },
    latency: { sample_count: 30, p50_ms: 24, p95_ms: 91 },
    errors: 2,
    ...overrides,
  };
}

export function makeEvent(overrides: Partial<OperationEvent> = {}): OperationEvent {
  return {
    operation_event_id: 812,
    attempt_id: "op_q6Jx",
    operation_kind: "memory.search",
    outcome: "succeeded",
    actor_agent_id: "agent-1",
    session_id: "session-42",
    started_at: "2026-07-22T11:59:58Z",
    completed_at: "2026-07-22T11:59:58.024Z",
    duration_ms: 24,
    input_items: 1,
    accepted_items: 0,
    duplicate_items: 0,
    result_items: 3,
    delivered_items: 2,
    evidence_items: 2,
    hint_items: 1,
    reference_items: 0,
    ...overrides,
  };
}

export function makeComponent(overrides: Partial<StorageComponent> = {}): StorageComponent {
  return {
    component: "session_lake",
    counts: {
      events: 1024,
      streams: 27,
      unextracted_events: 3,
      content_bytes: 481203,
      metadata_bytes: 32110,
    },
    logical_bytes: 513313,
    physical_bytes: 4194304,
    oldest_at: "2026-07-01T09:00:00Z",
    newest_at: "2026-07-22T11:59:58Z",
    ...overrides,
  };
}

export function makeSnapshot(
  overrides: Partial<OperationsStorageSnapshot> = {},
): OperationsStorageSnapshot {
  return {
    snapshot_id: 91,
    schema_version: 1,
    // Fresh by default so tests opt into staleness explicitly.
    captured_at: new Date().toISOString(),
    status: "complete",
    warning_codes: [],
    database_physical_bytes: 104857600,
    other_physical_bytes: 12582912,
    components: [makeComponent()],
    ...overrides,
  };
}

export function makeRecall(overrides: Partial<RecallDiagnostic> = {}): RecallDiagnostic {
  return {
    observation_id: 41,
    occurred_at: "2026-07-22T11:59:58Z",
    agent_id: "agent-1",
    session_id: "session-42",
    duration_ms: 24,
    token_budget: 256,
    max_items: 5,
    evidence_sufficient: true,
    reason_codes: ["fact_coverage"],
    lanes_executed: ["lexical", "recent"],
    candidates: 8,
    fusion_kept: 4,
    planned_notes: 3,
    planned_tokens: 182,
    delivered_items: 2,
    disposition_counts: { evidence: 2, hint: 1 },
    rejection_counts: { temporal_gate: 1 },
    budget_drop_counts: { token_budget: 1 },
    hard_gate_failure_counts: { authorization: 0 },
    ...overrides,
  };
}

export function eventsPage(
  events: OperationEvent[],
  nextCursor?: string,
): Record<string, unknown> {
  return {
    events,
    generated_at: GEN_AT,
    ...(nextCursor !== undefined ? { next_cursor: nextCursor } : {}),
  };
}

export interface OperationsEndpoints {
  summary?: () => Response;
  /** Receives the full path so tests can branch on cursor/filters. */
  events?: (path: string) => Response;
  storage?: () => Response;
  history?: (path: string) => Response;
  /** Receives the raw observation id segment from the URL. */
  recall?: (observationId: string) => Response;
  agents?: () => Response;
}

/**
 * Answers every endpoint the Operations page can fire and throws on anything
 * else, so a test that misses an endpoint fails loudly. The page also loads
 * Admin Agent labels on mount; that endpoint is stubbed here too.
 */
export function operationsFetch(endpoints: OperationsEndpoints = {}): FetchHandler {
  return (path, init) => {
    if (path.startsWith("/v1/admin/operations/summary")) {
      return endpoints.summary?.() ?? jsonResponse(makeSummary());
    }
    if (path.startsWith("/v1/admin/operations/events")) {
      return endpoints.events?.(path) ?? jsonResponse(eventsPage([]));
    }
    if (path.startsWith("/v1/admin/operations/storage/history")) {
      return endpoints.history?.(path) ?? jsonResponse({ snapshots: [] });
    }
    if (path.startsWith("/v1/admin/operations/storage")) {
      return endpoints.storage?.() ?? jsonResponse({ storage: makeSnapshot() });
    }
    if (path.startsWith("/v1/admin/operations/recalls/")) {
      const id = path.slice("/v1/admin/operations/recalls/".length);
      return endpoints.recall?.(id) ?? jsonResponse({ recall: makeRecall() });
    }
    if (path.startsWith("/v1/admin/agents")) {
      return endpoints.agents?.() ?? jsonResponse({ agents: [] });
    }
    throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
  };
}

/** Read the value stat rendered above a label inside a `.stat-grid` scope. */
export function statValue(scope: ParentNode, label: string): string | null {
  const labels = Array.from(scope.querySelectorAll(".stat-label"));
  const el = labels.find((node) => node.textContent === label);
  const stat = el?.closest(".stat");
  return stat?.querySelector(".stat-value")?.textContent ?? null;
}

/**
 * Mount the portal at /admin/operations with the Operations capability and
 * wait for the first summary/events/storage cycle to settle.
 */
export async function renderOperationsPage(endpoints: OperationsEndpoints = {}) {
  const app = await renderApp({
    route: "/admin/operations",
    me: opsMe(),
    fetch: operationsFetch(endpoints),
  });
  await screen.findByRole("heading", { name: "Operations" });
  return app;
}

/** The Recent activity table (the only table with an Operation header). */
export function eventsTable(): HTMLElement {
  return screen.getByText("Operation").closest("table") as HTMLElement;
}
