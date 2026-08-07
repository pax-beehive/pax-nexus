// Page-level DOM tests for the Operations console regions, covering
// operations doc section 12 items 3 (region independence on storage 503),
// 4 (legitimate zero values), 5 (insufficient latency samples), 6 (idempotent
// Observation replay), 8 (quarantined is not failed), 14 (five distinct
// storage states), 15 (partial zeros are never "healthy empty"), 16 (storage
// fallbacks for unknown codes), plus the section 4.1 time-window presets and
// section 13 IEC byte formatting at page level.

import { describe, expect, it } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import {
  apiErrorResponse,
  callsTo,
  jsonResponse,
  setupDomTest,
} from "./helpers";
import {
  eventsPage,
  eventsTable,
  makeComponent,
  makeEvent,
  makeSnapshot,
  makeSummary,
  pipelineMetric,
  renderOperationsPage,
  statValue,
} from "./operationsFixtures";
import type { OperationsSummary } from "../src/api/types";

setupDomTest();

function zeroSummary(): OperationsSummary {
  return makeSummary({
    observations: {
      requests: 0,
      succeeded: 0,
      input_events: 0,
      events_written: 0,
      duplicate_events: 0,
    },
    extraction: {
      runs: 0,
      completed: 0,
      quarantined: 0,
      failed: 0,
      admitted_revisions: 0,
      unextracted_events: 0,
    },
    recalls: {
      requests: 0,
      succeeded: 0,
      with_evidence: 0,
      empty: 0,
      memory_hits: 0,
      team_notes_delivered: 0,
      memory_search_requests: 0,
      memory_get_requests: 0,
      team_note_recall_requests: 0,
      evidence_hits: 0,
      hint_hits: 0,
      reference_hits: 0,
    },
    latency: { sample_count: 0 },
    errors: 0,
  });
}

function storageCard(): HTMLElement {
  return screen.getByText("database physical").closest(".card") as HTMLElement;
}

describe("section 12 item 3: regions survive an unavailable storage backend", () => {
  it("storage 503 gets its own empty state while summary and events render", async () => {
    await renderOperationsPage({
        storage: () => apiErrorResponse(503, "storage_not_available", "snapshot store down"),
        events: () => jsonResponse(eventsPage([makeEvent()])),
      });

    // Summary and events are fully rendered.
    expect(pipelineMetric("Typical delay").value).toBe("24 ms");
    expect(within(eventsTable())
      .getByText("Memory Search")).toBeTruthy();
    // Storage shows its dedicated state, not a generic region error.
    screen.getByText(/Storage statistics are temporarily unavailable \(storage_not_available\)/);
    expect(screen.queryByText("database physical")).toBeNull();
    expect(screen.queryByText("Server error; try again later")).toBeNull();
  });

  it("a failing events region leaves summary and storage intact", async () => {
    await renderOperationsPage({
        events: () => apiErrorResponse(500, "internal_error", "boom"),
      });

    screen.getByText("Server error; try again later");
    // makeSummary()'s default extraction.failed is 1 -- proves the summary
    // region rendered real data, not just an empty shell.
    expect(pipelineMetric("Failed").value).toBe("1");
    screen.getByText("database physical");
  });

  it("a failing summary region leaves events and storage intact", async () => {
    await renderOperationsPage({
        summary: () => apiErrorResponse(500, "internal_error", "boom"),
        events: () => jsonResponse(eventsPage([makeEvent()])),
      });

    screen.getByText("Server error; try again later");
    expect(document.querySelector(".gv-metrics")).toBeNull();
    within(eventsTable()).getByText(
      "Memory Search",
    );
    screen.getByText("database physical");
  });
});

describe("section 12 item 4: legitimate zero values render as 0, not as an error state", () => {
  it("an all-zero summary shows zeros and no empty-data error", async () => {
    await renderOperationsPage({ summary: () => jsonResponse(zeroSummary()) });

    expect(pipelineMetric("Held for review").value).toBe("0");
    expect(pipelineMetric("Failed").value).toBe("0");
    const queued = pipelineMetric("Waiting");
    expect(queued.value).toBe("0");
    // A healthy empty backlog shows no oldest-event age (doc section 7).
    expect(queued.sub).toBe("—");
    expect(pipelineMetric("Recalls refused").value).toBe("0");
    // sample_count 0 hides both percentiles rather than showing "0 ms".
    expect(pipelineMetric("Typical delay").value).toBe("insufficient samples");
    expect(pipelineMetric("Worst case").value).toBe("insufficient samples");
    // No region flipped into an error/empty-data note.
    expect(document.querySelector(".note.bad")).toBeNull();
    expect(screen.queryByText("Server error; try again later")).toBeNull();
  });
});

