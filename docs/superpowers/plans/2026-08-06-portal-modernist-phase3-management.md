# Modernist Portal 阶段 3 · Management 访问树 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `/management` 从临时顶替的 `MyAgentsPage` 换成三层下钻的访问树（人 → 机器 → Agent），member 分叉为重画后的本人 Agent 列表，四张平表按 Modernist 重画。

**Architecture:** 纯前端。根层一次取三个列表（成员/机器/Agent，全量翻页）构成时点一致的快照；第 1、2 层完全由快照内存派生，零额外请求；第 3 层取一次 `GET /v1/admin/devices/:credential_id`，使级联吊销预览与展示行同源。下钻位置存在 URL query 参数里，不用组件 state。

**Tech Stack:** React 18 + TypeScript + react-router-dom（已有）；Vitest + @testing-library/react；无新增运行时依赖。

**设计依据：** `docs/superpowers/specs/2026-08-06-portal-modernist-phase3-management-design.md`（本阶段设计）与 `docs/superpowers/specs/2026-08-04-portal-modernist-redesign-design.md`（七阶段总设计）。

## Global Constraints

- **不新增任何后端端点，不改任何写路径语义。** 本阶段是纯前端。
- **不新增前端运行时依赖。** 生产依赖仍限于 react / react-dom / react-router-dom / react-markdown / remark-gfm。
- **按钮必须走 `components/Button.tsx`**（`AGENTS.md` §Web frontend 硬约定）。
- **样式只引用 CSS 变量**，不写字面色值；新特性样式在 `styles/features/` 下**新建文件**，不往已有文件追加（`styles/index.css` 顶部注释）。
- **间距用布局工具类**，不用 inline style 做间距。
- **禁止引用 `tokens.css` 第二个 `:root` 里的兼容别名**（`--bg` / `--muted` / `--ok` 等 30 个旧名）。新代码一律用 `--color-*` / `--space-*` / `--font-*`。
- **`provisioned_by` 按字段存在性判断，不按真值**：人工注册的 Agent 整个省略该字段（`web/src/api/types.ts:100-106`）。
- 每个 Task 结束前跑 `npm --prefix web test` 与 `npm --prefix web run build`（后者是 `tsc --noEmit`），两者都必须绿。
- 提交信息用中文，与本仓库既有风格一致。

**两处与设计文档的细节偏离（计划阶段决定，已核实更优）：**

1. 设计 §3 列了 `AccessCrumbs.tsx`。计划直接用已有的 `components/Crumbs.tsx`
   （带 `aria-current` 与链接/末项区分），不再包一层——包装组件不增加任何东西。
2. 设计稿在**第 2 层的机器行上**画了 Revoke 按钮。计划不做：那会在 `<button>` 行里嵌
   `<button>`（无效 HTML，且 testing-library 取不准），并且第 2 层手上只有
   `provisioned_agent_count` 一个数、没有 Agent 明细，级联预览会与展示分成两个数据源。
   吊销统一在第 3 层的机器头部（「Revoke this machine」，与设计稿该处一致）与既有的
   机器详情页。第 2 层的行是纯下钻入口。

---

### Task 1: 全量翻页的 devices / agents 列表函数

**Files:**
- Modify: `web/src/api/queries.ts`（在 `listAdminAgents` 与 `listDevices` 之后各加一个）
- Test: `web/tests/queries.test.ts`（已有 `listAllMembers` 的同类测试，追加两个 describe）

**Interfaces:**
- Consumes: 已有的 `listDevices(params)` 与 `listAdminAgents(params)`，两者返回 `Page<T> = { items, nextCursor }`。
- Produces:
  - `listAllDevices(params?: Omit<ListParams, "cursor">): Promise<DeviceSummary[]>`
  - `listAllAgents(params?: Omit<ListParams, "cursor"> & { status?: string; q?: string }): Promise<AgentProfile[]>`

**背景（实现者需要知道的）：** 后端把每页 limit 硬顶在 100（`internal/teamnote/transport/httpapi/handler/identity_registry_endpoints.go:907-917`），单页拿不到真实计数。仓库里已有 `listAllMembers`（`web/src/api/queries.ts:105-119`）就是这个模式的实现与先例，包括「重复游标即抛错」的防死循环保护——照抄它的形状，不要自创。

- [ ] **Step 1: 写失败的测试**

追加到 `web/tests/queries.test.ts` 末尾（文件顶部的 import 改为 `import { getAuditEvent, listAllAgents, listAllDevices, listAllMembers } from "../src/api/queries";`）：

```ts
describe("listAllDevices", () => {
  it("follows every opaque cursor so machine counts are not truncated", async () => {
    vi.stubGlobal("document", { cookie: "" });
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ devices: [{ credential_id: "dev_1" }], next_cursor: "page-2" }),
          { status: 200 },
        ),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ devices: [{ credential_id: "dev_2" }] }), { status: 200 }),
      );
    vi.stubGlobal("fetch", fetchMock);

    const devices = await listAllDevices({ limit: 100 });

    expect(devices.map((d) => d.credential_id)).toEqual(["dev_1", "dev_2"]);
    expect(fetchMock.mock.calls.map(([path]) => path)).toEqual([
      "/v1/admin/devices?limit=100",
      "/v1/admin/devices?limit=100&cursor=page-2",
    ]);
  });

  it("throws instead of looping forever when the server repeats a cursor", async () => {
    vi.stubGlobal("document", { cookie: "" });
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ devices: [{ credential_id: "dev_1" }], next_cursor: "same" }), {
        status: 200,
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    await expect(listAllDevices({ limit: 100 })).rejects.toThrow(/repeated cursor/);
  });
});

describe("listAllAgents", () => {
  it("follows every opaque cursor so agent counts are not truncated", async () => {
    vi.stubGlobal("document", { cookie: "" });
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ agents: [{ agent_id: "a1" }], next_cursor: "page-2" }), {
          status: 200,
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ agents: [{ agent_id: "a2" }] }), { status: 200 }),
      );
    vi.stubGlobal("fetch", fetchMock);

    const agents = await listAllAgents({ limit: 100 });

    expect(agents.map((a) => a.agent_id)).toEqual(["a1", "a2"]);
    expect(fetchMock.mock.calls.map(([path]) => path)).toEqual([
      "/v1/admin/agents?limit=100",
      "/v1/admin/agents?limit=100&cursor=page-2",
    ]);
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `npm --prefix web test -- queries.test.ts`
Expected: FAIL，`listAllDevices is not a function`（TypeScript 也会报 import 不存在）。

- [ ] **Step 3: 写最小实现**

在 `web/src/api/queries.ts` 的 `listDevices` 之后加：

```ts
/**
 * 全量机器列表：后端每页硬顶 100，访问树的计数必须翻完所有页才准确。
 * 形状与 listAllMembers 一致，包括重复游标保护。
 */
