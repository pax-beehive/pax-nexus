// Page-level DOM smoke tests for the first-install bootstrap claim (doc
// section 5.1) covering section 10 item 3: when two browsers race to claim,
// the loser gets 409 bootstrap_closed and must not enter the Owner console.

import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
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

function renderBootstrap(options: {
  claim: (path: string, init: RequestInit) => Response | Promise<Response>;
  me?: () => ReturnType<typeof makeNoMembershipMe> | null;
}) {
  return renderApp({
    route: "/bootstrap",
    me: options.me ?? (() => makeNoMembershipMe()),
    fetch: (path, init) => {
      if (path === "/v1/bootstrap/claim" && init.method === "POST") {
        return options.claim(path, init);
      }
      if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
      if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [] });
      if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [] });
      throw new Error(`unexpected fetch: ${path}`);
    },
  });
}

describe("section 10 item 3: concurrent bootstrap claim", () => {
  it("the winner claims Owner and enters the console", async () => {
    let claimed = false;
    const { user } = await renderBootstrap({
      claim: () => {
        claimed = true;
        return jsonResponse(makeMe());
      },
      me: () => (claimed ? makeMe() : makeNoMembershipMe()),
    });

    await user.type(screen.getByLabelText("Bootstrap 密钥"), "top-secret");
    await user.click(screen.getByRole("button", { name: "认领 Owner" }));

    // The winner is genuinely Owner, and /management is admin+'s access
    // tree.
    await screen.findByRole("heading", { name: "Access flows downward" });
    expect(window.location.pathname).toBe("/management");
  });

  it("the loser sees bootstrap_closed, refreshes /v1/me, and stays out of the console", async () => {
    const { fetchMock, user } = await renderBootstrap({
      claim: () => apiErrorResponse(409, "bootstrap_closed", "bootstrap already claimed"),
    });
    const input = screen.getByLabelText("Bootstrap 密钥") as HTMLInputElement;
    await user.type(input, "top-secret");
    await user.click(screen.getByRole("button", { name: "认领 Owner" }));

    await screen.findByText("Bootstrap 已经被别人认领，或者已经关闭");

    // Never auto-retried: exactly one claim request, secret in the header
    // only, no Idempotency-Key (one-time secret creation, doc section 3.3).
    const claims = callsTo(fetchMock, "/v1/bootstrap/claim", "POST");
    expect(claims).toHaveLength(1);
    expect(claims[0].headers.get("X-PAX-Bootstrap-Secret")).toBe("top-secret");
    expect(claims[0].headers.get("Idempotency-Key")).toBeNull();
    // The secret input is cleared as soon as the request settles.
    expect(input.value).toBe("");
    // /v1/me was refreshed (boot + post-409) and the loser is still on the
    // claim page, not in the console.
    expect(callsTo(fetchMock, "/v1/me")).toHaveLength(2);
    expect(window.location.pathname).toBe("/bootstrap");
    expect(screen.getByRole("heading", { name: "认领第一个 Owner" })).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "Access flows downward" })).toBeNull();
  });
});
