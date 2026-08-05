// Page-level DOM tests for the Session Audit admin page: it renders the
// Findings view first, and the .seg toggle switches views while keeping the
// selection in the URL search params.

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

    await screen.findByRole("heading", { name: "Session Audit" });
    await screen.findByText("destructive command without approval");

    expect(screen.getByRole("button", { name: "Findings" }).getAttribute("aria-pressed")).toBe(
      "true",
    );
    expect(screen.getByRole("button", { name: "Tool Calls" }).getAttribute("aria-pressed")).toBe(
      "false",
    );
    expect(callsTo(fetchMock, "/v1/admin/session-audit/findings")).toHaveLength(1);
    expect(callsTo(fetchMock, "/v1/admin/session-audit/tool-calls")).toHaveLength(0);
    // Humanized kind label and severity badge render in the row (the filter
    // select options carry the same text, hence the getAllByText).
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

    await user.click(screen.getByRole("button", { name: "Tool Calls" }));
    await screen.findByText("rm -rf /tmp/x");
    expect(window.location.search).toBe("?view=tool-calls");
    expect(callsTo(fetchMock, "/v1/admin/session-audit/tool-calls")).toHaveLength(1);

    await user.click(screen.getByRole("button", { name: "Activity" }));
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
    expect(screen.getByRole("button", { name: "Activity" }).getAttribute("aria-pressed")).toBe(
      "true",
    );
    expect(callsTo(fetchMock, "/v1/admin/session-audit/activity")).toHaveLength(1);
    expect(callsTo(fetchMock, "/v1/admin/session-audit/findings")).toHaveLength(0);
  });
});
