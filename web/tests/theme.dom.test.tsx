import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { getStoredTheme } from "../src/lib/theme";
import { jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

describe("Theme picker", () => {
  it("defaults to beige, switches themes, and persists the choice", async () => {
    const { user } = await renderApp({
      route: "/agents",
      me: makeMe({ role: "member" }),
      fetch: (path) => {
        if (path === "/v1/me/agents" || path.startsWith("/v1/me/agents?")) {
          return jsonResponse({ agents: [] });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const picker = screen.getByRole("combobox", { name: "Theme" });
    expect((picker as HTMLSelectElement).value).toBe("beige");
    expect(document.documentElement.dataset.theme).toBeUndefined();

    await user.selectOptions(picker, "dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(localStorage.getItem("portal-theme")).toBe("dark");

    await user.selectOptions(picker, "arcade");
    expect(document.documentElement.dataset.theme).toBe("arcade");
    expect(localStorage.getItem("portal-theme")).toBe("arcade");

    await user.selectOptions(picker, "beige");
    expect(document.documentElement.dataset.theme).toBeUndefined();
    expect(localStorage.getItem("portal-theme")).toBe("beige");
  });

  it("restores the stored theme on load", () => {
    localStorage.setItem("portal-theme", "arcade");
    expect(getStoredTheme()).toBe("arcade");

    localStorage.setItem("portal-theme", "neon");
    expect(getStoredTheme()).toBe("beige");
  });
});
