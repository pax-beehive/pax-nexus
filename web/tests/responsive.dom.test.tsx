// jsdom 不做布局，所以断言的是「窄屏下渲染哪套 DOM」，而不是像素。
// matchMedia 在 jsdom 里不存在，这里按测试需要打桩。
import { screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

function stubMatchMedia(matches: boolean): void {
  vi.stubGlobal(
    "matchMedia",
    (query: string) =>
      ({
        matches,
        media: query,
        onchange: null,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
      }) as unknown as MediaQueryList,
  );
}

function shellFetch(path: string, init: RequestInit): Response {
  if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
  if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [] });
  if (path.startsWith("/v1/teams")) return jsonResponse({ teams: [] });
  throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
}

describe("responsive top bar", () => {
  it("renders the inline section nav on wide viewports", async () => {
    stubMatchMedia(false);
    await renderApp({ route: "/management", me: makeMe({ role: "admin" }), fetch: shellFetch });

    screen.getByRole("navigation", { name: "Sections" });
    expect(screen.queryByRole("button", { name: "Menu" })).toBeNull();
  });

  it("collapses the section nav into a menu on narrow viewports", async () => {
    stubMatchMedia(true);
    const { user } = await renderApp({
      route: "/management",
      me: makeMe({ role: "admin" }),
      fetch: shellFetch,
    });

    expect(screen.queryByRole("navigation", { name: "Sections" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Menu" }));
    const menu = screen.getByRole("menu", { name: "Sections" });
    expect(menu).toBeTruthy();
  });
});
