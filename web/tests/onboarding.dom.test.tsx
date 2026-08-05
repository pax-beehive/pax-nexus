// Page-level DOM tests for the saas onboarding page (design/m3-teams 01,
// M3 P6): create-a-team happy path with slug preview and slug-conflict
// display, and join-with-invitation from the onboarding seg toggle. Auth
// state is driven by stubbed /v1/me + /v1/teams responses only.

import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import {
  apiErrorResponse,
  callsTo,
  jsonResponse,
  makeNoMembershipMe,
  makeSaasMe,
  renderApp,
  setupDomTest,
} from "./helpers";

setupDomTest();

// Both a team-creator and an invitation acceptor land on /management, which
// is the member-rooted "my access" view for every role (MyAgentsPage, the
// stand-in until phase 3's AccessTree) — the Owner role does not change
// which page the Management root renders.
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

const CREATED_TEAM = {
  team_id: "team_alpha",
  name: "Acme ML",
  slug: "acme-ml",
  created_at: "2026-08-01T00:00:00Z",
  resource_version: 1,
};

describe("saas onboarding: create a team", () => {
  it("creates the team, scopes the session to it, and enters the portal", async () => {
    let created = false;
    const { fetchMock, user } = await renderApp({
      route: "/",
      me: () => (created ? makeSaasMe() : makeNoMembershipMe()),
      fetch: (path, init) => {
        if (path === "/v1/teams" && init.method === "GET") return jsonResponse({ teams: [] });
        if (path === "/v1/teams" && init.method === "POST") {
          return jsonResponse({ team: CREATED_TEAM }, 201);
        }
        if (path === "/v1/me/current-team" && init.method === "POST") {
          created = true;
          return jsonResponse(makeSaasMe());
        }
        if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });

    // The no-membership saas profile renders the onboarding page (the
    // /v1/teams probe returned 200).
    await screen.findByRole("heading", { name: "Welcome to Team Memory" });

    const nameInput = screen.getByLabelText("Team name");
    await user.type(nameInput, "Acme ML");
    // Derived slug preview updates as the name is typed.
    expect(screen.getByText("acme-ml")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Create team" }));

    await screen.findByRole("heading", { name: "My Agents" });
    expect(window.location.pathname).toBe("/management");

    const creates = callsTo(fetchMock, "/v1/teams", "POST");
    expect(creates).toHaveLength(1);
    expect(JSON.parse(String(creates[0].init.body))).toEqual({ name: "Acme ML" });
    expect(creates[0].headers.get("Idempotency-Key")).toMatch(UUID_RE);
    expect(creates[0].headers.get("X-CSRF-Token")).toBe("test-csrf");

    // Creating a team does not re-scope the session server-side, so the
    // client switches the current team before refreshing the auth state.
    const switches = callsTo(fetchMock, "/v1/me/current-team", "POST");
    expect(switches).toHaveLength(1);
    expect(JSON.parse(String(switches[0].init.body))).toEqual({ team_id: "team_alpha" });
  });

  it("shows the slug-conflict error and stays on the onboarding page", async () => {
    const { fetchMock, user } = await renderApp({
      route: "/",
      me: () => makeNoMembershipMe(),
      fetch: (path, init) => {
        if (path === "/v1/teams" && init.method === "GET") return jsonResponse({ teams: [] });
        if (path === "/v1/teams" && init.method === "POST") {
          return apiErrorResponse(409, "team_slug_conflict", "slug acme-ml is taken");
        }
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "Welcome to Team Memory" });
    await user.type(screen.getByLabelText("Team name"), "Acme ML");
    await user.click(screen.getByRole("button", { name: "Create team" }));

    await screen.findByText("A team with this slug already exists; choose a different name");
    // No session switch was attempted and the user stays on onboarding.
    expect(callsTo(fetchMock, "/v1/me/current-team", "POST")).toHaveLength(0);
    expect(screen.getByRole("heading", { name: "Welcome to Team Memory" })).toBeTruthy();
  });
});

describe("saas onboarding: join with invitation", () => {
  it("accepts a pasted join link and enters the portal", async () => {
    let joined = false;
    const { fetchMock, user } = await renderApp({
      route: "/",
      me: () => (joined ? makeSaasMe() : makeNoMembershipMe()),
      fetch: (path, init) => {
        if (path === "/v1/teams" && init.method === "GET") return jsonResponse({ teams: [] });
        if (path === "/v1/invitations/accept" && init.method === "POST") {
          joined = true;
          return jsonResponse(makeSaasMe());
        }
        if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "Welcome to Team Memory" });
    await user.click(screen.getByRole("button", { name: "Join with invitation" }));
    await user.type(
      screen.getByLabelText("Invitation token"),
      "https://app.example.com/join#invite=tm_invite_inv_01.abc123",
    );
    await user.click(screen.getByRole("button", { name: "Accept invitation" }));

    await screen.findByRole("heading", { name: "My Agents" });

    const accepts = callsTo(fetchMock, "/v1/invitations/accept", "POST");
    expect(accepts).toHaveLength(1);
    // The token is parsed out of the full join link before sending.
    expect(JSON.parse(String(accepts[0].init.body))).toEqual({
      token: "tm_invite_inv_01.abc123",
    });
    expect(accepts[0].headers.get("Idempotency-Key")).toMatch(UUID_RE);
  });
});
