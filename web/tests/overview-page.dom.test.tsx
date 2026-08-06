// Page-level DOM tests for the Overview landing page (portal-modernist phase
// 2b): the five metric tiles, window-driven refetch, block-level region
// isolation (a failing aggregate never unmounts the writers block), the
// positive empty state for an empty note mix, and capability gating.

import { describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { apiErrorResponse, callsTo, jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";
import { makeOverview, renderOverviewPage } from "./operationsFixtures";

setupDomTest();

describe("OverviewPage", () => {
  it("renders the five metric tiles from the aggregate", async () => {
    await renderOverviewPage({ overview: () => makeOverview() });

    screen.getByText("Evidence captured");
    screen.getByText("Facts in circulation");
    screen.getByText("Context handed to agents");
    screen.getByText("Time to remember");
    screen.getByText("Needs a person");
    // makeOverview()'s known attention_count value.
    screen.getByText(String(makeOverview().metrics.attention_count));
  });

  it("refetches with the selected window", async () => {
    const sevenDaySeries = Array.from({ length: 7 }, (_, i) => ({
      bucket_at: `2026-07-1${i}T00:00:00Z`,
      evidence: 10 + i,
      facts: 5 + i,
      recalls: 2 + i,
    }));
    const app = await renderOverviewPage({
      overview: (url) =>
        url.searchParams.get("window") === "7d"
          ? makeOverview({ series: sevenDaySeries })
          : makeOverview(),
    });

    // Default window is 24h: the fixture's default 6-bucket series renders 6 ticks.
    expect(document.querySelectorAll(".ov-chart-tick")).toHaveLength(6);

    await app.user.click(screen.getByRole("button", { name: "7d" }));

    await waitFor(() => {
      expect(
        callsTo(app.fetchMock, "/v1/admin/overview").some((c) => c.path.includes("window=7d")),
      ).toBe(true);
    });
    await waitFor(() => {
      expect(document.querySelectorAll(".ov-chart-tick")).toHaveLength(7);
    });
  });

  it("keeps the writers block alive when the aggregate fails", async () => {
    await renderOverviewPage({
      overview: () => apiErrorResponse(500, "internal", "boom"),
    });

    // Metrics region shows the region error...
    expect(screen.getAllByText("Server error; try again later").length).toBeGreaterThanOrEqual(1);
    // ...while the writers block, fed by an independent region, still renders
    // the default fixture agent.
    await screen.findByText("Alice Codex");
  });

  it("renders a positive empty state for an empty note mix", async () => {
    await renderOverviewPage({ overview: () => makeOverview({ note_mix: [] }) });

    screen.getByText(/No live notes/);
  });

  it("redirects to /management without view.operations", async () => {
    await renderApp({
      route: "/overview",
      me: makeMe({ capabilities: [] }),
      fetch: (path) => {
        if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "My Agents" });
  });
});
