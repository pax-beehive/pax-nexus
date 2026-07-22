// Page-level DOM smoke tests for agent creation and enrollment/credential
// artifacts (doc sections 5.4 and 5.5) covering section 10 item 6
// (Idempotency-Key reuse on replayed create/revoke) and item 11 (a consumed
// enrollment leaves only metadata; no secret material reaches the DOM).

import { describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import {
  callsTo,
  jsonResponse,
  makeAgent,
  makeCredential,
  makeEnrollment,
  makeMe,
  renderApp,
  setupDomTest,
} from "./helpers";

setupDomTest();

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

function artifactHandler(overrides?: {
  enrollments?: unknown[];
  credentials?: unknown[];
}) {
  return (path: string, init: RequestInit) => {
    if (path === "/v1/me/agents/agent-1" && init.method === "GET") {
      return jsonResponse({ agent: makeAgent() });
    }
    if (path.startsWith("/v1/me/agents/agent-1/enrollments")) {
      return jsonResponse({ enrollments: overrides?.enrollments ?? [] });
    }
    if (path.startsWith("/v1/me/agents/agent-1/credentials")) {
      return jsonResponse({ credentials: overrides?.credentials ?? [] });
    }
    throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
  };
}

describe("section 10 item 6: replaying agent create reuses the Idempotency-Key", () => {
  it("a retry after a lost response sends the same key", async () => {
    let attempts = 0;
    const { fetchMock, user } = await renderApp({
      route: "/agents",
      me: makeMe(),
      fetch: async (path, init) => {
        if (path === "/v1/me/agents" && init.method === "POST") {
          attempts += 1;
          if (attempts === 1) throw new TypeError("Failed to fetch");
          return jsonResponse({ agent: makeAgent({ agent_id: "agent-9", display_name: "Agent Nine" }) });
        }
        if (path === "/v1/me/agents" || path.startsWith("/v1/me/agents?")) {
          return jsonResponse({ agents: [] });
        }
        if (path === "/v1/me/agents/agent-9" && init.method === "GET") {
          return jsonResponse({ agent: makeAgent({ agent_id: "agent-9", display_name: "Agent Nine" }) });
        }
        if (path.startsWith("/v1/me/agents/agent-9/enrollments")) {
          return jsonResponse({ enrollments: [] });
        }
        if (path.startsWith("/v1/me/agents/agent-9/credentials")) {
          return jsonResponse({ credentials: [] });
        }
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });

    await user.click(await screen.findByRole("button", { name: "+ Create Agent" }));
    await user.type(screen.getByLabelText(/agent_id/), "agent-9");
    await user.type(screen.getByLabelText("display_name"), "Agent Nine");
    await user.click(screen.getByRole("button", { name: "创建" }));

    // Network failure surfaces without an automatic retry.
    await screen.findByText(/网络错误/);
    expect(callsTo(fetchMock, "/v1/me/agents", "POST")).toHaveLength(1);

    await user.click(screen.getByRole("button", { name: "创建" }));
    await screen.findByText("Agent 已创建");

    const creates = callsTo(fetchMock, "/v1/me/agents", "POST");
    expect(creates).toHaveLength(2);
    const keys = creates.map((call) => call.headers.get("Idempotency-Key"));
    expect(keys[0]).toMatch(UUID_RE);
    expect(keys[1]).toBe(keys[0]);
    // The explicit directory_visible choice is always sent (doc 5.4).
    expect(JSON.parse(String(creates[0].init.body))).toMatchObject({
      agent_id: "agent-9",
      directory_visible: true,
    });
  });
});

describe("section 10 item 6: replaying a credential revoke reuses the Idempotency-Key", () => {
  it("a retry after a lost response sends the same key", async () => {
    let revoked = false;
    let attempts = 0;
    const base = artifactHandler({ credentials: [makeCredential()] });
    const { fetchMock, user } = await renderApp({
      route: "/agents/agent-1",
      me: makeMe(),
      fetch: async (path, init) => {
        if (path === "/v1/me/agents/agent-1/credentials/cred_01" && init.method === "DELETE") {
          attempts += 1;
          if (attempts === 1) throw new TypeError("Failed to fetch");
          revoked = true;
          return jsonResponse({ credential: makeCredential({ revoked_at: "2026-07-22T00:00:00Z" }) });
        }
        if (path.startsWith("/v1/me/agents/agent-1/credentials") && revoked) {
          return jsonResponse({ credentials: [] });
        }
        return base(path, init);
      },
    });

    // Open the revoke confirmation for the active credential.
    const row = (await screen.findByText("Alice MacBook")).closest("tr") as HTMLElement;
    await user.click(within(row).getByRole("button", { name: "吊销" }));
    await screen.findByRole("heading", { name: "吊销 Credential" });

    await user.click(screen.getByRole("button", { name: "确认吊销" }));
    await screen.findByText(/网络错误/);
    expect(callsTo(fetchMock, "/v1/me/agents/agent-1/credentials/cred_01", "DELETE")).toHaveLength(1);

    await user.click(screen.getByRole("button", { name: "确认吊销" }));
    await screen.findByText("Credential 已吊销，对应 API key 立即失效");

    const deletes = callsTo(fetchMock, "/v1/me/agents/agent-1/credentials/cred_01", "DELETE");
    expect(deletes).toHaveLength(2);
    const keys = deletes.map((call) => call.headers.get("Idempotency-Key"));
    expect(keys[0]).toMatch(UUID_RE);
    expect(keys[1]).toBe(keys[0]);
  });
});

describe("section 10 item 11: a consumed enrollment leaves only metadata", () => {
  it("renders credential metadata without any secret material in the DOM", async () => {
    await renderApp({
      route: "/agents/agent-1",
      me: makeMe(),
      fetch: artifactHandler({
        enrollments: [makeEnrollment({ status: "consumed" })],
        credentials: [makeCredential()],
      }),
    });

    // Enrollment and credential metadata render normally (both rows carry
    // the same label, so assert per row).
    const rows = (await screen.findAllByText("Alice MacBook")).map(
      (el) => el.closest("tr") as HTMLElement,
    );
    expect(rows).toHaveLength(2);
    expect(rows.some((row) => within(row).queryByText("consumed") !== null)).toBe(true);
    expect(rows.some((row) => within(row).queryByText("active") !== null)).toBe(true);
    screen.getByRole("heading", { name: "Credentials（仅元数据，永不含 API key）" });
    expect(screen.getAllByText("observe, search").length).toBe(2);

    // No one-time-secret card is mounted, and nothing key-shaped leaks into
    // the document: after consumption the portal only ever sees metadata.
    expect(document.querySelector(".secret-val")).toBeNull();
    expect(document.querySelector(".secret-card")).toBeNull();
    expect(document.body.textContent).not.toMatch(/tm_(enroll|agent|cred)_[\w.-]*secret[\w.-]*/i);
    expect(document.body.textContent).not.toContain("api_key");
  });
});
