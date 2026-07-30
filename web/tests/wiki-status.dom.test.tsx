// The in-shell /wiki route is an observability page: ingestion controls,
// extraction progress, an Open Wiki entry, and a legacy deep-link redirect
// (spec 2026-07-29-wiki-standalone-page sections 1-3).

import { describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";
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
});
