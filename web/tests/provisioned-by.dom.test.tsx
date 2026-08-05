// Page-level DOM tests for the provisioned_by badge (doc section 8.1): the
// field is present only on device self-registered agents, so the badge is
// decided by field presence on both the agent list and the agent detail.

import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { jsonResponse, makeAgent, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

describe("provisioned_by badge", () => {
  it("distinguishes device-provisioned from human-registered agents in My Agents", async () => {
    // /management dispatches AdminAgentsPage to admin-likes and
    // MyAgentsPage (the self-serve page under test) to everyone else
    // (brief-mandated stand-in until phase 3's AccessTree).
    await renderApp({
      route: "/management",
      me: makeMe({ role: "member" }),
      fetch: (path) => {
        if (path.startsWith("/v1/me/agents")) {
          return jsonResponse({
            agents: [
              makeAgent(),
              makeAgent({
                agent_id: "personal-codex",
                display_name: "Dev Codex",
                provisioned_by: "dev_01",
              }),
            ],
          });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByText("Dev Codex");
    const deviceBadge = screen.getByText("device-provisioned");
    expect(deviceBadge.getAttribute("title")).toBe("provisioned by device dev_01");
    expect(screen.getByText("human-registered")).toBeDefined();
  });

  it("shows the badge on the admin agent detail", async () => {
    await renderApp({
      route: "/management/agents/personal-codex",
      me: makeMe(),
      fetch: (path) => {
        if (path === "/v1/admin/agents/personal-codex") {
          return jsonResponse({
            agent: makeAgent({
              agent_id: "personal-codex",
              display_name: "Dev Codex",
              provisioned_by: "dev_01",
            }),
          });
        }
        if (path === "/v1/admin/agents/personal-codex/enrollments") {
          return jsonResponse({ enrollments: [] });
        }
        if (path === "/v1/admin/agents/personal-codex/credentials") {
          return jsonResponse({ credentials: [] });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const badge = await screen.findByText("device-provisioned");
    expect(badge.getAttribute("title")).toBe("provisioned by device dev_01");
  });

  it("shows human-registered when the field is absent on the admin agent detail", async () => {
    await renderApp({
      route: "/management/agents/agent-1",
      me: makeMe(),
      fetch: (path) => {
        if (path === "/v1/admin/agents/agent-1") return jsonResponse({ agent: makeAgent() });
        if (path === "/v1/admin/agents/agent-1/enrollments") {
          return jsonResponse({ enrollments: [] });
        }
        if (path === "/v1/admin/agents/agent-1/credentials") {
          return jsonResponse({ credentials: [] });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByText("human-registered");
    expect(screen.queryByText("device-provisioned")).toBeNull();
  });
});
