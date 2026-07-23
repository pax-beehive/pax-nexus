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

describe("Members: status and role filter groups are labeled and pressed-aware", () => {
  const membersFetch: FetchHandler = (path, init) => {
    if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [] });
    throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
  };

  it("both groups are labeled, their all buttons are distinct and expose state", async () => {
    const { user } = await renderApp({ route: "/admin/members", me: makeMe(), fetch: membersFetch });
    await screen.findByRole("heading", { name: "Members" });

    const statusGroup = screen.getByRole("group", { name: "member status" });
    const roleGroup = screen.getByRole("group", { name: "member role" });

    // The two "all" buttons are scoped to their own labeled groups.
    const statusAll = within(statusGroup).getByRole("button", { name: "all" });
    const roleAll = within(roleGroup).getByRole("button", { name: "all" });
    expect(statusAll).not.toBe(roleAll);
    expectPressed(statusAll, true);
    expectPressed(roleAll, true);

    // Clicking a filter moves the pressed state within that group only.
    await user.click(within(statusGroup).getByRole("button", { name: "active" }));
    expectPressed(within(statusGroup).getByRole("button", { name: "active" }), true);
    expectPressed(statusAll, false);
    expectPressed(roleAll, true);
  });
});

describe("All Agents: owner select and status group", () => {
  it("the owner filter select has an accessible name and the tabs expose state", async () => {
    await renderApp({
      route: "/admin/agents",
      me: makeMe(),
      fetch: (path, init) => {
        if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [] });
        if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [] });
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });
    await screen.findByRole("heading", { name: "All Agents" });

    const ownerFilter = screen.getByLabelText("Owner 过滤");
    expect(ownerFilter.tagName).toBe("SELECT");
    expect(within(ownerFilter as HTMLSelectElement).getByText("全部 Owner")).toBeTruthy();

    const statusGroup = screen.getByRole("group", { name: "agent status" });
    expectPressed(within(statusGroup).getByRole("button", { name: "all" }), true);
    expectPressed(within(statusGroup).getByRole("button", { name: "suspended" }), false);
  });
});

describe("My Agents / Invitations: tab groups follow the same pattern", () => {
  it("My Agents status tabs are labeled and pressed-aware", async () => {
    const { user } = await renderApp({
      route: "/agents",
      me: makeMe(),
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
      route: "/admin/invitations",
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
    await renderApp({
      route: "/agents/agent-1",
      me: makeMe(),
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
  it("operation/outcome selects are named and the two 刷新 buttons are distinguishable", async () => {
    await renderOperationsPage();

    expect(screen.getByLabelText("按 operation 过滤").tagName).toBe("SELECT");
    expect(screen.getByLabelText("按 outcome 过滤").tagName).toBe("SELECT");

    // Both buttons render as 刷新; only their region-scoped names differ.
    const storageRefresh = screen.getByRole("button", { name: "刷新存储" });
    const activityRefresh = screen.getByRole("button", { name: "刷新最近活动" });
    expect(storageRefresh.textContent).toBe("刷新");
    expect(activityRefresh.textContent).toBe("刷新");
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
