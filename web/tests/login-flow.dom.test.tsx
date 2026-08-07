// Page-level DOM smoke tests for the global login routing (doc section 4)
// and the required edge cases 1 and 14 of doc section 10.

import { describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import {
  apiErrorResponse,
  callsTo,
  jsonResponse,
  makeMe,
  renderApp,
  setupDomTest,
} from "./helpers";

setupDomTest();

function noApiCalls(): never {
  throw new Error("no API calls expected on the login page");
}

describe("section 10 item 1: OIDC continuation for ordinary pages", () => {
  it("routes an unauthenticated visitor to the login page and preserves the target as return_url", async () => {
    const { user } = await renderApp({ route: "/management/members", me: null, fetch: noApiCalls });

    // The guard renders the login page; the target page never mounts, so no
    // admin API call is fired while unauthenticated.
    const loginButton = await screen.findByRole("button", { name: /使用 OIDC 登录/ });
    expect(screen.queryByRole("heading", { name: "Members" })).toBeNull();

    // Starting OIDC is a top-level navigation; before leaving, the current
    // internal path is stored as the read-once return_url continuation.
    await user.click(loginButton);
    expect(sessionStorage.getItem("return_url")).toBe("/management/members");
  });

  // Doc section 10 item 1: after the OIDC round trip lands on "/",
  // ContinuationRedirect navigates to the saved return_url and PortalShell's
  // catch-all DefaultRedirect yields while a continuation is pending. The
  // seeded return_url is deliberately the OLD path: a bookmark saved before
  // this task's route migration must still resolve, via the now-live
  // legacy redirect table, to the new canonical path.
  it("restores the pre-login target page after the OIDC round trip", async () => {
    await renderApp({
      route: "/",
      me: makeMe(),
      sessionStorage: { return_url: "/admin/members" },
      fetch: (path) => {
        if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
        if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "Members" });
    expect(window.location.pathname).toBe("/management/members");
    // The continuation is read-once.
    expect(sessionStorage.getItem("return_url")).toBeNull();
  });
});

describe("section 10 item 14: cookie misconfiguration must not loop", () => {
  it("classifies a 501 boot response as an operator page with the Secure-cookie hint", async () => {
    await renderApp({
      route: "/",
      fetch: (path) => {
        if (path === "/v1/me") {
          return apiErrorResponse(501, "not_configured", "human identity is not configured");
        }
        return noApiCalls();
      },
    });

    await screen.findByRole("heading", { name: "尚未启用 Human Identity" });
    // The operator hint names the Secure Cookie misconfiguration explicitly
    // instead of sending the user through another login attempt.
    expect(document.body.textContent).toContain("TEAM_MEMORY_HUMAN_COOKIE_SECURE=false");
    expect(screen.queryByRole("button", { name: /使用 OIDC 登录/ })).toBeNull();
  });

  it("keeps a persistent 401 on the manual login page instead of redirect-looping into OIDC", async () => {
    // Boot at a deep link with no session; the guard renders the login page
    // in place without any automatic navigation.
    const { fetchMock, user } = await renderApp({
      route: "/management/members",
      me: null,
      fetch: noApiCalls,
    });
    await screen.findByRole("button", { name: /使用 OIDC 登录/ });
    expect(window.location.pathname).toBe("/management/members");

    // After the OIDC callback, a Secure-cookie misconfiguration still yields
    // 401. The manual retry must not auto-redirect: the user stays put.
    await user.click(screen.getByRole("button", { name: "已经登录过？点击重试" }));
    await screen.findByRole("button", { name: /使用 OIDC 登录/ });
    await waitFor(() => expect(callsTo(fetchMock, "/v1/me")).toHaveLength(2));
    expect(window.location.pathname).toBe("/management/members");
  });
});
