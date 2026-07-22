// Page-level DOM smoke tests for member administration (doc section 5.6)
// covering section 10 items 7 (only resource_version_conflict refetches),
// 8 (last_active_owner prompt comes from the server, not the loaded page),
// and 9 (a mid-session 401 drops cached identity and unmounts member data).

import { describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import type { UserEvent } from "@testing-library/user-event";
import {
  apiErrorResponse,
  callsTo,
  jsonResponse,
  makeMe,
  makeMember,
  renderApp,
  setupDomTest,
  type FetchHandler,
} from "./helpers";

setupDomTest();

const SELF = makeMember({
  membership_id: "mbr_01",
  user_id: "usr_01",
  email: "alice@example.com",
  role: "owner",
  resource_version: 5,
});
const BOB = makeMember({ membership_id: "mbr_02", email: "bob@example.com", resource_version: 3 });

function membersHandler(patch: Response): FetchHandler {
  return (path, init) => {
    if (path === "/v1/admin/members" && init.method === "GET") {
      return jsonResponse({ members: [SELF, BOB] });
    }
    if (path.startsWith("/v1/admin/members/") && init.method === "PATCH") return patch;
    if (path === "/v1/admin/members/mbr_02" && init.method === "GET") {
      return jsonResponse({ member: makeMember({ ...BOB, resource_version: 4, role: "admin" }) });
    }
    throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
  };
}

async function openBobEditor(user: UserEvent) {
  const row = (await screen.findByText("bob@example.com")).closest("tr") as HTMLElement;
  await user.click(within(row).getByRole("button", { name: "编辑" }));
  await screen.findByRole("heading", { name: "编辑 bob@example.com" });
}

describe("section 10 item 7: concurrent member edits", () => {
  it("resource_version_conflict refetches the member and refreshes the list", async () => {
    const { fetchMock, user } = await renderApp({
      route: "/admin/members",
      me: makeMe(),
      fetch: membersHandler(apiErrorResponse(409, "resource_version_conflict", "stale version")),
    });
    await openBobEditor(user);
    await user.selectOptions(screen.getByLabelText("role"), "admin");
    await user.click(screen.getByRole("button", { name: "保存" }));

    await screen.findByText("已被他人修改，已刷新最新数据");
    // The stale write carried the old version in body and If-Match.
    const patches = callsTo(fetchMock, "/v1/admin/members/mbr_02", "PATCH");
    expect(patches).toHaveLength(1);
    expect(patches[0].headers.get("If-Match")).toBe('"3"');
    expect(JSON.parse(String(patches[0].init.body))).toMatchObject({
      role: "admin",
      resource_version: 3,
    });
    // Exactly this code refetches the single member; the modal closes via
    // onSaved and the list reloads.
    expect(callsTo(fetchMock, "/v1/admin/members/mbr_02", "GET")).toHaveLength(1);
    expect(screen.queryByRole("heading", { name: "编辑 bob@example.com" })).toBeNull();
    const listGets = callsTo(fetchMock, "/v1/admin/members", "GET").filter(
      (call) => call.path === "/v1/admin/members",
    );
    expect(listGets.length).toBeGreaterThanOrEqual(2);
  });

  it("other 409 codes never refetch the member", async () => {
    const { fetchMock, user } = await renderApp({
      route: "/admin/members",
      me: makeMe(),
      fetch: membersHandler(apiErrorResponse(409, "membership_conflict", "generic conflict")),
    });
    await openBobEditor(user);
    await user.click(screen.getByRole("button", { name: "保存" }));

    await screen.findByText("数据已被他人修改，请刷新后重试");
    expect(callsTo(fetchMock, "/v1/admin/members/mbr_02", "GET")).toHaveLength(0);
  });
});

describe("section 10 item 8: last active owner protection", () => {
  it("keeps the form and shows the server-side last_active_owner prompt", async () => {
    const { fetchMock, user } = await renderApp({
      route: "/admin/members",
      me: makeMe(),
      fetch: membersHandler(
        apiErrorResponse(409, "last_active_owner", "would remove the last active owner"),
      ),
    });
    // The page documents that the constraint is server-authoritative; the
    // portal never guesses it from the loaded page.
    screen.getByText("last_active_owner");

    const row = screen
      .getAllByText(/alice@example.com/)
      .map((el) => el.closest("tr"))
      .find((tr) => tr !== null) as HTMLElement;
    await user.click(within(row).getByRole("button", { name: "编辑" }));
    await screen.findByRole("heading", { name: "编辑 alice@example.com" });
    await user.selectOptions(screen.getByLabelText("role"), "admin");
    await user.click(screen.getByRole("button", { name: "保存" }));

    await screen.findByText("必须先提升另一位 active Owner");
    // No refetch (only resource_version_conflict refetches) and the draft
    // stays editable in place.
    expect(callsTo(fetchMock, "/v1/admin/members/mbr_01", "GET")).toHaveLength(0);
    expect(screen.getByRole("heading", { name: "编辑 alice@example.com" })).toBeTruthy();
    expect((screen.getByLabelText("role") as HTMLSelectElement).value).toBe("admin");
  });
});

describe("section 10 item 9: membership suspended or removed mid-session", () => {
  it("a 401 on the next request drops the cached identity and unmounts member data", async () => {
    await renderApp({
      route: "/admin/members",
      me: makeMe(),
      fetch: (path) => {
        if (path.startsWith("/v1/admin/members")) {
          return apiErrorResponse(401, "unauthorized", "session revoked");
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    // The route guard takes over: login page replaces the console, and the
    // member list (sensitive cached data) is gone from the document.
    await screen.findByRole("button", { name: /Continue with OIDC/ });
    expect(screen.queryByText("bob@example.com")).toBeNull();
    expect(screen.queryByRole("heading", { name: "Members" })).toBeNull();
  });
});
