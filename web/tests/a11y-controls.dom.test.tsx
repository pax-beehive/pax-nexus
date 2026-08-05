// DOM accessibility tests for labeled filter groups and named controls
// (TM-WKS-004): every tab-style filter group has a programmatic label and
// exposes selection via aria-pressed, every filter <select> has an
// accessible name, and same-label refresh buttons are distinguishable.

import { describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import {
  jsonResponse,
  makeAgent,
  makeMe,
  renderApp,
  setupDomTest,
  type FetchHandler,
} from "./helpers";
import { renderOperationsPage } from "./operationsFixtures";

setupDomTest();

function expectPressed(button: HTMLElement, pressed: boolean) {
  expect(button.getAttribute("aria-pressed")).toBe(String(pressed));
}

describe("Members: status and role filters are labeled selects", () => {
  const membersFetch: FetchHandler = (path, init) => {
    if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [] });
    throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
  };

  it("both selects have visible labels and filter independently", async () => {
    const { user } = await renderApp({ route: "/management/members", me: makeMe(), fetch: membersFetch });
    await screen.findByRole("heading", { name: "Members" });

    const statusFilter = screen.getByLabelText("Status");
    const roleFilter = screen.getByLabelText("Role");
    expect(statusFilter.tagName).toBe("SELECT");
    expect(roleFilter.tagName).toBe("SELECT");
    expect((statusFilter as HTMLSelectElement).value).toBe("all");
    expect((roleFilter as HTMLSelectElement).value).toBe("all");

    // Changing one filter leaves the other untouched.
    await user.selectOptions(statusFilter, "active");
    expect((statusFilter as HTMLSelectElement).value).toBe("active");
    expect((roleFilter as HTMLSelectElement).value).toBe("all");
    await user.selectOptions(roleFilter, "owner");
    expect((statusFilter as HTMLSelectElement).value).toBe("active");
    expect((roleFilter as HTMLSelectElement).value).toBe("owner");
  });
});

describe("All Agents: owner select and status group", () => {
  it("the owner filter select has an accessible name and the tabs expose state", async () => {
    await renderApp({
      route: "/management/agents",
      me: makeMe(),
      fetch: (path, init) => {
        if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [] });
        if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [] });
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });
    await screen.findByRole("heading", { name: "All Agents" });

    const ownerFilter = screen.getByLabelText("Owner filter");
    expect(ownerFilter.tagName).toBe("SELECT");
    expect(within(ownerFilter as HTMLSelectElement).getByText("All Owners")).toBeTruthy();

    const statusGroup = screen.getByRole("group", { name: "agent status" });
    expectPressed(within(statusGroup).getByRole("button", { name: "all" }), true);
    expectPressed(within(statusGroup).getByRole("button", { name: "suspended" }), false);
  });
});

describe("My Agents / Invitations: tab groups follow the same pattern", () => {
  it("My Agents status tabs are labeled and pressed-aware", async () => {
    // /management dispatches AdminAgentsPage to admin-likes and MyAgentsPage
    // to everyone else (brief-mandated stand-in until phase 3's AccessTree);
    // a member lands on the self-serve view this case exercises.
    const { user } = await renderApp({
      route: "/management",
      me: makeMe({ role: "member" }),
      fetch: (path, init) => {
        if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });
    await screen.findByRole("heading", { name: "My Agents" });

    const group = screen.getByRole("group", { name: "agent status" });
    await user.click(within(group).getByRole("button", { name: "suspended" }));
    expectPressed(within(group).getByRole("button", { name: "suspended" }), true);
    expectPressed(within(group).getByRole("button", { name: "all" }), false);
  });

  it("Invitations status tabs are labeled and pressed-aware", async () => {
    await renderApp({
      route: "/management/invitations",
      me: makeMe(),
      fetch: (path, init) => {
        if (path.startsWith("/v1/admin/invitations")) return jsonResponse({ invitations: [] });
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });
    await screen.findByRole("heading", { name: "Invitations" });

    const group = screen.getByRole("group", { name: "invitation status" });
    expectPressed(within(group).getByRole("button", { name: "all" }), true);
    expectPressed(within(group).getByRole("button", { name: "pending" }), false);
  });
});

describe("Agent artifacts: enrollment and credential filter groups", () => {
  it("both tab groups on the agent detail page are labeled and pressed-aware", async () => {
    // /management/agents/:agentId dispatches AdminAgentDetailPage to
    // admin-likes and AgentDetailPage (the self-serve page under test) to
    // everyone else (brief-mandated stand-in until phase 4).
    await renderApp({
      route: "/management/agents/agent-1",
      me: makeMe({ role: "member" }),
      fetch: (path, init) => {
        if (path === "/v1/me/agents/agent-1" && init.method === "GET") {
          return jsonResponse({ agent: makeAgent() });
        }
        if (path.startsWith("/v1/me/agents/agent-1/enrollments")) {
          return jsonResponse({ enrollments: [] });
        }
        if (path.startsWith("/v1/me/agents/agent-1/credentials")) {
          return jsonResponse({ credentials: [] });
        }
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });
    await screen.findByRole("heading", { name: "Enrollments" });

    const enrollmentGroup = screen.getByRole("group", { name: "enrollment status" });
    const credentialGroup = screen.getByRole("group", { name: "credential status" });
    expect(within(enrollmentGroup).getByRole("button", { name: "all" })).not.toBe(
      within(credentialGroup).getByRole("button", { name: "all" }),
    );
    expectPressed(within(enrollmentGroup).getByRole("button", { name: "all" }), true);
    expectPressed(within(credentialGroup).getByRole("button", { name: "all" }), true);
  });
});

describe("Operations: named selects, distinct refresh buttons, window presets", () => {
  it("operation/outcome selects are named and the two Refresh buttons are distinguishable", async () => {
    await renderOperationsPage();

    expect(screen.getByLabelText("Filter by operation").tagName).toBe("SELECT");
    expect(screen.getByLabelText("Filter by outcome").tagName).toBe("SELECT");

    // Both buttons render as Refresh; only their region-scoped names differ.
    const storageRefresh = screen.getByRole("button", { name: "Refresh storage" });
    const activityRefresh = screen.getByRole("button", { name: "Refresh recent activity" });
    expect(storageRefresh.textContent).toBe("Refresh");
    expect(activityRefresh.textContent).toBe("Refresh");
    expect(storageRefresh).not.toBe(activityRefresh);
  });

  it("time-window presets form a labeled group with pressed state", async () => {
    const { user } = await renderOperationsPage();

    const group = screen.getByRole("group", { name: "time window" });
    expectPressed(within(group).getByRole("button", { name: "24h" }), true);
    await user.click(within(group).getByRole("button", { name: "1h" }));
    expectPressed(within(group).getByRole("button", { name: "1h" }), true);
    expectPressed(within(group).getByRole("button", { name: "24h" }), false);
  });
});
