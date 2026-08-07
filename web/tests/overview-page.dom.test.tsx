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
import {
  apiErrorResponse,
  callsTo,
  jsonResponse,
  makeAgent,
  makeDevice,
  makeMe,
  makeMember,
  renderApp,
  setupDomTest,
} from "./helpers";
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

    // The fixture reports the default 7d retention, so 7d is offered plainly:
    // no disabled state and no explanatory tooltip. The stopgap hint that used
    // to sit on every option regardless is gone (issue #86).
    const sevenDay = screen.getByRole("button", { name: "7d" }) as HTMLButtonElement;
    expect(sevenDay.disabled).toBe(false);
    expect(sevenDay.getAttribute("title")).toBeNull();
    // Nothing is limited, so there is no retention note to show.
    expect(screen.queryByText(/^Retained for /)).toBeNull();

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

  // Tick format follows the span of the response being drawn, not the window
  // the user has selected. The two disagree for as long as a window switch is
  // in flight; this test makes them disagree permanently, which is the same
  // condition without needing to catch the transient (issue #84).
  //
  // Asserts the label *shape* rather than specific dates: bucket_at is UTC
  // while the labels are built from local getters, so hard-coded values would
  // pass or fail depending on the machine's timezone.
  const DATE_FORM = /^\d{1,2}\/\d{1,2}$/;
  const TIME_FORM = /^\d{2}:\d{2}$/;
  const tickTexts = () =>
    Array.from(document.querySelectorAll(".ov-chart-tick")).map((n) => n.textContent ?? "");

  it("labels ticks from the response span, not the selected window", async () => {
    // Selected window is the default 24h; the response covers a week.
    await renderOverviewPage({
      overview: () =>
        makeOverview({
          from_time: "2026-07-15T12:00:00Z",
          to_time: "2026-07-22T12:00:00Z",
          series: Array.from({ length: 7 }, (_, i) => ({
            bucket_at: `2026-07-${15 + i}T12:00:00Z`,
            evidence: 10 + i,
            facts: 5 + i,
            recalls: 2 + i,
          })),
        }),
    });

    const ticks = tickTexts();
    expect(ticks).toHaveLength(7);
    ticks.forEach((t) => expect(t).toMatch(DATE_FORM));
  });

  it("labels ticks by clock time for a sub-day response span", async () => {
    // makeOverview()'s default span is 24h across 6 four-hourly buckets.
    await renderOverviewPage({ overview: () => makeOverview() });

    const ticks = tickTexts();
    expect(ticks).toHaveLength(6);
    ticks.forEach((t) => expect(t).toMatch(TIME_FORM));
  });

  // On a deployment that keeps less than a week of events, the 7d button used
  // to be offered anyway, always fail, and fail with a generic 400 the user
  // could not act on (issue #86).
  it("disables windows the deployment cannot answer, naming the retention", async () => {
    const app = await renderOverviewPage({
      overview: () => makeOverview({ event_retention_seconds: 24 * 60 * 60 }),
    });

    const sevenDay = screen.getByRole("button", { name: "7d" }) as HTMLButtonElement;
    await waitFor(() => expect(sevenDay.disabled).toBe(true));
    expect(sevenDay.getAttribute("title")).toBe("This deployment keeps 24h of events");
    // The reason is on screen, not only in a tooltip: a hover-only explanation
    // is invisible on touch, which was the complaint against the phase 2b
    // stopgap this replaces (issue #86).
    screen.getByText("Retained for 24h");

    // Still offered, so the reason has somewhere to live -- and clicking it
    // fires no request rather than one the backend will reject.
    const before = callsTo(app.fetchMock, "/v1/admin/overview").length;
    await app.user.click(sevenDay).catch(() => {});
    expect(callsTo(app.fetchMock, "/v1/admin/overview")).toHaveLength(before);

    // The windows it can answer stay untouched.
    expect((screen.getByRole("button", { name: "24h" }) as HTMLButtonElement).disabled).toBe(false);
    expect((screen.getByRole("button", { name: "1h" }) as HTMLButtonElement).disabled).toBe(false);
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

    await screen.findByRole("heading", { name: "What the agents actually did" });
  });

  // The Access strip's all-fulfilled path. Its failure mode is that the strip
  // disappears, so without this test a broken request shape, a broken response
  // shape, or a miscount is invisible -- nothing fails and the page still looks
  // healthy (issue #83).
  it("renders the Access strip counts when all three legs resolve", async () => {
    await renderOverviewPage({
      // Three different counts against three different response shapes: a flat
      // Member[] (listAllMembers paginates and concatenates), and Page.items
      // for devices and agents. Equal counts would let a swapped pair pass.
      members: () =>
        jsonResponse({
          members: [
            makeMember({ membership_id: "mbr_01" }),
            makeMember({ membership_id: "mbr_02" }),
            makeMember({ membership_id: "mbr_03" }),
          ],
        }),
      devices: () =>
        jsonResponse({
          devices: [makeDevice({ credential_id: "dev_01" }), makeDevice({ credential_id: "dev_02" })],
        }),
      agents: () =>
        jsonResponse({
          agents: [
            makeAgent({ agent_id: "agent-1" }),
            makeAgent({ agent_id: "agent-2" }),
            makeAgent({ agent_id: "agent-3" }),
            makeAgent({ agent_id: "agent-4" }),
          ],
        }),
    });

    await screen.findByText("3 people · 2 machines · 4 agents");
    expect(
      screen.getByRole("link", { name: /Open Management/ }).getAttribute("href"),
    ).toBe("/management");
  });

  // The other half of the same contract: one rejected leg drops the whole strip
  // rather than rendering a partial or stale count, and does so without
  // disturbing the rest of the page. Pinned here so it stays a decision rather
  // than whatever the harness happens to produce (issue #83).
  it("hides the Access strip entirely when one leg fails", async () => {
    await renderOverviewPage({
      devices: () => apiErrorResponse(500, "internal", "boom"),
    });

    // The attention queue itself -- the strip's own card -- is unaffected.
    await screen.findByText("High-risk tool call without approval");
    expect(screen.queryByText(/people · .* machines · .* agents/)).toBeNull();
    expect(screen.queryByRole("link", { name: /Open Management/ })).toBeNull();
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

    await screen.findByText("1 new event held while you read");
    expect(screen.queryByText("Extraction")).toBeNull();

    await app.user.click(screen.getByRole("button", { name: "Show" }));

    await screen.findByText("Extraction");
    expect(screen.queryByText("1 new event held while you read")).toBeNull();
  });

  // Both arms of the held-events count, because only the plural arm was ever
  // written and "1 new events" shipped for a while (issue #85).
  it("pluralizes the held-events banner on the count", async () => {
    let pollCount = 0;
    const app = await renderOverviewPage({
      events: () =>
        jsonResponse(
          pollCount++ === 0
            ? eventsPage([makeEvent()])
            : eventsPage([
                makeEvent({ attempt_id: "op_new1", operation_kind: "extraction.run" }),
                makeEvent({ attempt_id: "op_new2", operation_kind: "extraction.run" }),
                makeEvent(),
              ]),
        ),
    });

    await screen.findByText("Memory Search");
    fireEvent.scroll(document.getElementById("overview-feed") as HTMLElement, {
      target: { scrollTop: 100 },
    });
    await app.user.click(screen.getByRole("button", { name: "Refresh event feed" }));

    await screen.findByText("2 new events held while you read");
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
