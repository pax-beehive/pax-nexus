// 全量翻页 + 客户端二次过滤。这两条是确认弹窗计数正确性的地基。
import { describe, expect, it, vi, afterEach } from "vitest";
import { listAllCredentials, listAllEnrollments } from "../src/api/queries";
import { jsonResponse, makeCredential, makeEnrollment, stubFetch } from "./helpers";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("listAllEnrollments", () => {
  it("跟着游标把每一页都取回来", async () => {
    stubFetch((path) => {
      if (path.includes("cursor=c1")) {
        return jsonResponse({ enrollments: [makeEnrollment({ enrollment_id: "enr_02" })] });
      }
      return jsonResponse({
        enrollments: [makeEnrollment({ enrollment_id: "enr_01" })],
        next_cursor: "c1",
      });
    });

    const all = await listAllEnrollments("me", "agent-1", "pending");
    expect(all.map((e) => e.enrollment_id)).toEqual(["enr_01", "enr_02"]);
  });

  it("把 status 透传给服务端", async () => {
    const fetchMock = stubFetch(() => jsonResponse({ enrollments: [] }));
    await listAllEnrollments("admin", "agent-1", "pending");
    expect(String(fetchMock.mock.calls[0][0])).toContain("status=pending");
    expect(String(fetchMock.mock.calls[0][0])).toContain("/v1/admin/agents/agent-1/enrollments");
  });

  it("游标重复时抛错而不是无限翻页", async () => {
    stubFetch(() =>
      jsonResponse({ enrollments: [makeEnrollment()], next_cursor: "loop" }),
    );
    await expect(listAllEnrollments("me", "agent-1", "pending")).rejects.toThrow(
      /repeated cursor/i,
    );
  });
});

describe("listAllCredentials", () => {
  it("跟着游标把每一页都取回来", async () => {
    stubFetch((path) => {
      if (path.includes("cursor=c1")) {
        return jsonResponse({ credentials: [makeCredential({ credential_id: "cred_02" })] });
      }
      return jsonResponse({
        credentials: [makeCredential({ credential_id: "cred_01" })],
        next_cursor: "c1",
      });
    });

    const all = await listAllCredentials("me", "agent-1", "active");
    expect(all.map((c) => c.credential_id)).toEqual(["cred_01", "cred_02"]);
  });

  it("剔除服务端标成 active 但按时间已过期的密钥", async () => {
    // credential 没有服务端 status 字段（lib/credentials.ts 的注释）：
    // status=active 是服务端过滤，客户端必须再用 deriveCredentialStatus
    // 过一遍。宁可少算，不可多算——这个数字会出现在销毁确认框里。
    stubFetch(() =>
      jsonResponse({
        credentials: [
          makeCredential({ credential_id: "cred_live", expires_at: "2099-01-01T00:00:00Z" }),
          makeCredential({ credential_id: "cred_stale", expires_at: "2020-01-01T00:00:00Z" }),
          makeCredential({ credential_id: "cred_revoked", revoked_at: "2026-01-01T00:00:00Z" }),
        ],
      }),
    );

    const all = await listAllCredentials("me", "agent-1", "active");
    expect(all.map((c) => c.credential_id)).toEqual(["cred_live"]);
  });

  it("游标重复时抛错而不是无限翻页", async () => {
    stubFetch(() => jsonResponse({ credentials: [makeCredential()], next_cursor: "loop" }));
    await expect(listAllCredentials("me", "agent-1", "active")).rejects.toThrow(
      /repeated cursor/i,
    );
  });
});
