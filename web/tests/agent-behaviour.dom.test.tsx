import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { AgentBehaviourCard, activityWindow, sumActivity } from "../src/pages/agent/AgentBehaviourCard";
import { AgentHeader } from "../src/pages/agent/AgentHeader";
import { resolveAgentAccess } from "../src/pages/agent/agentScope";
import { jsonResponse, makeAgent, makeDevice, makeMe, setupDomTest, stubFetch } from "./helpers";

setupDomTest();

describe("activityWindow", () => {
  it("是含两端的 7 天窗口，按本地日期算", () => {
    expect(activityWindow(new Date(2026, 7, 6))).toEqual({
      from_day: "2026-07-31",
      to_day: "2026-08-06",
    });
  });
});

describe("sumActivity", () => {
  it("把各天的三个计数分别求和", () => {
    expect(
      sumActivity([
        { user_id: "u", agent_id: "a", day: "2026-08-05", event_count: 9, tool_call_count: 10, high_risk_count: 1, session_count: 2, tool_breakdown: {} },
        { user_id: "u", agent_id: "a", day: "2026-08-06", event_count: 4, tool_call_count: 5, high_risk_count: 0, session_count: 3, tool_breakdown: {} },
      ]),
    ).toEqual({ toolCalls: 15, highRisk: 1, sessions: 5 });
  });
});

describe("AgentBehaviourCard", () => {
  it("渲染三格并带上 agent_id 与 7 天窗口", async () => {
    const fetchMock = stubFetch(() =>
      jsonResponse({
        activity: [
          { user_id: "u", agent_id: "agent-1", day: "2026-08-06", event_count: 1, tool_call_count: 318, high_risk_count: 2, session_count: 7, tool_breakdown: {} },
        ],
      }),
    );
    render(<AgentBehaviourCard agentId="agent-1" />);

    expect(await screen.findByText("318")).toBeDefined();
    expect(screen.getByText("2")).toBeDefined();
    expect(screen.getByText("7")).toBeDefined();
    const url = String(fetchMock.mock.calls[0][0]);
    expect(url).toContain("agent_id=agent-1");
    expect(url).toContain("from_day=");
    expect(url).toContain("to_day=");
  });

  it("取数失败时只塌自己这一格", async () => {
    stubFetch(() => jsonResponse({ code: "unavailable", message: "down" }, 503));
    render(<AgentBehaviourCard agentId="agent-1" />);
    expect(await screen.findByText(/近期行为没取到/)).toBeDefined();
  });
});

describe("AgentHeader", () => {
  it("admin 视角把 provisioned_by 换成机器名", async () => {
    stubFetch(() => jsonResponse({ device: makeDevice({ device_name: "todd-macbook-air" }), agents: [] }));
    const me = makeMe({ role: "admin", membership_id: "mbr_07" });
    const agent = makeAgent({ owner_membership_id: "mbr_99", provisioned_by: "dev_01" });
    render(
      <MemoryRouter>
        <AgentHeader agent={agent} access={resolveAgentAccess(me, agent)} me={me} />
      </MemoryRouter>,
    );
    expect(await screen.findByText(/todd-macbook-air/)).toBeDefined();
  });

  it("机器名取不到时静默退回 device-provisioned 标签", async () => {
    stubFetch(() => jsonResponse({ code: "not_found", message: "gone" }, 404));
    const me = makeMe({ role: "admin", membership_id: "mbr_07" });
    const agent = makeAgent({ owner_membership_id: "mbr_99", provisioned_by: "dev_01" });
    render(
      <MemoryRouter>
        <AgentHeader agent={agent} access={resolveAgentAccess(me, agent)} me={me} />
      </MemoryRouter>,
    );
    expect(await screen.findByText("device-provisioned")).toBeDefined();
  });

  it("member 视角不请求 /v1/admin/devices，只显示标签", async () => {
    const fetchMock = stubFetch(() => {
      throw new Error("member 不该请求 admin 端点");
    });
    const me = makeMe({ role: "member", membership_id: "mbr_01" });
    const agent = makeAgent({ owner_membership_id: "mbr_01", provisioned_by: "dev_01" });
    render(
      <MemoryRouter>
        <AgentHeader agent={agent} access={resolveAgentAccess(me, agent)} me={me} />
      </MemoryRouter>,
    );
    expect(screen.getByText("device-provisioned")).toBeDefined();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("两个跳转按钮各自按能力门控，且带上 agent 参数", () => {
    const me = makeMe({ role: "owner", membership_id: "mbr_01", capabilities: ["view.team-memory"] });
    const agent = makeAgent({ owner_membership_id: "mbr_01" });
    render(
      <MemoryRouter>
        <AgentHeader agent={agent} access={resolveAgentAccess(me, agent)} me={me} />
      </MemoryRouter>,
    );
    expect(screen.getByRole("link", { name: "查看它的会话" }).getAttribute("href")).toBe(
      "/governance/sessions?agent=agent-1",
    );
    expect(screen.getByRole("link", { name: "查看它的记忆" }).getAttribute("href")).toBe(
      "/governance/memory?agent=agent-1",
    );
  });

  it("member 看不到这两个跳转", () => {
    const me = makeMe({ role: "member", membership_id: "mbr_01" });
    const agent = makeAgent({ owner_membership_id: "mbr_01" });
    render(
      <MemoryRouter>
        <AgentHeader agent={agent} access={resolveAgentAccess(me, agent)} me={me} />
      </MemoryRouter>,
    );
    expect(screen.queryByRole("link", { name: "查看它的会话" })).toBeNull();
    expect(screen.queryByRole("link", { name: "查看它的记忆" })).toBeNull();
  });
});
