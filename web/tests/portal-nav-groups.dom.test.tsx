// DOM tests for the collapsible sidebar nav groups: group toggles persist to
// localStorage, defaults open Personal/Knowledge and collapse the admin
// groups, and the group holding the active route auto-expands as a render
// overlay without writing a stored toggle.

import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

function agentsOnlyFetch(path: string, init: RequestInit): Response {
  if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
  throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
}

function storedGroups(): Record<string, boolean> {
  return JSON.parse(localStorage.getItem("portal.nav-groups") ?? "{}") as Record<string, boolean>;
}

function groupToggle(name: string): HTMLElement {
  return screen.getByRole("button", { name });
}

describe("Portal nav groups", () => {
  it("applies the default open state and persists toggles to localStorage", async () => {
    const { user } = await renderApp({ route: "/agents", me: makeMe(), fetch: agentsOnlyFetch });

    expect(groupToggle("Personal").getAttribute("aria-expanded")).toBe("true");
    expect(groupToggle("Knowledge").getAttribute("aria-expanded")).toBe("true");
    expect(groupToggle("Directory").getAttribute("aria-expanded")).toBe("false");
    expect(groupToggle("Fleet").getAttribute("aria-expanded")).toBe("false");
    expect(groupToggle("Insights").getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByRole("link", { name: "All Agents" })).toBeNull();

    await user.click(groupToggle("Fleet"));
    expect(groupToggle("Fleet").getAttribute("aria-expanded")).toBe("true");
    screen.getByRole("link", { name: "All Agents" });
    expect(storedGroups()).toMatchObject({ fleet: true });

    await user.click(groupToggle("Fleet"));
    expect(groupToggle("Fleet").getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByRole("link", { name: "All Agents" })).toBeNull();
    expect(storedGroups()).toMatchObject({ fleet: false });
  });

  it("auto-expands the group holding the active route without storing a toggle", async () => {
    await renderApp({
      route: "/admin/audit",
      me: makeMe(),
      fetch: (path, init) => {
        if (path.startsWith("/v1/admin/audit-events")) return jsonResponse({ audit_events: [] });
        if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [] });
        if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [] });
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "Audit Events" });
    expect(groupToggle("Insights").getAttribute("aria-expanded")).toBe("true");
    screen.getByRole("link", { name: "Session Audit" });
    // Auto-expand is a render overlay: nothing was stored for the group, and
    // the other groups keep their collapsed default.
    expect(storedGroups()).not.toHaveProperty("insights");
    expect(groupToggle("Fleet").getAttribute("aria-expanded")).toBe("false");
  });
});
