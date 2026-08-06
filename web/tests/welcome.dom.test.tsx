// Page-level DOM tests for the unified /welcome entry screen (Modernist
// Portal phase 7 task 1). A dedicated onprem page and a dedicated saas page
// used to render at path="*" while no-membership, leaving the address bar on
// whatever path the user had originally asked for. WelcomePage replaces both
// with one component that branches on DeploymentProfile internally, and the
// no-membership catch-all now redirects to /welcome instead of rendering in
// place.

import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import {
  apiErrorResponse,
  jsonResponse,
  makeNoMembershipMe,
  makeSaasMe,
  renderApp,
  setupDomTest,
} from "./helpers";
import { WelcomeScreen } from "../src/pages/WelcomePage";

setupDomTest();

describe("/welcome: saas profile", () => {
  it("lands a no-membership saas session on /welcome with the waiting state", async () => {
    await renderApp({
      route: "/",
      me: () => makeNoMembershipMe(),
      fetch: (path, init) => {
        // 200 from /v1/teams is the saas probe (AuthContext.probeProfile).
        if (path === "/v1/teams" && init.method === "GET") return jsonResponse({ teams: [] });
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "你还不属于任何团队" });
    expect(window.location.pathname).toBe("/welcome");
    // No bootstrap-claim entry for saas.
    expect(screen.queryByRole("button", { name: "前往认领 Owner" })).toBeNull();
    // The join-with-token entry is still offered.
    screen.getByLabelText("邀请令牌或链接");
  });
});

describe("/welcome: onprem profile, bootstrap open", () => {
  it("lands a no-membership onprem session on /welcome with the bootstrap-claim entry", async () => {
    await renderApp({
      route: "/",
      me: () => makeNoMembershipMe(),
      fetch: (path, init) => {
        // 501 not_configured from /v1/teams is the onprem probe.
        if (path === "/v1/teams" && init.method === "GET") {
          return apiErrorResponse(501, "not_configured", "teams are not configured");
        }
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "认领这套部署的第一个 Owner" });
    expect(window.location.pathname).toBe("/welcome");
    screen.getByRole("button", { name: "前往认领 Owner" });
    // The join-with-token entry stays available alongside bootstrap.
    screen.getByLabelText("邀请令牌或链接");
  });
});

describe("/welcome: onprem profile, bootstrap closed", () => {
  // No backend signal for "bootstrap already closed" exists today (see the
  // WelcomeScreenProps.canBootstrap doc comment) — a no-membership onprem
  // session can only be driven through the always-open branch via renderApp.
  // This scenario is exercised at the component level instead.
  it("renders the same waiting state as saas when the bootstrap entry is withheld", () => {
    render(
      <MemoryRouter>
        <WelcomeScreen profile="onprem" canBootstrap={false} />
      </MemoryRouter>,
    );

    screen.getByRole("heading", { name: "你还不属于任何团队" });
    expect(screen.queryByRole("button", { name: "前往认领 Owner" })).toBeNull();
    screen.getByLabelText("邀请令牌或链接");
  });
});

describe("/welcome: no-membership catch-all", () => {
  it("redirects an arbitrary path to /welcome and updates the address bar", async () => {
    await renderApp({
      route: "/management",
      me: () => makeNoMembershipMe(),
      fetch: (path, init) => {
        if (path === "/v1/teams" && init.method === "GET") return jsonResponse({ teams: [] });
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "你还不属于任何团队" });
    // The regression this task fixes: the old path="*" render left the
    // address bar on /management. Asserting content alone would pass even
    // with the old in-place render, so this must check the URL.
    expect(window.location.pathname).toBe("/welcome");
  });

  it("does not redirect away from /welcome itself (no loop)", async () => {
    await renderApp({
      route: "/welcome",
      me: () => makeNoMembershipMe(),
      fetch: (path, init) => {
        if (path === "/v1/teams" && init.method === "GET") return jsonResponse({ teams: [] });
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "你还不属于任何团队" });
    expect(window.location.pathname).toBe("/welcome");
  });
});

describe("/onboarding legacy redirect", () => {
  it("redirects to /welcome instead of 404ing, and reaches real content (old bookmarks / external links)", async () => {
    // /onboarding only exists inside PortalRoutes (the active-session
    // shell) — a no-membership session never reaches it; that path is
    // already covered by the catch-all tests above. This exercises the
    // route this task actually edits (app/routes.tsx).
    //
    // app/routes.tsx also registers /welcome itself (reachable only while
    // active — App.tsx's own /welcome registration only mounts while
    // no-membership, so the two never collide for a given session; see the
    // mutual-exclusivity note on WelcomePage's doc comment), so an active
    // session that follows a stale /onboarding link lands on real
    // "join another team" content instead of silently bouncing back to its
    // landing page.
    await renderApp({
      route: "/onboarding",
      me: makeSaasMe({ role: "member" }),
      fetch: (path, init) => {
        if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "加入另一个团队" });
    expect(window.location.pathname).toBe("/welcome");
    expect(screen.getByLabelText("邀请令牌或链接")).toBeTruthy();
  });
});
