// Governance · Memory explorer 双栏页：左栏（NoteList，Task 6）与右栏
// （溯源链 + 召回表，Task 7）各自独立取数。核心不变量是：
//   - 左栏永远不因为打开一条笔记而多打请求（NoteList 自己管自己的分页）
//   - 打开一条笔记只发一次 getTeamNote——六段链条全从这一次响应里取
//   - 任何一段缺记录，只影响那一段，其余段与整条链的可读性不受影响
import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { callsTo, jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

const NOW = "2026-08-06T12:00:00Z";

function noteSummary(overrides: Record<string, unknown> = {}) {
  return {
    note_id: "note_01",
    kind: "decision",
    subject: "用 Postgres 存 evidence",
    state: "active",
    origin_agent_id: "alice-codex",
    audience_agent_ids: [],
    revision: 2,
    created_at: NOW,
    updated_at: NOW,
    soft_expires_at: "2026-09-01T00:00:00Z",
    hard_expires_at: "2026-10-01T00:00:00Z",
    ...overrides,
  };
}

const ME = makeMe({ role: "owner", capabilities: ["view.team-memory"] });

describe("Governance · Memory explorer — 左栏骨架", () => {
  it("/governance/memory 渲染左栏，且不发 getTeamNote", async () => {
    const { fetchMock } = await renderApp({
      route: "/governance/memory",
      me: ME,
      fetch: (path) => {
        if (path.startsWith("/v1/admin/team-notes")) {
          return jsonResponse({ notes: [noteSummary()] });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("link", { name: /用 Postgres 存 evidence/ });
    expect(callsTo(fetchMock, "/v1/admin/team-notes/note_01")).toHaveLength(0);
  });

  it("左栏链接指向 /governance/memory/:id", async () => {
    await renderApp({
      route: "/governance/memory",
      me: ME,
      fetch: (path) => {
        if (path.startsWith("/v1/admin/team-notes")) {
          return jsonResponse({ notes: [noteSummary()] });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const link = (await screen.findByRole("link", {
      name: /用 Postgres 存 evidence/,
    })) as HTMLAnchorElement;
    expect(link.getAttribute("href")).toBe("/governance/memory/note_01");
  });

  it("左栏取数失败时塌成可重试错误", async () => {
    let calls = 0;
    const { user } = await renderApp({
      route: "/governance/memory",
      me: ME,
      fetch: (path) => {
        if (path.startsWith("/v1/admin/team-notes")) {
          calls += 1;
          if (calls === 1) return jsonResponse({ code: "internal", message: "boom" }, 500);
          return jsonResponse({ notes: [noteSummary()] });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByText("加载失败。");
    await user.click(screen.getByRole("button", { name: "重试" }));
    await screen.findByRole("link", { name: /用 Postgres 存 evidence/ });
  });
});
