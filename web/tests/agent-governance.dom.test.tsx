// Page-level DOM smoke tests for agent governance (doc sections 5.6-5.7)
// covering section 10 item 10 (capability-based actions, cascade wording,
// retire semantics) and item 13 (filter changes reset cursor pagination).

import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import {
  callsTo,
  jsonResponse,
  makeAgent,
  makeMe,
  renderApp,
  setupDomTest,
} from "./helpers";

setupDomTest();

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

function detailHandler(agent: () => ReturnType<typeof makeAgent>, onPatch?: (init: RequestInit) => Response, onDelete?: (init: RequestInit) => Response) {
  return (path: string, init: RequestInit) => {
    if (path === "/v1/admin/agents/agent-1" && init.method === "GET") {
      return jsonResponse({ agent: agent() });
    }
    if (path === "/v1/admin/agents/agent-1" && init.method === "PATCH" && onPatch) {
      return onPatch(init);
    }
    if (path.startsWith("/v1/admin/agents/agent-1?") && init.method === "DELETE" && onDelete) {
      return onDelete(init);
    }
    if (path.startsWith("/v1/admin/agents/agent-1/enrollments")) {
      return jsonResponse({ enrollments: [] });
    }
    if (path.startsWith("/v1/admin/agents/agent-1/credentials")) {
      return jsonResponse({ credentials: [] });
    }
    throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
  };
}

describe("section 10 item 10: governance capabilities and lifecycle actions", () => {
  it("an admin can suspend but never retire, edit, or resume a foreign agent", async () => {
    await renderApp({
      route: "/admin/agents/agent-1",
      me: makeMe({ role: "admin" }),
      fetch: detailHandler(() => makeAgent({ owner_membership_id: "mbr_02" })),
    });

    await screen.findByRole("button", { name: "暂停 Agent" });
    expect(screen.queryByText(/Retire/)).toBeNull();
    expect(screen.queryByText("恢复 active")).toBeNull();
    expect((screen.getByLabelText("display_name") as HTMLInputElement).disabled).toBe(true);
    expect(screen.queryByRole("button", { name: "保存" })).toBeNull();
  });

  it("the suspend confirmation spells out the cascade and PATCHes with optimistic locking", async () => {
    let suspended = false;
    const { fetchMock, user } = await renderApp({
      route: "/admin/agents",
      me: makeMe(),
      fetch: (path, init) => {
        if (path === "/v1/admin/agents" && init.method === "GET") {
          return jsonResponse({ agents: [makeAgent({ status: suspended ? "suspended" : "active" })] });
        }
        if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [] });
        if (path === "/v1/admin/agents/agent-1" && init.method === "PATCH") {
          suspended = true;
          return jsonResponse({ agent: makeAgent({ status: "suspended", resource_version: 8 }) });
        }
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });

    await user.click(await screen.findByRole("button", { name: "暂停" }));
    // The confirmation dialog states the immediate cascade up front.
    await screen.findByRole("heading", { name: "暂停 Agent" });
    screen.getByText(/立即吊销 Alice Codex 的全部 Credential 和 pending Enrollment/);
    screen.getByText(/恢复 active 不会还原旧 key，必须重新签发 Enrollment/);

    await user.click(screen.getByRole("button", { name: "确认暂停" }));
    await screen.findByText("已暂停并级联吊销 Credential 与 pending Enrollment");

    const patches = callsTo(fetchMock, "/v1/admin/agents/agent-1", "PATCH");
    expect(patches).toHaveLength(1);
    expect(patches[0].headers.get("If-Match")).toBe('"7"');
    expect(JSON.parse(String(patches[0].init.body))).toEqual({
      status: "suspended",
      resource_version: 7,
    });
  });

  it("retire is a DELETE with Idempotency-Key; the edit form can never issue status=retired", async () => {
    let current = makeAgent();
    const { fetchMock, user } = await renderApp({
      route: "/admin/agents/agent-1",
      me: makeMe(),
      fetch: detailHandler(
        () => current,
        () => {
          current = makeAgent({ display_name: "Renamed", resource_version: 8 });
          return jsonResponse({ agent: current });
        },
        () => {
          current = makeAgent({ status: "retired", retired_at: "2026-07-22T00:00:00Z", resource_version: 9 });
          return jsonResponse({ agent: current });
        },
      ),
    });

    // A profile save never carries a status field, so the UI cannot express
    // the forbidden PATCH status=retired (doc section 10 item 10).
    const nameInput = screen.getByLabelText("display_name");
    await user.clear(nameInput);
    await user.type(nameInput, "Renamed");
    await user.click(await screen.findByRole("button", { name: "保存" }));
    await screen.findByText("已保存（v8）");
    const patches = callsTo(fetchMock, "/v1/admin/agents/agent-1", "PATCH");
    expect(patches).toHaveLength(1);
    expect(JSON.parse(String(patches[0].init.body))).not.toHaveProperty("status");

    // Retire goes through the terminal DELETE with both optimistic locking
    // and an Idempotency-Key.
    await user.click(screen.getByRole("button", { name: "Retire（不可逆）" }));
    await screen.findByRole("heading", { name: "Retire Agent" });
    screen.getByText("Retire 是终态、不可逆，Agent 不可恢复");
    screen.getByText("全部 Credential 与 Enrollment 立即吊销");
    await user.click(screen.getByRole("button", { name: "确认 Retire" }));
    await screen.findByText("Agent 已 retire（终态，不可恢复）");

    const deletes = callsTo(fetchMock, "/v1/admin/agents/agent-1", "DELETE");
    expect(deletes).toHaveLength(1);
    expect(deletes[0].path).toBe("/v1/admin/agents/agent-1?resource_version=8");
    expect(deletes[0].headers.get("If-Match")).toBe('"8"');
    expect(deletes[0].headers.get("Idempotency-Key")).toMatch(UUID_RE);
    screen.getByText("retired 为终态，不可恢复");
  });
});

