import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, humanFetch, readCookie } from "../src/api/client";

function stubDocument(cookie: string) {
  vi.stubGlobal("document", { cookie });
}

function stubFetch(impl: (input: string, init: RequestInit) => Promise<Response>) {
  const fn = vi.fn(impl);
  vi.stubGlobal("fetch", fn);
  return fn;
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("readCookie", () => {
  it("reads a cookie value by name", () => {
    stubDocument("a=1; tm_csrf=abc%20def; b=2");
    expect(readCookie("tm_csrf")).toBe("abc%20def");
  });

  it("returns undefined when the cookie is absent", () => {
    stubDocument("a=1");
    expect(readCookie("tm_csrf")).toBeUndefined();
  });

  it("does not match a cookie whose name is only a prefix", () => {
    stubDocument("tm_csrf_extra=nope");
    expect(readCookie("tm_csrf")).toBeUndefined();
  });
});

describe("humanFetch", () => {
  it("sends credentials include and Accept on every request", async () => {
    stubDocument("tm_csrf=tok");
    const fetchMock = stubFetch(async () => jsonResponse({ ok: true }));
    await humanFetch("/v1/me");
    const [input, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(input).toBe("/v1/me");
    expect(init.credentials).toBe("include");
    expect(new Headers(init.headers).get("Accept")).toBe("application/json");
  });

  it("attaches decoded X-CSRF-Token for non-GET methods", async () => {
    stubDocument("tm_csrf=abc%20def");
    const fetchMock = stubFetch(async () => jsonResponse({}));
    await humanFetch("/v1/auth/logout", { method: "POST" });
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(new Headers(init.headers).get("X-CSRF-Token")).toBe("abc def");
  });

  it.each(["GET", "HEAD", "OPTIONS"])("does not attach CSRF for %s", async (method) => {
    stubDocument("tm_csrf=tok");
    const fetchMock = stubFetch(async () => jsonResponse({}));
    await humanFetch("/v1/me", { method });
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(new Headers(init.headers).get("X-CSRF-Token")).toBeNull();
  });

  it("throws before fetch when the CSRF cookie is missing on a mutation", async () => {
    stubDocument("");
    const fetchMock = stubFetch(async () => jsonResponse({}));
    await expect(humanFetch("/v1/auth/logout", { method: "POST" })).rejects.toThrow(
      "Missing CSRF cookie",
    );
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("throws ApiError carrying status and diagnostic on !ok", async () => {
    stubDocument("tm_csrf=tok");
    stubFetch(async () => new Response("boom", { status: 409 }));
    const err = await humanFetch("/v1/me").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(409);
    expect((err as ApiError).diagnostic).toBe("boom");
  });

  it("parses the stable code from a structured API error", async () => {
    stubDocument("tm_csrf=tok");
    stubFetch(async () =>
      jsonResponse(
        {
          code: "resource_version_conflict",
          message: "the requested change conflicts with current state",
        },
        409,
      ),
    );
    const err = await humanFetch("/v1/admin/members/member").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).code).toBe("resource_version_conflict");
    expect((err as ApiError).diagnostic).toBe("the requested change conflicts with current state");
  });

  it("parses JSON bodies and tolerates empty bodies", async () => {
    stubDocument("tm_csrf=tok");
    stubFetch(async () => jsonResponse({ agent: { agent_id: "a" } }));
    await expect(humanFetch<{ agent: { agent_id: string } }>("/v1/me/agents/a")).resolves.toEqual({
      agent: { agent_id: "a" },
    });

    stubFetch(async () => new Response(null, { status: 200 }));
    await expect(humanFetch("/v1/x", { method: "DELETE" })).resolves.toBeUndefined();
  });

  it("exposes Retry-After delay-seconds on a 429 ApiError", async () => {
    stubDocument("tm_csrf=tok");
    stubFetch(
      async () =>
        new Response(JSON.stringify({ code: "rate_limited", message: "slow down" }), {
          status: 429,
          headers: { "Content-Type": "application/json", "Retry-After": "30" },
        }),
    );
    const err = await humanFetch("/v1/me").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(429);
    expect((err as ApiError).code).toBe("rate_limited");
    expect((err as ApiError).retryAfterSeconds).toBe(30);
  });

  it("supports the HTTP-date form of Retry-After", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-22T12:00:00Z"));
    stubDocument("tm_csrf=tok");
    stubFetch(
      async () =>
        new Response("slow down", {
          status: 429,
          headers: { "Retry-After": new Date("2026-07-22T12:00:45Z").toUTCString() },
        }),
    );
    const err = await humanFetch("/v1/me").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).retryAfterSeconds).toBe(45);
  });

  it("leaves retryAfterSeconds undefined without a usable Retry-After header", async () => {
    stubDocument("tm_csrf=tok");
    stubFetch(async () => new Response("slow down", { status: 429 }));
    let err = await humanFetch("/v1/me").catch((e: unknown) => e);
    expect((err as ApiError).retryAfterSeconds).toBeUndefined();

    stubFetch(
      async () => new Response("slow down", { status: 429, headers: { "Retry-After": "soon" } }),
    );
    err = await humanFetch("/v1/me").catch((e: unknown) => e);
    expect((err as ApiError).retryAfterSeconds).toBeUndefined();
  });

  it("ignores Retry-After on non-429 responses", async () => {
    stubDocument("tm_csrf=tok");
    stubFetch(
      async () => new Response("boom", { status: 500, headers: { "Retry-After": "10" } }),
    );
    const err = await humanFetch("/v1/me").catch((e: unknown) => e);
    expect((err as ApiError).status).toBe(500);
    expect((err as ApiError).retryAfterSeconds).toBeUndefined();
  });
});
