// Page-level DOM tests for the merged agent detail page (阶段 4,
// AgentDetailPage): scope dispatch (owner-only vs. everyone-else fetch
// paths) and the three top-level states (loading / not-found / ready), plus
// the two-legged key fetch degrading independently of Identity/Lifecycle.
// Component-level coverage for Identity, Lifecycle, Keys, and Behaviour
// themselves lives in agent-identity/agent-governance/agent-artifacts/
// agent-behaviour.dom.test.tsx — this file only pins the page-level
// assembly and agentScope.ts routing.

import { describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  apiErrorResponse,
  callsTo,
  jsonResponse,
  makeAgent,
  makeCredential,
  makeEnrollment,
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

  // 设计文档 §11 的同源计数要求：把喂给弹窗的数组截断一条，测试必须变红。
  // agent-governance.dom.test.tsx 只单独渲染 AgentLifecycleCard、自己传数组
  // （验证的是「弹窗渲染它收到的数组」，恒真），此前的页面级用例又把两条腿全
  // stub 成空数组——从没有同一批非空数据同时流过 Active keys 卡和暂停确认
  // 弹窗。这条用例补上那唯一能分叉的地方：断言必须来自同一次渲染的同一批
  // fetch 数据，这样 AgentDetailPage.tsx 把数组喂给 AgentLifecycleCard 时
  // 一旦被截断（例如误写成 `.items?.slice(1)`），卡片与弹窗就会同时失配。
  it("Active keys 卡与暂停确认弹窗对同一批密钥同源", async () => {
    const user = userEvent.setup();
    await renderApp({
      route: "/management/agents/agent-1",
      me: makeMe({ role: "member", membership_id: "mbr_01" }),
      fetch: (path: string, init: RequestInit) => {
        if (path === "/v1/me/agents/agent-1" && (init.method ?? "GET") === "GET") {
          return jsonResponse({ agent: makeAgent({ owner_membership_id: "mbr_01" }) });
        }
        if (path.startsWith("/v1/me/agents/agent-1/enrollments")) {
          return jsonResponse({
            enrollments: [
              makeEnrollment({ enrollment_id: "enr_pending", credential_label: "raspberry-pi" }),
            ],
          });
        }
        if (path.startsWith("/v1/me/agents/agent-1/credentials")) {
          return jsonResponse({
            credentials: [
              makeCredential({ credential_id: "cred_a", label: "mac-studio-01" }),
              makeCredential({ credential_id: "cred_b", label: "linux-box" }),
            ],
          });
        }
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });

    // Active keys 卡：两把密钥的 label 都在。
    await screen.findByText("mac-studio-01");
    expect(screen.getByText("linux-box")).toBeDefined();

    // 暂停确认弹窗：同一批数据必须在这里再出现一次，计数文案对得上。
    await user.click(screen.getByRole("button", { name: "暂停" }));
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByText(/2 把活跃密钥/)).toBeDefined();
    expect(within(dialog).getByText("mac-studio-01")).toBeDefined();
    expect(within(dialog).getByText("linux-box")).toBeDefined();
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