describe("section 12 item 5: missing latency percentiles show insufficient samples", () => {
  it("sample_count below 2 hides both percentiles without showing 0 ms or NaN", async () => {
    await renderOperationsPage({
        summary: () => jsonResponse(makeSummary({ latency: { sample_count: 1 } })),
      });

    expect(pipelineMetric("Typical delay").value).toBe("insufficient samples");
    expect(pipelineMetric("Worst case").value).toBe("insufficient samples");
    expect(screen.queryByText("0 ms")).toBeNull();
    expect(document.querySelector(".gv-metrics")?.textContent).not.toContain("NaN");
  });

  it("sample_count between 2 and 19 shows p50 but hides p95", async () => {
    await renderOperationsPage({
        summary: () => jsonResponse(makeSummary({ latency: { sample_count: 10, p50_ms: 24 } })),
      });

    expect(pipelineMetric("Typical delay").value).toBe("24 ms");
    expect(pipelineMetric("Worst case").value).toBe("insufficient samples");
  });
});

// section 12 item 6 (idempotent Observation replay renders as success, not a
// failure) previously asserted against the Observations card's
// events-written/duplicates fields and the Latency & Errors card's errors
// count. Design phase 5 (docs/superpowers/specs/2026-08-06-portal-modernist-
// phase5-governance-design.md §2.3/§8) replaces both cards with the six-cell
// PipelineMetrics bar, which surfaces neither duplicate_events nor errors as
// their own cell -- the old full assertion has no remaining UI surface. The
// numbered item still needs a page-level anchor for traceability, so a
// reduced form survives: a replay (events_written=0, duplicates>0, no real
// failures) must not push either "失败" or "扣下待查" up, and must not flip
// any region into an error/empty-data note.
describe("section 12 item 6: idempotent Observation replay never reads as a failure", () => {
  it("a replayed batch leaves 失败 and 扣下待查 at 0 with no error note", async () => {
    await renderOperationsPage({
      summary: () =>
        jsonResponse(
          makeSummary({
            observations: {
              requests: 18,
              succeeded: 18,
              input_events: 52,
              events_written: 0,
              duplicate_events: 4,
            },
            extraction: {
              runs: 12,
              completed: 10,
              quarantined: 0,
              failed: 0,
              admitted_revisions: 8,
              unextracted_events: 0,
            },
            errors: 0,
          }),
        ),
    });

    expect(pipelineMetric("Failed").value).toBe("0");
    expect(pipelineMetric("Held for review").value).toBe("0");
    expect(document.querySelector(".note.bad")).toBeNull();
  });
});

describe("section 12 item 8: quarantined extractions are not failures", () => {
  it("quarantined renders separately from failed", async () => {
    await renderOperationsPage({
        summary: () =>
          jsonResponse(
            makeSummary({
              extraction: {
                runs: 12,
                completed: 10,
                quarantined: 2,
                failed: 0,
                admitted_revisions: 8,
                unextracted_events: 0,
              },
              errors: 0,
            }),
          ),
      });

    expect(pipelineMetric("Held for review").value).toBe("2");
    expect(pipelineMetric("Failed").value).toBe("0");
    // Healthy empty backlog: no oldest-unextracted-event age is shown.
    expect(pipelineMetric("Waiting").sub).toBe("—");
  });

  it("a non-zero backlog without a timestamp names the field it's missing, never a guess", async () => {
    const extraction = {
      runs: 12,
      completed: 10,
      quarantined: 1,
      failed: 1,
      admitted_revisions: 8,
      unextracted_events: 3,
    };
    const summary = makeSummary();
    delete summary.extraction.oldest_unextracted_at;
    await renderOperationsPage({
        summary: () => jsonResponse({ ...summary, extraction }),
      });

    const queued = pipelineMetric("Waiting");
    expect(queued.value).toBe("3");
    expect(queued.sub).toBe("Oldest unextracted event: time unknown");
  });
});

