import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import {
  callsTo,
  jsonResponse,
  makeAgent,
  makeDevice,
  makeMe,
  makeMember,
  renderApp,
  setupDomTest,
} from "./helpers";

setupDomTest();

/** 两个人：alice（自己，1 台机器 2 个 Agent）、bob（无机器 1 个散装 Agent）。 */
function treeFetch(path: string) {
  if (path.startsWith("/v1/admin/members")) {
    return jsonResponse({
      members: [
        makeMember({ membership_id: "mbr_01", display_name: "Alice", role: "owner", email: "alice@example.com" }),
        makeMember({ membership_id: "mbr_02", display_name: "Bob", role: "member", email: "bob@example.com" }),
      ],
    });
  }
  if (path.startsWith("/v1/admin/devices")) {
    return jsonResponse({
      devices: [
        makeDevice({ credential_id: "dev_a", device_name: "alice-macbook", created_by_membership_id: "mbr_01", provisioned_agent_count: 2 }),
      ],
    });
  }
  if (path.startsWith("/v1/admin/agents")) {
    return jsonResponse({
      agents: [
        makeAgent({ agent_id: "alice-codex", owner_membership_id: "mbr_01", provisioned_by: "dev_a" }),
        makeAgent({ agent_id: "alice-claude", owner_membership_id: "mbr_01", provisioned_by: "dev_a" }),
        makeAgent({ agent_id: "bob-by-hand", owner_membership_id: "mbr_02" }),
      ],
    });
  }
  throw new Error(`unexpected fetch: ${path}`);
}

describe("Access tree · people level", () => {
  it("lists every person with their machine and agent counts", async () => {
    await renderApp({ route: "/management", me: makeMe(), fetch: (path) => treeFetch(path) });

    const alice = (await screen.findByText("Alice")).closest(".at-row") as HTMLElement;
    expect(alice.textContent).toContain("alice@example.com");
    // alice: 1 台机器、2 个 Agent
    expect(alice.textContent).toContain("1");
    expect(alice.textContent).toContain("2");

    const bob = (await screen.findByText("Bob")).closest(".at-row") as HTMLElement;
    expect(bob.textContent).toContain("bob@example.com");
  });

  it("renders the summary strip from the same snapshot", async () => {
    await renderApp({ route: "/management", me: makeMe(), fetch: (path) => treeFetch(path) });

    await screen.findByText("1 owner · 0 admins · 1 member");
    screen.getByText("1 connected · 0 revoked · 1 person has no machine");
  });

  it("falls back to the root and explains why when ?person= is stale", async () => {
    await renderApp({
      route: "/management?person=mbr_gone",
      me: makeMe(),
      fetch: (path) => treeFetch(path),
    });

    // 回到根层（两个人都在），并说明发生了什么——不静默重置。
    await screen.findByText("Alice");
    screen.getByText(/no longer on this team/i);
  });

  it("drills into a person by writing the query parameter", async () => {
    const { user } = await renderApp({
      route: "/management",
      me: makeMe(),
      fetch: (path) => treeFetch(path),
    });

    await user.click(await screen.findByText("Alice"));

    expect(window.location.search).toContain("person=mbr_01");
  });

  it("shows a retryable error when the members leg fails", async () => {
    await renderApp({
      route: "/management",
      me: makeMe(),
      fetch: (path) => {
        if (path.startsWith("/v1/admin/members")) throw new Error("members down");
        return treeFetch(path);
      },
    });

    await screen.findByRole("button", { name: /retry/i });
  });

  it("still lists people when only the devices leg fails, with — for that column", async () => {
    await renderApp({
      route: "/management",
      me: makeMe(),
      fetch: (path) => {
        if (path.startsWith("/v1/admin/devices")) throw new Error("devices down");
        return treeFetch(path);
      },
    });

    // 人还在（脊柱没断），机器数变成 —，而不是 0，也不是整页白屏。
    const alice = (await screen.findByText("Alice")).closest(".at-row") as HTMLElement;
    expect(alice.textContent).toContain("—");
    expect(screen.getAllByText("Could not be loaded").length).toBeGreaterThan(0);
  });
});

describe("Access tree · member fork", () => {
  it("fires no admin request at all on the member fork", async () => {
    // member 没有读 /v1/admin/* 的权限：这个分叉必须完全不碰 useAccessSnapshot，
    // 而不是「调用了但拿到 403，被 Promise.allSettled 吞掉」。fetch 桩对任何
    // /v1/admin/ 路径直接抛错——如果分叉在数据 hook 之后才发生，页面会崩溃
    // 或者静默丢三个失败请求，这个测试要能抓到两种情况。
    const { fetchMock } = await renderApp({
      route: "/management",
      me: makeMe({ role: "member" }),
      fetch: (path) => {
        if (path.startsWith("/v1/admin/")) throw new Error(`unexpected admin request: ${path}`);
        if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "My Agents" });
    expect(callsTo(fetchMock, "/v1/admin/")).toHaveLength(0);
  });
});
