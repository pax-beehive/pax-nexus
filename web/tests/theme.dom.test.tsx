// 主题控件从侧边栏迁到 /settings/appearance；持久化与 data-theme 的行为不变。
import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { getStoredTheme } from "../src/lib/theme";
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

    await screen.findByRole("heading", { name: "它看起来什么样" });
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

  // Arcade is the theme whose contrast forced a spec amendment (bg dropped
  // to accent-600 for 4.74:1). design-tokens.test.ts checks the ratio from
  // the stylesheet; nothing was selecting it through the actual control.
  it("applies and persists arcade", async () => {
    const { user } = await renderApp({
      route: "/settings/appearance",
      me: makeMe({ role: "member" }),
      fetch: appearanceFetch,
    });

    await user.click(await screen.findByRole("button", { name: "Arcade" }));

    expect(document.documentElement.dataset.theme).toBe("arcade");
    expect(localStorage.getItem("portal-theme")).toBe("arcade");
  });

  // The default theme is bare :root, so going back to it must REMOVE the
  // attribute rather than set data-theme="beige" — which would select no
  // rule at all and silently strand the user on the previous theme's
  // values. Only reachable after the attribute has actually been set.
  it("clears the data-theme attribute when reverting to the default", async () => {
    const { user } = await renderApp({
      route: "/settings/appearance",
      me: makeMe({ role: "member" }),
      fetch: appearanceFetch,
    });

    await user.click(await screen.findByRole("button", { name: "Dark" }));
    expect(document.documentElement.dataset.theme).toBe("dark");

    await user.click(screen.getByRole("button", { name: "Beige" }));

    expect(document.documentElement.dataset.theme).toBeUndefined();
    expect(localStorage.getItem("portal-theme")).toBe("beige");
  });

  // A persisted value from a retired (or tampered) theme name must fall back
  // to the default instead of being written onto data-theme, where it would
  // match no rule.
  it("falls back to beige when the persisted theme is not a known theme", () => {
    localStorage.setItem("portal-theme", "arcade");
    expect(getStoredTheme()).toBe("arcade");

    localStorage.setItem("portal-theme", "neon");
    expect(getStoredTheme()).toBe("beige");
  });

  it("is reachable from the user menu", async () => {
    const { user } = await renderApp({
      route: "/management",
      me: makeMe({ role: "member" }),
      fetch: appearanceFetch,
    });

    await user.click(screen.getByRole("button", { name: /alice@example\.com/ }));
    await user.click(screen.getByRole("menuitem", { name: "Appearance" }));

    await screen.findByRole("heading", { name: "它看起来什么样" });
  });
});
