// Page-level DOM tests for the /team settings page (design/m3-teams 03
// General panel, M3 P6): team info card, disabled Rename with the
// "coming soon" hint (no backend endpoint yet), and links to the existing
// team-scoped Members / Invitations admin pages.

import { describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import { jsonResponse, makeSaasMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

function agentsFetch(path: string, init: RequestInit): Response {
  if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
  throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
}

describe("team settings page", () => {
  it("renders the general panel with team info and a disabled rename", async () => {
    await renderApp({ route: "/settings/team", me: makeSaasMe(), fetch: agentsFetch });

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
});
