import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

describe("Portal sidebar collapse", () => {
  it("collapses the navigation rail, persists the choice, and expands it again", async () => {
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

    screen.getByRole("navigation", { name: "Portal navigation" });
    await user.click(screen.getByRole("button", { name: "Collapse navigation" }));

    expect(screen.queryByRole("navigation", { name: "Portal navigation" })).toBeNull();
    expect(screen.queryByText("Team Memory")).toBeNull();
    expect(localStorage.getItem("portal.side-collapsed")).toBe("1");

    await user.click(screen.getByRole("button", { name: "Expand navigation" }));

    screen.getByRole("navigation", { name: "Portal navigation" });
    expect(localStorage.getItem("portal.side-collapsed")).toBe("0");
  });
});