export async function listAllDevices(
  params: Omit<ListParams, "cursor"> = {},
): Promise<DeviceSummary[]> {
  const devices: DeviceSummary[] = [];
  const seenCursors = new Set<string>();
  let cursor: string | undefined;
  do {
    const page = await listDevices({ ...params, cursor });
    devices.push(...page.items);
    cursor = page.nextCursor;
    if (cursor && seenCursors.has(cursor)) {
      throw new Error("Device pagination returned a repeated cursor");
    }
    if (cursor) seenCursors.add(cursor);
  } while (cursor);
  return devices;
}
```

在 `listAdminAgents` 之后加：

```ts
/** 全量团队 Agent 列表。理由同 listAllDevices。 */
export async function listAllAgents(
  params: Omit<ListParams, "cursor"> & { status?: string; q?: string } = {},
): Promise<AgentProfile[]> {
  const agents: AgentProfile[] = [];
  const seenCursors = new Set<string>();
  let cursor: string | undefined;
  do {
    const page = await listAdminAgents({ ...params, cursor });
    agents.push(...page.items);
    cursor = page.nextCursor;
    if (cursor && seenCursors.has(cursor)) {
      throw new Error("Agent pagination returned a repeated cursor");
    }
    if (cursor) seenCursors.add(cursor);
  } while (cursor);
  return agents;
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `npm --prefix web test -- queries.test.ts && npm --prefix web run build`
Expected: PASS，build 干净。

- [ ] **Step 5: 提交**

```bash
git add web/src/api/queries.ts web/tests/queries.test.ts
git commit -m "feat(web): 全量翻页的 devices / agents 列表函数

访问树的计数必须翻完所有页才准确（后端每页硬顶 100）。形状照
listAllMembers 逐字同构，含重复游标保护。"
```

---

### Task 2: 提取 RevokeDeviceModal 为共用组件

**Files:**
- Create: `web/src/components/RevokeDeviceModal.tsx`
- Modify: `web/src/pages/AdminDeviceDetailPage.tsx`（删除本地定义，改为 import）
- Test: `web/tests/devices.dom.test.tsx`（**不改**，用它验证行为零变化）

**Interfaces:**
- Produces: `RevokeDeviceModal({ device, cascade, onClose, onDone })`，其中
  `device: DeviceSummary`、`cascade: DeviceProvisionedAgent[]`、`onClose: () => void`、
  `onDone: (device: DeviceSummary) => void`。签名与现有本地组件**逐字一致**。

**背景：** 这是纯搬迁，不是重写。现有实现在 `web/src/pages/AdminDeviceDetailPage.tsx:19-99`，
已经带级联表格预览、per-dialog `Idempotency-Key`、不可撤销文案。Task 7 的第 3 层要用它。

- [ ] **Step 1: 先跑现有测试，记下基线**

Run: `npm --prefix web test -- devices.dom.test.tsx`
Expected: PASS。记下通过的用例数——搬迁后必须一模一样。

- [ ] **Step 2: 新建组件文件**

把 `web/src/pages/AdminDeviceDetailPage.tsx:19-99` 的 `function RevokeDeviceModal` **整段剪切**
到新文件 `web/src/components/RevokeDeviceModal.tsx`，改 `function` 为 `export function`，
并按新位置修正 import 路径（`../api/...` → `../api/...` 不变；`../components/X` → `./X`；
`../lib/X` → `../lib/X` 不变）。文件顶部加一行说明：

```tsx
// 机器吊销确认弹窗：吊销会在同一事务里级联吊销该机器签发的全部存活 Agent
// 凭证，所以预览表与调用方展示的 Agent 行必须同源（都来自
// aliveProvisionedAgents），否则「预览行数 = 实际级联数」只能靠测试保证。
```

- [ ] **Step 3: 改调用方**

在 `web/src/pages/AdminDeviceDetailPage.tsx` 顶部加
`import { RevokeDeviceModal } from "../components/RevokeDeviceModal";`，
删掉剪走的那段，并**清理因此不再使用的 import**（`beginAction` / `revokeDevice` /
`useToast` / `Modal` 等——以 `npm run build` 的报错为准，不要凭印象删）。

- [ ] **Step 4: 跑测试确认行为零变化**

Run: `npm --prefix web test -- devices.dom.test.tsx && npm --prefix web run build`
Expected: PASS，通过用例数与 Step 1 完全相同；build 干净无未使用变量报错。

- [ ] **Step 5: 提交**

```bash
git add web/src/components/RevokeDeviceModal.tsx web/src/pages/AdminDeviceDetailPage.tsx
git commit -m "refactor(web): 提取 RevokeDeviceModal 为共用组件

访问树第 3 层要调起同一个弹窗。纯搬迁，签名与行为不变，
devices.dom.test.tsx 未改动即为证。"
```

---

### Task 3: 访问树的纯派生函数

**Files:**
- Create: `web/src/pages/management/accessTree.ts`
- Test: `web/tests/access-tree-derive.test.ts`

**Interfaces:**
- Produces（后续所有 Task 都依赖这些名字与签名）：

```ts
export function isLooseAgent(agent: AgentProfile): boolean;
export function devicesOf(devices: DeviceSummary[], membershipId: string): DeviceSummary[];
export function agentsOf(agents: AgentProfile[], membershipId: string): AgentProfile[];
export function looseAgentsOf(agents: AgentProfile[], membershipId: string): AgentProfile[];
export interface PeopleCounts { total: number; owners: number; admins: number; members: number }
export interface MachineCounts { total: number; connected: number; revoked: number; peopleWithoutMachine: number }
export interface AgentCounts { total: number; active: number; suspended: number; retired: number }
export function summarizePeople(members: Member[]): PeopleCounts;
export function summarizeMachines(devices: DeviceSummary[], members: Member[]): MachineCounts;
export function summarizeAgents(agents: AgentProfile[]): AgentCounts;
```

三个 summarize 分开而不是合成一个，是因为 devices / agents 两条腿会各自独立降级（见 Task 4），
合成的函数在缺腿时无法只渲染可用的那格。

- [ ] **Step 1: 写失败的测试**

新建 `web/tests/access-tree-derive.test.ts`：

```ts
import { describe, expect, it } from "vitest";
import {
  agentsOf,
  devicesOf,
  isLooseAgent,
  looseAgentsOf,
  summarizeAgents,
  summarizeMachines,
  summarizePeople,
} from "../src/pages/management/accessTree";
import { makeAgent, makeDevice, makeMember } from "./helpers";

describe("isLooseAgent", () => {
  it("treats a missing provisioned_by as hand-registered", () => {
    expect(isLooseAgent(makeAgent())).toBe(true);
  });

  it("treats an empty-string provisioned_by as device-provisioned", () => {
    // 存在性判断，不是真值判断：后端对人工注册的 Agent 整个省略该字段，
    // 空字符串是「有这个字段」的异常数据，不该被当成散装。
    expect(isLooseAgent(makeAgent({ provisioned_by: "" }))).toBe(false);
  });

  it("treats a device credential id as device-provisioned", () => {
    expect(isLooseAgent(makeAgent({ provisioned_by: "dev_01" }))).toBe(false);
  });
});

describe("devicesOf / agentsOf / looseAgentsOf", () => {
  const devices = [
    makeDevice({ credential_id: "dev_a", created_by_membership_id: "mbr_01" }),
    makeDevice({ credential_id: "dev_b", created_by_membership_id: "mbr_02" }),
  ];
  const agents = [
    makeAgent({ agent_id: "by-hand", owner_membership_id: "mbr_01" }),
    makeAgent({ agent_id: "by-device", owner_membership_id: "mbr_01", provisioned_by: "dev_a" }),
    makeAgent({ agent_id: "other-person", owner_membership_id: "mbr_02" }),
  ];

  it("groups devices by their creating membership", () => {
    expect(devicesOf(devices, "mbr_01").map((d) => d.credential_id)).toEqual(["dev_a"]);
  });

  it("returns every agent owned by the membership", () => {
    expect(agentsOf(agents, "mbr_01").map((a) => a.agent_id)).toEqual(["by-hand", "by-device"]);
  });

  it("returns only the hand-registered agents as loose", () => {
    expect(looseAgentsOf(agents, "mbr_01").map((a) => a.agent_id)).toEqual(["by-hand"]);
  });

  it("ignores agents whose owner_membership_id is absent", () => {
    expect(agentsOf([makeAgent({ owner_membership_id: undefined })], "mbr_01")).toEqual([]);
  });
});

describe("summarizePeople", () => {
  it("counts each role", () => {
    const counts = summarizePeople([
      makeMember({ membership_id: "m1", role: "owner" }),
      makeMember({ membership_id: "m2", role: "admin" }),
      makeMember({ membership_id: "m3", role: "member" }),
      makeMember({ membership_id: "m4", role: "member" }),
    ]);
    expect(counts).toEqual({ total: 4, owners: 1, admins: 1, members: 2 });
  });
});

describe("summarizeMachines", () => {
  it("counts machine states and people with no machine at all", () => {
    const members = [makeMember({ membership_id: "m1" }), makeMember({ membership_id: "m2" })];
    const devices = [
      makeDevice({ credential_id: "d1", created_by_membership_id: "m1", status: "active" }),
      makeDevice({ credential_id: "d2", created_by_membership_id: "m1", status: "revoked" }),
    ];
    expect(summarizeMachines(devices, members)).toEqual({
      total: 2,
      connected: 1,
      revoked: 1,
      peopleWithoutMachine: 1,
    });
  });

  it("counts a person whose only machine is revoked as still having one", () => {
    // 「没有机器」问的是有没有登记过，不是有没有能用的——把吊销过机器的人
    // 混进「一台都没有」会让 admin 以为那个人从没连过。
    const members = [makeMember({ membership_id: "m1" })];
    const devices = [
      makeDevice({ credential_id: "d1", created_by_membership_id: "m1", status: "revoked" }),
    ];
    expect(summarizeMachines(devices, members).peopleWithoutMachine).toBe(0);
  });
});

describe("summarizeAgents", () => {
  it("counts each agent status", () => {
    expect(
      summarizeAgents([
        makeAgent({ agent_id: "a", status: "active" }),
        makeAgent({ agent_id: "b", status: "suspended" }),
        makeAgent({ agent_id: "c", status: "retired" }),
        makeAgent({ agent_id: "d", status: "active" }),
      ]),
    ).toEqual({ total: 4, active: 2, suspended: 1, retired: 1 });
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `npm --prefix web test -- access-tree-derive.test.ts`
Expected: FAIL，`Failed to resolve import "../src/pages/management/accessTree"`。

- [ ] **Step 3: 写最小实现**

新建 `web/src/pages/management/accessTree.ts`：

```ts
// 访问树的纯派生：三个列表快照 → 各层需要的分组与计数。
// 全部是无副作用的纯函数，React 一概不进来。

import type { AgentProfile, DeviceSummary, Member, Role } from "../../api/types";

/**
 * 手工注册（散装）的 Agent：后端对人工注册的 Agent 整个省略 provisioned_by
 * （api/types.ts:100-106），所以判断的是字段存在性，不是真值。
 */
export function isLooseAgent(agent: AgentProfile): boolean {
  return agent.provisioned_by === undefined;
}

export function devicesOf(devices: DeviceSummary[], membershipId: string): DeviceSummary[] {
  return devices.filter((device) => device.created_by_membership_id === membershipId);
}

export function agentsOf(agents: AgentProfile[], membershipId: string): AgentProfile[] {
  return agents.filter((agent) => agent.owner_membership_id === membershipId);
}

export function looseAgentsOf(agents: AgentProfile[], membershipId: string): AgentProfile[] {
  return agentsOf(agents, membershipId).filter(isLooseAgent);
}

export interface PeopleCounts {
  total: number;
  owners: number;
  admins: number;
  members: number;
}

export function summarizePeople(members: Member[]): PeopleCounts {
  const byRole = (role: Role) => members.filter((member) => member.role === role).length;
  return {
    total: members.length,
    owners: byRole("owner"),
    admins: byRole("admin"),
    members: byRole("member"),
  };
}

export interface MachineCounts {
  total: number;
  connected: number;
  revoked: number;
  peopleWithoutMachine: number;
}

export function summarizeMachines(devices: DeviceSummary[], members: Member[]): MachineCounts {
  // 「没有机器」问的是有没有登记过，不是有没有能用的：把吊销过机器的人
  // 混进这个数会让 admin 以为那个人从没连过。
  const withMachine = new Set(devices.map((device) => device.created_by_membership_id));
  return {
    total: devices.length,
    connected: devices.filter((device) => device.status === "active").length,
    revoked: devices.filter((device) => device.status === "revoked").length,
    peopleWithoutMachine: members.filter((member) => !withMachine.has(member.membership_id)).length,
  };
}

export interface AgentCounts {
  total: number;
  active: number;
  suspended: number;
  retired: number;
}

export function summarizeAgents(agents: AgentProfile[]): AgentCounts {
  const byStatus = (status: AgentProfile["status"]) =>
    agents.filter((agent) => agent.status === status).length;
  return {
    total: agents.length,
    active: byStatus("active"),
    suspended: byStatus("suspended"),
    retired: byStatus("retired"),
  };
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `npm --prefix web test -- access-tree-derive.test.ts && npm --prefix web run build`
Expected: PASS，build 干净。

- [ ] **Step 5: 提交**

```bash
git add web/src/pages/management/accessTree.ts web/tests/access-tree-derive.test.ts
git commit -m "feat(web): 访问树的纯派生函数

按人分组机器与 Agent、识别散装（手工注册）Agent、三类计数。
三个 summarize 分开是因为 devices/agents 两条腿会各自独立降级。"
```

---

### Task 4: useAccessSnapshot —— 三列表快照与逐腿降级

**Files:**
- Create: `web/src/pages/management/useAccessSnapshot.ts`
- Test: `web/tests/access-snapshot.dom.test.tsx`

**Interfaces:**
- Consumes: Task 1 的 `listAllDevices` / `listAllAgents`，已有的 `listAllMembers`。
- Produces:

```ts
export interface AccessSnapshot {
  members: Member[];
  /** undefined 表示这条腿失败了（不是「空列表」）。 */
  devices?: DeviceSummary[];
  agents?: AgentProfile[];
}
export interface AccessSnapshotState {
  status: "loading" | "ready" | "error";
  snapshot?: AccessSnapshot;
  /** 仅 members 腿失败时有值。 */
  error?: unknown;
  retry: () => void;
}
export function useAccessSnapshot(): AccessSnapshotState;
```

**降级语义（设计 §6，实现者必须照此）：** members 是脊柱，它失败即 `status: "error"`；
devices / agents 任一失败时 `status` 仍是 `"ready"`，对应字段留 `undefined`，让调用方
把该格计数渲染成 `—`。**不要**把失败折叠成空数组——空数组和取不到是两件事，
折叠会让「这个团队一台机器都没有」和「机器列表挂了」在界面上长得一模一样。

- [ ] **Step 1: 写失败的测试**

新建 `web/tests/access-snapshot.dom.test.tsx`：

```tsx
import { describe, expect, it } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { useAccessSnapshot } from "../src/pages/management/useAccessSnapshot";
import { jsonResponse, makeAgent, makeDevice, makeMember, setupDomTest, stubFetch } from "./helpers";

setupDomTest();

describe("useAccessSnapshot", () => {
  it("returns one consistent snapshot of all three lists", async () => {
    stubFetch((path) => {
      if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [makeMember()] });
      if (path.startsWith("/v1/admin/devices")) return jsonResponse({ devices: [makeDevice()] });
      if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [makeAgent()] });
      throw new Error(`unexpected fetch: ${path}`);
    });

    const { result } = renderHook(() => useAccessSnapshot());

    await waitFor(() => expect(result.current.status).toBe("ready"));
    expect(result.current.snapshot?.members).toHaveLength(1);
    expect(result.current.snapshot?.devices).toHaveLength(1);
    expect(result.current.snapshot?.agents).toHaveLength(1);
  });

  it("fails the whole snapshot when the members leg fails", async () => {
    stubFetch((path) => {
      if (path.startsWith("/v1/admin/members")) throw new Error("members down");
      if (path.startsWith("/v1/admin/devices")) return jsonResponse({ devices: [] });
      if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [] });
      throw new Error(`unexpected fetch: ${path}`);
    });

    const { result } = renderHook(() => useAccessSnapshot());

    await waitFor(() => expect(result.current.status).toBe("error"));
    expect(result.current.error).toBeDefined();
  });

  it("stays ready and leaves devices undefined when only that leg fails", async () => {
    stubFetch((path) => {
      if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [makeMember()] });
      if (path.startsWith("/v1/admin/devices")) throw new Error("devices down");
      if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [makeAgent()] });
      throw new Error(`unexpected fetch: ${path}`);
    });

    const { result } = renderHook(() => useAccessSnapshot());

    await waitFor(() => expect(result.current.status).toBe("ready"));
    // undefined，不是 []：空列表和取不到必须在界面上长得不一样。
    expect(result.current.snapshot?.devices).toBeUndefined();
    expect(result.current.snapshot?.agents).toHaveLength(1);
  });
});
```

**注意：** 如果 `web/tests/helpers.tsx` 没有导出 `stubFetch`，先确认它的实际导出名
（`renderApp` 内部用的就是它，见 `helpers.tsx:135`），用实际名字，不要新建一个。

- [ ] **Step 2: 跑测试确认失败**

Run: `npm --prefix web test -- access-snapshot.dom.test.tsx`
Expected: FAIL，无法解析 `useAccessSnapshot`。

- [ ] **Step 3: 写最小实现**

新建 `web/src/pages/management/useAccessSnapshot.ts`：

```ts
// 访问树的根层取数：三个列表一次拉全，构成时点一致的快照。
// 第 1、2 层完全由这份快照内存派生，不再发请求（设计 §4.2）。

import { useCallback, useEffect, useState } from "react";
import { listAllAgents, listAllDevices, listAllMembers } from "../../api/queries";
import type { AgentProfile, DeviceSummary, Member } from "../../api/types";

export interface AccessSnapshot {
  members: Member[];
  /** undefined 表示这条腿失败了，与「空列表」是两件事。 */
  devices?: DeviceSummary[];
  agents?: AgentProfile[];
}

export interface AccessSnapshotState {
  status: "loading" | "ready" | "error";
  snapshot?: AccessSnapshot;
  /** 仅 members 腿失败时有值：没有人就没有树。 */
  error?: unknown;
  retry: () => void;
}

export function useAccessSnapshot(): AccessSnapshotState {
  const [state, setState] = useState<Omit<AccessSnapshotState, "retry">>({ status: "loading" });
  const [epoch, setEpoch] = useState(0);
  const retry = useCallback(() => setEpoch((value) => value + 1), []);

  useEffect(() => {
    let cancelled = false;
    setState({ status: "loading" });
    void Promise.allSettled([
      listAllMembers({ limit: 100 }),
      listAllDevices({ limit: 100 }),
      listAllAgents({ limit: 100 }),
    ]).then(([members, devices, agents]) => {
      if (cancelled) return;
      if (members.status === "rejected") {
        setState({ status: "error", error: members.reason });
        return;
      }
      setState({
        status: "ready",
        snapshot: {
          members: members.value,
          devices: devices.status === "fulfilled" ? devices.value : undefined,
          agents: agents.status === "fulfilled" ? agents.value : undefined,
        },
      });
    });
    return () => {
      cancelled = true;
    };
  }, [epoch]);

  return { ...state, retry };
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `npm --prefix web test -- access-snapshot.dom.test.tsx && npm --prefix web run build`
Expected: PASS，build 干净。

- [ ] **Step 5: 提交**

```bash
git add web/src/pages/management/useAccessSnapshot.ts web/tests/access-snapshot.dom.test.tsx
git commit -m "feat(web): 访问树根层快照 hook

三个列表一次拉全构成时点一致的快照。members 是脊柱，失败即整体失败；
devices/agents 各自独立降级为 undefined —— 不折叠成空数组，因为
「一台都没有」和「列表挂了」必须在界面上长得不一样。"
```

---

### Task 5: 汇总条与访问树样式表

**Files:**
- Create: `web/src/pages/management/AccessSummary.tsx`
- Create: `web/src/styles/features/access-tree.css`
- Modify: `web/src/styles/index.css`（末尾加一行 import）
- Test: `web/tests/access-summary.dom.test.tsx`

**Interfaces:**
- Consumes: Task 3 的 `summarizePeople` / `summarizeMachines` / `summarizeAgents`；
  Task 4 的 `AccessSnapshot`；已有的 `MetricTile`（`web/src/components/MetricTile.tsx`）。
- Produces: `AccessSummary({ snapshot }: { snapshot: AccessSnapshot })`。

- [ ] **Step 1: 写失败的测试**

新建 `web/tests/access-summary.dom.test.tsx`：

```tsx
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { AccessSummary } from "../src/pages/management/AccessSummary";
import { makeAgent, makeDevice, makeMember, setupDomTest } from "./helpers";

setupDomTest();

describe("AccessSummary", () => {
  it("renders three cells with human-worded breakdowns", () => {
    render(
      <AccessSummary
        snapshot={{
          members: [
            makeMember({ membership_id: "m1", role: "owner" }),
            makeMember({ membership_id: "m2", role: "member" }),
          ],
          devices: [makeDevice({ credential_id: "d1", created_by_membership_id: "m1" })],
          agents: [makeAgent({ agent_id: "a1", status: "active" })],
        }}
      />,
    );

    expect(screen.getByText("2")).toBeTruthy();
    screen.getByText("1 owner · 0 admins · 1 member");
    screen.getByText("1 connected · 0 revoked · 1 person has no machine");
    screen.getByText("1 active · 0 suspended · 0 retired");
  });

  it("shows an em dash instead of a number when a leg failed", () => {
    render(
      <AccessSummary snapshot={{ members: [makeMember({ membership_id: "m1" })] }} />,
    );

    // 两格失败 → 两个 —，而不是两个 0：0 会被读成「确实一台都没有」。
    expect(screen.getAllByText("—")).toHaveLength(2);
    expect(screen.getAllByText("Could not be loaded")).toHaveLength(2);
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `npm --prefix web test -- access-summary.dom.test.tsx`
Expected: FAIL，无法解析 `AccessSummary`。

- [ ] **Step 3: 写样式表**

新建 `web/src/styles/features/access-tree.css`：

```css
/* Management 访问树：汇总条、面包屑条、三层的行。 */
.at-summary { display: grid; grid-template-columns: repeat(3, 1fr); border-top: 2px solid var(--color-divider); border-bottom: 2px solid var(--color-divider); }

.at-bar { display: flex; align-items: center; gap: var(--space-2); padding: var(--space-3) var(--space-4); border-bottom: 1px solid var(--color-divider); }
.at-bar-hint { margin-left: auto; font-size: 11px; opacity: 0.5; }

.at-row { display: grid; gap: var(--space-3); align-items: center; width: 100%; text-align: left; background: transparent; border: 0; border-bottom: 1px solid var(--color-divider); padding: var(--space-3) var(--space-4); cursor: pointer; font-family: var(--font-body); color: inherit; }
.at-row:hover { background: color-mix(in srgb, var(--color-text) 5%, transparent); }
.at-row-name { font-family: var(--font-heading); font-weight: var(--font-heading-weight); font-size: 16px; }
.at-row-go { opacity: 0.35; text-align: right; }

.at-people { grid-template-columns: minmax(220px, 1fr) minmax(200px, 1fr) 110px 110px 24px; }
.at-machines { grid-template-columns: minmax(220px, 1fr) 120px 110px minmax(140px, 1fr) auto 24px; }
.at-agents { grid-template-columns: minmax(240px, 1fr) 120px 120px minmax(140px, 1fr) 24px; }

.at-head { display: grid; grid-template-columns: 1fr auto; gap: var(--space-4); align-items: center; padding: var(--space-3) var(--space-4); border-bottom: 2px solid var(--color-divider); background: color-mix(in srgb, var(--color-text) 5%, transparent); }

.at-group-note { padding: var(--space-3) var(--space-4); border-bottom: 1px solid var(--color-divider); font-size: 12px; opacity: 0.65; }
```

在 `web/src/styles/index.css` **末尾**追加：

```css
@import "./features/access-tree.css";
```

- [ ] **Step 4: 写组件**

新建 `web/src/pages/management/AccessSummary.tsx`：

```tsx
import { MetricTile } from "../../components/MetricTile";
import { summarizeAgents, summarizeMachines, summarizePeople } from "./accessTree";
import type { AccessSnapshot } from "./useAccessSnapshot";

/** 单复数：计数文案里 1 台机器不该写成 "1 machines"。 */
function plural(count: number, one: string, many: string): string {
  return `${count} ${count === 1 ? one : many}`;
}

/**
 * 访问树的三格汇总条。取不到的那条腿显示 —，不是 0：0 会被读成
 * 「确实一个都没有」，而实际是列表没加载出来。
 */
export function AccessSummary({ snapshot }: { snapshot: AccessSnapshot }) {
  const people = summarizePeople(snapshot.members);
  const machines = snapshot.devices && summarizeMachines(snapshot.devices, snapshot.members);
  const agents = snapshot.agents && summarizeAgents(snapshot.agents);

  return (
    <div className="at-summary">
      <MetricTile
        label="People"
        value={people.total}
        note={`${plural(people.owners, "owner", "owners")} · ${plural(people.admins, "admin", "admins")} · ${plural(people.members, "member", "members")}`}
      />
      <MetricTile
        label="Machines"
        value={machines ? machines.total : "—"}
        note={
          machines
            ? `${machines.connected} connected · ${machines.revoked} revoked · ${plural(machines.peopleWithoutMachine, "person has", "people have")} no machine`
            : "Could not be loaded"
        }
      />
      <MetricTile
        label="Agents"
        value={agents ? agents.total : "—"}
        note={
          agents
            ? `${agents.active} active · ${agents.suspended} suspended · ${agents.retired} retired`
            : "Could not be loaded"
        }
      />
    </div>
  );
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `npm --prefix web test -- access-summary.dom.test.tsx && npm --prefix web run build`
Expected: PASS。若「1 person has no machine」这类文案与断言对不上，
**改测试还是改实现取决于哪个更像人话**——两处必须一致。

- [ ] **Step 6: 提交**

```bash
git add web/src/pages/management/AccessSummary.tsx web/src/styles/features/access-tree.css web/src/styles/index.css web/tests/access-summary.dom.test.tsx
git commit -m "feat(web): 访问树汇总条与样式表

三格（People/Machines/Agents），失败的腿显示 — 而非 0。
新建 features/access-tree.css，不往已有特性表追加。"
```

---

### Task 6: AccessTreePage 外壳 + 第 1 层（人）

**Files:**
- Create: `web/src/pages/AccessTreePage.tsx`
- Create: `web/src/pages/management/PeopleLevel.tsx`
- Modify: `web/src/app/routes.tsx:99-106`（`/management` 路由与它上面那段注释）
- Test: `web/tests/access-tree.dom.test.tsx`

**Interfaces:**
- Consumes: Task 3 的派生函数、Task 4 的 `useAccessSnapshot`、Task 5 的 `AccessSummary`；
  已有的 `Crumbs`（`web/src/components/Crumbs.tsx`）、`EmptyState`、`Tag`、`Button`。
- Produces:
  - `AccessTreePage({ me }: { me: HumanMe })`
  - `PeopleLevel({ members, devices, agents, onDrill })`，其中
    `devices?: DeviceSummary[]`、`agents?: AgentProfile[]`（可缺，缺时该列显示 `—`）、
    `onDrill: (membershipId: string) => void`

**本 Task 只做 admin+ 分叉的第 1 层。** member 分叉在 Task 9，第 2、3 层在 Task 7、8。
本 Task 里 member 角色暂时继续渲染 `MyAgentsPage`（保持现状，不制造中间态回归）。

**URL 契约：** `?person=<membership_id>&machine=<credential_id>`。本 Task 只需读取并校验
`person`：指向的成员不在快照里（离职/被移除/链接过期）时，回退到根层并渲染说明。

- [ ] **Step 1: 写失败的测试**

新建 `web/tests/access-tree.dom.test.tsx`：

```tsx
import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import {
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
```

- [ ] **Step 2: 跑测试确认失败**

Run: `npm --prefix web test -- access-tree.dom.test.tsx`
Expected: FAIL——`/management` 现在渲染的是 MyAgentsPage，找不到 "Alice"。

- [ ] **Step 3: 写 PeopleLevel**

新建 `web/src/pages/management/PeopleLevel.tsx`：

```tsx
import type { AgentProfile, DeviceSummary, Member } from "../../api/types";
import { agentsOf, devicesOf } from "./accessTree";

/** 取不到的那条腿显示 —，与汇总条同一约定。 */
function count(items: unknown[] | undefined): string {
  return items === undefined ? "—" : String(items.length);
}

export function PeopleLevel({
  members,
  devices,
  agents,
  onDrill,
}: {
  members: Member[];
  devices?: DeviceSummary[];
  agents?: AgentProfile[];
  onDrill: (membershipId: string) => void;
}) {
  return (
    <div>
      {members.map((member) => (
        <button
          key={member.membership_id}
          type="button"
          className="at-row at-people"
          onClick={() => onDrill(member.membership_id)}
        >
          <span className="row">
            <span className="at-row-name">{member.display_name}</span>
            <span className="card-kicker">{member.role}</span>
          </span>
          <span className="small mono muted">{member.email}</span>
          <span className="small">{count(devices && devicesOf(devices, member.membership_id))}</span>
          <span className="small">{count(agents && agentsOf(agents, member.membership_id))}</span>
          <span className="at-row-go" aria-hidden="true">
            →
          </span>
        </button>
      ))}
    </div>
  );
}
```

- [ ] **Step 4: 写 AccessTreePage**

新建 `web/src/pages/AccessTreePage.tsx`：

```tsx
import { useSearchParams } from "react-router-dom";
import type { HumanMe } from "../api/types";
import { can } from "../lib/capabilities";
import { Button } from "../components/Button";
import { Crumbs } from "../components/Crumbs";
import { MyAgentsPage } from "./MyAgentsPage";
import { AccessSummary } from "./management/AccessSummary";
import { PeopleLevel } from "./management/PeopleLevel";
import { useAccessSnapshot } from "./management/useAccessSnapshot";

/**
 * Management 的访问树。下钻位置存在 URL 里（?person=&machine=）而不是组件
 * state，所以深链可分享、刷新保位、后退键逐层往上走而不是直接离开页面。
 */
export function AccessTreePage({ me }: { me: HumanMe }) {
  const [params, setParams] = useSearchParams();
  const snapshot = useAccessSnapshot();

  // member 分叉在 Task 9 换成 MyAgentsLevel；这里先维持现状。
  if (!can(me.role, "view.members")) return <MyAgentsPage />;

  if (snapshot.status === "loading") return <p className="muted">Loading…</p>;
  if (snapshot.status === "error" || !snapshot.snapshot) {
    return (
      <div className="note bad row between" role="alert">
        <span>Could not load the team’s people.</span>
        <Button size="sm" onClick={snapshot.retry}>
          Retry
        </Button>
      </div>
    );
  }

  const { members, devices, agents } = snapshot.snapshot;
  const requestedPerson = params.get("person") ?? undefined;
  const person = members.find((member) => member.membership_id === requestedPerson);
  // 链接指向的人已不在快照里：回退到根层并说明，不静默重置。
  const stalePerson = requestedPerson !== undefined && person === undefined;

  const drill = (membershipId: string) => {
    setParams({ person: membershipId });
  };

  return (
    <>
      <div className="page-head">
        <div>
          <p className="card-kicker">MANAGEMENT · ACCESS TREE</p>
          <h1>Access flows downward</h1>
          <p className="muted flush">
            A person joins the team. That person connects a machine. The machine registers the
            agents that run on it — and every key those agents hold traces back up this chain.
          </p>
        </div>
      </div>

      <AccessSummary snapshot={snapshot.snapshot} />

      <div className="at-bar">
        <Crumbs items={[{ label: "Everyone" }]} />
        <span className="at-bar-hint">{members.length} people</span>
      </div>

      {stalePerson && (
        <div className="note warn small" role="status">
          That person is no longer on this team, so we brought you back to everyone.
        </div>
      )}

      <PeopleLevel members={members} devices={devices} agents={agents} onDrill={drill} />
    </>
  );
}
```

- [ ] **Step 5: 改路由**

在 `web/src/app/routes.tsx`：把 `import { MyAgentsPage } from "../pages/MyAgentsPage";`
下面加 `import { AccessTreePage } from "../pages/AccessTreePage";`，
把 `<Route path="/management" element={<MyAgentsPage />} />` 换成
`<Route path="/management" element={<AccessTreePage me={me} />} />`，
并把它上面那段「阶段 3 用真正的 AccessTree 替换它」的注释改写为现状描述：

```tsx
      {/*
        Management 根节点：admin+ 是三层访问树，member 是本人 Agent 列表
        （AccessTreePage 内部按角色分叉）。下钻位置在 query 参数里，所以
        这里只有一条路由，与 /management/members 等平表路由不冲突。
      */}
```

**注意：** 此时 `MyAgentsPage` 仍被 `AccessTreePage` 引用（member 分叉），不要删。
若 `adminLike` 变量因此变成未使用，**不要删它**——`/management/agents/:agentId` 还在用。

- [ ] **Step 6: 跑测试确认通过**

Run: `npm --prefix web test && npm --prefix web run build`
Expected: 新测试 PASS。**既有测试可能因为 `/management` 换页而失败**——本 Task 只修
因路由变更而失败的断言中「owner/admin 看到什么」那部分；`a11y-controls` /
`app-shell` / `onboarding` / `admin-operations-access` 四个文件的系统性更新留到 Task 9，
若它们此刻已失败，在本 Task 用最小改动让它们绿（例如把 owner 的期望从 "My Agents" 改成
"Access flows downward"），并在提交信息里点名。

- [ ] **Step 7: 提交**

```bash
git add web/src/pages/AccessTreePage.tsx web/src/pages/management/PeopleLevel.tsx web/src/app/routes.tsx web/tests/access-tree.dom.test.tsx
git add -u web/tests
git commit -m "feat(web): 访问树外壳与第 1 层（人）

/management 换成 AccessTreePage：admin+ 走访问树，member 暂时仍走
MyAgentsPage（Task 9 换）。下钻位置进 ?person=，失效的 id 回退到根层
并说明原因而不是静默重置。"
```

---

### Task 7: 第 2 层（某人的机器）+ 散装 Agent 分组 + 本人限定的动作

**Files:**
- Create: `web/src/pages/management/MachinesLevel.tsx`
- Modify: `web/src/pages/AccessTreePage.tsx`（增加第 2 层分支与面包屑）
- Test: `web/tests/access-tree.dom.test.tsx`（追加 describe）

**Interfaces:**
- Consumes: Task 3 的 `devicesOf` / `looseAgentsOf`。
- Produces:

```ts
export function MachinesLevel({
  person, devices, looseAgents, isSelf, onDrill, onCreateAgent, onConnectMachine,
}: {
  person: Member;
  devices: DeviceSummary[];
  looseAgents: AgentProfile[];
  isSelf: boolean;
  onDrill: (credentialId: string) => void;
  onCreateAgent: () => void;
  onConnectMachine: () => void;
}): JSX.Element;
```

**这一层不含吊销动作**（见 Global Constraints 的偏离说明 2）：机器行是纯下钻入口，
吊销在第 3 层的机器头部。所以本 Task 不引入 `RevokeDeviceModal`。

**本人限定（设计 §5.2，这是后端强制的，不是产品偏好）：**
设备注册端点是 `POST /v1/me/device-enrollments`（`web/src/api/actions.ts:303-311`），
入参只有 `device_name` 与 `expires_in_seconds`，**永远为调用者本人注册**。
所以「Connect a machine」与「+ Create Agent」只在 `isSelf` 为真时渲染；
别人的头部只有「Change access」（链到 `/management/members`）。
在别人头部渲染那两个按钮 = 承诺一件做不到的事。

- [ ] **Step 1: 写失败的测试**

追加到 `web/tests/access-tree.dom.test.tsx`（复用文件顶部已有的 `treeFetch`）：

```tsx
describe("Access tree · machines level", () => {
  it("shows the person's machines with live agent counts", async () => {
    await renderApp({
      route: "/management?person=mbr_01",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => treeFetch(path),
    });

    const row = (await screen.findByText("alice-macbook")).closest(".at-row") as HTMLElement;
    expect(row.textContent).toContain("dev_a");
    // provisioned_agent_count = 2，与级联吊销同口径
    expect(row.textContent).toContain("2");
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

    await screen.findByText("Bob");
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
```

- [ ] **Step 2: 跑测试确认失败**

Run: `npm --prefix web test -- access-tree.dom.test.tsx`
Expected: FAIL——第 2 层还没实现，`?person=mbr_01` 仍渲染人员层。

- [ ] **Step 3: 写 MachinesLevel**

新建 `web/src/pages/management/MachinesLevel.tsx`：

```tsx
import { Link } from "react-router-dom";
import type { AgentProfile, DeviceSummary, Member } from "../../api/types";
import { Button } from "../../components/Button";
import { EmptyState } from "../../components/EmptyState";
import { Tag } from "../../components/Tag";
import { formatTime } from "../../lib/format";

export function MachinesLevel({
  person,
  devices,
  looseAgents,
  isSelf,
  onDrill,
  onCreateAgent,
  onConnectMachine,
}: {
  person: Member;
  devices: DeviceSummary[];
  looseAgents: AgentProfile[];
  isSelf: boolean;
  onDrill: (credentialId: string) => void;
  onCreateAgent: () => void;
  onConnectMachine: () => void;
}) {
  return (
    <>
      <div className="at-head">
        <div>
          <div className="row">
            <span className="at-row-name">{person.display_name}</span>
            <Tag tone="outline">{person.role}</Tag>
          </div>
          <div className="small muted">
            {person.email} · joined {formatTime(person.joined_at)}
          </div>
        </div>
        <div className="row">
          <Link to="/management/members" className="btn ghost">
            Change access
          </Link>
          {/* 注册端点是 /v1/me/device-enrollments 与 /v1/me/agents：
              永远为调用者本人注册，所以别人的头部不渲染这两个按钮。 */}
          {isSelf && <Button onClick={onConnectMachine}>Connect a machine</Button>}
          {isSelf && (
            <Button variant="primary" onClick={onCreateAgent}>
              + Create Agent
            </Button>
          )}
        </div>
      </div>

      {devices.length === 0 ? (
        <EmptyState
          title="No machines yet"
          body={
            isSelf
              ? "Connect a machine and the agents running on it register themselves."
              : `${person.display_name} has not connected a machine.`
          }
        />
      ) : (
        devices.map((device) => (
          <button
            key={device.credential_id}
            type="button"
            className="at-row at-machines"
            onClick={() => onDrill(device.credential_id)}
          >
            <span>
              <span className="at-row-name">{device.device_name}</span>
              <span className="small mono faint"> {device.credential_id}</span>
            </span>
            <span>
              <Tag tone={device.status === "active" ? "neutral" : "attention"}>
                {device.status === "active" ? "connected" : "revoked"}
              </Tag>
            </span>
            <span className="small">{device.provisioned_agent_count}</span>
            <span className="small muted">{formatTime(device.last_used_at)}</span>
            <span className="at-row-go" aria-hidden="true">
              →
            </span>
          </button>
        ))
      )}

      {looseAgents.length > 0 && (
        <>
          <p className="at-group-note">
            Registered by hand, without a machine — these keys were issued one at a time.
          </p>
          {looseAgents.map((agent) => (
            <Link
              key={agent.agent_id}
              to={`/management/agents/${encodeURIComponent(agent.agent_id)}`}
              className="at-row at-agents"
            >
              <span className="at-row-name">{agent.display_name}</span>
              <span className="small mono faint">{agent.agent_id}</span>
              <span className="small">{agent.agent_type}</span>
              <span className="small muted">{agent.status}</span>
              <span className="at-row-go" aria-hidden="true">
                →
              </span>
            </Link>
          ))}
        </>
      )}

    </>
  );
}
```

**这一层没有 `RevokeDeviceModal`，这是有意的。** 机器行是 `<button>`，往里再塞一个
`<Button>` 就是嵌套按钮（无效 HTML）；而且第 2 层手上只有 `provisioned_agent_count`
一个数，没有 Agent 明细，弹窗的级联预览会与第 3 层的展示分成两个数据源。
吊销统一在第 3 层的机器头部（Task 8）。

- [ ] **Step 4: 接进 AccessTreePage**

在 `web/src/pages/AccessTreePage.tsx` 里，`person` 已解析出来（Task 6）。加：

```tsx
  // 第 2 层：某人的机器。devices/agents 缺腿时这一层没有意义，回落到根层。
  if (person && devices && agents) {
    return (
      <>
        <div className="page-head">
          <div>
            <p className="card-kicker">MANAGEMENT · ACCESS TREE</p>
            <h1>Access flows downward</h1>
          </div>
        </div>
        <AccessSummary snapshot={snapshot.snapshot} />
        <div className="at-bar">
          <Crumbs
            items={[
              { label: "Everyone", to: "/management" },
              { label: person.display_name },
            ]}
          />
          <span className="at-bar-hint">
            {devicesOf(devices, person.membership_id).length} machines
          </span>
        </div>
        <MachinesLevel
          person={person}
          devices={devicesOf(devices, person.membership_id)}
          looseAgents={looseAgentsOf(agents, person.membership_id)}
          isSelf={person.membership_id === me.membership_id}
          onDrill={(credentialId) =>
            setParams({ person: person.membership_id, machine: credentialId })
          }
          onCreateAgent={() => setCreateAgentOpen(true)}
          onConnectMachine={() => setConnectMachineOpen(true)}
        />
      </>
    );
  }
```

`setCreateAgentOpen` / `setConnectMachineOpen` 两个弹窗在 Task 9 与 `MyAgentsLevel` 一起接。
**本 Task 先用 `useState` 声明并让按钮可点但暂不开弹窗**，改为跳转：
`onCreateAgent={() => navigate("/management/agents")}`、
`onConnectMachine={() => navigate("/management/devices")}`——两处目标页都已有对应的创建入口，
测试只断言按钮存在与否，不断言点击后果。Task 9 再换成就地开弹窗。

补齐 import：`useNavigate`、`MachinesLevel`、`devicesOf`、`looseAgentsOf`。

- [ ] **Step 5: 跑测试确认通过**

Run: `npm --prefix web test && npm --prefix web run build`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add web/src/pages/management/MachinesLevel.tsx web/src/pages/AccessTreePage.tsx web/tests/access-tree.dom.test.tsx
git commit -m "feat(web): 访问树第 2 层（某人的机器）与散装 Agent 分组

「Connect a machine」「+ Create Agent」只在本人头部渲染——注册端点是
/v1/me/*，永远为调用者本人注册，在别人头部渲染就是承诺做不到的事。

机器行是纯下钻入口，不含吊销：行本身是 <button>，嵌 <Button> 是无效
HTML；且这一层没有 Agent 明细，就地吊销会让级联预览与第 3 层的展示
分成两个数据源。吊销统一在第 3 层的机器头部。"
```

---

### Task 8: 第 3 层（某机器的 Agent）+ 级联预览同源

**Files:**
- Create: `web/src/pages/management/useDeviceDetail.ts`
- Create: `web/src/pages/management/DeviceAgentsLevel.tsx`
- Modify: `web/src/pages/AccessTreePage.tsx`（第 3 层分支）
- Modify: `web/src/pages/management/MachinesLevel.tsx`（修掉 Task 7 的 `cascade={[]}`）
- Test: `web/tests/access-tree.dom.test.tsx`（追加 describe）

**Interfaces:**
- Consumes: 已有的 `getDevice(credentialId): Promise<DeviceDetail>`（`web/src/api/queries.ts:170`）
  与 `aliveProvisionedAgents(agents)`（`web/src/lib/devices.ts:11`）。
- Produces:
  - `useDeviceDetail(credentialId?: string): { detail?: DeviceDetail; status: "loading" | "ready" | "error"; retry: () => void }`
  - `DeviceAgentsLevel({ person, device, agents, onRevoked })`，其中
    `agents: DeviceProvisionedAgent[]`（**已经过 `aliveProvisionedAgents` 过滤**）

**这一层的全部意义：** 级联预览与展示行同源。第 3 层展示的 Agent 行与吊销弹窗的预览表
都来自同一次 `getDevice` + 同一个 `aliveProvisionedAgents`，所以设计的验收标准
「预览行数 = 实际级联数」是结构性成立的，而不是靠两个数据源不漂移。
**不要**为了省一次请求改从 agents 列表里筛 `provisioned_by`——那样两者又变成两个源了。

- [ ] **Step 1: 写失败的测试**

追加到 `web/tests/access-tree.dom.test.tsx`：

```tsx
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
      // 已吊销的历史行：不该出现在展示里，也不该进级联预览。
      makeDeviceAgent({ agent_id: "alice-codex", credential_id: "cred_0", revoked_at: "2026-07-01T00:00:00Z" }),
    ],
  };

  const detailFetch = (path: string) => {
    if (path === "/v1/admin/devices/dev_a") return jsonResponse(deviceDetail);
    return treeFetch(path);
  };

  it("lists the machine's live agents", async () => {
    await renderApp({
      route: "/management?person=mbr_01&machine=dev_a",
      me: makeMe({ membership_id: "mbr_01" }),
      fetch: (path) => detailFetch(path),
    });

    await screen.findByText("alice-codex");
    screen.getByText("alice-claude");
    // 吊销的历史行不展示
    expect(screen.queryByText("cred_0")).toBeNull();
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
});
```

文件顶部 import 补上 `makeDeviceAgent` 与 `within`（`import { screen, within } from "@testing-library/react";`）。

- [ ] **Step 2: 跑测试确认失败**

Run: `npm --prefix web test -- access-tree.dom.test.tsx`
Expected: FAIL——第 3 层还没实现。

- [ ] **Step 3: 写 useDeviceDetail**

新建 `web/src/pages/management/useDeviceDetail.ts`：

```ts
// 访问树第 3 层的唯一一次取数。展示行与级联预览都从这一份详情派生，
// 所以「预览行数 = 实际级联数」是结构性的，不靠两个数据源保持同步。

import { useCallback, useEffect, useState } from "react";
import { getDevice } from "../../api/queries";
import type { DeviceDetail } from "../../api/types";

export interface DeviceDetailState {
  detail?: DeviceDetail;
  status: "loading" | "ready" | "error";
  retry: () => void;
}

export function useDeviceDetail(credentialId?: string): DeviceDetailState {
  const [state, setState] = useState<Omit<DeviceDetailState, "retry">>({ status: "loading" });
  const [epoch, setEpoch] = useState(0);
  const retry = useCallback(() => setEpoch((value) => value + 1), []);

  useEffect(() => {
    if (credentialId === undefined) {
      setState({ status: "ready" });
      return;
    }
    let cancelled = false;
    setState({ status: "loading" });
    getDevice(credentialId)
      .then((detail) => {
        if (!cancelled) setState({ status: "ready", detail });
      })
      .catch(() => {
        if (!cancelled) setState({ status: "error" });
      });
    return () => {
      cancelled = true;
    };
  }, [credentialId, epoch]);

  return { ...state, retry };
}
```

- [ ] **Step 4: 写 DeviceAgentsLevel**

新建 `web/src/pages/management/DeviceAgentsLevel.tsx`：

```tsx
import { useState } from "react";
import { Link } from "react-router-dom";
import type { DeviceProvisionedAgent, DeviceSummary, Member } from "../../api/types";
import { Button } from "../../components/Button";
import { EmptyState } from "../../components/EmptyState";
import { RevokeDeviceModal } from "../../components/RevokeDeviceModal";
import { Tag } from "../../components/Tag";
import { formatTime } from "../../lib/format";

export function DeviceAgentsLevel({
  person,
  device,
  agents,
  onRevoked,
}: {
  person: Member;
  device: DeviceSummary;
  /** 已经过 aliveProvisionedAgents 过滤——与级联预览同源。 */
  agents: DeviceProvisionedAgent[];
  onRevoked: () => void;
}) {
  const [revoking, setRevoking] = useState(false);

  return (
    <>
      <div className="at-head">
        <div>
          <div className="row">
            <span className="at-row-name">{device.device_name}</span>
            <Tag tone={device.status === "active" ? "neutral" : "attention"}>
              {device.status === "active" ? "connected" : "revoked"}
            </Tag>
          </div>
          <div className="small muted">
            <span className="mono">{device.credential_id}</span> · last used{" "}
            {formatTime(device.last_used_at)} · trusted by {person.display_name}
          </div>
        </div>
        {device.status === "active" && (
          <Button variant="danger" onClick={() => setRevoking(true)}>
            Revoke this machine
          </Button>
        )}
      </div>

      {agents.length === 0 ? (
        <EmptyState
          title="No agents registered yet"
          body="This machine has not registered any agent that still holds a live key."
        />
      ) : (
        agents.map((agent) => (
          <Link
            key={agent.credential_id}
            to={`/management/agents/${encodeURIComponent(agent.agent_id)}`}
            className="at-row at-agents"
          >
            <span className="at-row-name">{agent.display_name}</span>
            <span className="small mono faint">{agent.agent_id}</span>
            <span className="small">{agent.agent_type}</span>
            <span className="small muted">last used {formatTime(agent.last_used_at)}</span>
            <span className="at-row-go" aria-hidden="true">
              →
            </span>
          </Link>
        ))
      )}

      {revoking && (
        <RevokeDeviceModal
          device={device}
          cascade={agents}
          onClose={() => setRevoking(false)}
          onDone={() => {
            setRevoking(false);
            onRevoked();
          }}
        />
      )}
    </>
  );
}
```

- [ ] **Step 5: 接进 AccessTreePage 并修掉 Task 7 的 cascade=[]**

在 `AccessTreePage` 里 `useDeviceDetail(params.get("machine") ?? undefined)`（**hook 必须无条件调用**，
放在组件顶部与 `useAccessSnapshot` 并列，不能塞进 if 分支里）。第 3 层分支放在第 2 层分支**之前**：

```tsx
  const requestedMachine = params.get("machine") ?? undefined;
  const deviceDetail = useDeviceDetail(requestedMachine);
  // ...（在 person/devices/agents 都就绪后）
  if (person && devices && requestedMachine) {
    const device = devicesOf(devices, person.membership_id).find(
      (candidate) => candidate.credential_id === requestedMachine,
    );
    if (!device) {
      // 机器已删或不属于这个人：回落到这个人的机器层并说明。
      return renderMachinesLevel({ staleMachine: true });
    }
    if (deviceDetail.status === "loading") return <p className="muted">Loading…</p>;
    if (deviceDetail.status === "error" || !deviceDetail.detail) {
      return (
        <div className="note bad row between" role="alert">
          <span>Could not load this machine’s agents.</span>
          <Button size="sm" onClick={deviceDetail.retry}>
            Retry
          </Button>
        </div>
      );
    }
    return (
      <DeviceAgentsLevel
        person={person}
        device={device}
        agents={aliveProvisionedAgents(deviceDetail.detail.agents)}
        onRevoked={() => {
          snapshot.retry();
          setParams({ person: person.membership_id });
        }}
      />
    );
  }
```

面包屑在这一层是三段：`Everyone / <人名> / <机器名>`，前两段带 `to`。
把第 2 层的渲染抽成局部函数 `renderMachinesLevel({ staleMachine })` 以便复用，
`staleMachine` 为真时多渲染一条 `<div className="note warn small" role="status">That machine
no longer exists, so we brought you back to {person.display_name}’s machines.</div>`。

`MachinesLevel` 本 Task 无需改动——它从 Task 7 起就不含吊销动作，机器行是纯下钻入口，
所以级联预览只有 `getDevice` 一个来源，不存在两层各有一份取数逻辑的问题。

- [ ] **Step 6: 跑测试确认通过**

Run: `npm --prefix web test && npm --prefix web run build`
Expected: PASS。

- [ ] **Step 7: 提交**

```bash
git add web/src/pages/management/useDeviceDetail.ts web/src/pages/management/DeviceAgentsLevel.tsx web/src/pages/management/MachinesLevel.tsx web/src/pages/AccessTreePage.tsx web/tests/access-tree.dom.test.tsx
git commit -m "feat(web): 访问树第 3 层与级联预览同源

第 3 层取一次 device 详情，展示行与吊销预览都由同一份
aliveProvisionedAgents 派生，「预览行数 = 实际级联数」因此是结构性的。
机器行的 Revoke 改为下钻——取数逻辑只留一处。"
```

---

### Task 9: member 分叉（MyAgentsLevel）、删除 MyAgentsPage、更新四个既有测试

**Files:**
- Create: `web/src/pages/management/MyAgentsLevel.tsx`
- Create: `web/src/pages/management/CreateAgentModal.tsx`（从 MyAgentsPage 提取）
- Delete: `web/src/pages/MyAgentsPage.tsx`
- Modify: `web/src/pages/AccessTreePage.tsx`（member 分叉 + 本人头部的两个弹窗）
- Modify: `web/tests/app-shell.dom.test.tsx`、`web/tests/a11y-controls.dom.test.tsx`、
  `web/tests/onboarding.dom.test.tsx`、`web/tests/admin-operations-access.dom.test.tsx`
- Test: `web/tests/access-tree.dom.test.tsx`（追加 member describe）

**Interfaces:**
- Produces:
  - `CreateAgentModal({ onClose, onCreated })`——签名与 `MyAgentsPage.tsx:19-128` 里的**逐字一致**
  - `MyAgentsLevel()`——member 根层，自带状态筛选与创建入口

**四个既有测试写死了「`/management` 在阶段 3 前对所有角色渲染 MyAgentsPage」**（设计 §8bis）。
它们不是要删，是要改成新的正确行为：

| 文件 | 现有假设 | 改成 |
|---|---|---|
| `app-shell.dom.test.tsx:22` | 「+ Create Agent」全站唯一入口的护栏 | **必须继续成立**：owner 从人员层点自己 → 本人机器层有该按钮 |
| `a11y-controls.dom.test.tsx:78` | owner 落到 MyAgentsPage，与 member 相同 | owner 落到访问树 |
| `onboarding.dom.test.tsx:21` | `/management` 是各角色统一的 member-rooted 视图 | 按角色分叉 |
| `admin-operations-access.dom.test.tsx:90,124` | 同上，两处 | 按角色分叉 |

- [ ] **Step 1: 写失败的测试**

追加到 `web/tests/access-tree.dom.test.tsx`：

```tsx
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
    // fetch stub 对任何 /v1/admin/* 抛错：member 分叉仍必须渲染成功。
    await renderApp({
      route: "/management",
      me: makeMe({ role: "member", membership_id: "mbr_02" }),
      fetch: (path) => {
        if (path.startsWith("/v1/admin/")) throw new Error(`member fired an admin request: ${path}`);
        if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("button", { name: /create agent/i });
  });

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
```

- [ ] **Step 2: 跑测试确认失败**

Run: `npm --prefix web test -- access-tree.dom.test.tsx`
Expected: 三个新用例 FAIL。

- [ ] **Step 3: 提取 CreateAgentModal**

把 `web/src/pages/MyAgentsPage.tsx:19-128` 的 `CreateAgentModal` **整段剪切**到
`web/src/pages/management/CreateAgentModal.tsx`，改为 `export function`，修正 import 路径
（`../api/X` → `../../api/X`，`../components/X` → `../../components/X`，`../lib/X` → `../../lib/X`）。

- [ ] **Step 4: 写 MyAgentsLevel**

新建 `web/src/pages/management/MyAgentsLevel.tsx`，把 `MyAgentsPage` 的函数体搬过来并做三处改动：

1. `.tabs` 手写筛选器换成 `<Seg>`（`web/src/components/Seg.tsx`）——顺手退掉一个
   `components.css` 遗留别名的调用点：

```tsx
const STATUS_FILTERS = ["all", "active", "suspended", "retired"] as const;
type StatusFilter = (typeof STATUS_FILTERS)[number];
// ...
<Seg
  label="agent status"
  options={STATUS_FILTERS.map((s) => ({ value: s, label: s }))}
  value={filter}
  onChange={setFilter}
/>
```

2. 卡片网格换成访问树的行（`className="at-row at-agents"`），与第 3 层视觉一致。
3. **导航目标从 `/agents/:id` 改为 `/management/agents/:id`**——现有代码跳的是旧路由，
   靠 `LegacyRedirect` 多绕一跳，新代码不该再制造这一跳。两处：行的 `to` 与
   `onCreated` 里的 `navigate`。

页头保留 `<h1>My Agents</h1>` 与 `+ Create Agent`。

- [ ] **Step 5: 接进 AccessTreePage 并删除 MyAgentsPage**

- member 分叉从 `return <MyAgentsPage />` 改为 `return <MyAgentsLevel />`。
- 本人机器层的两个按钮从 Task 7 的「跳转到列表页」改成就地开弹窗：
  `CreateAgentModal`（本 Task 提取的）与 `CreateDeviceEnrollmentModal`。
  后者目前是 `AdminDevicesPage` 的局部组件（`web/src/pages/AdminDevicesPage.tsx:32`）——
  照 Task 2 的做法提取到 `web/src/components/CreateDeviceEnrollmentModal.tsx`，
  `AdminDevicesPage` 改为 import，**签名与行为不变**（`web/tests/devices.dom.test.tsx` 不改即为证）。
- 删除 `web/src/pages/MyAgentsPage.tsx`，并删掉 `routes.tsx` 里已无用的 import。

- [ ] **Step 6: 更新四个既有测试**

逐个打开上表里的四个文件，把「`/management` 渲染 MyAgentsPage」的断言改成分叉后的正确期望。
**`app-shell.dom.test.tsx` 的护栏断言不许弱化**——如果它现在断言的是「owner 在
`/management` 能看到 + Create Agent」，改成「owner 在 `/management` 点自己后能看到」，
把那条注释一并更新为树上的路径。

- [ ] **Step 7: 跑全量测试**

Run: `npm --prefix web test && npm --prefix web run build`
Expected: 全绿。若有 `MyAgentsPage` 的残留引用，build 会报出来。

- [ ] **Step 8: 提交**

```bash
git add -A web/src web/tests
git commit -m "feat(web)!: member 分叉落地，删除 MyAgentsPage

MyAgentsLevel 接手 member 根层，筛选器从 .tabs 换成 <Seg>（退掉一个
components.css 遗留别名调用点），导航直接指向 /management/agents/:id
不再绕 LegacyRedirect。

owner/admin 注册个人 Agent 的路径变成「人员层点自己 → 本人机器层」，
app-shell 的全站唯一入口护栏改为断言这条路径，未弱化。"
```

---

### Task 10: 四张平表按 Modernist 重画

**Files:**
- Modify: `web/src/pages/AdminMembersPage.tsx`
- Modify: `web/src/pages/AdminDevicesPage.tsx`
- Modify: `web/src/pages/AdminAgentsPage.tsx`
- Modify: `web/src/pages/AdminInvitationsPage.tsx`
- Test: 四页各自的既有 DOM 测试（`admin-members` / `devices` / `admin-invitations` 等）

**范围严格限定为版式。** 功能、筛选器语义、分页、权限门控、请求一律不动——
任何一处行为改动都属于超范围。四页已全部走 `PagedListCard`，className 只用 Modernist
期的工具类，**没有 `.badge` / `.b-*` / `.tabs` 遗留**（那些债在 explorer / operations /
wiki 三页，按总设计留到阶段 6）。

- [ ] **Step 1: 先跑四页的既有测试，记下基线**

Run: `npm --prefix web test -- admin-members admin-invitations devices admin-agents`
Expected: PASS。记下用例数——重画后必须一个不少。

- [ ] **Step 2: 逐页加页头 kicker**

四页的 `page-head` 里，`<h1>` 之上加一行分区标识（与访问树一致）：

```tsx
<p className="card-kicker">MANAGEMENT · MEMBERS</p>
```

依次为 `MEMBERS` / `DEVICES` / `AGENTS` / `INVITATIONS`。

- [ ] **Step 3: 主列上标题字族**

四页表格的第一列（成员名 / 机器名 / Agent 名 / 邀请邮箱）外面包一层
`<span className="at-row-name">`，让主列与访问树的行同一视觉重量。

- [ ] **Step 4: 可下钻的行补 → 可供性**

Devices 与 Agents 两页的行可以点进详情。在每行末尾加一个列：

```tsx
<td className="at-row-go" aria-hidden="true">→</td>
```

并在 `PagedListCard` 的 `columns` 数组末尾补一个 `""`（无标签动作列，
`PagedListCard` 的 `columns` 注释已说明用法）。Members 与 Invitations 两页
没有详情页，**不加**——加了就是承诺一个点不进去的下钻。

- [ ] **Step 5: 跑测试**

Run: `npm --prefix web test && npm --prefix web run build`
Expected: 全绿，用例数与 Step 1 一致。若某个断言按 `<td>` 位置取值而被新增列打乱，
改断言到按文本取，不要为了迁就测试放弃那一列。

- [ ] **Step 6: 三主题各过一遍**

Run: `npm --prefix web run dev`，在浏览器里切 beige / dark / arcade 三个主题，
逐页确认没有掉进浏览器默认样式（症状：字体变回 Times、分隔线消失）。
这是 CSS 变量引用错名时唯一的表现——它不报错。

- [ ] **Step 7: 提交**

```bash
git add web/src/pages/AdminMembersPage.tsx web/src/pages/AdminDevicesPage.tsx web/src/pages/AdminAgentsPage.tsx web/src/pages/AdminInvitationsPage.tsx
git add -u web/tests
git commit -m "feat(web): 四张平表按 Modernist 重画

页头 kicker、主列上标题字族、可下钻的行补 → 可供性（Members 与
Invitations 无详情页故不加）。功能、筛选器、分页、门控一律未动。"
```

---

## 收尾检查

全部 Task 完成后，逐条对设计 §9 的验收：

- [ ] owner / admin / member 三种角色各走通一次完整下钻
- [ ] 吊销机器时预览行数 = 展示的 Agent 行数
- [ ] 三层深链直接刷新进入可用，面包屑标签正确
- [ ] 失效的 `?person=` / `?machine=` 回退到最近有效层并给出说明
- [ ] member 分叉不发出任何 `/v1/admin/*` 请求
- [ ] 带散装 Agent 的人，第 2 层出现「手工注册」分组且计数包含它们
- [ ] `npm --prefix web test` 与 `npm --prefix web run build` 全绿
- [ ] 三主题各过一遍
- [ ] `grep -rn "MyAgentsPage" web/src web/tests` 无残留
