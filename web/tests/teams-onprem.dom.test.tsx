// On-prem regression tests (M3 P6): the portal is a single build serving
// both profiles. With no membership and GET /v1/teams answering 501
// not_configured, the no-membership state must keep rendering the existing
// EntryPage (bootstrap claim); an active on-prem principal (no teams field)
// must not render the team switcher or the Team settings nav entry.

import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import {
  apiErrorResponse,
  jsonResponse,
  makeMe,
  makeNoMembershipMe,
  renderApp,
  setupDomTest,
} from "./helpers";

setupDomTest();

describe("on-prem profile regression", () => {
  it("a 501 from /v1/teams keeps the bootstrap-claim EntryPage", async () => {
    await renderApp({
      route: "/",
      me: () => makeNoMembershipMe(),
      fetch: (path, init) => {
        if (path === "/v1/teams") {
          return apiErrorResponse(501, "not_configured", "teams are not configured");
        }
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });

    // The on-prem entry page, not the saas onboarding page.
    await screen.findByRole("button", { name: "Claim Bootstrap Owner" });
    expect(screen.getByLabelText("Invitation token")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Create team" })).toBeNull();
    expect(screen.queryByRole("group", { name: "onboarding mode" })).toBeNull();
  });

  it("an active on-prem principal renders no team switcher or team nav entry", async () => {
    await renderApp({
      route: "/agents",
      me: makeMe(),
      fetch: (path, init) => {
        if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "My Agents" });
    expect(screen.queryByRole("menu", { name: "Switch team" })).toBeNull();
    expect(screen.queryByRole("button", { name: /Switch team/ })).toBeNull();
    expect(screen.queryByRole("link", { name: "Team settings" })).toBeNull();
    // The default subtitle stays (no team scope line).
    expect(
      screen.getByText("Register and manage the Agent identities you own"),
    ).toBeTruthy();
    // Directory group is unchanged for the on-prem owner: Members/Invitations.
    expect(screen.queryByRole("button", { name: "Directory" })).toBeTruthy();
  });
});
