// Page-level DOM tests for the Session Audit admin page: it renders the
// Findings view first (as row cards), the Seg toggle switches views while
// keeping the selection in the URL search params, the Findings "看这些调用"
// action jumps to the tool-calls view with the session pre-filled, and the
// "按天" view renders a data-driven bar chart.

import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { callsTo, jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

function sessionAuditFetch(path: string, init: RequestInit): Response {
  if (path.startsWith("/v1/admin/session-audit/findings")) {
    return jsonResponse({
      findings: [
        {
          finding_id: 7,
          user_id: "usr_01",
          agent_id: "agent-1",
          session_id: "sess-1",
          kind: "high_risk_unapproved",
          severity: "high",
          summary: "destructive command without approval",
          evidence_event_ids: ["evt-1", "evt-2"],
          created_at: "2026-08-01T10:00:00Z",
        },
      ],
    });
  }
  if (path.startsWith("/v1/admin/session-audit/tool-calls")) {
    return jsonResponse({
      tool_calls: [
        {
          event_id: "evt-1",
          user_id: "usr_01",
          agent_id: "agent-1",
          session_id: "sess-1",
          call_id: "call-1",
          tool_name: "bash",
          input_summary: "rm -rf /tmp/x",
          risk_level: "critical",
          risk_reasons: ["destructive command"],
          approval_state: "denied",
          occurred_at: "2026-08-01T10:00:00Z",
          captured_at: "2026-08-01T10:00:01Z",
        },
      ],
    });
  }
  if (path.startsWith("/v1/admin/session-audit/activity")) {
    return jsonResponse({
      activity: [
        {
          user_id: "usr_01",
          agent_id: "agent-1",
          day: "2026-08-01",
          event_count: 12,
          tool_call_count: 5,
          high_risk_count: 2,
          session_count: 2,
          tool_breakdown: { bash: 3, read: 2 },
        },
      ],
    });
  }
  throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
}

describe("Session Audit page", () => {
  it("renders the Findings view first and fetches findings", async () => {
    const { fetchMock } = await renderApp({
      route: "/governance/sessions",
      me: makeMe(),
      fetch: sessionAuditFetch,
    });

    await screen.findByRole("heading", { name: "Agent 到底做了什么" });
    await screen.findByText("destructive command without approval");

    expect(screen.getByRole("button", { name: "Findings" }).getAttribute("aria-pressed")).toBe(
      "true",
    );
    expect(screen.getByRole("button", { name: "工具调用" }).getAttribute("aria-pressed")).toBe(
      "false",
    );
    expect(callsTo(fetchMock, "/v1/admin/session-audit/findings")).toHaveLength(1);
    expect(callsTo(fetchMock, "/v1/admin/session-audit/tool-calls")).toHaveLength(0);
    // Humanized kind label and severity tag render in the row card (the
    // filter select options carry the same text, hence getAllByText).
    expect(screen.getAllByText("High-risk unapproved").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("high").length).toBeGreaterThanOrEqual(1);
  });

  it("switches views through the toggle and keeps the view in the URL", async () => {
    const { fetchMock, user } = await renderApp({
      route: "/governance/sessions",
      me: makeMe(),
      fetch: sessionAuditFetch,
    });
    await screen.findByText("destructive command without approval");

    await user.click(screen.getByRole("button", { name: "工具调用" }));
    await screen.findByText("rm -rf /tmp/x");
    expect(window.location.search).toBe("?view=tool-calls");
    expect(callsTo(fetchMock, "/v1/admin/session-audit/tool-calls")).toHaveLength(1);

    await user.click(screen.getByRole("button", { name: "按天" }));
    await screen.findByText("2026-08-01");
    expect(window.location.search).toBe("?view=activity");
    expect(callsTo(fetchMock, "/v1/admin/session-audit/activity")).toHaveLength(1);

    await user.click(screen.getByRole("button", { name: "Findings" }));
    expect(window.location.search).toBe("");
  });

  it("honors a shared ?view= link by opening that view directly", async () => {
    const { fetchMock } = await renderApp({
      route: "/governance/sessions?view=activity",
      me: makeMe(),
      fetch: sessionAuditFetch,
    });

    await screen.findByText("2026-08-01");
    expect(screen.getByRole("button", { name: "按天" }).getAttribute("aria-pressed")).toBe(
      "true",
    );
    expect(callsTo(fetchMock, "/v1/admin/session-audit/activity")).toHaveLength(1);
    expect(callsTo(fetchMock, "/v1/admin/session-audit/findings")).toHaveLength(0);
  });

  it("三视图切换写进 URL 的 ?view=，刷新后停在同一视图", async () => {
    const { user, unmount } = await renderApp({
      route: "/governance/sessions",
      me: makeMe(),
      fetch: sessionAuditFetch,
    });
    await screen.findByText("destructive command without approval");

    await user.click(screen.getByRole("button", { name: "按天" }));
    await screen.findByText("2026-08-01");
    expect(window.location.search).toBe("?view=activity");
    const search = window.location.search;
    unmount();

    // Simulate a page refresh: re-render the app fresh at the URL the
    // previous render left in the location bar.
    await renderApp({
      route: `/governance/sessions${search}`,
      me: makeMe(),
      fetch: sessionAuditFetch,
    });
    await screen.findByText("2026-08-01");
    expect(screen.getByRole("button", { name: "按天" }).getAttribute("aria-pressed")).toBe(
      "true",
    );
  });

  it("Findings 的「看这些调用」切到工具调用视图并带上该 session", async () => {
    const { fetchMock, user } = await renderApp({
      route: "/governance/sessions",
      me: makeMe(),
      fetch: sessionAuditFetch,
    });
    await screen.findByText("destructive command without approval");

    await user.click(screen.getByRole("button", { name: "看这些调用" }));
    await screen.findByText("rm -rf /tmp/x");

    expect(window.location.search).toContain("view=tool-calls");
    expect(window.location.search).toContain("session=sess-1");
    const toolCallCalls = callsTo(fetchMock, "/v1/admin/session-audit/tool-calls");
    expect(toolCallCalls.length).toBeGreaterThanOrEqual(1);
    expect(toolCallCalls.some((c) => c.path.includes("session_id=sess-1"))).toBe(true);
    expect(
      ((await screen.findByPlaceholderText("session_id")) as HTMLInputElement).value,
    ).toBe("sess-1");
  });
});

describe("Session Audit page — 按天柱状图", () => {
  function daysFetch(days: unknown[]) {
    return (path: string, init: RequestInit): Response => {
      if (path.startsWith("/v1/admin/session-audit/findings")) return jsonResponse({ findings: [] });
      if (path.startsWith("/v1/admin/session-audit/tool-calls")) return jsonResponse({ tool_calls: [] });
      if (path.startsWith("/v1/admin/session-audit/activity")) return jsonResponse({ activity: days });
      throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
    };
  }

  it("柱高与当天事件数成比例", async () => {
    await renderApp({
      route: "/governance/sessions?view=activity",
      me: makeMe(),
      fetch: daysFetch([
        { user_id: "u1", agent_id: "a1", day: "2026-08-01", event_count: 10, tool_call_count: 4, high_risk_count: 1, session_count: 2, tool_breakdown: {} },
        { user_id: "u1", agent_id: "a1", day: "2026-08-02", event_count: 5, tool_call_count: 2, high_risk_count: 0, session_count: 1, tool_breakdown: {} },
        { user_id: "u1", agent_id: "a1", day: "2026-08-03", event_count: 0, tool_call_count: 0, high_risk_count: 0, session_count: 0, tool_breakdown: {} },
      ]),
    });

    await screen.findByText("2026-08-01");
    const bars = document.querySelectorAll(".gv-day-bar");
    expect(bars).toHaveLength(3);
    const heights = Array.from(bars).map((b) => (b as HTMLElement).style.height);
    expect(heights).toEqual(["100%", "50%", "0%"]);
  });

  it("每列下方给出会话数、调用数与高危数", async () => {
    await renderApp({
      route: "/governance/sessions?view=activity",
      me: makeMe(),
      fetch: daysFetch([
        { user_id: "u1", agent_id: "a1", day: "2026-08-01", event_count: 10, tool_call_count: 4, high_risk_count: 3, session_count: 2, tool_breakdown: {} },
      ]),
    });

    await screen.findByText("2026-08-01");
    await screen.findByText("2 会话 · 4 调用");
    await screen.findByText("3 高危");
  });

  it("全部为 0 时不产生 NaN 或除零", async () => {
    await renderApp({
      route: "/governance/sessions?view=activity",
      me: makeMe(),
      fetch: daysFetch([
        { user_id: "u1", agent_id: "a1", day: "2026-08-01", event_count: 0, tool_call_count: 0, high_risk_count: 0, session_count: 0, tool_breakdown: {} },
        { user_id: "u1", agent_id: "a1", day: "2026-08-02", event_count: 0, tool_call_count: 0, high_risk_count: 0, session_count: 0, tool_breakdown: {} },
        { user_id: "u1", agent_id: "a1", day: "2026-08-03", event_count: 0, tool_call_count: 0, high_risk_count: 0, session_count: 0, tool_breakdown: {} },
      ]),
    });

    await screen.findByText("2026-08-01");
    const bars = document.querySelectorAll(".gv-day-bar");
    expect(bars).toHaveLength(3);
    const heights = Array.from(bars).map((b) => (b as HTMLElement).style.height);
    expect(heights).toEqual(["0%", "0%", "0%"]);
    expect(document.body.textContent).not.toContain("NaN");
  });
});
