// Cross-screen acceptance for Modernist Portal phase 7 (entry cleanup, task
// 5). Phases 4, 5, and 6 each shipped a "whole phase" bug that no single
// task's brief was responsible for catching — content correctly renders as
// its own tested unit, but the seams between tasks (which route table wins,
// whether a credential really leaves storage once a flow completes) went
// unverified. This file is that phase-level check for the entry rewrite.
//
// Item 1 (two deployment profiles, unauthenticated through to /welcome) and
// item 2 (legacy entry paths + the no-membership catch-all) were verified by
// mutation: temporarily breaking NoMembershipRedirect's target, the
// /onboarding legacy redirect, the /bootstrap route registration, the /join
// route registration, and WelcomeScreen's profile branch each made the
// existing suite (tests/welcome.dom.test.tsx, tests/bootstrap-claim.dom.test.tsx,
// tests/join-invitation.dom.test.tsx, tests/teams-onprem.dom.test.tsx) go
// red — see task-5-report.md for the actual `npx vitest run` output of each
// mutation. Those four paths already have real coverage; item 1 gets one
// explicit end-to-end test per profile below anyway, since none of the
// existing tests starts from a genuinely unauthenticated session and follows
// the OIDC hop through to the branch content — every existing test injects
// the no-membership `me` directly.
//
// Item 3 (one-time credentials never survive the flow) is the one that
// mutation-testing actually caught: leaking the Join invitation token into
// localStorage after a successful accept, and leaking the Bootstrap secret
// into sessionStorage after a successful claim, both left all 75 test files
// / 651 tests green. Neither existing test file scans storage exhaustively
// after the flow completes — join-invitation.dom.test.tsx only asserts the
// single `pending_invitation` key is cleared, and bootstrap-claim.dom.test.tsx
// only asserts the component's own input state is cleared. The tests below
// close that gap by scanning every key and value in both Storages, and are
// self-verified the same way: temporarily reintroduce either leak and rerun
// this file to confirm it goes red (see task-5-report.md for that output).

import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import {
  apiErrorResponse,
  callsTo,
  jsonResponse,
  makeMe,
  makeNoMembershipMe,
  renderApp,
  setupDomTest,
} from "./helpers";

setupDomTest();

/** True if `needle` appears in any key or value currently held by `storage`. */
function storageLeaks(storage: Storage, needle: string): boolean {
  for (let i = 0; i < storage.length; i += 1) {
    const key = storage.key(i);
    if (key === null) continue;
    if (key.includes(needle)) return true;
    if ((storage.getItem(key) ?? "").includes(needle)) return true;
  }
  return false;
}

