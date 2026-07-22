// Page-level DOM smoke tests for the invitation join flow (doc section 5.3)
// covering section 10 items 2 (token hygiene across the OIDC round trip),
// 5 (uniform invalid state for every token failure), and 6 (Idempotency-Key
// reuse when an accept is retried after a lost response).

import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import {
  apiErrorResponse,
  callsTo,
  jsonResponse,
  makeMe,
  makeNoMembershipMe,
  renderApp,
  setupDomTest,
} from "./helpers";

setupDomTest();

const TOKEN = "tm_invite_inv_01.super-secret-token";
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

describe("section 10 item 2: invitation token hygiene across the OIDC round trip", () => {
  it("reads #invite=, erases the address bar, and keeps the token tab-scoped only", async () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
    const consoleWarn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const consoleLog = vi.spyOn(console, "log").mockImplementation(() => {});

    const { user } = await renderApp({
      route: `/join#invite=${TOKEN}`,
      me: null,
      fetch: () => {
        throw new Error("no API calls expected while unauthenticated");
      },
    });

    // The fragment is erased before first render and the token lives only in
    // tab-scoped sessionStorage; nothing durable ever sees it.
    expect(window.location.hash).toBe("");
    expect(window.location.href).not.toContain(TOKEN);
    expect(sessionStorage.getItem("pending_invitation")).toBe(TOKEN);
    expect(localStorage.length).toBe(0);
    // The unauthenticated branch must not render the token either.
    expect(document.body.textContent).not.toContain(TOKEN);
    // No console channel (the stand-in for analytics/error trackers here)
    // ever receives the token.
    for (const spy of [consoleError, consoleWarn, consoleLog]) {
      expect(spy.mock.calls.flat().join(" ")).not.toContain(TOKEN);
    }

    // Leaving for OIDC keeps the continuation in this tab.
    await user.click(await screen.findByRole("button", { name: "登录并继续 →" }));
    expect(sessionStorage.getItem("pending_invitation")).toBe(TOKEN);
  });

  it("resumes the accept flow after login and clears the token once accepted", async () => {
    let joined = false;
    const { fetchMock, user } = await renderApp({
      route: "/",
      // A plain return_url must NOT win over the pending invitation.
      sessionStorage: { pending_invitation: TOKEN, return_url: "/agents" },
      me: () =>
        joined
          ? makeMe({ membership_id: "mbr_02", role: "member" })
          : makeNoMembershipMe(),
      fetch: (path, init) => {
        if (path === "/v1/invitations/accept" && init.method === "POST") {
          joined = true;
          return jsonResponse(makeMe({ membership_id: "mbr_02", role: "member" }));
        }
        if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    // The continuation redirect lands on /join with the token intact.
    const acceptButton = await screen.findByRole("button", { name: "接受邀请" });
    expect(window.location.pathname).toBe("/join");
    expect(document.body.textContent).toContain(TOKEN);

    await user.click(acceptButton);

    await screen.findByRole("heading", { name: "My Agents" });
    expect(sessionStorage.getItem("pending_invitation")).toBeNull();
    const accepts = callsTo(fetchMock, "/v1/invitations/accept", "POST");
    expect(accepts).toHaveLength(1);
    expect(JSON.parse(String(accepts[0].init.body))).toEqual({ token: TOKEN });
    expect(accepts[0].headers.get("Idempotency-Key")).toMatch(UUID_RE);
    expect(accepts[0].headers.get("X-CSRF-Token")).toBe("test-csrf");
  });
});

describe("section 10 item 5: every token failure renders one uniform invalid state", () => {
  it.each([
    ["expired", "invitation expired at 2026-07-01T00:00:00Z"],
    ["revoked", "invitation revoked by admin mbr_09"],
    ["email mismatch", "oidc email carol@example.com does not match target"],
  ])("410 for a %s token shows the same state and leaks no detail", async (_kind, diagnostic) => {
    const { user } = await renderApp({
      route: `/join#invite=${TOKEN}`,
      me: makeNoMembershipMe(),
      fetch: (path) => {
        if (path === "/v1/invitations/accept") {
          return apiErrorResponse(410, "invitation_gone", diagnostic);
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await user.click(await screen.findByRole("button", { name: "接受邀请" }));

    await screen.findByText(/邀请无效（已过期 \/ 已撤销 \/ 已使用 \/ 邮箱不匹配）/);
    expect(document.body.textContent).not.toContain(diagnostic);
    expect(sessionStorage.getItem("pending_invitation")).toBeNull();
  });
});

describe("section 10 item 6: replaying invitation accept reuses the Idempotency-Key", () => {
  it("a retry after a lost response sends the same key and succeeds once", async () => {
    let joined = false;
    let attempts = 0;
    const { fetchMock, user } = await renderApp({
      route: `/join#invite=${TOKEN}`,
      me: () => (joined ? makeMe({ membership_id: "mbr_02", role: "member" }) : makeNoMembershipMe()),
      fetch: async (path, init) => {
        if (path === "/v1/invitations/accept" && init.method === "POST") {
          attempts += 1;
          if (attempts === 1) throw new TypeError("Failed to fetch");
          joined = true;
          return jsonResponse(makeMe({ membership_id: "mbr_02", role: "member" }));
        }
        if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await user.click(await screen.findByRole("button", { name: "接受邀请" }));
    // The network failure is surfaced without an automatic retry.
    await screen.findByText(/网络错误/);
    expect(callsTo(fetchMock, "/v1/invitations/accept", "POST")).toHaveLength(1);

    await user.click(await screen.findByRole("button", { name: "接受邀请" }));
    await screen.findByRole("heading", { name: "My Agents" });

    const accepts = callsTo(fetchMock, "/v1/invitations/accept", "POST");
    expect(accepts).toHaveLength(2);
    const keys = accepts.map((call) => call.headers.get("Idempotency-Key"));
    expect(keys[0]).toMatch(UUID_RE);
    expect(keys[1]).toBe(keys[0]);
    // The token travels in the JSON body, and the mutation carries CSRF.
    expect(JSON.parse(String(accepts[0].init.body))).toEqual({ token: TOKEN });
    expect(accepts[0].headers.get("X-CSRF-Token")).toBe("test-csrf");
    await waitFor(() => expect(sessionStorage.getItem("pending_invitation")).toBeNull());
  });
});
