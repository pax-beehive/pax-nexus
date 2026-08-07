// Page-level DOM tests for the provisioned_by badge (doc section 8.1): the
// field is present only on device self-registered agents, so the badge is
// decided by field presence on both the agent list and the agent detail.

import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { apiErrorResponse, jsonResponse, makeAgent, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

describe("provisioned_by badge", () => {
  it("distinguishes device-provisioned from human-registered agents in My Agents", async () => {
    // /management is admin+'s access tree now; MyAgentsLevel is reachable
    // there only for the member fork (owner/admin see their own agents via
    // the access tree's own-machine level instead).
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
    // 阶段 4：owner/admin 默认拥有 view.audit，AgentBehaviourCard 会挂载并
    // 打 session-audit；owner_membership_id 特意设成查看者之外的人，
    // 让密钥两条腿走 admin scope（管理员在看别人的 Agent，不是自己的）。
    // provisioned_by 的机器名解析（getDevice）刻意不 stub 成功——它只是
    // 锦上添花，取不到时静默退回 ProvisionedByBadge，这正是本测试要盯的
    // 兜底路径。
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
              owner_membership_id: "mbr_99",
            }),
          });
        }
        if (path === "/v1/admin/agents/personal-codex/enrollments") {
          return jsonResponse({ enrollments: [] });
        }
        if (path === "/v1/admin/agents/personal-codex/credentials") {
          return jsonResponse({ credentials: [] });
        }
        if (path === "/v1/admin/devices/dev_01") {
          return apiErrorResponse(404, "not_found", "no such device");
        }
        if (path.startsWith("/v1/admin/session-audit/activity")) {
          return jsonResponse({ activity: [] });
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
        if (path === "/v1/admin/agents/agent-1") {
          return jsonResponse({ agent: makeAgent({ owner_membership_id: "mbr_99" }) });
        }
        if (path === "/v1/admin/agents/agent-1/enrollments") {
          return jsonResponse({ enrollments: [] });
        }
        if (path === "/v1/admin/agents/agent-1/credentials") {
          return jsonResponse({ credentials: [] });
        }
        if (path.startsWith("/v1/admin/session-audit/activity")) {
          return jsonResponse({ activity: [] });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByText("human-registered");
    expect(screen.queryByText("device-provisioned")).toBeNull();
  });
});
