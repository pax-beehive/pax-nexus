// Page-level DOM tests for the Operations Recent activity table and the
// recall drawer, covering operations doc section 12 items 7 (empty vs failed
// recall), 9 (filter changes abort in-flight work and reset the cursor),
// 10 (operation_kind query name and mismatched detail kinds), 11 (only
// recall_observation opens the drawer), 12 (404/410/500 drawer lifecycle),
// 13 (no leakage into URL/storage/console), 16 (unknown enums fall back),
// 17 (cursor termination and invalid-cursor recovery), 19 (raw agent id
// survives label enrichment failure) and 20 (no unsafe payload fields).

import { describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import {
  apiErrorResponse,
  callsTo,
  jsonResponse,
  makeAgent,
  setupDomTest,
} from "./helpers";
import {
  eventsPage,
  eventsTable,
  makeEvent,
  makeRecall,
  renderOperationsPage,
} from "./operationsFixtures";
import type { OperationKind, OperationOutcome } from "../src/api/types";

setupDomTest();

const RECALL_EVENT = makeEvent({
  operation_event_id: 812,
  detail_kind: "recall_observation",
  detail_id: "41",
});

function drawer(): HTMLElement {
  return document.querySelector(".drawer") as HTMLElement;
}

/** The operation/outcome filter selects next to the Recent activity heading. */
function filterSelects(): [HTMLElement, HTMLElement] {
  const header = screen.getByText("Recent activity").closest(".row") as HTMLElement;
  const selects = within(header).getAllByRole("combobox");
  return [selects[0], selects[1]];
}

describe("section 12 item 7: empty successful recall differs from a failed one", () => {
  it("renders distinct labels and badge tones", async () => {
    await renderOperationsPage({
      events: () =>
        jsonResponse(
          eventsPage([
            makeEvent({ operation_event_id: 812, outcome: "succeeded", result_items: 0 }),
            makeEvent({
              operation_event_id: 811,
              operation_kind: "team_note.recall",
              outcome: "failed",
              result_items: 0,
              delivered_items: 0,
              error_code: "llm_timeout",
            }),
          ]),
        ),
    });

    const emptyRow = within(eventsTable())
      .getByText("Memory Search")
      .closest("tr") as HTMLElement;
    const succeededBadge = within(emptyRow).getByText("succeeded");
    expect(succeededBadge.className).toBe("badge b-active");
    expect(within(emptyRow).getByText("empty").className).toBe("badge b-pending");

    const failedRow = within(eventsTable())
      .getByText("Team Note Recall")
      .closest("tr") as HTMLElement;
    expect(within(failedRow).getByText("failed").className).toBe("badge b-retired");
    expect(within(failedRow).queryByText("empty")).toBeNull();
    within(failedRow).getByText("llm_timeout");
  });
});

describe("section 12 item 9: filter changes abort and reset pagination", () => {
  it("changing the kind filter aborts a paged request and refetches without cursor", async () => {
    const events = (path: string): Response => {
      const params = new URLSearchParams(path.split("?")[1]);
      if (params.get("operation_kind") === "memory.search") {
        return jsonResponse(eventsPage([makeEvent({ operation_event_id: 900, duration_ms: 14 })]));
      }
      if (params.get("cursor") === "c2") {
        return jsonResponse(eventsPage([makeEvent({ operation_event_id: 811, duration_ms: 13 })]));
      }
      return jsonResponse(
        eventsPage([makeEvent({ operation_event_id: 812, duration_ms: 12 })], "c2"),
      );
    };
    const { fetchMock, user } = await renderOperationsPage({ events });

    const eventCalls = () => callsTo(fetchMock, "/v1/admin/operations/events");
    const paramsOf = (index: number) =>
      new URLSearchParams(eventCalls()[index].path.split("?")[1]);

    // Page 1, then append page 2 through the opaque cursor.
    within(eventsTable()).getByText("12 ms");
    await user.click(screen.getByRole("button", { name: "加载更多" }));
    await within(eventsTable()).findByText("13 ms");
    expect(eventCalls()).toHaveLength(2);
    expect(paramsOf(1).get("cursor")).toBe("c2");
    // Next pages carry the first page's discrete filters verbatim (doc 4.2).
    expect(paramsOf(1).get("operation_kind")).toBeNull();
    expect(paramsOf(1).get("outcome")).toBeNull();
    expect(paramsOf(1).get("limit")).toBe("50");

    // Changing the filter drops the cursor and every appended page.
    await user.selectOptions(filterSelects()[0], "memory.search");
    await within(eventsTable()).findByText("14 ms");

    expect(screen.queryByText("12 ms")).toBeNull();
    expect(screen.queryByText("13 ms")).toBeNull();
    expect(eventCalls()).toHaveLength(3);
    expect(paramsOf(2).get("operation_kind")).toBe("memory.search");
    expect(paramsOf(2).get("cursor")).toBeNull();
    // The in-flight load-more was aborted by the filter change.
    const pagedSignal = eventCalls()[1].init.signal as AbortSignal;
    expect(pagedSignal.aborted).toBe(true);
    // Summary does not refetch on an events-only filter change.
    expect(callsTo(fetchMock, "/v1/admin/operations/summary")).toHaveLength(1);
  });
});

describe("section 12 item 10: operation_kind query name and detail-kind mismatches", () => {
  it("the kind filter sends operation_kind, never kind", async () => {
    const { fetchMock, user } = await renderOperationsPage({
      events: (path) => {
        const params = new URLSearchParams(path.split("?")[1]);
        if (params.get("operation_kind") === "memory.search") {
          return jsonResponse(eventsPage([makeEvent()]));
        }
        return jsonResponse(eventsPage([]));
      },
    });

    await user.selectOptions(filterSelects()[0], "memory.search");
    await waitFor(() =>
      expect(callsTo(fetchMock, "/v1/admin/operations/events")).toHaveLength(2),
    );

    const params = new URLSearchParams(
      callsTo(fetchMock, "/v1/admin/operations/events")[1].path.split("?")[1],
    );
    expect(params.get("operation_kind")).toBe("memory.search");
    expect(params.get("kind")).toBeNull();
  });

  it("rows whose detail cannot be a recall observation render no Inspect button", async () => {
    const { fetchMock } = await renderOperationsPage({
      events: () =>
        jsonResponse(
          eventsPage([
            makeEvent({
              operation_event_id: 801,
              operation_kind: "extraction.run",
              detail_kind: "extraction_run",
              detail_id: "77",
            }),
            makeEvent({
              operation_event_id: 802,
              operation_kind: "channel.send",
              detail_kind: "channel_envelope",
              detail_id: "88",
            }),
            // recall_observation with ids that are not positive integers.
            makeEvent({ operation_event_id: 803, detail_kind: "recall_observation", detail_id: "0" }),
            makeEvent({
              operation_event_id: 804,
              detail_kind: "recall_observation",
              detail_id: "abc",
            }),
            makeEvent({ operation_event_id: 805, detail_kind: "recall_observation" }),
          ]),
        ),
    });

    await within(eventsTable()).findByText("Extraction");
    expect(screen.queryByRole("button", { name: "Inspect recall" })).toBeNull();
    expect(callsTo(fetchMock, "/v1/admin/operations/recalls")).toHaveLength(0);
  });
});

describe("section 12 item 11: only recall_observation details open the drawer", () => {
  it("opens the drawer for the one valid row and never calls recalls for others", async () => {
    const recall = makeRecall({
      // Unknown lane/reason/disposition codes render raw (item 16).
      lanes_executed: ["lexical", "vector_lane"],
      reason_codes: ["fact_coverage", "future_reason"],
      disposition_counts: { evidence: 2, future_disposition: 1 },
    });
    const { fetchMock, user } = await renderOperationsPage({
      events: () =>
        jsonResponse(
          eventsPage([
            RECALL_EVENT,
            makeEvent({
              operation_event_id: 811,
              operation_kind: "channel.archive",
              detail_kind: "channel_envelope",
              detail_id: "88",
            }),
          ]),
        ),
      recall: () => jsonResponse({ recall }),
    });

    const buttons = await screen.findAllByRole("button", { name: "Inspect recall" });
    expect(buttons).toHaveLength(1);
    await user.click(buttons[0]);

    await within(drawer()).findByText("Delivery funnel");
    expect(callsTo(fetchMock, "/v1/admin/operations/recalls/41")).toHaveLength(1);
    within(drawer()).getByText("Lanes executed");
    within(drawer()).getByText("vector_lane");
    within(drawer()).getByText("future_reason");
    within(drawer()).getByText("future_disposition: 1");
    // The funnel renders candidates -> fusion kept -> planned -> delivered.
    within(drawer()).getByText("candidates");
    within(drawer()).getByText("delivered");

    await user.click(within(drawer()).getByRole("button", { name: "关闭" }));
    expect(document.querySelector(".drawer")).toBeNull();
    expect(callsTo(fetchMock, "/v1/admin/operations/recalls/41")).toHaveLength(1);
  });
});

describe("section 12 item 12: recall drawer lifecycle for 404, 410 and 500", () => {
  async function openDrawer(recall: (id: string) => Response) {
    const { fetchMock, user } = await renderOperationsPage({
      events: () => jsonResponse(eventsPage([RECALL_EVENT])),
      recall,
    });
    await user.click(await screen.findByRole("button", { name: "Inspect recall" }));
    return { fetchMock, user };
  }

  it("404 recall_diagnostic_not_found is terminal and keeps the activity row", async () => {
    const { fetchMock } = await openDrawer(() =>
      apiErrorResponse(404, "recall_diagnostic_not_found", "never existed"),
    );

    await within(drawer()).findByText(/诊断不存在/);
    // No automatic retry and no retry affordance (doc section 9).
    expect(within(drawer()).queryByRole("button", { name: "重试" })).toBeNull();
    expect(callsTo(fetchMock, "/v1/admin/operations/recalls/41")).toHaveLength(1);
    // The safe activity row stays in the table.
    screen.getByRole("button", { name: "Inspect recall" });
  });

  it("410 diagnostic_expired is terminal and keeps the activity row", async () => {
    const { fetchMock } = await openDrawer(() =>
      apiErrorResponse(410, "diagnostic_expired", "diagnostic cleaned up"),
    );

    await within(drawer()).findByText(/诊断已过期/);
    expect(within(drawer()).queryByRole("button", { name: "重试" })).toBeNull();
    expect(callsTo(fetchMock, "/v1/admin/operations/recalls/41")).toHaveLength(1);
    screen.getByRole("button", { name: "Inspect recall" });
  });

  it("500 stays region-local with a manual retry that recovers", async () => {
    let calls = 0;
    const { fetchMock, user } = await openDrawer(() => {
      calls += 1;
      return calls === 1
        ? apiErrorResponse(500, "internal_error", "boom")
        : jsonResponse({ recall: makeRecall() });
    });

    await within(drawer()).findByText("服务端错误，请稍后重试");
    await user.click(within(drawer()).getByRole("button", { name: "重试" }));

    await within(drawer()).findByText("Delivery funnel");
    expect(callsTo(fetchMock, "/v1/admin/operations/recalls/41")).toHaveLength(2);
  });
});

describe("section 12 item 13: drawer data never leaks to URL, storage or console", () => {
  it("opening and closing the drawer leaves no trace outside React memory", async () => {
    const spies = [
      vi.spyOn(console, "log"),
      vi.spyOn(console, "info"),
      vi.spyOn(console, "warn"),
      vi.spyOn(console, "error"),
      vi.spyOn(console, "debug"),
    ];
    const { user } = await renderOperationsPage({
      events: () => jsonResponse(eventsPage([RECALL_EVENT])),
    });

    await user.click(await screen.findByRole("button", { name: "Inspect recall" }));
    await within(drawer()).findByText("Delivery funnel");
    // The session id is part of the safe projection and may render...
    within(drawer()).getByText("session-42");
    await user.click(within(drawer()).getByRole("button", { name: "关闭" }));

    // ...but it never reaches the URL, durable storage or the console.
    expect(window.location.pathname).toBe("/admin/operations");
    expect(window.location.search).toBe("");
    expect(window.location.hash).toBe("");
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
    for (const spy of spies) {
      for (const args of spy.mock.calls) {
        expect(args.map(String).join(" ")).not.toContain("session-42");
      }
    }
  });
});

describe("section 12 item 16: unknown operation and outcome codes fall back safely", () => {
  it("renders unknown kinds and outcomes as raw safe codes", async () => {
    await renderOperationsPage({
      events: () =>
        jsonResponse(
          eventsPage([
            makeEvent({
              operation_event_id: 812,
              operation_kind: "memory.vector_search" as OperationKind,
              outcome: "purged" as OperationOutcome,
            }),
          ]),
        ),
    });

    const table = eventsTable();
    within(table).getByText("memory.vector_search");
    expect(within(table).getByText("purged").className).toContain("badge");
  });
});

describe("section 12 item 17: cursor termination and invalid-cursor recovery", () => {
  it("a missing next_cursor hides the load-more button", async () => {
    await renderOperationsPage({
      events: () => jsonResponse(eventsPage([makeEvent()])),
    });

    await within(eventsTable()).findByText("Memory Search");
    expect(screen.queryByRole("button", { name: "加载更多" })).toBeNull();
  });

  it("a 400 on the cursor restarts from the first page", async () => {
    const events = (path: string): Response => {
      const params = new URLSearchParams(path.split("?")[1]);
      if (params.get("cursor") === "c2") {
        return apiErrorResponse(400, "invalid_request", "cursor expired past retention");
      }
      return jsonResponse(
        eventsPage([makeEvent({ operation_event_id: 812, duration_ms: 12 })], "c2"),
      );
    };
    const { fetchMock, user } = await renderOperationsPage({ events });

    await user.click(await screen.findByRole("button", { name: "加载更多" }));

    await waitFor(() =>
      expect(callsTo(fetchMock, "/v1/admin/operations/events")).toHaveLength(3),
    );
    const calls = callsTo(fetchMock, "/v1/admin/operations/events");
    expect(new URLSearchParams(calls[1].path.split("?")[1]).get("cursor")).toBe("c2");
    // The recovery refetch drops the cursor and reloads page one.
    expect(new URLSearchParams(calls[2].path.split("?")[1]).get("cursor")).toBeNull();
    await within(eventsTable()).findByText("12 ms");
    expect(within(eventsTable()).getAllByRole("row")).toHaveLength(2);
  });
});

describe("section 12 item 19: the raw agent id survives label enrichment problems", () => {
  it("shows the display name with the raw id when enrichment succeeds", async () => {
    await renderOperationsPage({
      events: () => jsonResponse(eventsPage([makeEvent()])),
      agents: () =>
        jsonResponse({ agents: [makeAgent({ agent_id: "agent-1", display_name: "Alice Codex" })] }),
    });

    const row = (await within(eventsTable()).findByText("Alice Codex")).closest(
      "tr",
    ) as HTMLElement;
    within(row).getByText("(agent-1)");
  });

  it("falls back to the raw id for retired agents and failed enrichment", async () => {
    await renderOperationsPage({
      events: () =>
        jsonResponse(
          eventsPage([
            makeEvent({ operation_event_id: 812, duration_ms: 12 }),
            makeEvent({ operation_event_id: 811, actor_agent_id: "agent-9", duration_ms: 13 }),
          ]),
        ),
      // The retired agent is absent from the directory and the request fails.
      agents: () => apiErrorResponse(500, "internal_error", "boom"),
    });

    const table = eventsTable();
    await within(table).findByText("agent-1");
    within(table).getByText("agent-9");
  });
});

describe("section 12 item 20: operations traffic carries no unsafe content", () => {
  it("all operations requests are bodyless GETs with allow-listed params only", async () => {
    const { fetchMock, user } = await renderOperationsPage({
      events: () => jsonResponse(eventsPage([RECALL_EVENT])),
    });
    await user.click(await screen.findByRole("button", { name: "Inspect recall" }));
    await within(drawer()).findByText("Delivery funnel");

    const allowed = new Set([
      "from",
      "to",
      "agent_id",
      "operation_kind",
      "outcome",
      "limit",
      "cursor",
    ]);
    const opsCalls = callsTo(fetchMock, "/v1/admin/operations");
    // summary + events + storage + recall detail.
    expect(opsCalls.length).toBeGreaterThanOrEqual(4);
    for (const call of opsCalls) {
      expect((call.init.method ?? "GET").toUpperCase()).toBe("GET");
      expect(call.init.body).toBeUndefined();
      for (const key of new URLSearchParams(call.path.split("?")[1] ?? "").keys()) {
        expect(allowed.has(key)).toBe(true);
      }
    }
    // Payload internals the page must not display stay unrendered.
    expect(screen.queryByText(/op_q6Jx/)).toBeNull();
  });
});
