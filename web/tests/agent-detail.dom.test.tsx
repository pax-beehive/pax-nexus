// Page-level DOM tests for the merged agent detail page (阶段 4,
// AgentDetailPage): scope dispatch (owner-only vs. everyone-else fetch
// paths) and the three top-level states (loading / not-found / ready), plus
// the two-legged key fetch degrading independently of Identity/Lifecycle.
// Component-level coverage for Identity, Lifecycle, Keys, and Behaviour
// themselves lives in agent-identity/agent-governance/agent-artifacts/
// agent-behaviour.dom.test.tsx — this file only pins the page-level
// assembly and access.ts routing.

import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import {
  apiErrorResponse,
  callsTo,
  jsonResponse,
  makeAgent,
  makeMe,
  renderApp,
  setupDomTest,
} from "./helpers";

setupDomTest();

type AgentFixture = ReturnType<typeof makeAgent>;

/** Stub the detail GET (or a fixed error Response) plus the empty key legs. */
function agentFetch(scope: "me" | "admin", agent: AgentFixture | Response) {
  const base = `/v1/${scope}/agents/agent-1`;
  return (path: string, init: RequestInit) => {
    if (path === base && (init.method ?? "GET") === "GET") {
      return agent instanceof Response ? agent : jsonResponse({ agent });
    }
    if (path.startsWith(`${base}/enrollments`)) return jsonResponse({ enrollments: [] });
    if (path.startsWith(`${base}/credentials`)) return jsonResponse({ credentials: [] });
    if (path.startsWith("/v1/admin/session-audit/activity")) return jsonResponse({ activity: [] });
    throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
  };
}

describe("scope 分派", () => {
  it("member 只打 /v1/me/*，一个 admin 请求都不发", async () => {
    const { fetchMock } = await renderApp({
      route: "/management/agents/agent-1",
      me: makeMe({ role: "member", membership_id: "mbr_01" }),
      fetch: agentFetch("me", makeAgent({ owner_membership_id: "mbr_01" })),
    });

    await screen.findByLabelText("显示名");
    const adminCalls = fetchMock.mock.calls.filter(([url]) => String(url).startsWith("/v1/admin/"));
    expect(adminCalls).toHaveLength(0);
  });

  it("admin 看自己的 Agent：详情走 admin，密钥与发放走 me", async () => {
    // 本阶段修的洞：createEnrollment 只有 me scope。
    const { fetchMock } = await renderApp({
      route: "/management/agents/agent-1",
      me: makeMe({ role: "admin", membership_id: "mbr_07" }),
      fetch: (path: string, init: RequestInit) => {
        if (path === "/v1/admin/agents/agent-1" && (init.method ?? "GET") === "GET") {
          return jsonResponse({ agent: makeAgent({ owner_membership_id: "mbr_07" }) });
        }
        if (path.startsWith("/v1/me/agents/agent-1/enrollments")) return jsonResponse({ enrollments: [] });
        if (path.startsWith("/v1/me/agents/agent-1/credentials")) return jsonResponse({ credentials: [] });
        if (path.startsWith("/v1/admin/session-audit/activity")) return jsonResponse({ activity: [] });
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });

    expect(await screen.findByRole("button", { name: "发放接入权限" })).toBeDefined();
    expect(callsTo(fetchMock, "/v1/me/agents/agent-1/credentials")).not.toHaveLength(0);
  });

  it("admin 看别人的 Agent：没有发放按钮，密钥走 admin scope", async () => {
    const { fetchMock } = await renderApp({
      route: "/management/agents/agent-1",
      me: makeMe({ role: "admin", membership_id: "mbr_07" }),
      fetch: agentFetch("admin", makeAgent({ owner_membership_id: "mbr_99" })),
    });

    await screen.findByLabelText("显示名");
    expect(screen.queryByRole("button", { name: "发放接入权限" })).toBeNull();
    expect(callsTo(fetchMock, "/v1/admin/agents/agent-1/credentials")).not.toHaveLength(0);
  });
});

describe("三态与降级", () => {
  it("404 渲染 not-found 卡并链回访问树", async () => {
    await renderApp({
      route: "/management/agents/agent-1",
      me: makeMe({ role: "member", membership_id: "mbr_01" }),
      fetch: agentFetch("me", apiErrorResponse(404, "not_found", "no such agent")),
    });

    await screen.findByText(/这个 Agent 不存在，或者你看不到它/);
    expect(screen.getByRole("link", { name: "回到访问树" }).getAttribute("href")).toBe("/management");
  });

  it("密钥两条腿都失败，Identity 与 Lifecycle 照常渲染", async () => {
    await renderApp({
      route: "/management/agents/agent-1",
      me: makeMe({ role: "member", membership_id: "mbr_01" }),
      fetch: (path: string, init: RequestInit) => {
        if (path === "/v1/me/agents/agent-1" && (init.method ?? "GET") === "GET") {
          return jsonResponse({ agent: makeAgent({ owner_membership_id: "mbr_01" }) });
        }
        if (path.startsWith("/v1/me/agents/agent-1/")) {
          return jsonResponse({ code: "unavailable", message: "down" }, 503);
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    expect(await screen.findByLabelText("显示名")).toBeDefined();
    expect(screen.getByRole("button", { name: "暂停" })).toBeDefined();
  });

  it("member 不挂 Recent behaviour（零 session-audit 请求）", async () => {
    const { fetchMock } = await renderApp({
      route: "/management/agents/agent-1",
      me: makeMe({ role: "member", membership_id: "mbr_01" }),
      fetch: agentFetch("me", makeAgent({ owner_membership_id: "mbr_01" })),
    });

    await screen.findByLabelText("显示名");
    expect(
      fetchMock.mock.calls.filter(([url]) => String(url).includes("session-audit")),
    ).toHaveLength(0);
  });
});
