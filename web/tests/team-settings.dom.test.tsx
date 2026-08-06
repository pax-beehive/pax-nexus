// Page-level DOM tests for the /team settings page (design/m3-teams 03
// General panel, M3 P6): team info card, disabled Rename with the
// "coming soon" hint (no backend endpoint yet), and links to the existing
// team-scoped Members / Invitations admin pages.

import { describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import {
  apiErrorResponse,
  jsonResponse,
  makeSaasMe,
  makeTeamSummary,
  renderApp,
  setupDomTest,
} from "./helpers";

setupDomTest();

function agentsFetch(path: string, init: RequestInit): Response {
  if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
  if (path.startsWith("/v1/teams")) {
    return jsonResponse({
      teams: [
        makeTeamSummary(),
        makeTeamSummary({
          team_id: "team_beta",
          name: "Weekend Projects",
          slug: "weekend-projects",
          role: "member",
          membership_id: "mbr_02",
        }),
      ],
    });
  }
  throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
}

describe("team settings page", () => {
  it("renders the general panel with team info and a disabled rename", async () => {
    await renderApp({ route: "/settings/team", me: makeSaasMe(), fetch: agentsFetch });

    // The page owns its own GET /v1/teams call (retryable-error requirement
    // below), so the team name only appears once that resolves.
    await screen.findByRole("heading", { name: "Acme ML" });
    expect(screen.getAllByText("Acme ML").length).toBeGreaterThan(0);
    expect(screen.getAllByText("acme-ml").length).toBeGreaterThan(0);
    expect(screen.getAllByText("owner").length).toBeGreaterThan(0);

    // No rename endpoint exists on the backend: disabled, never a mutation.
    const rename = screen.getByRole("button", { name: "Rename team" }) as HTMLButtonElement;
    expect(rename.disabled).toBe(true);
    expect(screen.getByText("coming soon")).toBeTruthy();

    // Members / Invitations management stays on the existing admin pages
    // (the nav group renders the same labels, so assert on any match).
    expect(
      screen
        .getAllByRole("link", { name: "Members" })
        .some((link) => link.getAttribute("href") === "/admin/members"),
    ).toBe(true);
    expect(
      screen
        .getAllByRole("link", { name: "Invitations" })
        .some((link) => link.getAttribute("href") === "/admin/invitations"),
    ).toBe(true);

    // The nav entry lives in the Settings section's subnav, labeled "Team".
    const subnav = screen.getByRole("navigation", { name: "Section pages" });
    expect(within(subnav).getByRole("link", { name: "Team" }).getAttribute("aria-current")).toBe(
      "page",
    );
  });

  // spec §4: Team is a whole-page retryable error, not a partial degrade —
  // the page's own GET /v1/teams call is what makes this a real failure
  // mode instead of a prop the app already resolved before this page mounts.
  it("shows a page-level retryable error when the team list fails to load, then recovers on retry", async () => {
    let attempts = 0;
    const fetch = (path: string, init: RequestInit): Response => {
      if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
      if (path.startsWith("/v1/teams")) {
        attempts += 1;
        if (attempts === 1) return apiErrorResponse(500, "internal", "boom");
        return jsonResponse({ teams: [makeTeamSummary()] });
      }
      throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
    };

    const { user } = await renderApp({ route: "/settings/team", me: makeSaasMe(), fetch });

    await screen.findByRole("heading", { name: "Team settings" });
    expect(screen.getByText("Server error; try again later")).toBeTruthy();
    // Nothing team-specific rendered below the page head: this is a
    // page-level error, not a degraded info bar under a half-drawn page.
    // (The sidebar's team switcher still shows "Acme ML" from the `me`
    // prop, which is unrelated to this page's own failed fetch.)
    expect(screen.queryByText("acme-ml")).toBeNull();
    expect(screen.queryByRole("button", { name: "Rename team" })).toBeNull();

    await user.click(screen.getByRole("button", { name: "Retry" }));

    await screen.findByRole("heading", { name: "Acme ML" });
    expect(attempts).toBe(2);
  });
});
