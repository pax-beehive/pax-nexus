import { afterEach, describe, expect, it, vi } from "vitest";
import {
  getOperationsStorage,
  getOperationsSummary,
  getRecallDiagnostic,
  listOperationEvents,
  listOperationsStorageHistory,
} from "../src/api/queries";

afterEach(() => {
  vi.unstubAllGlobals();
});

function stubFetch(payload: unknown) {
  const fetchMock = vi
    .fn()
    .mockResolvedValue(new Response(JSON.stringify(payload), { status: 200 }));
  vi.stubGlobal("document", { cookie: "" });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

describe("operations query wrappers (operations doc section 6)", () => {
  it("listOperationEvents uses the operation_kind query name and maps the response", async () => {
    const fetchMock = stubFetch({
      events: [{ operation_event_id: 812 }],
      next_cursor: "opaque-cursor",
      generated_at: "2026-07-22T12:00:01Z",
    });

    const page = await listOperationEvents({
      from: "2026-07-22T10:00:00Z",
      to: "2026-07-22T12:00:00Z",
      agent_id: "agent-1",
      operation_kind: "memory.search",
      outcome: "succeeded",
      limit: 50,
      cursor: "page-2",
    });

    const [path] = fetchMock.mock.calls[0] as [string, RequestInit];
    const params = new URLSearchParams(path.split("?")[1]);
    expect(params.get("operation_kind")).toBe("memory.search");
    expect(params.get("kind")).toBeNull();
    expect(params.get("outcome")).toBe("succeeded");
    expect(params.get("agent_id")).toBe("agent-1");
    expect(params.get("from")).toBe("2026-07-22T10:00:00Z");
    expect(params.get("to")).toBe("2026-07-22T12:00:00Z");
    expect(params.get("limit")).toBe("50");
    expect(params.get("cursor")).toBe("page-2");
    expect(page).toEqual({
      items: [{ operation_event_id: 812 }],
      nextCursor: "opaque-cursor",
      generatedAt: "2026-07-22T12:00:01Z",
    });
  });

  it("listOperationEvents omits empty values and never sends an empty cursor", async () => {
    const fetchMock = stubFetch({ events: [], generated_at: "2026-07-22T12:00:01Z" });

    const page = await listOperationEvents({});

    const [path] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/v1/admin/operations/events");
    expect(page.nextCursor).toBeUndefined();
  });

  it("getOperationsSummary forwards filters and the AbortSignal", async () => {
    const fetchMock = stubFetch({ from_time: "2026-07-22T10:00:00Z" });
    const controller = new AbortController();

    await getOperationsSummary(
      { from: "2026-07-22T10:00:00Z", to: "2026-07-22T12:00:00Z", agent_id: "agent-1" },
      controller.signal,
    );

    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    const params = new URLSearchParams(path.split("?")[1]);
    expect(params.get("from")).toBe("2026-07-22T10:00:00Z");
    expect(params.get("to")).toBe("2026-07-22T12:00:00Z");
    expect(params.get("agent_id")).toBe("agent-1");
    expect(init.signal).toBe(controller.signal);
  });

  it("getRecallDiagnostic encodes the id and unwraps the recall envelope", async () => {
    const fetchMock = stubFetch({ recall: { observation_id: 41 } });

    const recall = await getRecallDiagnostic(41);

    const [path] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/v1/admin/operations/recalls/41");
    expect(recall).toEqual({ observation_id: 41 });
  });

  it("getOperationsStorage unwraps the storage envelope", async () => {
    const fetchMock = stubFetch({ storage: { snapshot_id: 91 } });

    const snapshot = await getOperationsStorage();

    const [path] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/v1/admin/operations/storage");
    expect(snapshot).toEqual({ snapshot_id: 91 });
  });

  it("listOperationsStorageHistory maps snapshots and the opaque cursor", async () => {
    const fetchMock = stubFetch({
      snapshots: [{ snapshot_id: 90 }, { snapshot_id: 91 }],
      next_cursor: "history-cursor",
    });

    const page = await listOperationsStorageHistory({ limit: 50, cursor: "opaque-page-2" });

    const [path] = fetchMock.mock.calls[0] as [string, RequestInit];
    const params = new URLSearchParams(path.split("?")[1]);
    expect(path.startsWith("/v1/admin/operations/storage/history?")).toBe(true);
    expect(params.get("limit")).toBe("50");
    expect(params.get("cursor")).toBe("opaque-page-2");
    expect(page).toEqual({
      items: [{ snapshot_id: 90 }, { snapshot_id: 91 }],
      nextCursor: "history-cursor",
    });
  });
});
