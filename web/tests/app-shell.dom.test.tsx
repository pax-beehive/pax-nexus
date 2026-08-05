// 外壳的 DOM 契约：顶栏项按角色渲染、subnav 跟随当前分区、用户菜单能登出。
// 可见性的穷举在 tests/navModel.test.ts，这里只验证渲染与交互。
// Task 9 接线后移除 .skip
import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

function shellFetch(path: string, init: RequestInit): Response {
  if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
  if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [] });
  if (path.startsWith("/v1/teams")) return jsonResponse({ teams: [] });
  throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
}

function topbar(): HTMLElement {
  return screen.getByRole("navigation", { name: "Sections" });
}

describe.skip("AppShell top bar", () => {
  it("renders only the sections a member may see", async () => {
    await renderApp({
      route: "/management",
      me: makeMe({ role: "member" }),
      fetch: shellFetch,
    });

    const nav = topbar();
    within(nav).getByRole("link", { name: "Management" });
    within(nav).getByRole("link", { name: "Apps" });
    within(nav).getByRole("link", { name: "Settings" });
    expect(within(nav).queryByRole("link", { name: "Overview" })).toBeNull();
    expect(within(nav).queryByRole("link", { name: "Governance" })).toBeNull();
  });

  it("marks the active section and renders its sub-navigation", async () => {
    await renderApp({
      route: "/management/members",
      me: makeMe({ role: "admin" }),
      fetch: (path, init) => {
        if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [] });
        return shellFetch(path, init);
      },
    });

    expect(
      within(topbar()).getByRole("link", { name: "Management" }).getAttribute("aria-current"),
    ).toBe("page");
    const sub = screen.getByRole("navigation", { name: "Section pages" });
    within(sub).getByRole("link", { name: "Access tree" });
    within(sub).getByRole("link", { name: "Members" });
    within(sub).getByRole("link", { name: "Devices" });
  });

  it("hides the sub-navigation for a section with no sub-pages", async () => {
    // /overview 在阶段 1 由 AdminPulsePage 顶替，它会请求 operations 的
    // agents 与 events；两个响应都要带上各自的时间字段，否则页面在渲染
    // "generated at" 时抛错，被 ErrorBoundary 接住，断言就测不到本意。
    await renderApp({
      route: "/overview",
      me: makeMe({ capabilities: ["view.operations"] }),
      fetch: (path, init) => {
        if (path.startsWith("/v1/admin/operations/agents")) {
          return jsonResponse({
            agents: [],
            from_time: "2026-08-04T00:00:00Z",
            to_time: "2026-08-04T01:00:00Z",
            generated_at: "2026-08-04T01:00:00Z",
          });
        }
        if (path.startsWith("/v1/admin/operations/events")) {
          return jsonResponse({ events: [], generated_at: "2026-08-04T01:00:00Z" });
        }
        return shellFetch(path, init);
      },
    });

    await screen.findByRole("heading", { name: "Team Pulse" });
    expect(screen.queryByRole("navigation", { name: "Section pages" })).toBeNull();
  });

  it("signs out from the user menu", async () => {
    const { user, fetchMock } = await renderApp({
      route: "/management",
      me: makeMe({ role: "member" }),
      fetch: (path, init) => {
        if (path === "/v1/auth/logout") return jsonResponse({});
        return shellFetch(path, init);
      },
    });

    await user.click(screen.getByRole("button", { name: /alice@example\.com/ }));
    await user.click(screen.getByRole("menuitem", { name: "Sign out" }));

    expect(
      fetchMock.mock.calls.some(([input, init]) =>
        String(input) === "/v1/auth/logout" && (init as RequestInit)?.method === "POST",
      ),
    ).toBe(true);
  });

  it("closes the user menu on Escape", async () => {
    const { user } = await renderApp({
      route: "/management",
      me: makeMe({ role: "member" }),
      fetch: shellFetch,
    });

    await user.click(screen.getByRole("button", { name: /alice@example\.com/ }));
    screen.getByRole("menu");
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("menu")).toBeNull();
  });
});
