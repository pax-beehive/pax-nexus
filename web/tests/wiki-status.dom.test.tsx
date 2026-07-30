// The in-shell /wiki route is an observability page: ingestion controls,
// extraction progress, an Open Wiki entry, and a legacy deep-link redirect
// (spec 2026-07-29-wiki-standalone-page sections 1-3).
//
// The ingestion-control cases below were ported from the retired
// web/tests/wiki.dom.test.tsx (see d874ac5~1), adapted to the /wiki route:
// that file exercised WikiPage's inline ingestion controls directly; those
// controls now live on WikiStatusPage while browsing moved to
// WikiBrowsePage (see wiki-browse.dom.test.tsx). The retired file's
// "adds a dedicated Knowledge sidebar and renders grounded wiki context"
// case asserted the old inline wiki at /wiki and no longer applies; it is
// replaced below by a case proving the sidebar Wiki link still routes to
// this status page.

import { describe, expect, it } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import { callsTo, jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";
import { wikiFetch } from "./wiki-browse.dom.test";

setupDomTest();

describe("wiki status page", () => {
  it("shows ingestion controls, progress, and opens the full-screen wiki", async () => {
    const { user } = await renderApp({
      route: "/wiki",
      me: makeMe(),
      fetch: (path) => {
        if (path === "/v1/wiki/ingestion") {
          return jsonResponse({
            auto_inject: true,
            pending_sessions: 3,
            last_processed_at: "2026-07-29T08:00:00Z",
          });
        }
        return wikiFetch(path);
      },
    });

    await waitFor(() => expect(screen.getByRole("switch")).toBeTruthy());
    expect(screen.getByText("3")).toBeTruthy();
    // Portal shell stays visible around the status page.
    expect(screen.getByText("My Agents")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Open Wiki" }));
    await waitFor(() => expect(window.location.pathname).toBe("/wiki/browse"));
  });

  it("degrades to a progress-unavailable notice without blocking controls", async () => {
    await renderApp({
      route: "/wiki",
      me: makeMe(),
      fetch: (path) => {
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        return wikiFetch(path);
      },
    });

    await waitFor(() => expect(screen.getByText("Progress is unavailable.")).toBeTruthy());
    expect(screen.getByRole("switch")).toBeTruthy();
  });

  it("redirects legacy /wiki?page= deep links to the browse route", async () => {
    await renderApp({ route: "/wiki?page=alpha", me: makeMe(), fetch: wikiFetch });

    await waitFor(() => expect(window.location.pathname).toBe("/wiki/browse"));
    expect(window.location.search).toBe("?page=alpha");
    await waitFor(() => expect(screen.getByText("Alpha summary")).toBeTruthy());
  });

  it("routes to the status page from the portal sidebar Wiki link", async () => {
    const { user } = await renderApp({
      route: "/agents",
      me: makeMe(),
      fetch: (path) => {
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const portalNav = screen.getByRole("navigation", { name: "Portal navigation" });
    within(portalNav).getByText("Knowledge");
    await user.click(within(portalNav).getByRole("link", { name: "Wiki" }));

    await waitFor(() => expect(window.location.pathname).toBe("/wiki"));
    await screen.findByRole("switch");
    expect(screen.getByRole("button", { name: "Open Wiki" })).toBeTruthy();
  });
});

// -- ingestion controls: toggle, fixed-session injection, and owner-only
// reset & rebuild (ported from the retired wiki.dom.test.tsx's "Page Wiki
// portal integration" describe block) --
describe("wiki status page ingestion controls", () => {
  it("toggles auto injection and manually injects a fixed session", async () => {
    const { user, fetchMock } = await renderApp({
      route: "/wiki",
      me: makeMe({ role: "member" }),
      fetch: (path, init) => {
        const method = init?.method ?? "GET";
        if (path === "/v1/wiki/ingestion" && method === "PUT") {
          return jsonResponse({ auto_inject: true });
        }
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path === "/v1/wiki/sessions/runtime-session/inject") {
          return jsonResponse({ processed_streams: 1 });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const toggle = await screen.findByRole("switch", { name: "Off" });
    await user.click(toggle);
    await waitFor(() => expect(toggle.getAttribute("aria-checked")).toBe("true"));

    await user.type(screen.getByLabelText("Fixed session ID"), "runtime-session");
    await user.click(screen.getByRole("button", { name: "Inject session" }));
    await screen.findByText("Injected 1 stream from runtime-session.");

    expect(callsTo(fetchMock, "/v1/wiki/ingestion", "PUT")).toHaveLength(1);
    expect(callsTo(fetchMock, "/v1/wiki/sessions/runtime-session/inject", "POST")).toHaveLength(1);
  });

  it("lets an owner confirm a full Wiki rebuild without deleting Session Lake", async () => {
    const { user, fetchMock } = await renderApp({
      route: "/wiki",
      me: makeMe({ role: "owner" }),
      fetch: (path, init) => {
        const method = init?.method ?? "GET";
        if (path === "/v1/wiki/rebuild" && method === "POST") {
          return jsonResponse({ auto_inject: true });
        }
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("switch");
    await user.click(screen.getByRole("button", { name: "Reset & rebuild" }));
    const dialog = screen.getByRole("dialog", { name: "Reset and rebuild Wiki" });
    within(dialog).getByText("Session Lake events and Team Notes are preserved.");
    await user.click(within(dialog).getByRole("button", { name: "Confirm reset & rebuild" }));

    await screen.findByText("Wiki cleared. Rebuilding from Session Lake…");
    expect(callsTo(fetchMock, "/v1/wiki/rebuild", "POST")).toHaveLength(1);
  });

  it("hides the destructive rebuild control from members", async () => {
    await renderApp({
      route: "/wiki",
      me: makeMe({ role: "member" }),
      fetch: (path) => {
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("switch");
    expect(screen.queryByRole("button", { name: "Reset & rebuild" })).toBeNull();
  });
});
