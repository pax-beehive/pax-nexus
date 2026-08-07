// Page-level DOM tests for the sidebar team switcher (design/m3-teams 02,
// M3 P6): rendering under the brand block, switching re-scopes the session
// via POST /v1/me/current-team, and the popover closes on Escape.

import { describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import {
  callsTo,
  jsonResponse,
  makeSaasMe,
  renderApp,
  setupDomTest,
} from "./helpers";

setupDomTest();

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

function agentsFetch(path: string, init: RequestInit): Response {
  if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
  throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
}

describe("team switcher", () => {
  it("renders the current team under the brand block and lists all teams in the popover", async () => {
    const { user } = await renderApp({
      route: "/management",
      me: makeSaasMe({ role: "member" }),
      fetch: agentsFetch,
    });

    await screen.findByRole("heading", { name: "My Agents" });
    // Mockup 04: the page subtitle shows the current team scope.
    expect(screen.getByText("Acme ML team scope")).toBeTruthy();

    const trigger = screen.getByRole("button", { name: /Acme ML/ });
    expect(trigger.getAttribute("aria-expanded")).toBe("false");

    await user.click(trigger);
    expect(trigger.getAttribute("aria-expanded")).toBe("true");
    const popover = screen.getByRole("menu", { name: "Switch team" });
    const items = within(popover).getAllByRole("menuitem");
    expect(items.map((item) => item.textContent)).toEqual([
      "AAcme MLowner✓",
      "WWeekend Projectsmember",
      "+Create a team",
      "→Join with invitation",
    ]);

    // Escape closes the popover.
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("menu", { name: "Switch team" })).toBeNull();
  });

  it("switches the current team and re-scopes the UI", async () => {
    let switched = false;
    const { fetchMock, user } = await renderApp({
      route: "/management",
      me: () =>
        switched
          ? makeSaasMe({ role: "member", current_team_id: "team_beta" })
          : makeSaasMe({ role: "member" }),
      fetch: (path, init) => {
        if (path === "/v1/me/current-team" && init.method === "POST") {
          switched = true;
          return jsonResponse(makeSaasMe({ current_team_id: "team_beta" }));
        }
        return agentsFetch(path, init);
      },
    });

    await screen.findByRole("heading", { name: "My Agents" });
    await user.click(screen.getByRole("button", { name: /Acme ML/ }));
    await user.click(
      within(screen.getByRole("menu", { name: "Switch team" })).getByRole("menuitem", {
        name: /Weekend Projects/,
      }),
    );

    // The session switch POSTs the target team and the auth state refreshes
    // from /v1/me afterwards.
    const switches = callsTo(fetchMock, "/v1/me/current-team", "POST");
    expect(switches).toHaveLength(1);
    expect(JSON.parse(String(switches[0].init.body))).toEqual({ team_id: "team_beta" });
    expect(switches[0].headers.get("Idempotency-Key")).toMatch(UUID_RE);

    // UI re-scopes: the trigger and the page subtitle follow the new team.
    await screen.findByText("Weekend Projects team scope");
    expect(screen.getByRole("button", { name: /Weekend Projects/ })).toBeTruthy();
    expect(callsTo(fetchMock, "/v1/me").length).toBeGreaterThanOrEqual(2);
  });

  it("navigates to /welcome (join-another-team) from the popover footer rows", async () => {
    // Modernist Portal phase 7 task 1 merged the dedicated onboarding page
    // into /welcome; /onboarding now redirects there (app/routes.tsx). That
    // route table also registers /welcome itself for an active session
    // (App.tsx's own /welcome registration only mounts while no-membership,
    // so the two never collide — see WelcomePage.tsx), so this footer row
    // still reaches a real join-with-token screen instead of silently
    // bouncing back to the landing page.
    const { user } = await renderApp({
      route: "/management",
      me: makeSaasMe({ role: "member" }),
      fetch: agentsFetch,
    });

    await screen.findByRole("heading", { name: "My Agents" });
    await user.click(screen.getByRole("button", { name: /Acme ML/ }));
    await user.click(
      within(screen.getByRole("menu", { name: "Switch team" })).getByRole("menuitem", {
        name: /Join with invitation/,
      }),
    );

    // /onboarding -> /welcome, rendered inside the shell for this active
    // session, with the "join another team" copy (not the bare
    // no-membership waiting state).
    await screen.findByRole("heading", { name: "Join another team" });
    expect(window.location.pathname).toBe("/welcome");
    expect(screen.getByLabelText("Invitation token or link")).toBeTruthy();
    // Bootstrap claim never applies to an already-active session.
    expect(screen.queryByRole("button", { name: "Claim owner" })).toBeNull();
  });
});
