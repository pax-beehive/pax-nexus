// Page-level DOM tests for the Overview landing page (portal-modernist phase
// 2b): the five metric tiles, window-driven refetch, block-level region
// isolation (a failing aggregate never unmounts the writers block), the
// positive empty state for an empty note mix, capability gating, the
// attention queue (rendering + CTA navigation + its own empty state), and
// the held-events feed (scroll-hold mechanics ported from the old Team
// Pulse page's useFeedRegion, and its own region isolation from the rest of
// the page).

import { describe, expect, it } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { apiErrorResponse, callsTo, jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";
import {
  eventsPage,
  makeEvent,
  makeOverview,
  opsMe,
  operationsFetch,
  renderOverviewPage,
} from "./operationsFixtures";

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

    // The 7d seg button carries the same retention hint as AdminOperationsPage's
    // window selector (plan ruling #5).
    expect(screen.getByRole("button", { name: "7d" }).getAttribute("title")).toBe(
      "Windows beyond the deployment retention are rejected by the backend",
    );

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
        if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    // Default makeMe() is owner, so /management (its landing redirect)
    // renders the access tree.
    await screen.findByRole("heading", { name: "Access flows downward" });
  });

  it("renders attention items and navigates on the CTA", async () => {
    const app = await renderApp({
      route: "/overview",
      me: opsMe(),
      fetch: (path, init) => {
        // The CTA below navigates to /governance/sessions; feed it a bare
        // empty findings page so Session Audit's own default view settles
        // instead of throwing on an unstubbed fetch.
        if (path.startsWith("/v1/admin/session-audit/findings")) {
          return jsonResponse({ findings: [] });
        }
        return operationsFetch()(path, init);
      },
    });
    await screen.findByRole("heading", { name: "Overview" });

    // makeOverview()'s default attention items: a high-severity finding
    // (CTA "Review", target /governance/sessions) and a high-severity
    // quarantine (CTA "Inspect").
    const findingRow = screen.getByText("High-risk tool call without approval").closest("li");
    expect(findingRow?.querySelector(".tag-attention")).toBeTruthy();
    screen.getByText("finding:41");
    screen.getByRole("button", { name: "Inspect" });

    await app.user.click(screen.getByRole("button", { name: "Review" }));

    await screen.findByRole("heading", { name: "Agent 到底做了什么" });
  });

  // 7. attention 空 → 正向空态
  it("shows the positive empty state when nothing needs attention", async () => {
    await renderOverviewPage({
      overview: () =>
        jsonResponse(
          makeOverview({ attention: [], metrics: { ...makeOverview().metrics, attention_count: 0 } }),
        ),
    });

    screen.getByText("Nothing needs you right now");
  });

  it("holds new events while the reader has scrolled", async () => {
    let pollCount = 0;
    const app = await renderOverviewPage({
      overview: () => makeOverview(),
      events: () =>
        jsonResponse(
          pollCount++ === 0
            ? eventsPage([makeEvent()])
            : eventsPage([
                makeEvent({ attempt_id: "op_new1", operation_kind: "extraction.run" }),
                makeEvent(),
              ]),
        ),
    });

    // First poll: a single "Memory Search" event, no held banner.
    await screen.findByText("Memory Search");
    expect(screen.queryByText("Extraction")).toBeNull();
    expect(screen.queryByRole("button", { name: "Show" })).toBeNull();

    // Reader scrolls the feed down...
    const feedList = document.getElementById("overview-feed") as HTMLElement;
    fireEvent.scroll(feedList, { target: { scrollTop: 100 } });

    // ...then a poll cycle lands a new event (triggered here via the
    // feed's own "Refresh" button rather than waiting out the 10s
    // interval): the update is held behind a notice instead of shifting
    // the list under the reader.
    await app.user.click(screen.getByRole("button", { name: "Refresh event feed" }));

    await screen.findByText("1 new events held while you read");
    expect(screen.queryByText("Extraction")).toBeNull();

    await app.user.click(screen.getByRole("button", { name: "Show" }));

    await screen.findByText("Extraction");
    expect(screen.queryByText("1 new events held while you read")).toBeNull();
  });

  it("isolates an events-feed failure", async () => {
    await renderOverviewPage({
      overview: () => makeOverview(),
      events: () => apiErrorResponse(500, "internal", "boom"),
    });

    // The feed region shows its own error...
    await screen.findByText("Server error; try again later");
    // ...while the metrics region (a separate region entirely) stays intact.
    screen.getByText("Evidence captured");
    screen.getByText(String(makeOverview().metrics.attention_count));
  });
});
