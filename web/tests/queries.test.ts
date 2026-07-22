import { afterEach, describe, expect, it, vi } from "vitest";
import { getAuditEvent, listAllMembers } from "../src/api/queries";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("listAllMembers", () => {
  it("follows every opaque cursor so transfer targets are not truncated", async () => {
    vi.stubGlobal("document", { cookie: "" });
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            members: [{ membership_id: "member-1" }],
            next_cursor: "opaque-page-2",
          }),
          { status: 200 },
        ),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ members: [{ membership_id: "member-2" }] }), {
          status: 200,
        }),
      );
    vi.stubGlobal("fetch", fetchMock);

    const members = await listAllMembers({ limit: 100 });

    expect(members.map((member) => member.membership_id)).toEqual(["member-1", "member-2"]);
    expect(fetchMock.mock.calls.map(([path]) => path)).toEqual([
      "/v1/admin/members?limit=100",
      "/v1/admin/members?limit=100&cursor=opaque-page-2",
    ]);
  });
});

describe("getAuditEvent", () => {
  it("fetches a single audit event by id and unwraps the envelope", async () => {
    vi.stubGlobal("document", { cookie: "" });
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          audit_event: {
            audit_event_id: 42,
            actor_kind: "human",
            actor_membership_id: "member-1",
            action: "identity.agent.retired",
            target_kind: "agent",
            target_id: "agent-1",
            occurred_at: "2026-07-22T12:00:00Z",
          },
        }),
        { status: 200 },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const event = await getAuditEvent(42);

    expect(fetchMock.mock.calls.map(([path]) => path)).toEqual(["/v1/admin/audit-events/42"]);
    expect(event.audit_event_id).toBe(42);
    expect(event.actor_membership_id).toBe("member-1");
    expect(event.action).toBe("identity.agent.retired");
  });
});