describe("section 10 item 13: filter changes reset cursor pagination", () => {
  it("switching the status filter reloads from page one without mixing old results", async () => {
    const seen: string[] = [];
    const { user } = await renderApp({
      route: "/admin/agents",
      me: makeMe(),
      fetch: (path, init) => {
        if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [] });
        if (path.startsWith("/v1/admin/agents") && init.method === "GET") {
          seen.push(path);
          if (path === "/v1/admin/agents") {
            return jsonResponse({
              agents: [makeAgent({ agent_id: "agent-1", display_name: "First Agent" })],
              next_cursor: "opaque-c2",
            });
          }
          if (path === "/v1/admin/agents?cursor=opaque-c2") {
            return jsonResponse({
              agents: [makeAgent({ agent_id: "agent-2", display_name: "Second Agent" })],
            });
          }
          if (path === "/v1/admin/agents?status=suspended") {
            return jsonResponse({
              agents: [
                makeAgent({ agent_id: "agent-3", display_name: "Suspended Agent", status: "suspended" }),
              ],
            });
          }
        }
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });

    // Page one loads, then the cursor is passed back verbatim for page two.
    await screen.findByText("First Agent");
    await user.click(screen.getByRole("button", { name: "加载更多" }));
    await screen.findByText("Second Agent");

    // Switching filters mid-pagination restarts without a cursor; the old
    // pages are dropped rather than mixed in.
    await user.click(screen.getByRole("button", { name: "suspended" }));
    await screen.findByText("Suspended Agent");
    expect(screen.queryByText("First Agent")).toBeNull();
    expect(screen.queryByText("Second Agent")).toBeNull();

    expect(seen).toEqual([
      "/v1/admin/agents",
      "/v1/admin/agents?cursor=opaque-c2",
      "/v1/admin/agents?status=suspended",
    ]);
  });
});
