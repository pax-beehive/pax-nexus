// Agent 详情页头的两个跳转要真的带上过滤，否则点过去是一张空表、用户还得
// 自己粘 agent_id。这里钉住「URL 参数进筛选框，并进首次请求」。
import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

describe("/governance/sessions?agent=", () => {
  it("参数进筛选框并进首次请求", async () => {
    const { fetchMock } = await renderApp({
      route: "/governance/sessions?agent=agent-1",
      me: makeMe({ role: "owner" }),
      fetch: (path: string) => {
        if (path.startsWith("/v1/admin/session-audit/findings")) return jsonResponse({ findings: [] });
        if (path.startsWith("/v1/admin/session-audit/tool-calls")) return jsonResponse({ tool_calls: [] });
        if (path.startsWith("/v1/admin/session-audit/activity")) return jsonResponse({ activity: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    expect(((await screen.findByPlaceholderText("agent_id")) as HTMLInputElement).value).toBe("agent-1");
    expect(
      fetchMock.mock.calls.some(([url]) => String(url).includes("agent_id=agent-1")),
    ).toBe(true);
  });

  it("没有参数时筛选框为空", async () => {
    await renderApp({
      route: "/governance/sessions",
      me: makeMe({ role: "owner" }),
      fetch: (path: string) => {
        if (path.startsWith("/v1/admin/session-audit/")) return jsonResponse({ findings: [], tool_calls: [], activity: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    expect(((await screen.findByPlaceholderText("agent_id")) as HTMLInputElement).value).toBe("");
  });
});

describe("/governance/memory?agent=", () => {
  it("参数进筛选框并进首次请求", async () => {
    const { fetchMock } = await renderApp({
      route: "/governance/memory?agent=agent-1",
      me: makeMe({ role: "owner", capabilities: ["view.team-memory"] }),
      fetch: (path: string) => {
        if (path.startsWith("/v1/admin/team-notes")) return jsonResponse({ notes: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    expect(
      ((await screen.findByPlaceholderText("Agent ID")) as HTMLInputElement).value,
    ).toBe("agent-1");
    expect(
      fetchMock.mock.calls.some(([url]) => String(url).includes("agent_id=agent-1")),
    ).toBe(true);
  });
});
