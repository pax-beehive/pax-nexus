import { describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import {
  callsTo,
  jsonResponse,
  makeAgent,
  makeDevice,
  makeDeviceAgent,
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
    // 按列位置断言，而不是整行 textContent 子串匹配：子串匹配在机器数/
    // Agent 数两列被调换时依然会通过。列顺序见 PeopleLevel：
    // [0]=姓名+角色 [1]=email [2]=机器数 [3]=Agent 数 [4]=箭头。
    expect(alice.children[1].textContent).toBe("alice@example.com");
    expect(alice.children[2].textContent).toBe("1"); // alice: 1 台机器
    expect(alice.children[3].textContent).toBe("2"); // alice: 2 个 Agent

    const bob = (await screen.findByText("Bob")).closest(".at-row") as HTMLElement;
    expect(bob.children[1].textContent).toBe("bob@example.com");
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
    // 不只断言 URL 写对了参数：level 2 真的渲染出来了（她的机器），
    // 否则这个测试分不清「下钻生效」和「点击写了参数但什么也没画」。
    await screen.findByText("alice-macbook");
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

describe("Access tree · machines level", () => {
  it("shows the person's machines with live agent counts", async () => {
    await renderApp({
      route: "/management?person=mbr_01",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => treeFetch(path),
    });

    const row = (await screen.findByText("alice-macbook")).closest(".at-row") as HTMLElement;
    // 按列位置断言，不对整行 textContent 做子串匹配：[0]=名称+credential id
    // [1]=状态标签 [2]=provisioned_agent_count [3]=last_used [4]=箭头。
    // 子串匹配在机器数/Agent 数列被调换、或 "2" 恰好出现在别处（id、日期）
    // 时依然会通过。
    expect(row.children[0].textContent).toContain("dev_a");
    // provisioned_agent_count = 2，与级联吊销同口径
    expect(row.children[2].textContent).toBe("2");
  });

  it("groups hand-registered agents under their own heading", async () => {
    await renderApp({
      route: "/management?person=mbr_02",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => treeFetch(path),
    });

    await screen.findByText(/registered by hand/i);
    screen.getByText("bob-by-hand");
  });

  it("offers Connect a machine and Create Agent only on your own header", async () => {
    await renderApp({
      route: "/management?person=mbr_01",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => treeFetch(path),
    });

    await screen.findByRole("button", { name: /connect a machine/i });
    screen.getByRole("button", { name: /create agent/i });
  });

  it("hides both on someone else's header because the endpoint is /v1/me", async () => {
    await renderApp({
      route: "/management?person=mbr_02",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => treeFetch(path),
    });

    // "Bob" 本身在面包屑与头部都会出现（同名同文本），用 email 定位头部
    // 已经渲染，避免 findByText 因命中多个元素而报错。
    await screen.findByText(/bob@example\.com/);
    expect(screen.queryByRole("button", { name: /connect a machine/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /create agent/i })).toBeNull();
    screen.getByRole("link", { name: /change access/i });
  });

  it("walks back up through the breadcrumb", async () => {
    const { user } = await renderApp({
      route: "/management?person=mbr_01",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => treeFetch(path),
    });

    await user.click(await screen.findByRole("link", { name: "Everyone" }));

    expect(window.location.search).not.toContain("person=");
  });
});

describe("Access tree · agents level", () => {
  const deviceDetail = {
    device: makeDevice({
      credential_id: "dev_a",
      device_name: "alice-macbook",
      created_by_membership_id: "mbr_01",
      provisioned_agent_count: 2,
    }),
    agents: [
      makeDeviceAgent({ agent_id: "alice-codex", credential_id: "cred_1" }),
      makeDeviceAgent({ agent_id: "alice-claude", credential_id: "cred_2" }),
      // 已吊销的历史行：用一个与其它两行都不同的 agent_id，这样它被剔除
      // 是靠 revoked_at 过滤，而不是巧合被 dedupe-by-agent_id 吸收掉——
      // 如果这行复用了别的行的 agent_id，去掉过滤器这个测试也会照样通过。
      makeDeviceAgent({
        agent_id: "alice-retired",
        credential_id: "cred_0",
        revoked_at: "2026-07-01T00:00:00Z",
      }),
    ],
  };

  const detailFetch = (path: string) => {
    if (path === "/v1/admin/devices/dev_a") return jsonResponse(deviceDetail);
    return treeFetch(path);
  };

  it("lists the machine's live agents, excluding a revoked credential-history row", async () => {
    const { user } = await renderApp({
      route: "/management?person=mbr_01&machine=dev_a",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => detailFetch(path),
    });

    await screen.findByText("alice-codex");
    screen.getByText("alice-claude");
    // 吊销的历史行不展示——用一个独立 agent_id，所以这个查询不会被
    // dedupe 巧合救活。
    expect(screen.queryByText("alice-retired")).toBeNull();

    // 级联预览也不该带上这行：2 行，不是 3 行。
    await user.click(screen.getByRole("button", { name: /revoke this machine/i }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getAllByRole("row").length - 1).toBe(2); // 减表头
  });

  it("shows a cascade preview whose row count equals the displayed agent rows", async () => {
    const { user } = await renderApp({
      route: "/management?person=mbr_01&machine=dev_a",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => detailFetch(path),
    });

    const displayed = (await screen.findAllByText(/alice-(codex|claude)/)).length;
    await user.click(screen.getByRole("button", { name: /revoke this machine/i }));

    const dialog = await screen.findByRole("dialog");
    const previewRows = within(dialog).getAllByRole("row").length - 1; // 减表头
    expect(previewRows).toBe(displayed);
  });

  it("falls back to the machine's owner level when ?machine= is stale", async () => {
    await renderApp({
      route: "/management?person=mbr_01&machine=dev_gone",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => detailFetch(path),
    });

    await screen.findByText("alice-macbook");
    screen.getByText(/no longer exists/i);
  });

  it("still renders when the snapshot's agents leg fails — level 3 has its own getDevice call", async () => {
    // agents 腿失败只影响散装 Agent 分组这个第 2 层专属功能；第 3 层展示
    // 的 Agent 行来自 deviceDetail 自己的 getDevice，不该被这条腿拖下水，
    // 更不该把正坐在 ?machine= 上的人一路弹回根层。
    await renderApp({
      route: "/management?person=mbr_01&machine=dev_a",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => {
        if (path === "/v1/admin/devices/dev_a") return jsonResponse(deviceDetail);
        if (path.startsWith("/v1/admin/agents")) throw new Error("agents leg down");
        return treeFetch(path);
      },
    });

    await screen.findByText("alice-codex");
    screen.getByText("alice-claude");
  });
});