describe("item 1: two deployment profiles, unauthenticated through to /welcome", () => {
  it("saas: an unauthenticated visitor logs in, lands no-membership, and /welcome shows the saas waiting state", async () => {
    const first = await renderApp({
      route: "/",
      me: null,
      fetch: () => {
        throw new Error("no API calls expected while unauthenticated");
      },
    });
    const loginButton = await screen.findByRole("button", { name: /Sign in with OIDC/ });
    // Login is a top-level navigation to the OIDC provider (window.location.assign),
    // not a fetch — clicking it is only meant to prove this is the real login
    // page and the click handler runs cleanly (same pattern as
    // tests/login-flow.dom.test.tsx, which does not stub location.assign
    // either: jsdom's Location.assign isn't spy-configurable, and stubbing
    // the whole `window.location` object would desync it from the
    // history.pushState calls the next renderApp below relies on).
    await first.user.click(loginButton);
    first.unmount();

    // The OIDC round trip is a full page load, not an SPA transition — a
    // fresh render simulates the page the user actually lands on: same
    // origin, GET /v1/me now 200s with no membership_id, and the saas probe
    // (GET /v1/teams) answers 200.
    await renderApp({
      route: "/",
      me: () => makeNoMembershipMe(),
      fetch: (path, init) => {
        if (path === "/v1/teams" && init.method === "GET") return jsonResponse({ teams: [] });
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "You don't belong to any team yet" });
    expect(window.location.pathname).toBe("/welcome");
    // The saas branch: no bootstrap-claim entry, only the join-with-token card.
    expect(screen.queryByRole("button", { name: "Claim owner" })).toBeNull();
    expect(screen.getByLabelText("Invitation token or link")).toBeTruthy();
  });

  it("onprem: an unauthenticated visitor logs in, lands no-membership, and /welcome shows the bootstrap-claim entry", async () => {
    const first = await renderApp({
      route: "/",
      me: null,
      fetch: () => {
        throw new Error("no API calls expected while unauthenticated");
      },
    });
    const loginButton = await screen.findByRole("button", { name: /Sign in with OIDC/ });
    await first.user.click(loginButton);
    first.unmount();

    // On-prem's probe signal is a 501 not_configured from GET /v1/teams.
    await renderApp({
      route: "/",
      me: () => makeNoMembershipMe(),
      fetch: (path, init) => {
        if (path === "/v1/teams" && init.method === "GET") {
          return apiErrorResponse(501, "not_configured", "teams are not configured");
        }
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "Claim the first owner of this deployment" });
    expect(window.location.pathname).toBe("/welcome");
    screen.getByRole("button", { name: "Claim owner" });
    expect(screen.getByLabelText("Invitation token or link")).toBeTruthy();
  });
});

// Item 2: legacy entry paths stay reachable, and no-membership catches any
// unmatched path back to /welcome (address bar, not content-only).
//
// /bootstrap and /join#invite=... reachability are already covered by
// bootstrap-claim.dom.test.tsx and join-invitation.dom.test.tsx respectively
// (each renders directly at that route and finds real content, and was
// confirmed above by mutation); the no-membership catch-all is covered
// generically by welcome.dom.test.tsx using /management as the arbitrary
// unmatched path. /onboarding itself is only exercised for an *active*
// session in welcome.dom.test.tsx (a real route registered in
// app/routes.tsx there); under no-membership it has no route of its own and
// falls through to the same catch-all — this test names that specific path
// explicitly, since the brief calls it out by name, and asserts the address
// bar (content-only would also pass for an in-place render).
describe("item 2: /onboarding stays reachable (redirects, not 404) under no-membership", () => {
  it("redirects /onboarding to /welcome for a no-membership session and updates the address bar", async () => {
    await renderApp({
      route: "/onboarding",
      me: () => makeNoMembershipMe(),
      fetch: (path, init) => {
        if (path === "/v1/teams" && init.method === "GET") return jsonResponse({ teams: [] });
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "You don't belong to any team yet" });
    expect(window.location.pathname).toBe("/welcome");
  });
});

describe("item 3: one-time credentials never survive the flow", () => {
  const TOKEN = "tm_invite_inv_01.cross-screen-secret-token";

  it("join: the invitation token is gone from localStorage, sessionStorage, and the address bar once accepted", async () => {
    let joined = false;
    const { user } = await renderApp({
      route: `/join#invite=${TOKEN}`,
      me: () =>
        joined ? makeMe({ membership_id: "mbr_02", role: "member" }) : makeNoMembershipMe(),
      fetch: (path, init) => {
        if (path === "/v1/invitations/accept" && init.method === "POST") {
          joined = true;
          return jsonResponse(makeMe({ membership_id: "mbr_02", role: "member" }));
        }
        if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await user.click(await screen.findByRole("button", { name: "Accept invitation" }));
    await screen.findByRole("heading", { name: "My Agents" });

    // Exhaustive scan, not just the known pending_invitation key: a leak
    // written under any other key name must still be caught.
    expect(storageLeaks(localStorage, TOKEN)).toBe(false);
    expect(storageLeaks(sessionStorage, TOKEN)).toBe(false);
    expect(window.location.href).not.toContain(TOKEN);
  });

  const SECRET = "cross-screen-bootstrap-secret";

  it("bootstrap: the claim secret is gone from localStorage, sessionStorage, and the address bar once claimed", async () => {
    let claimed = false;
    const { user, fetchMock } = await renderApp({
      route: "/bootstrap",
      me: () => (claimed ? makeMe() : makeNoMembershipMe()),
      fetch: (path, init) => {
        if (path === "/v1/bootstrap/claim" && init.method === "POST") {
          claimed = true;
          return jsonResponse(makeMe());
        }
        if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
        if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [] });
        if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await user.type(screen.getByLabelText("Bootstrap key"), SECRET);
    await user.click(screen.getByRole("button", { name: "Claim owner" }));

    await screen.findByRole("heading", { name: "Access flows downward" });

    // The secret only ever travels in the request header, never the body.
    const claims = callsTo(fetchMock, "/v1/bootstrap/claim", "POST");
    expect(claims).toHaveLength(1);
    expect(claims[0].headers.get("X-PAX-Bootstrap-Secret")).toBe(SECRET);
    expect(claims[0].init.body ?? null).toBeNull();

    expect(storageLeaks(localStorage, SECRET)).toBe(false);
    expect(storageLeaks(sessionStorage, SECRET)).toBe(false);
    expect(window.location.href).not.toContain(SECRET);
  });
});
