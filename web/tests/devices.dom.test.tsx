// Page-level DOM tests for device-scoped agent provisioning (doc sections
// 5.9, 5.10, 8.4-8.6): device enrollment creation without a permission
// matrix, the Devices list, and the cascade-preview revocation flow.

import { describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import {
  callsTo,
  jsonResponse,
  makeDevice,
  makeDeviceAgent,
  makeMe,
  makeMember,
  renderApp,
  setupDomTest,
} from "./helpers";

setupDomTest();

const SECRET = {
  enrollment_id: "denr_01",
  token: "tm_enroll_denr_01.secret",
  expires_at: "2026-07-24T18:15:00Z",
  device_name: "todd-macbook-air",
  grantable_permissions: ["observe", "search", "get", "channel_send", "channel_receive"],
};

describe("Devices list + device enrollment creation", () => {
  it("renders the list and creates a device enrollment without a permission matrix", async () => {
    const { fetchMock, user } = await renderApp({
      route: "/admin/devices",
      me: makeMe(),
      fetch: (path, init) => {
        if (path.startsWith("/v1/admin/members")) {
          return jsonResponse({
            members: [makeMember({ membership_id: "mbr_01", email: "alice@example.com" })],
          });
        }
        if (path === "/v1/me/device-enrollments" && init.method === "POST") {
          return jsonResponse(SECRET, 201);
        }
        if (path.startsWith("/v1/admin/devices")) {
          return jsonResponse({ devices: [makeDevice()] });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    // List: device name, credential id, enriched creator, agent count, status.
    const row = (await screen.findByText("todd-macbook-air")).closest("tr");
    expect(row).not.toBeNull();
    expect(within(row as HTMLElement).getByText("dev_01")).toBeDefined();
    expect(within(row as HTMLElement).getByText("alice@example.com")).toBeDefined();
    expect(within(row as HTMLElement).getByText("2")).toBeDefined();
    expect(within(row as HTMLElement).getByText("active")).toBeDefined();

    // Open the create form: device name field, fixed agent_provision
    // permission, and no agent permission matrix (no checkboxes at all).
    await user.click(screen.getByRole("button", { name: "+ 创建 Device Enrollment" }));
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByLabelText(/device_name/)).toBeDefined();
    expect(within(dialog).getByText(/agent_provision/)).toBeDefined();
    expect(within(dialog).queryByRole("checkbox")).toBeNull();

    await user.type(within(dialog).getByLabelText(/device_name/), "todd-macbook-air");
    await user.click(within(dialog).getByRole("button", { name: "创建" }));

    // One-time secret: exactly one POST, CSRF sent, no Idempotency-Key.
    const creates = callsTo(fetchMock, "/v1/me/device-enrollments", "POST");
    expect(creates).toHaveLength(1);
    expect(creates[0].headers.get("Idempotency-Key")).toBeNull();
    expect(creates[0].headers.get("X-CSRF-Token")).toBe("test-csrf");
    expect(JSON.parse(String(creates[0].init.body))).toEqual({
      device_name: "todd-macbook-air",
      expires_in_seconds: 900,
    });

    // The one-time token renders in the SecretCard with a copy-command action.
    await screen.findByText("tm_enroll_denr_01.secret");
    expect(screen.getByRole("button", { name: "复制客户端命令" })).toBeDefined();
  });

  it("rejects an empty device_name locally without issuing a request", async () => {
    const { fetchMock, user } = await renderApp({
      route: "/admin/devices",
      me: makeMe(),
      fetch: (path) => {
        if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [] });
        if (path.startsWith("/v1/admin/devices")) return jsonResponse({ devices: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await user.click(await screen.findByRole("button", { name: "+ 创建 Device Enrollment" }));
    const dialog = screen.getByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "创建" }));

    await within(dialog).findByText(/device_name 必填/);
    expect(callsTo(fetchMock, "/v1/me/device-enrollments", "POST")).toHaveLength(0);
  });
});

describe("Device detail and cascade revocation", () => {
  it("shows the live provisioned agents and previews the cascade before revoking", async () => {
    let revoked = false;
    const aliveCodex = makeDeviceAgent();
    const aliveClaude = makeDeviceAgent({
      agent_id: "personal-claude",
      display_name: "personal-claude",
      agent_type: "claude",
      credential_id: "cred_03",
      last_used_at: undefined,
    });
    const rotatedOut = makeDeviceAgent({
      credential_id: "cred_01",
      created_at: "2026-07-24T18:01:00Z",
      revoked_at: "2026-07-24T18:05:00Z",
    });
    const detailBody = () => ({
      device: revoked
        ? makeDevice({
            status: "revoked",
            provisioned_agent_count: 0,
            revoked_at: "2026-07-24T19:00:00Z",
          })
        : makeDevice(),
      agents: revoked
        ? [
            { ...aliveCodex, revoked_at: "2026-07-24T19:00:00Z" },
            { ...aliveClaude, revoked_at: "2026-07-24T19:00:00Z" },
            rotatedOut,
          ]
        : [aliveCodex, aliveClaude, rotatedOut],
    });

    const { fetchMock, user } = await renderApp({
      route: "/admin/devices/dev_01",
      me: makeMe(),
      fetch: (path, init) => {
        if (path === "/v1/admin/devices/dev_01" && init.method === "DELETE") {
          revoked = true;
          return jsonResponse({
            device: makeDevice({
              status: "revoked",
              provisioned_agent_count: 0,
              revoked_at: "2026-07-24T19:00:00Z",
            }),
          });
        }
        if (path === "/v1/admin/devices/dev_01") return jsonResponse(detailBody());
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    // The detail lists only live rows: the rotated-out credential is hidden.
    await screen.findByText("Provisioned Agents（2）");
    expect(screen.getByRole("link", { name: "personal-codex" })).toBeDefined();
    expect(screen.getByRole("link", { name: "personal-claude" })).toBeDefined();
    expect(screen.queryByText("cred_01")).toBeNull();

    // The revoke dialog previews exactly the live credentials that will be
    // cascade-revoked, without any extra request (doc section 5.10).
    await user.click(screen.getByRole("button", { name: "吊销 Device" }));
    const dialog = screen.getByRole("dialog");
    expect(
      within(dialog).getByText(/以下 2 个 Agent Credential 将随 Device 一起被吊销/),
    ).toBeDefined();
    expect(within(dialog).getByText("cred_02")).toBeDefined();
    expect(within(dialog).getByText("cred_03")).toBeDefined();
    expect(within(dialog).queryByText("cred_01")).toBeNull();
    const fetchesBefore = callsTo(fetchMock, "/v1/admin/devices/dev_01", "GET").length;

    await user.click(within(dialog).getByRole("button", { name: "确认吊销" }));

    // Idempotency-Key only: no resource_version / If-Match on this path.
    const deletes = callsTo(fetchMock, "/v1/admin/devices/dev_01", "DELETE");
    expect(deletes).toHaveLength(1);
    expect(deletes[0].headers.get("Idempotency-Key")).not.toBeNull();
    expect(deletes[0].headers.get("If-Match")).toBeNull();
    expect(deletes[0].headers.get("X-CSRF-Token")).toBe("test-csrf");

    // After the cascade the page refetches once and shows the revoked state.
    await screen.findByText(/Device 已吊销/);
    expect(callsTo(fetchMock, "/v1/admin/devices/dev_01", "GET")).toHaveLength(fetchesBefore + 1);
    await screen.findByText("该 Device 尚未铸发仍活跃的 Agent Credential。");
    expect(screen.queryByRole("button", { name: "吊销 Device" })).toBeNull();
  });
});