describe("section 12 item 14: storage complete, partial, stale, 503 and empty history are distinct", () => {
  it("complete fresh snapshot renders IEC sizes with no warning notes", async () => {
    await renderOperationsPage();

    const cardEl = storageCard();
    // IEC binary formatting (doc section 13), logical and physical separate.
    expect(statValue(cardEl, "database physical")).toBe("100 MiB");
    expect(statValue(cardEl, "other physical")).toBe("12 MiB");
    expect(statValue(cardEl, "status")).toBe("complete");
    const row = within(cardEl).getByText("Session Lake").closest("tr") as HTMLTableRowElement;
    expect(row.cells[2].textContent).toBe("501.3 KiB");
    expect(row.cells[3].textContent).toBe("4 MiB");
    expect(within(cardEl).queryByText(/incomplete \(partial\)/)).toBeNull();
    expect(within(cardEl).queryByText(/may be stale/)).toBeNull();
    expect(within(cardEl).queryByText(/Unknown capture status/)).toBeNull();
  });

  it("partial snapshot shows the warning codes and capture time", async () => {
    await renderOperationsPage({
        storage: () =>
          jsonResponse({
            storage: makeSnapshot({
              status: "partial",
              warning_codes: ["recall_diagnostics_logical_unavailable"],
              components: [
                makeComponent(),
                makeComponent({
                  component: "recall_diagnostics",
                  counts: { recall_observations: 0, hint_deliveries: 0, diagnostic_bytes: 0 },
                  logical_bytes: 0,
                  physical_bytes: 0,
                }),
              ],
            }),
          }),
      });

    const cardEl = storageCard();
    within(cardEl).getByText(/This capture is incomplete \(partial\)/);
    within(cardEl).getByText("recall_diagnostics_logical_unavailable");
    expect(statValue(cardEl, "status")).toBe("partial");
    // The healthy component still renders real sizes.
    const row = within(cardEl).getByText("Session Lake").closest("tr") as HTMLTableRowElement;
    expect(row.cells[3].textContent).toBe("4 MiB");
  });

  it("a snapshot older than two hours is flagged stale, never a database failure", async () => {
    const captured = new Date(Date.now() - 3 * 60 * 60 * 1000).toISOString();
    await renderOperationsPage({
        storage: () => jsonResponse({ storage: makeSnapshot({ captured_at: captured }) }),
      });

    const cardEl = storageCard();
    within(cardEl).getByText(/may be stale/);
    // Data is still rendered; only the freshness note is added.
    expect(statValue(cardEl, "database physical")).toBe("100 MiB");
    expect(within(cardEl).queryByText(/incomplete \(partial\)/)).toBeNull();
  });

  it("history loads only on demand and an empty history is its own state", async () => {
    const { fetchMock, user } = await renderOperationsPage();
    expect(callsTo(fetchMock, "/v1/admin/operations/storage/history")).toHaveLength(0);

    await user.click(screen.getByRole("button", { name: "History trend" }));

    await screen.findByText("No history snapshots yet.");
    const historyCalls = callsTo(fetchMock, "/v1/admin/operations/storage/history");
    expect(historyCalls).toHaveLength(1);
    expect(historyCalls[0].path).toContain("limit=50");
  });

  it("history rows render as a trend sorted by capture time", async () => {
    const older = makeSnapshot({
      snapshot_id: 90,
      captured_at: "2026-07-22T10:00:00Z",
      database_physical_bytes: 104857600,
    });
    const newer = makeSnapshot({
      snapshot_id: 91,
      captured_at: "2026-07-22T11:00:00Z",
      database_physical_bytes: 209715200,
      status: "partial",
      warning_codes: ["operations_relation_size_unavailable"],
    });
    const { user } = await renderOperationsPage({
        history: () => jsonResponse({ snapshots: [newer, older] }),
      });

    await user.click(screen.getByRole("button", { name: "History trend" }));

    const cardEl = (await screen.findByText("Storage history")).closest(".card") as HTMLElement;
    const rows = within(cardEl).getAllByRole("row");
    // Header plus two snapshots; oldest first (doc section 10).
    expect(rows).toHaveLength(3);
    expect(rows[1].textContent).toContain("100 MiB");
    expect(rows[2].textContent).toContain("200 MiB");
    expect(rows[2].textContent).toContain("operations_relation_size_unavailable");
  });
});

describe("section 12 item 15: partial zeros are never shown as a healthy empty database", () => {
  it("a component named by warning codes renders unavailable cells, not 0 B", async () => {
    await renderOperationsPage({
        storage: () =>
          jsonResponse({
            storage: makeSnapshot({
              status: "partial",
              warning_codes: [
                "session_lake_logical_unavailable",
                "recall_diagnostics_logical_unavailable",
                "recall_diagnostics_relation_size_unavailable",
              ],
              components: [
                makeComponent({ logical_bytes: 0 }),
                makeComponent({
                  component: "recall_diagnostics",
                  counts: { recall_observations: 0, hint_deliveries: 0, diagnostic_bytes: 0 },
                  logical_bytes: 0,
                  physical_bytes: 0,
                }),
              ],
            }),
          }),
      });

    const cardEl = storageCard();
    const lakeRow = within(cardEl).getByText("Session Lake").closest("tr") as HTMLTableRowElement;
    // Logical is unavailable; physical is still a real measurement.
    expect(lakeRow.cells[2].textContent).toBe("unavailable");
    expect(lakeRow.cells[3].textContent).toBe("4 MiB");
    const diagRow = within(cardEl).getByText("Recall Diagnostics").closest("tr") as HTMLTableRowElement;
    expect(diagRow.cells[2].textContent).toBe("unavailable");
    expect(diagRow.cells[3].textContent).toBe("unavailable");
  });
});

