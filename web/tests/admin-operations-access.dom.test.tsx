// Page-level DOM tests for operations doc section 12 items 1 and 2:
// navigation and the /admin/operations route are gated on the server-issued
// `view.operations` capability, never on the client role matrix, and the
// guard fires no Operations API request when access is denied.

import { describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
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

/** The top bar's Governance subnav only renders once a Governance-section
    route is current (there is no collapsible sidebar group to expand
    anymore); "Pipeline health" is its Operations entry point. */
function pipelineHealthLink(): HTMLElement | null {
  const subnav = screen.queryByRole("navigation", { name: "Section pages" });
  return subnav ? within(subnav).queryByRole("link", { name: "Pipeline health" }) : null;
}

describe("section 12 item 1: Operations nav follows the server capability", () => {
  it("shows the nav item for an active admin carrying view.operations", async () => {
    // /governance/audit is a stable anchor available to any owner/admin
    // (view.audit); once there, the subnav lists every visible Governance
    // item, so "Pipeline health" shows purely from the server capability.
    await renderApp({ route: "/governance/audit", me: opsMe(), fetch: agentsOnlyFetch });
    await screen.findByRole("heading", { name: "Everything that happened, unedited" });
    expect(pipelineHealthLink()).not.toBeNull();
  });

  it("hides the nav item when the capability list is empty", async () => {
    await renderApp({ route: "/governance/audit", me: makeMe(), fetch: agentsOnlyFetch });
    await screen.findByRole("heading", { name: "Everything that happened, unedited" });
    expect(pipelineHealthLink()).toBeNull();
  });

  it("hides the nav item when only unknown capabilities are published", async () => {
    await renderApp({
      route: "/governance/audit",
      me: makeMe({ capabilities: ["view.future-feature"] }),
      fetch: agentsOnlyFetch,
    });
    await screen.findByRole("heading", { name: "Everything that happened, unedited" });
    expect(pipelineHealthLink()).toBeNull();
  });

  it("hides the nav item when the capabilities field is missing entirely", async () => {
    // Rolling upgrade: /v1/me predates capabilities. The conservative
    // behavior is an empty list, not a role-based guess (doc section 2.1).
    const me = makeMe();
    delete (me as Partial<HumanMe>).capabilities;
    await renderApp({ route: "/governance/audit", me, fetch: agentsOnlyFetch });
    await screen.findByRole("heading", { name: "Everything that happened, unedited" });
    expect(pipelineHealthLink()).toBeNull();
  });

  it("shows the nav item for a Member once the capability is published", async () => {
    // navModel.ts gates Pipeline health purely on the server-issued
    // view.operations capability, never on the client role matrix (this
    // file's own premise, doc section 12); a Member with the capability
    // gets a Governance section containing only that one item.
    await renderApp({
      route: "/governance/pipeline",
      me: makeMe({ role: "member", capabilities: ["view.operations"] }),
      fetch: operationsFetch(),
    });
    await screen.findByRole("heading", { name: "Is memory keeping up?" });
    expect(pipelineHealthLink()).not.toBeNull();
    const subnav = screen.getByRole("navigation", { name: "Section pages" });
    expect(within(subnav).queryByRole("link", { name: "Audit trail" })).toBeNull();
  });
});

describe("section 12 item 2: route guard denies without firing Operations requests", () => {
  it("redirects a capable-looking admin without the capability back to /management", async () => {
    // Default makeMe() is owner, so landingPath(me) resolves to /management,
    // which renders admin+'s access tree.
    const { fetchMock } = await renderApp({
      route: "/governance/pipeline",
      me: makeMe({ capabilities: ["view.audit-future"] }),
      fetch: (path, init) => {
        if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [] });
        if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [] });
        return agentsOnlyFetch(path, init);
      },
    });

    await screen.findByRole("heading", { name: "Access flows downward" });
    expect(window.location.pathname).toBe("/management");
    expect(callsTo(fetchMock, "/v1/admin/operations")).toHaveLength(0);
    expect(pipelineHealthLink()).toBeNull();
  });

  it("an unauthenticated visitor lands on login with no Operations request", async () => {
    const { fetchMock } = await renderApp({
      route: "/governance/pipeline",
      me: null,
      fetch: agentsOnlyFetch,
    });

    await screen.findByRole("button", { name: /Sign in with OIDC/ });
    expect(callsTo(fetchMock, "/v1/admin/operations")).toHaveLength(0);
  });

  it("a 403 forbidden refreshes /v1/me and leaves the route when the capability is gone", async () => {
    // First boot publishes the capability; after the backend starts
    // answering 403 the refreshed identity no longer carries it (doc 11),
    // so the guard redirects. Default makeMe() (post-revoke) is owner, so
    // landingPath is /management, which renders admin+'s access tree.
    let privileged = true;
    const me = () => (privileged ? opsMe() : makeMe());
    const forbidden = () => apiErrorResponse(403, "forbidden", "capability revoked");
    const { fetchMock } = await renderApp({
      route: "/governance/pipeline",
      me,
      fetch: (path, init) => {
        if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
        if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [] });
        if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [] });
        if (path.startsWith("/v1/admin/operations/")) {
          privileged = false;
          return forbidden();
        }
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "Access flows downward" });
    expect(window.location.pathname).toBe("/management");
    expect(pipelineHealthLink()).toBeNull();
    // /v1/me was refetched at least once in response to the 403 (doc 11).
    expect(callsTo(fetchMock, "/v1/me").length).toBeGreaterThanOrEqual(2);
  });

  it("a 401 mid-session drops the cached identity and unmounts the console", async () => {
    const { fetchMock } = await renderApp({
      route: "/governance/pipeline",
      me: opsMe(),
      fetch: operationsFetch({
        summary: () => apiErrorResponse(401, "unauthorized", "session revoked"),
        events: () => jsonResponse({ events: [], generated_at: "2026-07-22T12:00:01Z" }),
      }),
    });

    await screen.findByRole("button", { name: /Sign in with OIDC/ });
    expect(screen.queryByRole("heading", { name: "Is memory keeping up?" })).toBeNull();
    expect(callsTo(fetchMock, "/v1/admin/operations/summary")).toHaveLength(1);
  });
});
