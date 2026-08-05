// Page-level DOM tests for the merged agent detail view (doc sections
// 5.6-5.7): the owner scope (/agents/:agentId) and the admin governance
// scope (/admin/agents/:agentId) share one layout. These tests pin the load
// path (name, badges, governance permissions), the 404 card with its
// scope-specific back link, and the admin-only owner row.

import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import {
  apiErrorResponse,
  jsonResponse,
  makeAgent,
  makeMe,
  renderApp,
  setupDomTest,
} from "./helpers";

setupDomTest();

type AgentFixture = ReturnType<typeof makeAgent>;

/** Stub the detail GET (or a fixed error Response) plus the empty artifact lists. */
function detailFetch(scope: "me" | "admin", agent: AgentFixture | Response) {
  const base = `/v1/${scope}/agents/agent-1`;
  return (path: string, init: RequestInit) => {
    if (path === base && (init.method ?? "GET") === "GET") {
      return agent instanceof Response ? agent : jsonResponse({ agent });
    }
    if (path.startsWith(`${base}/enrollments`)) return jsonResponse({ enrollments: [] });
    if (path.startsWith(`${base}/credentials`)) return jsonResponse({ credentials: [] });
    throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
  };
}

describe("owner scope (/agents/:agentId)", () => {
  it("renders the agent head, badges, and full owner governance without the owner row", async () => {
    await renderApp({
      route: "/agents/agent-1",
      me: makeMe(),
      fetch: detailFetch("me", makeAgent()),
    });

    await screen.findByRole("heading", { name: "Alice Codex" });
    expect(screen.getByText("agent-1")).toBeDefined();
    expect(screen.getByText("human-registered")).toBeDefined();
    // Status badge (the .tag span from Badge) in the page head and in the
    // governance card. Scoped to .tag so it doesn't also match the
    // Credentials filter tab labeled "active".
    expect(
      screen.getAllByText(
        (content, element) => content === "active" && element?.classList.contains("tag") === true,
      ).length,
    ).toBe(2);

    // The owner row is admin-only.
    expect(screen.queryByText(/owner:/)).toBeNull();

    expect(screen.getByRole("link", { name: "← Back" }).getAttribute("href")).toBe("/agents");

    // Owner scope: editable profile, lifecycle actions, enrollment issuance.
    expect(screen.getByRole("button", { name: "Save" })).toBeDefined();
    expect(screen.getByRole("button", { name: "Suspend Agent" })).toBeDefined();
    expect(screen.getByRole("button", { name: "Retire (irreversible)" })).toBeDefined();
    expect(screen.getByRole("button", { name: "+ Issue one-time Enrollment" })).toBeDefined();
  });

  it("renders the 404 card with a link back to /agents", async () => {
    await renderApp({
      route: "/agents/agent-1",
      me: makeMe(),
      fetch: detailFetch("me", apiErrorResponse(404, "not_found", "no such agent")),
    });

    await screen.findByText(/Agent does not exist or is not visible/);
    expect(screen.getByRole("link", { name: "Back to list" }).getAttribute("href")).toBe("/agents");
  });
});

describe("admin scope (/admin/agents/:agentId)", () => {
  it("renders the owner row and admin governance constraints", async () => {
    await renderApp({
      route: "/admin/agents/agent-1",
      me: makeMe({ role: "admin" }),
      fetch: detailFetch("admin", makeAgent()),
    });

    await screen.findByRole("heading", { name: "Alice Codex" });
    expect(screen.getByText("human-registered")).toBeDefined();
    expect(screen.getByText(/owner: mbr_01/)).toBeDefined();

    expect(screen.getByRole("link", { name: "← Back" }).getAttribute("href")).toBe("/admin/agents");

    // Admin may only suspend (doc section 5.7): no edit, no retire, and
    // enrollment issuance stays with the owning human.
    expect((screen.getByLabelText("display_name") as HTMLInputElement).disabled).toBe(true);
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
    expect(screen.getByRole("button", { name: "Suspend Agent" })).toBeDefined();
    expect(screen.queryByRole("button", { name: "Retire (irreversible)" })).toBeNull();
    expect(screen.queryByRole("button", { name: "+ Issue one-time Enrollment" })).toBeNull();
  });

  it("renders the 404 card with a link back to /admin/agents", async () => {
    await renderApp({
      route: "/admin/agents/agent-1",
      me: makeMe({ role: "admin" }),
      fetch: detailFetch("admin", apiErrorResponse(404, "not_found", "no such agent")),
    });

    await screen.findByText(/Agent does not exist or is not visible/);
    expect(screen.getByRole("link", { name: "Back to list" }).getAttribute("href")).toBe(
      "/admin/agents",
    );
  });
});
