import { afterEach, describe, expect, it, vi } from "vitest";
import {
  acceptInvitation,
  beginAction,
  createAgent,
  createEnrollment,
  createInvitation,
  ifMatch,
  retireAgent,
  revokeCredential,
  revokeEnrollment,
  revokeInvitation,
  transferAgent,
  updateAgent,
  updateMember,
} from "../src/api/actions";

interface CapturedCall {
  input: string;
  init: RequestInit;
  headers: Headers;
}

const calls: CapturedCall[] = [];

function stubFetchOk(body: unknown = {}) {
  calls.length = 0;
  vi.stubGlobal("document", { cookie: "tm_csrf=tok" });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({
        input: String(input),
        init: init ?? {},
        headers: new Headers(init?.headers),
      });
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("beginAction / idempotency", () => {
  it("generates unique UUID keys per action", () => {
    const a = beginAction();
    const b = beginAction();
    expect(a).not.toBe(b);
    expect(a).toMatch(/^[0-9a-f-]{36}$/);
  });

  it("reuses the same key across retries of one accept action", async () => {
    stubFetchOk();
    const key = beginAction();
    await acceptInvitation("tm_invite_inv_01.x", key);
    await acceptInvitation("tm_invite_inv_01.x", key); // network retry
    expect(calls).toHaveLength(2);
    for (const call of calls) {
      expect(call.headers.get("Idempotency-Key")).toBe(key);
    }
  });

  it("sends Idempotency-Key on agent create and on supported revokes", async () => {
    stubFetchOk({ agent: {}, enrollment: {}, credential: {} });
    const key = beginAction();
    await createAgent(
      { agent_id: "a", display_name: "A", directory_visible: true },
      key,
    );
    await revokeEnrollment("me", "a", "enr_01", key);
    await revokeCredential("admin", "a", "cred_01", key);
    expect(calls[0].headers.get("Idempotency-Key")).toBe(key);
    expect(calls[1].headers.get("Idempotency-Key")).toBe(key);
    expect(calls[2].headers.get("Idempotency-Key")).toBe(key);
  });

  it("never sends Idempotency-Key on one-time-secret creations", async () => {
    stubFetchOk({});
    await createInvitation({ target_email: "b@example.com", role: "member", expires_in_seconds: 86400 });
    await createEnrollment("a", {
      credential_label: "l",
      permissions: ["observe"],
      expires_in_seconds: 900,
    });
    expect(calls[0].headers.get("Idempotency-Key")).toBeNull();
    expect(calls[1].headers.get("Idempotency-Key")).toBeNull();
  });

  it("sends no Idempotency-Key and no body on invitation revoke", async () => {
    stubFetchOk({});
    await revokeInvitation("inv_01");
    expect(calls[0].init.method).toBe("DELETE");
    expect(calls[0].headers.get("Idempotency-Key")).toBeNull();
    expect(calls[0].init.body).toBeUndefined();
  });
});

describe("optimistic locking", () => {
  it("ifMatch quotes the version", () => {
    expect(ifMatch(7)).toBe('"7"');
  });

  it("updateMember sends resource_version in body AND If-Match", async () => {
    stubFetchOk({ member: {} });
    await updateMember("mbr_01", { role: "admin" }, 7);
    expect(calls[0].input).toBe("/v1/admin/members/mbr_01");
    expect(calls[0].init.method).toBe("PATCH");
    expect(calls[0].headers.get("If-Match")).toBe('"7"');
    expect(JSON.parse(String(calls[0].init.body))).toEqual({ role: "admin", resource_version: 7 });
  });

  it("updateAgent scopes the path and dual-sends the version", async () => {
    stubFetchOk({ agent: {} });
    await updateAgent("me", "alice-codex", { display_name: "x" }, 4);
    expect(calls[0].input).toBe("/v1/me/agents/alice-codex");
    expect(calls[0].headers.get("If-Match")).toBe('"4"');
    expect(JSON.parse(String(calls[0].init.body))).toEqual({
      display_name: "x",
      resource_version: 4,
    });
  });

  it.each(["me", "admin"] as const)("retireAgent uses the %s DELETE endpoint", async (scope) => {
    stubFetchOk({ agent: {} });
    const key = beginAction();
    await retireAgent(scope, "alice-codex", 5, key);
    expect(calls[0].input).toBe(`/v1/${scope}/agents/alice-codex?resource_version=5`);
    expect(calls[0].init.method).toBe("DELETE");
    expect(calls[0].headers.get("If-Match")).toBe('"5"');
    expect(calls[0].headers.get("Idempotency-Key")).toBe(key);
    expect(calls[0].init.body).toBeUndefined();
  });

  it("transferAgent posts target + version with If-Match", async () => {
    stubFetchOk({ agent: {} });
    await transferAgent("a", "mbr_02", 5);
    expect(calls[0].input).toBe("/v1/admin/agents/a/transfer");
    expect(calls[0].headers.get("If-Match")).toBe('"5"');
    expect(JSON.parse(String(calls[0].init.body))).toEqual({
      target_membership_id: "mbr_02",
      resource_version: 5,
    });
  });

  it("URL-encodes agent ids in paths", async () => {
    stubFetchOk({ agent: {} });
    await updateAgent("me", "weird id", { status: "suspended" }, 1);
    expect(calls[0].input).toBe("/v1/me/agents/weird%20id");
  });
});
