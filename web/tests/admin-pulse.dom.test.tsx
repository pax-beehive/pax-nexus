// Page-level DOM tests for the Team Pulse page: agent cards with count-up
// stats and recent notes, the freshness status dot, the live event feed, the
// empty state, and region independence when the aggregate endpoint fails.

import { describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import { apiErrorResponse, jsonResponse, setupDomTest } from "./helpers";
import {
  agentStatsPage,
  eventsPage,
  makeAgentStats,
  makeEvent,
  renderPulsePage,
  statValue,
} from "./operationsFixtures";

setupDomTest();

function agentCard(name: string): HTMLElement {
  return screen.getByText(name).closest(".pulse-card") as HTMLElement;
}

function feedList(): HTMLElement {
  return document.getElementById("pulse-feed") as HTMLElement;
}

describe("agent cards", () => {
  it("renders each agent with count-up stats, notes and a last-active label", async () => {
    await renderPulsePage({
      agentStats: () =>
        jsonResponse(
          agentStatsPage([
            makeAgentStats(),
            makeAgentStats({
              agent_id: "agent-2",
              display_name: "Bob Pi",
              events_written: 3,
              notes_authored: 0,
              recall_requests: 1,
              channel_sent: 0,
              channel_received_accepted: 0,
              recent_notes: [],
            }),
          ]),
        ),
    });

    const first = agentCard("Alice Codex");
    expect(statValue(first, "events written")).toBe("48");
    expect(statValue(first, "notes authored")).toBe("5");
    expect(statValue(first, "recalls")).toBe("7");
    // capsules = channel sent + accepted.
    expect(statValue(first, "capsules")).toBe("5");
    within(first).getByText("Postgres is the only metadata store");
    within(first).getByText("decision");
    within(first).getByText("just now");

    const second = agentCard("Bob Pi");
    expect(statValue(second, "events written")).toBe("3");
    expect(statValue(second, "capsules")).toBe("0");
    expect(within(second).queryByText("decision")).toBeNull();
  });

  it("drives the status dot from last_active_at freshness", async () => {
    const minutesAgo = (m: number) => new Date(Date.now() - m * 60_000).toISOString();
    await renderPulsePage({
      agentStats: () =>
        jsonResponse(
          agentStatsPage([
            makeAgentStats({ display_name: "Active Agent", last_active_at: minutesAgo(0) }),
            makeAgentStats({
              agent_id: "agent-2",
              display_name: "Recent Agent",
              last_active_at: minutesAgo(5),
            }),
            makeAgentStats({
              agent_id: "agent-3",
              display_name: "Idle Agent",
              last_active_at: minutesAgo(120),
            }),
            makeAgentStats({
              agent_id: "agent-4",
              display_name: "Silent Agent",
              last_active_at: "",
            }),
          ]),
        ),
    });

    const dotOf = (name: string) =>
      agentCard(name).querySelector(".pulse-dot") as HTMLElement;
    expect(dotOf("Active Agent").classList.contains("s-active")).toBe(true);
    expect(dotOf("Recent Agent").classList.contains("s-recent")).toBe(true);
    expect(dotOf("Idle Agent").classList.contains("s-idle")).toBe(true);
    // No recorded activity is idle and never shows a relative time.
    expect(dotOf("Silent Agent").classList.contains("s-idle")).toBe(true);
    within(agentCard("Silent Agent")).getByText(/Last active: no activity/);
  });

  it("shows the flow strip with the aggregate volumes of the window", async () => {
    await renderPulsePage();
    screen.getByRole("img", {
      name: /48 events written, 5 notes produced, 7 recalls/,
    });
  });

  it("guides to the Agents page when no agent has activity", async () => {
    await renderPulsePage({
      agentStats: () => jsonResponse(agentStatsPage([])),
    });

    screen.getByText("No agent activity yet");
    const link = screen.getByRole("link", { name: "Go to All Agents to register an agent" });
    expect(link.getAttribute("href")).toBe("/admin/agents");
    expect(document.querySelector(".pulse-card")).toBeNull();
  });
});

describe("live event feed", () => {
  it("renders polled events without a first-load highlight", async () => {
    await renderPulsePage({
      events: () => jsonResponse(eventsPage([makeEvent()])),
    });

    const feed = feedList();
    within(feed).getByText("Memory Search");
    within(feed).getByText("succeeded");
    // The agent label comes from the stats display name.
    within(feed).getByText("Alice Codex");
    // First load highlights nothing: slide-in/flash is for later arrivals.
    expect(feed.querySelector(".pulse-feed-item.new")).toBeNull();
  });

  it("keeps the feed alive when the agent-stats region fails", async () => {
    await renderPulsePage({
      agentStats: () => apiErrorResponse(500, "internal_error", "boom"),
      events: () => jsonResponse(eventsPage([makeEvent()])),
    });

    expect(screen.getAllByText("Server error; try again later").length).toBeGreaterThanOrEqual(1);
    expect(document.querySelector(".pulse-card")).toBeNull();
    within(feedList()).getByText("Memory Search");
  });

  it("keeps the cards alive when the events region fails", async () => {
    await renderPulsePage({
      events: () => apiErrorResponse(500, "internal_error", "boom"),
    });

    const first = agentCard("Alice Codex");
    expect(statValue(first, "events written")).toBe("48");
    expect(screen.getAllByText("Server error; try again later").length).toBeGreaterThanOrEqual(1);
    expect(document.getElementById("pulse-feed")).toBeNull();
  });
});
