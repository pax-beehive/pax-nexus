import { afterEach, describe, expect, it, vi } from "vitest";
import {
  listSessionAuditActivity,
  listSessionAuditFindings,
  listSessionAuditToolCalls,
} from "../src/api/queries";

afterEach(() => {
  vi.unstubAllGlobals();
});

function stubJsonFetch(body: unknown) {
  vi.stubGlobal("document", { cookie: "" });
  const fetchMock = vi
    .fn()
    .mockResolvedValue(new Response(JSON.stringify(body), { status: 200 }));
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

describe("listSessionAuditFindings", () => {
  it("serializes filters as query params and unwraps the findings envelope", async () => {
    const fetchMock = stubJsonFetch({
      findings: [
        {
          finding_id: 7,
          user_id: "usr_01",
          agent_id: "agent-1",
          session_id: "sess-1",
          kind: "high_risk_unapproved",
          severity: "high",
          summary: "rm -rf without approval",
          evidence_event_ids: ["evt-1"],
          created_at: "2026-08-01T10:00:00Z",
        },
      ],
    });

    const findings = await listSessionAuditFindings({
      user_id: "usr_01",
      kind: "high_risk_unapproved",
      severity: "high",
      limit: 50,
    });

    expect(fetchMock.mock.calls.map(([path]) => path)).toEqual([
      "/v1/admin/session-audit/findings?user_id=usr_01&kind=high_risk_unapproved&severity=high&limit=50",
    ]);
    expect(findings).toHaveLength(1);
    expect(findings[0].finding_id).toBe(7);
    expect(findings[0].evidence_event_ids).toEqual(["evt-1"]);
  });

  it("omits empty filters from the query string", async () => {
    const fetchMock = stubJsonFetch({ findings: [] });

    await listSessionAuditFindings({});

    expect(fetchMock.mock.calls.map(([path]) => path)).toEqual([
      "/v1/admin/session-audit/findings",
    ]);
  });
});

describe("listSessionAuditToolCalls", () => {
  it("serializes filters as query params and unwraps the tool_calls envelope", async () => {
    const fetchMock = stubJsonFetch({
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

    const toolCalls = await listSessionAuditToolCalls({
      agent_id: "agent-1",
      session_id: "sess-1",
      risk_level: "critical",
      approval_state: "denied",
    });

    expect(fetchMock.mock.calls.map(([path]) => path)).toEqual([
      "/v1/admin/session-audit/tool-calls?agent_id=agent-1&session_id=sess-1&risk_level=critical&approval_state=denied",
    ]);
    expect(toolCalls).toHaveLength(1);
    expect(toolCalls[0].tool_name).toBe("bash");
    expect(toolCalls[0].risk_reasons).toEqual(["destructive command"]);
  });
});

describe("listSessionAuditActivity", () => {
  it("serializes day bounds as query params and unwraps the activity envelope", async () => {
    const fetchMock = stubJsonFetch({
      activity: [
        {
          user_id: "usr_01",
          agent_id: "agent-1",
          day: "2026-08-01",
          event_count: 12,
          tool_call_count: 5,
          high_risk_count: 1,
          session_count: 2,
          tool_breakdown: { bash: 3, read: 2 },
        },
      ],
    });

    const activity = await listSessionAuditActivity({
      user_id: "usr_01",
      from_day: "2026-08-01",
      to_day: "2026-08-03",
    });

    expect(fetchMock.mock.calls.map(([path]) => path)).toEqual([
      "/v1/admin/session-audit/activity?user_id=usr_01&from_day=2026-08-01&to_day=2026-08-03",
    ]);
    expect(activity).toHaveLength(1);
    expect(activity[0].tool_breakdown).toEqual({ bash: 3, read: 2 });
  });
});
