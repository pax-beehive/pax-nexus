import { describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import {
  apiErrorResponse,
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

/** 第 3 层 GET /v1/admin/devices/dev_a 的详情体（alice-macbook）。 */
const ALICE_DEVICE_DETAIL = {
  device: makeDevice({ credential_id: "dev_a", device_name: "alice-macbook", created_by_membership_id: "mbr_01", provisioned_agent_count: 2 }),
  agents: [
    makeDeviceAgent({ agent_id: "alice-codex", credential_id: "cred_1" }),
    makeDeviceAgent({ agent_id: "alice-claude", credential_id: "cred_2" }),
  ],
};

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
  // 详情分支必须排在列表分支**前面**：`/v1/admin/devices` 是
  // `/v1/admin/devices/<id>` 的前缀，反过来排的话第 3 层的 getDevice 会拿到
  // 列表体，detail.device 变成 undefined —— 桩坏了，却让生产代码看起来需要
  // 对 `device` 做可选链防御（复审：那层防御除了这份坏桩什么也没在挡）。
  if (path.startsWith("/v1/admin/devices/")) {
    const credentialId = path.slice("/v1/admin/devices/".length);
    if (credentialId === "dev_a") return jsonResponse(ALICE_DEVICE_DETAIL);
    // 只有 dev_a 存在；其余 id（dev_gone 等）与服务端一样答 404。
    return apiErrorResponse(404, "not_found", `no such device: ${credentialId}`);
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

  // Final review, small fix: `params.get("person") ?? undefined` keeps the
  // empty string a truncated link leaves behind, and "" matches no member —
  // so the page claimed the person had left the team. `|| undefined` folds
  // "" back into "no selection".
  it("treats a truncated ?person= as no selection rather than a departed person", async () => {
    await renderApp({ route: "/management?person=", me: makeMe(), fetch: (path) => treeFetch(path) });

    await screen.findByText("Alice");
    expect(screen.queryByText(/no longer on this team/i)).toBeNull();
  });

  it("fires no device-detail request for a truncated ?machine=", async () => {
    // Same `?? undefined` bug on the machine leg. Level 3 never rendered
    // (the "" is falsy at the branch), so it was invisible on screen — but
    // useDeviceDetail still saw a defined credentialId and fired
    // GET /v1/admin/devices/ for a machine that does not exist.
    const { fetchMock } = await renderApp({
      route: "/management?person=mbr_01&machine=",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => treeFetch(path),
    });

    await screen.findByText("alice-macbook");
    expect(callsTo(fetchMock, "/v1/admin/devices/")).toHaveLength(0);
  });

  /**
   * Re-review of the fix wave, finding 2. `?machine=` without `?person=`
   * cannot draw level 3 — a machine always hangs off a person — so it lands
   * on the root. It did so silently (`stalePerson` is false: nobody was
   * asked for), *and* still fired a GET /v1/admin/devices/<id> whose answer
   * was thrown away. The app never writes such a URL itself; it only comes
   * from a hand-edited or truncated link.
   */
  it("explains the fall to the root when ?machine= has no ?person=", async () => {
    const { fetchMock } = await renderApp({
      route: "/management?machine=dev_a",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => treeFetch(path),
    });

    await screen.findByText("Alice");
    screen.getByText(/names a machine but not the person/i);
    // …and no admin request was spent on a machine we can never render.
    expect(callsTo(fetchMock, "/v1/admin/devices/")).toHaveLength(0);
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
  it("renders the member's own agents with a create entry", async () => {
    await renderApp({
      route: "/management",
      me: makeMe({ role: "member", membership_id: "mbr_02" }),
      fetch: (path) => {
        if (path.startsWith("/v1/me/agents")) {
          return jsonResponse({ agents: [makeAgent({ agent_id: "bob-by-hand" })] });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByText("bob-by-hand");
    screen.getByRole("button", { name: /create agent/i });
  });

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

describe("Access tree · owner-machine self-registration", () => {
  it("keeps a self-registration entry reachable for an owner", async () => {
    // app-shell.dom.test.tsx 的护栏在树上的对应路径：
    // owner 从人员层点自己 → 本人机器层有 + Create Agent。
    const { user } = await renderApp({
      route: "/management",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => treeFetch(path),
    });

    await user.click(await screen.findByText("Alice"));

    await screen.findByRole("button", { name: /create agent/i });
  });
});

/**
 * Fix round 1 (review of Task 9): the one-time enrollment secret and the
 * two own-machine modal flags all lived in AdminAccessTree, which stays
 * mounted across every in-tree navigation (only `?person=`/`?machine=`
 * change — the route component never remounts). Nothing bound that state
 * to the tree coordinates that produced it, so the secret could render
 * over a *different* person's machines, or reappear after drilling into a
 * machine and back. These cases pin the fix: the secret only shows on the
 * surface that created it, disappears the moment the coordinates change,
 * and survives a failed post-create snapshot refetch (the server-side
 * enrollment already exists at that point; losing the token from state
 * leaves revoke-and-recreate as the only way to see it again).
 */
const ENROLLMENT_SECRET = {
  enrollment_id: "denr_01",
  token: "tm_enroll_denr_01.secret",
  expires_at: "2026-07-24T18:15:00Z",
  device_name: "todd-macbook-air",
  grantable_permissions: ["observe", "search", "get", "channel_send", "channel_receive"],
};

function enrollmentFetch(path: string, init: RequestInit) {
  if (path === "/v1/me/device-enrollments" && init.method === "POST") {
    return jsonResponse(ENROLLMENT_SECRET, 201);
  }
  // 第 3 层的 GET /v1/admin/devices/dev_a（drill-in-and-back 用例要走进去）
  // 由 treeFetch 的详情分支答，不再在这里重复一份。
  return treeFetch(path);
}

/** Own machine level (mbr_01 = Alice = the caller) → open, fill, submit
    Connect a machine, and wait for the one-time secret to render. */
async function createEnrollmentOnOwnMachineLevel(fetch = enrollmentFetch) {
  const rendered = await renderApp({
    route: "/management?person=mbr_01",
    me: makeMe({ membership_id: "mbr_01" }),
    fetch,
  });
  const { user } = rendered;

  await user.click(await screen.findByRole("button", { name: /connect a machine/i }));
  const dialog = screen.getByRole("dialog");
  await user.type(within(dialog).getByLabelText(/device_name/), "todd-macbook-air");
  await user.click(within(dialog).getByRole("button", { name: "Create" }));

  await screen.findByText(ENROLLMENT_SECRET.token);
  return rendered;
}

describe("Access tree · own-machine enrollment secret lifecycle", () => {
  it("shows the one-time secret after creating an enrollment on your own machine level", async () => {
    await createEnrollmentOnOwnMachineLevel();

    screen.getByText("One-time Device Enrollment token (shown only once)");
  });

  it("clears the secret when navigating to a different person", async () => {
    const { user } = await createEnrollmentOnOwnMachineLevel();

    await user.click(screen.getByRole("link", { name: "Everyone" }));
    await user.click(await screen.findByText("Bob"));

    // Bob's header rendered (email is a unique anchor for it — "Bob" itself
    // also appears in the breadcrumb).
    await screen.findByText(/bob@example\.com/);
    expect(screen.queryByText(ENROLLMENT_SECRET.token)).toBeNull();
  });

  it("does not resurface the secret after drilling into a machine and back", async () => {
    const { user } = await createEnrollmentOnOwnMachineLevel();

    await user.click(await screen.findByText("alice-macbook"));
    await screen.findByText("alice-codex"); // level 3 rendered
    expect(screen.queryByText(ENROLLMENT_SECRET.token)).toBeNull();

    await user.click(screen.getByRole("link", { name: "Alice" }));
    await screen.findByText("alice-macbook"); // back on level 2
    expect(screen.queryByText(ENROLLMENT_SECRET.token)).toBeNull();
  });

  it("keeps the secret visible even when the post-create snapshot refetch fails", async () => {
    // Deliberately not using createEnrollmentOnOwnMachineLevel here: that
    // helper's own success check (findByText the token right after create)
    // would otherwise become the failure point, one step before the thing
    // this case actually pins down — that the token survives the *next*
    // (failing) snapshot refetch, not just the moment it's minted.
    let enrolled = false;
    const { user } = await renderApp({
      route: "/management?person=mbr_01",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path, init) => {
        if (path === "/v1/me/device-enrollments" && init.method === "POST") {
          enrolled = true;
          return jsonResponse(ENROLLMENT_SECRET, 201);
        }
        // The retry triggered by a successful create re-fetches all three
        // legs; make the spine leg fail so the page would normally flip to
        // the full error card.
        if (enrolled && path.startsWith("/v1/admin/members")) {
          throw new Error("members down after enrollment");
        }
        return treeFetch(path);
      },
    });

    await user.click(await screen.findByRole("button", { name: /connect a machine/i }));
    const dialog = screen.getByRole("dialog");
    await user.type(within(dialog).getByLabelText(/device_name/), "todd-macbook-air");
    await user.click(within(dialog).getByRole("button", { name: "Create" }));

    // The refetch failed (spine leg down): the page shows the retryable
    // error card, but the secret — already in state before the refetch
    // ever started — must still be on screen above it.
    await screen.findByRole("button", { name: /retry/i });
    expect(screen.getByText(ENROLLMENT_SECRET.token)).toBeTruthy();
  });

  // Finding I2: createAgentOpen (and connectMachineOpen) had the same
  // unscoped lifetime as the secret — a tree-coordinate change while a
  // modal was open didn't close it, so it re-materialised over whatever
  // level came next. Covers the modal-open flags with the same probe shape
  // used for the secret above (navigate away, assert it's gone).
  it("closes an open Create Agent modal when the tree coordinates change under it", async () => {
    const { user } = await renderApp({
      route: "/management?person=mbr_01",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => treeFetch(path),
    });

    await user.click(await screen.findByRole("button", { name: /create agent/i }));
    screen.getByRole("dialog", { name: "Create Agent" });

    // The dialog has no backdrop-blocking semantics in jsdom, so this
    // stands in for "Back navigates while the modal is still open" (the
    // reviewer's probe) without needing to drive real browser history.
    await user.click(screen.getByRole("link", { name: "Everyone" }));
    await user.click(await screen.findByText("Bob"));

    await screen.findByText(/bob@example\.com/);
    expect(screen.queryByRole("dialog")).toBeNull();
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

  /**
   * Final whole-branch review, finding 1. useDeviceDetail(undefined) settles
   * to {status:"ready", detail:undefined} while the user sits on level 2.
   * Clicking a machine changes `?machine=` and re-renders *before* the
   * passive effect flips the hook back to "loading", so that first commit
   * fell past the loading branch into `!detail` and inserted the red
   * role="alert" card — announced by assistive tech, and the one window in
   * which device B's header could pair with device A's cascade array.
   *
   * An end-state assertion cannot see this: the card is replaced on the very
   * next commit, so `queryByText(...)` afterwards is null either way. The
   * probe therefore watches the *commit sequence* with a MutationObserver
   * and fails if the error card was ever inserted at all.
   */
  it("never commits an error card at any point while drilling into a machine", async () => {
    const { user } = await renderApp({
      route: "/management?person=mbr_01",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => detailFetch(path),
    });

    await screen.findByText("alice-macbook");

    const offending: string[] = [];
    const inspect = (records: MutationRecord[]) => {
      for (const mutation of records) {
        for (const node of mutation.addedNodes) {
          if (!(node instanceof HTMLElement)) continue;
          const alerted =
            node.matches('[role="alert"]') || node.querySelector('[role="alert"]') !== null;
          const errorCopy = /could not load this machine/i.test(node.textContent ?? "");
          if (alerted || errorCopy) offending.push(node.textContent ?? "");
        }
      }
    };
    const observer = new MutationObserver(inspect);
    observer.observe(document.body, { childList: true, subtree: true });

    await user.click(screen.getByText("alice-macbook"));
    await screen.findByText("alice-codex"); // level 3 really arrived

    inspect(observer.takeRecords());
    observer.disconnect();

    expect(offending).toEqual([]);
  });

  /**
   * Final review, finding 3. Both level-3 non-success returns were bare —
   * no page head, no summary strip, no breadcrumbs — so a user whose
   * getDevice call fails saw a red box with no in-page way back to the
   * person. Design §6: 第 3 层详情取数失败：该层报错可重试，面包屑与上层仍可用。
   */
  it("keeps the breadcrumbs while the machine's detail is still loading", async () => {
    let release: ((response: Response) => void) | undefined;
    const pending = new Promise<Response>((resolve) => {
      release = resolve;
    });

    await renderApp({
      route: "/management?person=mbr_01&machine=dev_a",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => (path === "/v1/admin/devices/dev_a" ? pending : treeFetch(path)),
    });

    const crumbs = await screen.findByRole("navigation", { name: "Breadcrumb" });
    expect(within(crumbs).getByRole("link", { name: "Everyone" })).toBeTruthy();
    expect(within(crumbs).getByRole("link", { name: "Alice" })).toBeTruthy();
    expect(crumbs.textContent).toContain("alice-macbook");

    release?.(jsonResponse(deviceDetail));
    await screen.findByText("alice-codex");
  });

  it("keeps the breadcrumbs, and the way back up, when the machine's detail fails", async () => {
    await renderApp({
      route: "/management?person=mbr_01&machine=dev_a",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => {
        if (path === "/v1/admin/devices/dev_a") throw new Error("detail down");
        return treeFetch(path);
      },
    });

    await screen.findByText(/could not load this machine/i);
    screen.getByRole("button", { name: /retry/i });

    const crumbs = screen.getByRole("navigation", { name: "Breadcrumb" });
    expect(within(crumbs).getByRole("link", { name: "Everyone" })).toBeTruthy();
    expect(within(crumbs).getByRole("link", { name: "Alice" })).toBeTruthy();
    expect(crumbs.textContent).toContain("alice-macbook");
  });

  /**
   * Re-review of the fix wave, finding 1. useDeviceDetail folded two very
   * different things into "loading": a frame that is genuinely one render
   * behind (self-heals on the next commit) and a response whose body is not
   * the machine we asked for (never heals). The second one settled as
   * `ready` internally but was reported as `loading` forever — level 3's
   * loading branch has no error and no retry, so an API-contract break
   * presented as a permanent spinner that no test could see.
   *
   * These two cases pin the split: a mismatched or malformed detail body is
   * an `error`, which level 3 already renders as a retryable card.
   */
  it("surfaces a retryable error when the detail body names a different machine", async () => {
    const otherMachineDetail = {
      device: makeDevice({
        credential_id: "dev_b",
        device_name: "bob-thinkpad",
        created_by_membership_id: "mbr_02",
      }),
      agents: [makeDeviceAgent({ agent_id: "bob-codex", credential_id: "cred_9" })],
    };

    await renderApp({
      route: "/management?person=mbr_01&machine=dev_a",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) =>
        path === "/v1/admin/devices/dev_a" ? jsonResponse(otherMachineDetail) : treeFetch(path),
    });

    await screen.findByText(/could not load this machine/i);
    screen.getByRole("button", { name: /retry/i });
    // Not a spinner, and above all not the other machine's agent rows —
    // the header would have been dev_a's while the list was dev_b's.
    expect(screen.queryByText(/loading this machine/i)).toBeNull();
    expect(screen.queryByText("bob-codex")).toBeNull();
  });

  it("surfaces a retryable error when the detail body has no device at all", async () => {
    // Same contract break, degenerate shape: `device` is required by the
    // type, so a body without it can only come from a server that broke the
    // contract. It must not crash the page and must not spin forever.
    await renderApp({
      route: "/management?person=mbr_01&machine=dev_a",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) =>
        path === "/v1/admin/devices/dev_a" ? jsonResponse({ agents: [] }) : treeFetch(path),
    });

    await screen.findByText(/could not load this machine/i);
    screen.getByRole("button", { name: /retry/i });
    expect(screen.queryByText(/loading this machine/i)).toBeNull();
  });

  it("recovers into the real level 3 when the in-level retry succeeds", async () => {
    // The retry button is only worth keeping if it re-runs getDevice; nothing
    // exercised it before (Task 4 recorded that as deferred).
    let attempt = 0;
    const { user } = await renderApp({
      route: "/management?person=mbr_01&machine=dev_a",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => {
        if (path === "/v1/admin/devices/dev_a") {
          attempt += 1;
          if (attempt === 1) throw new Error("detail down");
          return jsonResponse(deviceDetail);
        }
        return treeFetch(path);
      },
    });

    await screen.findByText(/could not load this machine/i);
    await user.click(screen.getByRole("button", { name: /retry/i }));

    await screen.findByText("alice-codex");
    expect(screen.queryByText(/could not load this machine/i)).toBeNull();
  });
});

/**
 * Re-review of the fix wave, finding 3. The fix wave gave every *level*
 * state its chrome via AccessChrome, but the two snapshot-level returns
 * (loading / members-leg error) predate that and stayed bare. So pressing
 * Retry inside the devices-failure level blanked the page head, the summary
 * strip and the breadcrumbs for the whole duration of the refetch, and a
 * members-leg failure left a red box floating on an otherwise empty page.
 */
describe("Access tree · chrome around the snapshot's own states", () => {
  it("keeps the page chrome while the snapshot is being refetched", async () => {
    let membersAttempt = 0;
    let release: ((response: Response) => void) | undefined;
    const pending = new Promise<Response>((resolve) => {
      release = resolve;
    });

    const { user } = await renderApp({
      route: "/management?person=mbr_01",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => {
        if (path.startsWith("/v1/admin/members")) {
          membersAttempt += 1;
          // First load succeeds; the Retry-triggered refetch hangs, which
          // parks the page in the snapshot's own loading state.
          return membersAttempt === 1 ? treeFetch(path) : pending;
        }
        if (path.startsWith("/v1/admin/devices")) throw new Error("devices down");
        return treeFetch(path);
      },
    });

    await screen.findByText(/could not load alice/i);
    await user.click(screen.getByRole("button", { name: /retry/i }));

    // Mid-refetch: still a page, not a blank one.
    const crumbs = await screen.findByRole("navigation", { name: "Breadcrumb" });
    expect(crumbs.textContent).toContain("Everyone");
    expect(screen.getByRole("heading", { name: "Access flows downward" })).toBeTruthy();
    // The summary tiles degrade to — rather than to 0 or to nothing.
    expect(screen.getAllByText("—").length).toBeGreaterThan(0);

    release?.(treeFetch("/v1/admin/members"));
    await screen.findByText(/could not load alice/i);
  });

  it("keeps the page chrome when the members leg itself fails", async () => {
    await renderApp({
      route: "/management",
      me: makeMe(),
      fetch: (path) => {
        if (path.startsWith("/v1/admin/members")) throw new Error("members down");
        return treeFetch(path);
      },
    });

    await screen.findByText(/could not load the team/i);
    screen.getByRole("button", { name: /retry/i });
    const crumbs = screen.getByRole("navigation", { name: "Breadcrumb" });
    expect(crumbs.textContent).toContain("Everyone");
    expect(screen.getByRole("heading", { name: "Access flows downward" })).toBeTruthy();
  });
});

/**
 * Final review, finding 2. Level 2 and level 3 both required the snapshot's
 * `devices` leg; when it failed, execution fell through to the *root* render
 * — which explained nothing, because `stalePerson` is false (the person was
 * found). The URL said level 2, the page drew level 1, and clicking the same
 * person again did nothing because setParams wrote an identical URL. Design
 * §6 forbids exactly this: a non-spine leg failure reports in its own level
 * with its own retry, and any fallback must explain itself.
 */
describe("Access tree · degraded legs inside a person's level", () => {
  it("reports a failed devices leg inside the person's level instead of dropping to the root", async () => {
    await renderApp({
      route: "/management?person=mbr_01",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => {
        if (path.startsWith("/v1/admin/devices")) throw new Error("devices down");
        return treeFetch(path);
      },
    });

    await screen.findByText(/could not load alice/i);
    screen.getByRole("button", { name: /retry/i });

    // Still on the person's level: the breadcrumb names her, and the root
    // level's people rows (Bob only ever appears there) are not on screen.
    const crumbs = screen.getByRole("navigation", { name: "Breadcrumb" });
    expect(crumbs.textContent).toContain("Alice");
    expect(within(crumbs).getByRole("link", { name: "Everyone" })).toBeTruthy();
    expect(screen.queryByText("bob@example.com")).toBeNull();
    expect(window.location.search).toContain("person=mbr_01");
  });

  it("reports a failed devices leg inside the level even with ?machine= set", async () => {
    await renderApp({
      route: "/management?person=mbr_01&machine=dev_a",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => {
        if (path === "/v1/admin/devices/dev_a") throw new Error("detail down");
        if (path.startsWith("/v1/admin/devices")) throw new Error("devices down");
        return treeFetch(path);
      },
    });

    await screen.findByText(/could not load alice/i);
    expect(screen.queryByText("bob@example.com")).toBeNull();
  });

  it("keeps the machines level when only the agents leg fails, degrading that cell alone", async () => {
    await renderApp({
      route: "/management?person=mbr_01",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => {
        if (path.startsWith("/v1/admin/agents")) throw new Error("agents down");
        return treeFetch(path);
      },
    });

    // Machine rows come from the devices leg and are unaffected; only the
    // hand-registered grouping is missing, and it says so.
    await screen.findByText("alice-macbook");
    screen.getByText(/hand-registered agents/i);
    screen.getByRole("button", { name: /retry/i });
  });

  it("still falls back to the owner's level for a stale ?machine= when the agents leg is down too", async () => {
    // Recorded separately under Task 8: the stale-machine fallback needed
    // `agents` for the hand-registered grouping, and without it the whole
    // fallback — explanation included — vanished into the root level.
    await renderApp({
      route: "/management?person=mbr_01&machine=dev_gone",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => {
        if (path.startsWith("/v1/admin/agents")) throw new Error("agents down");
        return treeFetch(path);
      },
    });

    await screen.findByText("alice-macbook");
    screen.getByText(/no longer exists/i);
    screen.getByText(/hand-registered agents/i);
  });
});
