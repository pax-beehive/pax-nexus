import { afterEach, describe, expect, it, vi } from "vitest";
import { listAllMembers } from "../src/api/queries";

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
