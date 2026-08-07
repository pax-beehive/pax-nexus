// Page-level DOM smoke tests for invitation administration (doc section
// 5.2) covering section 10 item 4: when the create response is lost, the
// portal never auto-retries; the pending record is discoverable from the
// list and can be revoked.

import { describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import {
  callsTo,
  jsonResponse,
  makeInvitation,
  makeMe,
  renderApp,
  setupDomTest,
} from "./helpers";

setupDomTest();

describe("section 10 item 4: lost invitation create response", () => {
  it("does not retry; the pending record appears in the list and is revocable", async () => {
    let pendingVisible = false;
    const { fetchMock, user } = await renderApp({
      route: "/management/invitations",
      me: makeMe(),
      fetch: async (path, init) => {
        if (path === "/v1/admin/invitations" && init.method === "POST") {
          // The server processed the create but the response never arrived.
          pendingVisible = true;
          throw new TypeError("Failed to fetch");
        }
        if (path.startsWith("/v1/admin/invitations/") && init.method === "DELETE") {
          pendingVisible = false;
          return jsonResponse(makeInvitation({ status: "revoked" }));
        }
        if (path.startsWith("/v1/admin/invitations")) {
          return jsonResponse({
            invitations: pendingVisible ? [makeInvitation()] : [],
          });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    // Open the create modal and submit a valid invitation.
    await user.click(await screen.findByRole("button", { name: "+ Create invitation" }));
    await user.type(screen.getByLabelText("target_email"), "bob@example.com");
    await user.click(screen.getByRole("button", { name: "Create" }));

    // No blind retry of a one-time-secret creation: exactly one POST, and it
    // carries no Idempotency-Key (doc section 3.3).
    await screen.findByText(/Request failed; no automatic retry/);
    const creates = callsTo(fetchMock, "/v1/admin/invitations", "POST");
    expect(creates).toHaveLength(1);
    expect(creates[0].headers.get("Idempotency-Key")).toBeNull();
    expect(JSON.parse(String(creates[0].init.body))).toMatchObject({
      target_email: "bob@example.com",
      role: "member",
    });

    // The modal closed and the reloaded list reveals the pending record.
    expect(screen.queryByRole("heading", { name: "Create invitation" })).toBeNull();
    const row = (await screen.findByText("bob@example.com")).closest("tr");
    expect(row).not.toBeNull();

    // The duplicate can be revoked from the list.
    await user.click(within(row as HTMLElement).getByRole("button", { name: "Revoke" }));
    await screen.findByText("Invitation revoked");
    expect(callsTo(fetchMock, "/v1/admin/invitations/inv_01", "DELETE")).toHaveLength(1);
    await screen.findByText("No matching records.");
  });
});

describe("invitation created: one-time ceremony", () => {
  it("shows the ceremony without a connect-command button — invitees open the link in a browser, not a CLI", async () => {
    const { user } = await renderApp({
      route: "/management/invitations",
      me: makeMe(),
      fetch: async (path, init) => {
        if (path === "/v1/admin/invitations" && init.method === "POST") {
          return jsonResponse(makeInvitation({ token: "inv_tok_01.secret" }), 201);
        }
        if (path.startsWith("/v1/admin/invitations")) {
          return jsonResponse({ invitations: [] });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await user.click(await screen.findByRole("button", { name: "+ Create invitation" }));
    await user.type(screen.getByLabelText("target_email"), "bob@example.com");
    await user.click(screen.getByRole("button", { name: "Create" }));

    // The ceremony appears with the join URL...
    await screen.findByText("One-time invitation link · shown once, stored nowhere");
    screen.getByText("Send the link out now. We can't show it to you again.");
    // ...but carries no connect-command action: invitees follow the link in
    // a browser and log in, they never run a CLI command against it.
    expect(screen.queryByRole("button", { name: "Copy client command" })).toBeNull();
  });
});