describe("section 12 item 16: unknown storage codes fall back safely", () => {
  it("unknown components and warning codes render as raw safe codes", async () => {
    await renderOperationsPage({
        storage: () =>
          jsonResponse({
            storage: makeSnapshot({
              status: "partial",
              warning_codes: ["vector_index_logical_unavailable"],
              components: [
                makeComponent(),
                makeComponent({
                  component: "vector_index",
                  counts: { embeddings: 12, payload_bytes: 2048 },
                  logical_bytes: 2048,
                  physical_bytes: 4096,
                }),
              ],
            }),
          }),
      });

    const cardEl = storageCard();
    // Unknown component keeps its raw name; unknown warning code shows raw.
    const row = within(cardEl).getByText("vector_index").closest("tr") as HTMLTableRowElement;
    expect(row.cells[2].textContent).toBe("unavailable");
    within(cardEl).getByText("vector_index_logical_unavailable");
  });

  it("an unknown snapshot status shows the raw code and keeps the data", async () => {
    await renderOperationsPage({
        storage: () => jsonResponse({ storage: makeSnapshot({ status: "degraded" }) }),
      });

    const cardEl = storageCard();
    within(cardEl).getByText(/Unknown capture status/);
    // The raw code appears in the note and as the status badge.
    expect(within(cardEl).getAllByText("degraded").length).toBeGreaterThanOrEqual(2);
    expect(statValue(cardEl, "database physical")).toBe("100 MiB");
  });

  it("an unknown schema_version degrades to totals plus raw component names", async () => {
    await renderOperationsPage({
        storage: () =>
          jsonResponse({
            storage: makeSnapshot({
              schema_version: 2,
              components: [makeComponent(), makeComponent({ component: "vector_index" })],
            }),
          }),
      });

    const cardEl = storageCard();
    expect(cardEl.textContent).toContain("only database totals and raw component names are shown");
    expect(cardEl.textContent).toContain("components: session_lake, vector_index");
    expect(statValue(cardEl, "database physical")).toBe("100 MiB");
    // No per-component size table under a foreign schema version.
    expect(within(cardEl).queryByText("Logical")).toBeNull();
  });
});

describe("section 4.1: time-window presets emit RFC3339 from/to parameters", () => {
  it("the default window is 24h and presets change both summary and events", async () => {
    const { fetchMock, user } = await renderOperationsPage();

    const windowOf = (path: string) => {
      const params = new URLSearchParams(path.split("?")[1]);
      const from = params.get("from");
      const to = params.get("to");
      expect(from).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/);
      expect(to).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/);
      return new Date(to as string).getTime() - new Date(from as string).getTime();
    };
    for (const call of callsTo(fetchMock, "/v1/admin/operations/summary")) {
      expect(windowOf(call.path)).toBe(24 * 60 * 60 * 1000);
    }
    for (const call of callsTo(fetchMock, "/v1/admin/operations/events")) {
      expect(windowOf(call.path)).toBe(24 * 60 * 60 * 1000);
    }

    await user.click(screen.getByRole("button", { name: "1h" }));

    await waitFor(() =>
      expect(callsTo(fetchMock, "/v1/admin/operations/summary")).toHaveLength(2),
    );
    await waitFor(() =>
      expect(callsTo(fetchMock, "/v1/admin/operations/events")).toHaveLength(2),
    );
    const summaryCalls = callsTo(fetchMock, "/v1/admin/operations/summary");
    expect(windowOf(summaryCalls[summaryCalls.length - 1].path)).toBe(60 * 60 * 1000);
    const eventCalls = callsTo(fetchMock, "/v1/admin/operations/events");
    expect(windowOf(eventCalls[eventCalls.length - 1].path)).toBe(60 * 60 * 1000);
  });

  it("the agent filter is sent to both summary and events", async () => {
    const { fetchMock, user } = await renderOperationsPage();

    await user.type(screen.getByPlaceholderText("Filter by Agent ID"), "agent-1");
    await user.click(screen.getByRole("button", { name: "Apply filter" }));

    await waitFor(() =>
      expect(callsTo(fetchMock, "/v1/admin/operations/summary")).toHaveLength(2),
    );
    const lastOf = (prefix: string) => {
      const calls = callsTo(fetchMock, prefix);
      return new URLSearchParams(calls[calls.length - 1].path.split("?")[1]);
    };
    expect(lastOf("/v1/admin/operations/summary").get("agent_id")).toBe("agent-1");
    expect(lastOf("/v1/admin/operations/events").get("agent_id")).toBe("agent-1");
  });
});
