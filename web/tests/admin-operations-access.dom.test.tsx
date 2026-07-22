// Page-level DOM tests for operations doc section 12 items 1 and 2:
// navigation and the /admin/operations route are gated on the server-issued
// `view.operations` capability, never on the client role matrix, and the
// guard fires no Operations API request when access is denied.

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
import { operationsFetch, opsMe } from "./operationsFixtures";
import type { HumanMe } from "../src/api/types";

setupDomTest();

function agentsOnlyFetch(path: string, init: RequestInit): Response {
  if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
  throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
}

/** Nav link lookups stay inside <nav> so page content can never false-match. */
function operationsNavLink(): HTMLElement | null {
  return document.querySelector('nav a[href="/admin/operations"]');
}

describe("section 12 item 1: Operations nav follows the server capability", () => {
  it("shows the nav item for an active admin carrying view.operations", async () => {
    await renderApp({ route: "/agents", me: opsMe(), fetch: agentsOnlyFetch });
    expect(operationsNavLink()).not.toBeNull();
  });

  it("hides the nav item when the capability list is empty", async () => {
    await renderApp({ route: "/agents", me: makeMe(), fetch: agentsOnlyFetch });
    expect(operationsNavLink()).toBeNull();
  });

  it("hides the nav item when only unknown capabilities are published", async () => {
    await renderApp({
      route: "/agents",
      me: makeMe({ capabilities: ["view.future-feature"] }),
      fetch: agentsOnlyFetch,
    });
    expect(operationsNavLink()).toBeNull();
  });

  it("hides the nav item when the capabilities field is missing entirely", async () => {
    // Rolling upgrade: /v1/me predates capabilities. The conservative
    // behavior is an empty list, not a role-based guess (doc section 2.1).
    const me = makeMe();
    delete (me as Partial<HumanMe>).capabilities;
    await renderApp({ route: "/agents", me, fetch: agentsOnlyFetch });
    expect(operationsNavLink()).toBeNull();
  });

  it("hides the nav item for a Member even if a capability were published", async () => {
    // The Admin Console section itself is role-gated; a Member never sees
    // the Operations entry point regardless of the capability payload.
    await renderApp({
      route: "/agents",
      me: makeMe({ role: "member", capabilities: ["view.operations"] }),
      fetch: agentsOnlyFetch,
    });
    expect(operationsNavLink()).toBeNull();
  });
});

describe("section 12 item 2: route guard denies without firing Operations requests", () => {
  it("redirects a capable-looking admin without the capability back to /agents", async () => {
    const { fetchMock } = await renderApp({
      route: "/admin/operations",
      me: makeMe({ capabilities: ["view.audit-future"] }),
      fetch: agentsOnlyFetch,
    });

    await screen.findByRole("heading", { name: "My Agents" });
    expect(window.location.pathname).toBe("/agents");
    expect(callsTo(fetchMock, "/v1/admin/operations")).toHaveLength(0);
    expect(operationsNavLink()).toBeNull();
  });

  it("an unauthenticated visitor lands on login with no Operations request", async () => {
    const { fetchMock } = await renderApp({
      route: "/admin/operations",
      me: null,
      fetch: agentsOnlyFetch,
    });

    await screen.findByRole("button", { name: /Continue with OIDC/ });
    expect(callsTo(fetchMock, "/v1/admin/operations")).toHaveLength(0);
  });

  it("a 403 forbidden refreshes /v1/me and leaves the route when the capability is gone", async () => {
    // First boot publishes the capability; after the backend starts
    // answering 403 the refreshed identity no longer carries it (doc 11),
    // so the guard redirects and the nav entry disappears.
    let privileged = true;
    const me = () => (privileged ? opsMe() : makeMe());
    const forbidden = () => apiErrorResponse(403, "forbidden", "capability revoked");
    const { fetchMock } = await renderApp({
      route: "/admin/operations",
      me,
      fetch: (path, init) => {
        if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
        if (path.startsWith("/v1/admin/operations/")) {
          privileged = false;
          return forbidden();
        }
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "My Agents" });
    await waitFor(() => expect(operationsNavLink()).toBeNull());
    expect(window.location.pathname).toBe("/agents");
    // /v1/me was refetched at least once in response to the 403 (doc 11).
    expect(callsTo(fetchMock, "/v1/me").length).toBeGreaterThanOrEqual(2);
  });

  it("a 401 mid-session drops the cached identity and unmounts the console", async () => {
    const { fetchMock } = await renderApp({
      route: "/admin/operations",
      me: opsMe(),
      fetch: operationsFetch({
        summary: () => apiErrorResponse(401, "unauthorized", "session revoked"),
        events: () => jsonResponse({ events: [], generated_at: "2026-07-22T12:00:01Z" }),
      }),
    });

    await screen.findByRole("button", { name: /Continue with OIDC/ });
    expect(screen.queryByRole("heading", { name: "Operations" })).toBeNull();
    expect(callsTo(fetchMock, "/v1/admin/operations/summary")).toHaveLength(1);
  });
});
