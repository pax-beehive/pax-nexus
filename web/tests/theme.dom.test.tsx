// 主题控件从侧边栏迁到 /settings/appearance；持久化与 data-theme 的行为不变。
import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

function appearanceFetch(path: string, init: RequestInit): Response {
  if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
  if (path.startsWith("/v1/teams")) return jsonResponse({ teams: [] });
  throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
}

describe("Appearance", () => {
  it("defaults to beige with no data-theme attribute", async () => {
    await renderApp({
      route: "/settings/appearance",
      me: makeMe({ role: "member" }),
      fetch: appearanceFetch,
    });

    await screen.findByRole("heading", { name: "Appearance" });
    expect(document.documentElement.dataset.theme).toBeUndefined();
  });

  it("applies and persists a non-default theme", async () => {
    const { user } = await renderApp({
      route: "/settings/appearance",
      me: makeMe({ role: "member" }),
      fetch: appearanceFetch,
    });

    await user.click(await screen.findByRole("button", { name: "Dark" }));

    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(localStorage.getItem("portal-theme")).toBe("dark");
  });

  it("is reachable from the user menu", async () => {
    const { user } = await renderApp({
      route: "/management",
      me: makeMe({ role: "member" }),
      fetch: appearanceFetch,
    });

    await user.click(screen.getByRole("button", { name: /alice@example\.com/ }));
    await user.click(screen.getByRole("menuitem", { name: "Appearance" }));

    await screen.findByRole("heading", { name: "Appearance" });
  });
});
