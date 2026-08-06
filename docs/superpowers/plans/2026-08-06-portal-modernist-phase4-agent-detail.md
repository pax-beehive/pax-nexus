# Modernist Portal 阶段 4 · Agent 详情 + 令牌仪式 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把两个 Agent 详情页合并成一个按归属判定 scope 的五段式页面，并把一次性令牌的展示升级为全屏仪式，四个调用点统一迁移。

**Architecture:** 一个页面 `AgentDetailPage` 组合五个自包含部件（Header / Identity / Lifecycle / Keys / Behaviour），scope 与权限由一个纯函数 `resolveAgentAccess(me, agent)` 从「目标 Agent 是不是你的」推出。密钥现状全量翻页取到一个数组，同时喂给展示卡与销毁类确认弹窗，使「预览计数 = 页面行数」结构性成立。`SecretCeremony` 是全屏覆盖层，取代 `SecretCard` 的全部四个调用点。

**Tech Stack:** React 18 + TypeScript + react-router-dom；vitest + @testing-library/react；纯 CSS（无 CSS-in-JS、无组件库）。

## Global Constraints

- **纯前端，零后端改动。** 不新增/修改任何端点、IDL 或 Go 代码。diff 只允许出现在 `web/` 与 `docs/` 下。
- **不引入任何前端运行时依赖。** 生产依赖仍限于 react / react-dom / react-router-dom / react-markdown / remark-gfm。
- 按钮一律走 `web/src/components/Button.tsx`；**禁止**已废弃的点分写法 `.btn.ghost`。
- 样式只引用 `--color-*` / `--space-*` / `--font-*`；**禁止**使用 `web/src/styles/tokens.css` 第二个 `:root` 块里的兼容别名（`--bg` `--muted` `--accent` `--text` `--border` `--surface` `--mono` 等）。
- 新特性样式写进**新建**的 `web/src/styles/features/agent-detail.css`，并在 `web/src/styles/index.css` 末尾追加 `@import`；不得往已有特性样式文件追加。
- 间距用 `layout.css` 的布局工具类，不用 inline style。
- 一次性密钥只存在于组件 state：**永不**写入 `localStorage`、`sessionStorage`、URL 或日志。
- 提交信息用中文。
- 每个任务结束前跑 `npm --prefix web test`，最后一个任务额外跑 `npm --prefix web run build`。

---

## 文件结构

**新建**

| 文件 | 职责 |
|---|---|
| `web/src/pages/agent/agentScope.ts` | 纯函数：从 `me` + `agent` 推出读/动作 scope 与七个权限位 |
| `web/src/pages/agent/useAgentKeys.ts` | 全量取 pending enrollments 与 active credentials，两条腿独立降级 |
| `web/src/pages/agent/AgentHeader.tsx` | 面包屑、标题、状态标签、归属行、两个门控跳转按钮 |
| `web/src/pages/agent/AgentIdentityCard.tsx` | Identity 表单 + 乐观锁保存 |
| `web/src/pages/agent/AgentLifecycleCard.tsx` | 三张动作卡 + 三个确认弹窗 + 移交目标选择 |
| `web/src/pages/agent/AgentKeysSection.tsx` | 「Waiting to be claimed」+「Active keys」两张卡 + 历史开关 |
| `web/src/pages/agent/AgentBehaviourCard.tsx` | Recent behaviour 三格 |
| `web/src/components/SecretCeremony.tsx` | 全屏一次性密钥仪式 |
| `web/src/components/DeviceEnrollmentCeremony.tsx` | 设备注册令牌仪式（两个调用点共用） |
| `web/src/components/IssueAccessModal.tsx` | 发放接入令牌表单 |
| `web/src/styles/features/agent-detail.css` | 本页与仪式的特性样式 |

**重写**：`web/src/pages/AgentDetailPage.tsx`

**删除**：`web/src/pages/AdminAgentDetailPage.tsx`、`web/src/components/AgentDetailView.tsx`、`web/src/components/AgentGovernanceCard.tsx`、`web/src/components/AgentArtifacts.tsx`、`web/src/components/SecretCard.tsx`、`web/src/components/artifacts/` 整个目录（6 个文件）

**修改**：`web/src/api/queries.ts`（加两个全量翻页函数）、`web/src/app/routes.tsx`、`web/src/styles/index.css`、`web/src/pages/AccessTreePage.tsx`、`web/src/pages/AdminDevicesPage.tsx`、`web/src/pages/AdminInvitationsPage.tsx`、`web/src/pages/AdminSessionAuditPage.tsx`、`web/src/pages/AdminExplorerPage.tsx`

**测试改动面（已实测，不要低估）**

- 整体重写：`tests/agent-detail.dom.test.tsx`、`tests/agent-artifacts.dom.test.tsx`、`tests/agent-governance.dom.test.tsx`
- 受牵连需改：`tests/a11y-controls.dom.test.tsx`（第 114–130 行渲染详情页）、`tests/provisioned-by.dom.test.tsx`（两处 `/management/agents/:id` 路由）、`tests/devices.dom.test.tsx`（第 78–80 行断言 SecretCard 文案与「Copy client command」按钮名）、`tests/access-tree.dom.test.tsx`（第 299 行断言密钥卡标题字符串）、`tests/admin-invitations.dom.test.tsx`（渲染邀请密钥展示）
- 新增：`tests/agent-scope.test.ts`、`tests/agent-keys.test.ts`、`tests/secret-ceremony.dom.test.tsx`、`tests/issue-access.dom.test.tsx`、`tests/agent-behaviour.dom.test.tsx`

---

## Task 1: scope 与权限判定（纯函数）

**Files:**
- Create: `web/src/pages/agent/agentScope.ts`
- Test: `web/tests/agent-scope.test.ts`

**Interfaces:**
- Consumes: `AgentScope` from `web/src/api/actions.ts`；`can(role, capability)` from `web/src/lib/capabilities.ts`；`AgentProfile`、`HumanMe` from `web/src/api/types.ts`
- Produces:
  - `readScopeFor(me: HumanMe): AgentScope`
  - `resolveAgentAccess(me: HumanMe, agent: AgentProfile): AgentAccess`
  - `interface AgentAccess { readScope; actScope; isSelf; retired; canEdit; canSuspend; canResume; canRetire; canTransfer; canIssue; canRevoke }`（全部 boolean，前两个是 `AgentScope`）

- [ ] **Step 1: 写失败测试**

`web/tests/agent-scope.test.ts`：

```ts
// resolveAgentAccess 是整个详情页的权限单一真源：读走哪个 scope、动作走哪个
// scope、七个按钮各自可见与否，全部由它推出。这里逐分支钉死，页面层不再
// 重复判断。
import { describe, expect, it } from "vitest";
import { resolveAgentAccess, readScopeFor } from "../src/pages/agent/agentScope";
import { makeAgent, makeMe } from "./helpers";

describe("readScopeFor", () => {
  it("admin+ 读 admin scope，member 读 me scope", () => {
    expect(readScopeFor(makeMe({ role: "owner" }))).toBe("admin");
    expect(readScopeFor(makeMe({ role: "admin" }))).toBe("admin");
    expect(readScopeFor(makeMe({ role: "member" }))).toBe("me");
  });
});

describe("resolveAgentAccess", () => {
  it("member 看自己的 Agent：me scope，可编辑可发令牌，不可移交", () => {
    const access = resolveAgentAccess(
      makeMe({ role: "member", membership_id: "mbr_01" }),
      makeAgent({ owner_membership_id: "mbr_01" }),
    );
    expect(access).toMatchObject({
      readScope: "me",
      actScope: "me",
      isSelf: true,
      canEdit: true,
      canSuspend: true,
      canResume: true,
      canRetire: true,
      canIssue: true,
      canRevoke: true,
      canTransfer: false,
    });
  });

  it("admin 看自己的 Agent：读 admin、动作 me，可发令牌", () => {
    // 这是本阶段修的洞：createEnrollment 只有 me scope，合并前 admin+
    // 恒走 admin scope，于是管理员给自己的 Agent 发不了接入令牌。
    const access = resolveAgentAccess(
      makeMe({ role: "admin", membership_id: "mbr_07" }),
      makeAgent({ owner_membership_id: "mbr_07" }),
    );
    expect(access.readScope).toBe("admin");
    expect(access.actScope).toBe("me");
    expect(access.isSelf).toBe(true);
    expect(access.canIssue).toBe(true);
    expect(access.canEdit).toBe(true);
  });

  it("admin 看别人的 Agent：只能暂停与吊销", () => {
    const access = resolveAgentAccess(
      makeMe({ role: "admin", membership_id: "mbr_07" }),
      makeAgent({ owner_membership_id: "mbr_99" }),
    );
    expect(access).toMatchObject({
      readScope: "admin",
      actScope: "admin",
      isSelf: false,
      canSuspend: true,
      canRevoke: true,
      canEdit: false,
      canResume: false,
      canRetire: false,
      canIssue: false,
      canTransfer: false,
    });
  });

  it("owner 看别人的 Agent：治理齐全且可移交，但不可发令牌", () => {
    const access = resolveAgentAccess(
      makeMe({ role: "owner", membership_id: "mbr_01" }),
      makeAgent({ owner_membership_id: "mbr_99" }),
    );
    expect(access).toMatchObject({
      actScope: "admin",
      canEdit: true,
      canResume: true,
      canRetire: true,
      canTransfer: true,
      canIssue: false,
    });
  });

  it("membership_id 或 owner_membership_id 缺失时判为「不是自己的」", () => {
    // 保守：宁可少给权限。后端仍是唯一执法者。
    const noOwner = resolveAgentAccess(
      makeMe({ role: "member", membership_id: "mbr_01" }),
      makeAgent({ owner_membership_id: undefined }),
    );
    expect(noOwner.isSelf).toBe(false);
    expect(noOwner.canIssue).toBe(false);

    const noMembership = resolveAgentAccess(
      makeMe({ role: "member", membership_id: undefined }),
      makeAgent({ owner_membership_id: "mbr_01" }),
    );
    expect(noMembership.isSelf).toBe(false);
    expect(noMembership.canIssue).toBe(false);
  });

  it("退役后一切写动作关闭，读 scope 不变", () => {
    const access = resolveAgentAccess(
      makeMe({ role: "owner", membership_id: "mbr_01" }),
      makeAgent({ owner_membership_id: "mbr_01", status: "retired", retired_at: "2026-08-01T00:00:00Z" }),
    );
    expect(access.retired).toBe(true);
    expect(access.readScope).toBe("admin");
    for (const key of ["canEdit", "canSuspend", "canResume", "canRetire", "canTransfer", "canIssue", "canRevoke"] as const) {
      expect(access[key]).toBe(false);
    }
  });

  it("status 与 retired_at 任一表明退役即视为退役", () => {
    // 后端两个字段都能表达终态；只看其中一个会让某些响应形态漏判。
    const byStatus = resolveAgentAccess(
      makeMe({ role: "owner", membership_id: "mbr_01" }),
      makeAgent({ owner_membership_id: "mbr_01", status: "retired" }),
    );
    const byTimestamp = resolveAgentAccess(
      makeMe({ role: "owner", membership_id: "mbr_01" }),
      makeAgent({ owner_membership_id: "mbr_01", retired_at: "2026-08-01T00:00:00Z" }),
    );
    expect(byStatus.retired).toBe(true);
    expect(byTimestamp.retired).toBe(true);
  });
});
```

- [ ] **Step 2: 跑测试确认红**

Run: `npm --prefix web test -- agent-scope`
Expected: FAIL —— 找不到模块 `../src/pages/agent/agentScope`

- [ ] **Step 3: 实现**

`web/src/pages/agent/agentScope.ts`：

```ts
// Agent 详情页的权限单一真源。
//
// 关键点：动作 scope 由「这个 Agent 是不是你的」决定，不由角色决定。
// POST /v1/me/agents/:id/enrollments 是发放接入令牌的唯一端点（admin scope
// 没有对应物），所以 admin+ 看自己的 Agent 时动作必须切到 me scope，
// 否则管理员给自己的 Agent 发不了令牌。
//
// 唯一例外是移交：只有 POST /v1/admin/agents/:id/transfer 一个端点，
// 且要求 owner 角色，所以即使 actScope 是 "me"，移交也走 admin scope。
// 调用方在 AgentLifecycleCard 里显式处理这一个动作。

import type { AgentScope } from "../../api/actions";
import type { AgentProfile, HumanMe } from "../../api/types";
import { can } from "../../lib/capabilities";

export interface AgentAccess {
  /** 取详情用的 scope。 */
  readScope: AgentScope;
  /** 编辑、生命周期、吊销、发放用的 scope（移交除外，见文件头注释）。 */
  actScope: AgentScope;
  /** 目标 Agent 归当前用户所有。 */
  isSelf: boolean;
  retired: boolean;
  canEdit: boolean;
  canSuspend: boolean;
  canResume: boolean;
  canRetire: boolean;
  canTransfer: boolean;
  canIssue: boolean;
  canRevoke: boolean;
}

export function readScopeFor(me: HumanMe): AgentScope {
  return can(me.role, "view.all-agents") ? "admin" : "me";
}

export function resolveAgentAccess(me: HumanMe, agent: AgentProfile): AgentAccess {
  const readScope = readScopeFor(me);
  // 任一侧缺失都判为「不是自己的」：宁可少给权限，后端仍是唯一执法者。
  const isSelf =
    me.membership_id !== undefined &&
    agent.owner_membership_id !== undefined &&
    agent.owner_membership_id === me.membership_id;
  const retired = agent.status === "retired" || agent.retired_at !== undefined;
  const govern = can(me.role, "govern.any-agent");
  const suspendAny = can(me.role, "suspend.any-agent");
  const live = !retired;

  return {
    readScope,
    actScope: isSelf ? "me" : readScope,
    isSelf,
    retired,
    canEdit: live && (isSelf || govern),
    canSuspend: live && (isSelf || suspendAny),
    canResume: live && (isSelf || govern),
    canRetire: live && (isSelf || govern),
    // 移交端点只有 admin scope 且 owner-only；自己的 Agent 也不例外。
    canTransfer: live && govern,
    // 发放接入令牌没有 admin scope 端点，只有本人可以。
    canIssue: live && isSelf,
    canRevoke: live && (isSelf || suspendAny),
  };
}
```

- [ ] **Step 4: 跑测试确认绿**

Run: `npm --prefix web test -- agent-scope`
Expected: PASS（8 个用例）

- [ ] **Step 5: 变异验证**

把 `canIssue` 临时改成 `live && (isSelf || suspendAny)`，重跑：「admin 看别人的 Agent」用例必须红。改回。

- [ ] **Step 6: 提交**

```bash
git add web/src/pages/agent/agentScope.ts web/tests/agent-scope.test.ts
git commit -m "feat(web): Agent 详情的 scope 与权限判定按归属而非角色"
```

---

## Task 2: 密钥现状全量取数

**Files:**
- Modify: `web/src/api/queries.ts`（在 `listCredentials` 之后追加两个函数）
- Create: `web/src/pages/agent/useAgentKeys.ts`
- Test: `web/tests/agent-keys.test.ts`

**Interfaces:**
- Consumes: `listEnrollments(scope, agentId, params)`、`listCredentials(scope, agentId, params)` from `web/src/api/queries.ts`；`deriveCredentialStatus(credential)` from `web/src/lib/credentials.ts`
- Produces:
  - `listAllEnrollments(scope: AgentScope, agentId: string, status: string): Promise<EnrollmentMetadata[]>`
  - `listAllCredentials(scope: AgentScope, agentId: string, status: string): Promise<CredentialMetadata[]>`
  - `useAgentKeys(scope: AgentScope, agentId: string): AgentKeys`
  - `interface KeyLeg<T> { items?: T[]; error?: unknown; loading: boolean }`
  - `interface AgentKeys { enrollments: KeyLeg<EnrollmentMetadata>; credentials: KeyLeg<CredentialMetadata>; reload: () => void }`

**为什么要全量翻页：** 暂停与退役的确认弹窗要说「这会销毁 N 把活跃密钥和 M 张未认领令牌」。这个数字必须精确——destructive 确认框里给一个被分页截断的数字比不给更糟。同一个数组既喂卡片行也喂确认弹窗，「预览计数 = 页面行数」因此在代码里无法分叉。

- [ ] **Step 1: 写失败测试**

`web/tests/agent-keys.test.ts`：

```ts
// 全量翻页 + 客户端二次过滤。这两条是确认弹窗计数正确性的地基。
import { describe, expect, it, vi, afterEach } from "vitest";
import { listAllCredentials, listAllEnrollments } from "../src/api/queries";
import { jsonResponse, makeCredential, makeEnrollment, stubFetch } from "./helpers";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("listAllEnrollments", () => {
  it("跟着游标把每一页都取回来", async () => {
    stubFetch((path) => {
      if (path.includes("cursor=c1")) {
        return jsonResponse({ enrollments: [makeEnrollment({ enrollment_id: "enr_02" })] });
      }
      return jsonResponse({
        enrollments: [makeEnrollment({ enrollment_id: "enr_01" })],
        next_cursor: "c1",
      });
    });

    const all = await listAllEnrollments("me", "agent-1", "pending");
    expect(all.map((e) => e.enrollment_id)).toEqual(["enr_01", "enr_02"]);
  });

  it("把 status 透传给服务端", async () => {
    const fetchMock = stubFetch(() => jsonResponse({ enrollments: [] }));
    await listAllEnrollments("admin", "agent-1", "pending");
    expect(String(fetchMock.mock.calls[0][0])).toContain("status=pending");
    expect(String(fetchMock.mock.calls[0][0])).toContain("/v1/admin/agents/agent-1/enrollments");
  });

  it("游标重复时抛错而不是无限翻页", async () => {
    stubFetch(() =>
      jsonResponse({ enrollments: [makeEnrollment()], next_cursor: "loop" }),
    );
    await expect(listAllEnrollments("me", "agent-1", "pending")).rejects.toThrow(
      /repeated cursor/i,
    );
  });
});

describe("listAllCredentials", () => {
  it("跟着游标把每一页都取回来", async () => {
    stubFetch((path) => {
      if (path.includes("cursor=c1")) {
        return jsonResponse({ credentials: [makeCredential({ credential_id: "cred_02" })] });
      }
      return jsonResponse({
        credentials: [makeCredential({ credential_id: "cred_01" })],
        next_cursor: "c1",
      });
    });

    const all = await listAllCredentials("me", "agent-1", "active");
    expect(all.map((c) => c.credential_id)).toEqual(["cred_01", "cred_02"]);
  });

  it("剔除服务端标成 active 但按时间已过期的密钥", async () => {
    // credential 没有服务端 status 字段（lib/credentials.ts 的注释）：
    // status=active 是服务端过滤，客户端必须再用 deriveCredentialStatus
    // 过一遍。宁可少算，不可多算——这个数字会出现在销毁确认框里。
    stubFetch(() =>
      jsonResponse({
        credentials: [
          makeCredential({ credential_id: "cred_live", expires_at: "2099-01-01T00:00:00Z" }),
          makeCredential({ credential_id: "cred_stale", expires_at: "2020-01-01T00:00:00Z" }),
          makeCredential({ credential_id: "cred_revoked", revoked_at: "2026-01-01T00:00:00Z" }),
        ],
      }),
    );

    const all = await listAllCredentials("me", "agent-1", "active");
    expect(all.map((c) => c.credential_id)).toEqual(["cred_live"]);
  });

  it("游标重复时抛错而不是无限翻页", async () => {
    stubFetch(() => jsonResponse({ credentials: [makeCredential()], next_cursor: "loop" }));
    await expect(listAllCredentials("me", "agent-1", "active")).rejects.toThrow(
      /repeated cursor/i,
    );
  });
});
```

- [ ] **Step 2: 跑测试确认红**

Run: `npm --prefix web test -- agent-keys`
Expected: FAIL —— `listAllEnrollments` / `listAllCredentials` 未导出

- [ ] **Step 3: 实现两个全量翻页函数**

在 `web/src/api/queries.ts` 中 `listCredentials` 之后追加（`deriveCredentialStatus` 需要新增 import，放在文件已有 import 区）：

```ts
/**
 * 取全部 pending Enrollment。翻页守卫与 listAllMembers 同构：
 * 服务端返回重复游标时抛错，绝不无限翻页。
 */
export async function listAllEnrollments(
  scope: AgentScope,
  agentId: string,
  status: string,
): Promise<EnrollmentMetadata[]> {
  const items: EnrollmentMetadata[] = [];
  const seenCursors = new Set<string>();
  let cursor: string | undefined;
  do {
    const page = await listEnrollments(scope, agentId, { status, cursor });
    items.push(...page.items);
    cursor = page.nextCursor;
    if (cursor && seenCursors.has(cursor)) {
      throw new Error("Enrollment pagination returned a repeated cursor");
    }
    if (cursor) seenCursors.add(cursor);
  } while (cursor);
  return items;
}

/**
 * 取全部活跃 Credential。credential 没有服务端 status 字段
 * （见 lib/credentials.ts），status=active 是服务端过滤；这里再用
 * deriveCredentialStatus 过一遍，因为返回值会作为销毁确认框里的计数，
 * 宁可少算不可多算。
 */
export async function listAllCredentials(
  scope: AgentScope,
  agentId: string,
  status: string,
): Promise<CredentialMetadata[]> {
  const items: CredentialMetadata[] = [];
  const seenCursors = new Set<string>();
  let cursor: string | undefined;
  do {
    const page = await listCredentials(scope, agentId, { status, cursor });
    items.push(...page.items);
    cursor = page.nextCursor;
    if (cursor && seenCursors.has(cursor)) {
      throw new Error("Credential pagination returned a repeated cursor");
    }
    if (cursor) seenCursors.add(cursor);
  } while (cursor);
  return status === "active"
    ? items.filter((c) => deriveCredentialStatus(c) === "active")
    : items;
}
```

- [ ] **Step 4: 跑测试确认绿**

Run: `npm --prefix web test -- agent-keys`
Expected: PASS（7 个用例）

- [ ] **Step 5: 实现 useAgentKeys**

`web/src/pages/agent/useAgentKeys.ts`：

```ts
// Agent 详情页的密钥现状：待认领的一次性令牌 + 活跃的长期密钥。
//
// 两条腿各自独立降级——一条挂了另一条照常渲染。返回的数组引用同时喂给
// 展示卡与销毁类确认弹窗的后果预览，所以「预览计数 = 页面行数」是结构性
// 成立的，不靠测试盯两个数据源不漂移。

import { useCallback, useEffect, useState } from "react";
import type { AgentScope } from "../../api/actions";
import { listAllCredentials, listAllEnrollments } from "../../api/queries";
import type { CredentialMetadata, EnrollmentMetadata } from "../../api/types";

export interface KeyLeg<T> {
  items?: T[];
  error?: unknown;
  loading: boolean;
}

export interface AgentKeys {
  enrollments: KeyLeg<EnrollmentMetadata>;
  credentials: KeyLeg<CredentialMetadata>;
  reload: () => void;
}

const LOADING: KeyLeg<never> = { loading: true };

export function useAgentKeys(scope: AgentScope, agentId: string): AgentKeys {
  const [enrollments, setEnrollments] = useState<KeyLeg<EnrollmentMetadata>>(LOADING);
  const [credentials, setCredentials] = useState<KeyLeg<CredentialMetadata>>(LOADING);
  const [epoch, setEpoch] = useState(0);

  const reload = useCallback(() => setEpoch((n) => n + 1), []);

  useEffect(() => {
    let cancelled = false;
    setEnrollments(LOADING);
    setCredentials(LOADING);

    listAllEnrollments(scope, agentId, "pending")
      .then((items) => {
        if (!cancelled) setEnrollments({ items, loading: false });
      })
      .catch((error: unknown) => {
        if (!cancelled) setEnrollments({ error, loading: false });
      });

    listAllCredentials(scope, agentId, "active")
      .then((items) => {
        if (!cancelled) setCredentials({ items, loading: false });
      })
      .catch((error: unknown) => {
        if (!cancelled) setCredentials({ error, loading: false });
      });

    return () => {
      cancelled = true;
    };
  }, [scope, agentId, epoch]);

  return { enrollments, credentials, reload };
}
```

- [ ] **Step 6: 跑全量测试**

Run: `npm --prefix web test`
Expected: 全绿（`useAgentKeys` 此时还没有调用方，只需保证不破坏既有测试）

- [ ] **Step 7: 提交**

```bash
git add web/src/api/queries.ts web/src/pages/agent/useAgentKeys.ts web/tests/agent-keys.test.ts
git commit -m "feat(web): Agent 密钥现状全量翻页取数，两条腿独立降级"
```

---

## Task 3: SecretCeremony 全屏仪式

**Files:**
- Create: `web/src/components/SecretCeremony.tsx`
- Create: `web/src/styles/features/agent-detail.css`
- Modify: `web/src/styles/index.css`（末尾追加 `@import "./features/agent-detail.css";`）
- Test: `web/tests/secret-ceremony.dom.test.tsx`

**Interfaces:**
- Consumes: `Button` from `web/src/components/Button.tsx`；`Countdown` from `web/src/components/Countdown.tsx`；`copyTextToClipboard` from `web/src/lib/clipboard.ts`；`useToast` from `web/src/components/Toasts.tsx`
- Produces: `SecretCeremony(props)`，props 如下（`ReactNode` 从 react 导入类型）：

```ts
{
  title: string;        // 顶栏 kicker
  headline: string;     // 大标题
  body: ReactNode;      // 标题下的解释段
  value: string;        // 令牌本身
  valueLabel: string;   // 「复制…」按钮里的名词
  expiresAt?: string;   // 有则渲染倒计时
  steps: string[];      // 「接下来会发生什么」
  recovery: string;     // 「丢了怎么办」
  meta?: ReactNode;     // 右下角签发元信息
  command?: string;     // 有则渲染命令块与第二个复制按钮
  onClose: () => void;
}
```

**行为要求：**
- 全屏覆盖层，`role="dialog"`、`aria-modal="true"`、由 `headline` 提供可访问名。
- **不响应 `Escape`，不响应点击遮罩**——一次性密钥不该被误触销毁。这与站内其他 `Modal` 行为不同，是有意的（spec §8 第 7 条）。
- 关闭两段：第一次点主按钮切确认态（出现「先别关」，主按钮变「确定关闭」，左侧提示改为「关掉后这串令牌就再也看不到了」），第二次才调 `onClose`。
- 令牌只在 props/state 中；组件不写任何存储。

- [ ] **Step 1: 写失败测试**

`web/tests/secret-ceremony.dom.test.tsx`：

```ts
// 一次性密钥仪式。三条不变量：两段式关闭、Esc/遮罩不关、关闭后三处存储
// 都不含令牌。
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SecretCeremony } from "../src/components/SecretCeremony";
import { ToastProvider } from "../src/components/Toasts";
import { setupDomTest } from "./helpers";

setupDomTest();

const TOKEN = "tm_enroll_denr_01.https://portal.example.com.secret-value";

function renderCeremony(onClose = () => {}) {
  return render(
    <ToastProvider>
      <SecretCeremony
        title="一次性接入令牌"
        headline="现在就复制。我们没法再给你看一次。"
        body="这串令牌让 Alice Codex 换取一把长期密钥。"
        value={TOKEN}
        valueLabel="令牌"
        expiresAt="2099-01-01T00:00:00Z"
        steps={["在目标机器上执行命令", "令牌兑换成密钥并自毁", "密钥出现在「活跃密钥」"]}
        recovery="什么都不会坏。取消这条待认领记录、重新发一张即可。"
        command="paxl channel connect onprem --enrollment-token <token>"
        onClose={onClose}
      />
    </ToastProvider>,
  );
}

describe("SecretCeremony", () => {
  it("渲染令牌、倒计时、三步说明与命令块", () => {
    renderCeremony();
    expect(screen.getByText(TOKEN)).toBeDefined();
    expect(screen.getByRole("dialog").getAttribute("aria-modal")).toBe("true");
    expect(screen.getByText("在目标机器上执行命令")).toBeDefined();
    expect(screen.getByText(/paxl channel connect onprem/)).toBeDefined();
    expect(screen.getByRole("button", { name: /复制令牌/ })).toBeDefined();
    expect(screen.getByRole("button", { name: /复制接入命令/ })).toBeDefined();
  });

  it("没有 command 时不渲染命令块与第二个复制按钮", () => {
    render(
      <ToastProvider>
        <SecretCeremony
          title="一次性邀请令牌"
          headline="现在就复制。"
          body="邀请链接。"
          value={TOKEN}
          valueLabel="邀请链接"
          steps={["把链接发给对方"]}
          recovery="吊销后重建即可。"
          onClose={() => {}}
        />
      </ToastProvider>,
    );
    expect(screen.queryByRole("button", { name: /复制接入命令/ })).toBeNull();
  });

  it("第一次点关闭只进入确认态，第二次才真的关", async () => {
    const user = userEvent.setup();
    let closed = 0;
    renderCeremony(() => {
      closed += 1;
    });

    await user.click(screen.getByRole("button", { name: "我已保存，关闭" }));
    expect(closed).toBe(0);
    expect(screen.getByRole("button", { name: "先别关" })).toBeDefined();
    expect(screen.getByText(/再也看不到/)).toBeDefined();

    await user.click(screen.getByRole("button", { name: "确定关闭" }));
    expect(closed).toBe(1);
  });

  it("「先别关」退回未确认态", async () => {
    const user = userEvent.setup();
    let closed = 0;
    renderCeremony(() => {
      closed += 1;
    });

    await user.click(screen.getByRole("button", { name: "我已保存，关闭" }));
    await user.click(screen.getByRole("button", { name: "先别关" }));
    expect(screen.getByRole("button", { name: "我已保存，关闭" })).toBeDefined();
    expect(closed).toBe(0);
  });

  it("Escape 与点击遮罩都不关闭", async () => {
    const user = userEvent.setup();
    let closed = 0;
    const { container } = renderCeremony(() => {
      closed += 1;
    });

    await user.keyboard("{Escape}");
    expect(closed).toBe(0);

    const backdrop = container.querySelector(".ceremony");
    expect(backdrop).not.toBeNull();
    await user.click(backdrop as Element);
    expect(closed).toBe(0);
    expect(screen.getByText(TOKEN)).toBeDefined();
  });

  it("令牌不进入 localStorage、sessionStorage 或 URL", async () => {
    const user = userEvent.setup();
    const { unmount } = renderCeremony();

    await user.click(screen.getByRole("button", { name: "我已保存，关闭" }));
    await user.click(screen.getByRole("button", { name: "确定关闭" }));
    unmount();

    expect(JSON.stringify(localStorage)).not.toContain("secret-value");
    expect(JSON.stringify(sessionStorage)).not.toContain("secret-value");
    expect(window.location.href).not.toContain("secret-value");
  });
});
```

- [ ] **Step 2: 跑测试确认红**

Run: `npm --prefix web test -- secret-ceremony`
Expected: FAIL —— 找不到 `../src/components/SecretCeremony`

- [ ] **Step 3: 实现组件**

`web/src/components/SecretCeremony.tsx`：

```tsx
// 一次性密钥仪式：全屏、朱红铺底、两段式关闭。
//
// 与站内其他 Modal 的两处刻意不同：
// 1. 不响应 Escape，不响应点击遮罩——一次性密钥被误触销毁的代价远高于
//    交互一致性。
// 2. 关闭要点两次。第一次只是把底栏切成确认态。
//
// 关闭不取消这条待认领记录：令牌在有效期内仍可兑换，只是不再可见。
// 调用方负责在 onClose 里给出相应提示。
//
// 密钥只存在于 props 与本组件的渲染树中：不写存储、不进 URL、不进日志。

import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { copyTextToClipboard } from "../lib/clipboard";
import { Button } from "./Button";
import { Countdown } from "./Countdown";
import { useToast } from "./Toasts";

export function SecretCeremony({
  title,
  headline,
  body,
  value,
  valueLabel,
  expiresAt,
  steps,
  recovery,
  meta,
  command,
  onClose,
}: {
  title: string;
  headline: string;
  body: ReactNode;
  value: string;
  valueLabel: string;
  expiresAt?: string;
  steps: string[];
  recovery: string;
  meta?: ReactNode;
  command?: string;
  onClose: () => void;
}) {
  const toast = useToast();
  const headlineId = useId();
  const [confirming, setConfirming] = useState(false);
  const dialogRef = useRef<HTMLDivElement>(null);

  // 打开时把焦点移进来，这样键盘用户不会停在背后的页面上。
  useEffect(() => {
    dialogRef.current?.focus();
  }, []);

  const copy = async (text: string, what: string) => {
    if (await copyTextToClipboard(text)) {
      toast("ok", `${what}已复制`);
      return;
    }
    // 剪贴板不可用（权限或非安全上下文）：退回手动复制提示。
    // 密钥仍然不落任何存储。
    window.prompt("请手动复制：", text);
  };

  return (
    <div className="ceremony">
      <div
        className="ceremony-panel"
        role="dialog"
        aria-modal="true"
        aria-labelledby={headlineId}
        tabIndex={-1}
        ref={dialogRef}
      >
        <header className="ceremony-top">
          <span className="ceremony-kicker">{title}</span>
          {expiresAt !== undefined && (
            <span className="ceremony-clock">
              可认领 <Countdown to={expiresAt} />
            </span>
          )}
        </header>

        <div className="ceremony-main">
          <div className="ceremony-primary">
            <h1 id={headlineId}>{headline}</h1>
            <p className="ceremony-body">{body}</p>
            <div className="ceremony-value">{value}</div>
            <div className="row wrap">
              <Button variant="primary" onClick={() => void copy(value, valueLabel)}>
                复制{valueLabel}
              </Button>
              {command !== undefined && (
                <Button onClick={() => void copy(command, "接入命令")}>复制接入命令</Button>
              )}
            </div>
            {command !== undefined && <div className="ceremony-command">{command}</div>}
          </div>

          <aside className="ceremony-aside">
            <span className="ceremony-kicker">接下来会发生什么</span>
            <ol className="ceremony-steps">
              {steps.map((step) => (
                <li key={step}>{step}</li>
              ))}
            </ol>
            <hr className="ceremony-rule" />
            <span className="ceremony-kicker">丢了怎么办</span>
            <p className="ceremony-body">{recovery}</p>
            {meta !== undefined && <div className="ceremony-meta">{meta}</div>}
          </aside>
        </div>

        <footer className="ceremony-foot">
          <span className="ceremony-hint">
            {confirming
              ? "关掉后这串令牌就再也看不到了。"
              : "关掉不会作废它——令牌在有效期内仍可兑换，只是不再可见。"}
          </span>
          <div className="row">
            {confirming && <Button onClick={() => setConfirming(false)}>先别关</Button>}
            <Button
              variant="primary"
              onClick={() => (confirming ? onClose() : setConfirming(true))}
            >
              {confirming ? "确定关闭" : "我已保存，关闭"}
            </Button>
          </div>
        </footer>
      </div>
    </div>
  );
}
```

- [ ] **Step 4: 写样式**

新建 `web/src/styles/features/agent-detail.css`（本任务只写仪式部分，后续任务往同一文件追加各段样式）：

```css
/* Agent 详情与一次性密钥仪式。 */

/* 仪式：全屏覆盖层，朱红铺底。三行网格 = 顶栏 / 主体 / 底栏。 */
.ceremony { position: fixed; inset: 0; z-index: 80; background: var(--color-accent); color: var(--color-accent-100); }
.ceremony-panel { height: 100%; display: grid; grid-template-rows: auto 1fr auto; outline: none; }

.ceremony-top { display: flex; align-items: center; justify-content: space-between; gap: var(--space-4); padding: var(--space-4) var(--space-6); border-bottom: 2px solid color-mix(in srgb, var(--color-accent-100) 45%, transparent); }
.ceremony-kicker { display: block; font-size: 10px; letter-spacing: 0.2em; text-transform: uppercase; }
.ceremony-clock { font-family: var(--font-mono); font-size: 12px; }

.ceremony-main { display: grid; grid-template-columns: 1.15fr 0.85fr; gap: var(--space-6); align-content: center; padding: var(--space-6); overflow-y: auto; }
.ceremony-primary h1 { color: var(--color-accent-100); max-width: 22ch; margin: 0 0 var(--space-2); }
.ceremony-body { font-size: 15px; max-width: 52ch; margin: 0; }
.ceremony-value { margin-top: var(--space-5); padding: var(--space-4); background: var(--color-neutral-900); color: var(--color-neutral-100); font-family: var(--font-mono); font-size: 19px; word-break: break-all; line-height: 1.5; }
.ceremony-command { margin-top: var(--space-3); padding: var(--space-3); border: 1px dashed color-mix(in srgb, var(--color-accent-100) 50%, transparent); font-family: var(--font-mono); font-size: 12px; word-break: break-all; }

.ceremony-aside { border-left: 2px solid color-mix(in srgb, var(--color-accent-100) 45%, transparent); padding-left: var(--space-5); }
.ceremony-steps { margin: var(--space-3) 0 0; padding-left: var(--space-4); font-size: 14px; line-height: 1.7; }
.ceremony-rule { border: 0; height: 2px; background: color-mix(in srgb, var(--color-accent-100) 45%, transparent); margin: var(--space-5) 0; }
.ceremony-meta { margin-top: var(--space-5); font-family: var(--font-mono); font-size: 11px; }

.ceremony-foot { display: flex; align-items: center; gap: var(--space-4); padding: var(--space-4) var(--space-6); border-top: 2px solid color-mix(in srgb, var(--color-accent-100) 45%, transparent); }
.ceremony-hint { font-size: 13px; }
.ceremony-foot .row { margin-left: auto; }

@media (max-width: 860px) {
  .ceremony-main { grid-template-columns: 1fr; }
  .ceremony-aside { border-left: 0; border-top: 2px solid color-mix(in srgb, var(--color-accent-100) 45%, transparent); padding-left: 0; padding-top: var(--space-5); }
}
```

在 `web/src/styles/index.css` 末尾追加：

```css
@import "./features/agent-detail.css";
```

- [ ] **Step 5: 跑测试确认绿**

Run: `npm --prefix web test -- secret-ceremony`
Expected: PASS（6 个用例）

- [ ] **Step 6: 变异验证**

把关闭按钮的 `onClick` 临时改成直接 `onClose()`（去掉两段），重跑：「第一次点关闭只进入确认态」必须红。改回。

- [ ] **Step 7: 提交**

```bash
git add web/src/components/SecretCeremony.tsx web/src/styles/features/agent-detail.css web/src/styles/index.css web/tests/secret-ceremony.dom.test.tsx
git commit -m "feat(web): 一次性密钥全屏仪式，两段式关闭且不响应 Esc 与遮罩"
```

---

## Task 4: 迁移其余三个一次性密钥调用点

**Files:**
- Create: `web/src/components/DeviceEnrollmentCeremony.tsx`
- Modify: `web/src/pages/AccessTreePage.tsx`、`web/src/pages/AdminDevicesPage.tsx`、`web/src/pages/AdminInvitationsPage.tsx`
- Delete: `web/src/components/SecretCard.tsx`
- Test: `web/tests/access-tree.dom.test.tsx`（第 299 行附近）、`web/tests/devices.dom.test.tsx`（第 78–80 行附近）、`web/tests/admin-invitations.dom.test.tsx`

**Interfaces:**
- Consumes: `SecretCeremony` from Task 3；`DeviceEnrollmentSecret` from `web/src/api/types.ts`；`deviceConnectCommand`、`isSelfDescribingEnrollmentToken` from `web/src/lib/enrollment.ts`
- Produces: `DeviceEnrollmentCeremony({ secret, onClose }: { secret: DeviceEnrollmentSecret; onClose: () => void })`

**注意：** `AccessTreePage` 与 `AdminDevicesPage` 现有的设备密钥展示是**逐字重复**的两份（标题、note 的两个分支、extraActions 的复制逻辑）。本任务把它们收敛成 `DeviceEnrollmentCeremony` 一个组件。

**另外：** `AccessTreePage` 里那段「坐标键清理 effect + 渲染门控」双保险（约第 60–90 行）是阶段 3 为「密钥卡内联挂在树里、导航后错绑到别人机器上」打的补丁。全屏仪式让这个问题在结构上消失。**保留清理 effect**（导航即关闭仪式），**去掉渲染门控里的坐标比较**，只留 `enrollmentSecret &&`。改动理由写进代码注释。

- [ ] **Step 1: 先跑一遍现有测试，记下会红的断言**

Run: `npm --prefix web test -- access-tree devices admin-invitations`
Expected: PASS（这是改动前的基线；把 `access-tree` 第 299 行、`devices` 第 78–80 行记下来）

- [ ] **Step 2: 写设备仪式组件**

`web/src/components/DeviceEnrollmentCeremony.tsx`：

```tsx
// 设备注册令牌的仪式。AccessTreePage 与 AdminDevicesPage 共用——两处此前
// 是逐字重复的两份 SecretCard 调用。

import type { DeviceEnrollmentSecret } from "../api/types";
import { deviceConnectCommand, isSelfDescribingEnrollmentToken } from "../lib/enrollment";
import { SecretCeremony } from "./SecretCeremony";

export function DeviceEnrollmentCeremony({
  secret,
  onClose,
}: {
  secret: DeviceEnrollmentSecret;
  onClose: () => void;
}) {
  return (
    <SecretCeremony
      title="一次性设备注册令牌 · 只展示一次，不存任何地方"
      headline="现在就复制。我们没法再给你看一次。"
      body={
        <>
          这串令牌让 <b>{secret.device_name}</b> 换取一把长期设备密钥。
          它只存在于这个屏幕上——不在数据库里，不在邮件里，也不在审计日志里。
        </>
      }
      value={secret.token}
      valueLabel="令牌"
      expiresAt={secret.expires_at}
      steps={[
        `在 ${secret.device_name} 上执行下面的命令。`,
        "令牌兑换成长期密钥并自毁。",
        "这台机器出现在访问树里，它上面的 Agent 可以自助注册。",
      ]}
      recovery={
        isSelfDescribingEnrollmentToken(secret.token)
          ? "什么都不会坏。吊销这台设备、重新创建一次注册即可；令牌内嵌了连接地址，客户端可以直接解析。"
          : "什么都不会坏。吊销这台设备、重新创建一次注册即可。"
      }
      command={deviceConnectCommand(secret.token, window.location.origin, secret.device_name)}
      onClose={onClose}
    />
  );
}
```


- [ ] **Step 3: 改三个调用点**

`web/src/pages/AdminDevicesPage.tsx`：把 `{secret && (<SecretCard … />)}` 整块换成

```tsx
{secret && <DeviceEnrollmentCeremony secret={secret} onClose={() => setSecret(undefined)} />}
```

并删除随之不再使用的 import（`SecretCard`、`Button` 若只为 extraActions 而引、`copyTextToClipboard`、`deviceConnectCommand`、`isSelfDescribingEnrollmentToken`、`useToast` 若只为复制而引）。**逐个确认这些符号在文件里没有其它用途再删。**

`web/src/pages/AccessTreePage.tsx`：把 `const secretCard = enrollmentSecret && !requestedMachine && requestedPerson === me.membership_id && (<SecretCard … />)` 换成

```tsx
  // 阶段 4：仪式是全屏覆盖层，出现时它是页面上唯一可交互的东西，不存在
  // 「挂在哪个节点下」的语义——阶段 3 那道「坐标比较渲染门控」随之取消。
  // 下方的坐标变更 effect 保留：树内导航时关闭仪式。
  // 仍然故意提到任何 snapshot early return 之前算：失败/进行中的 retry()
  // 不该把已经拿到手的一次性密钥带走。
  const secretCard = enrollmentSecret && (
    <DeviceEnrollmentCeremony
      secret={enrollmentSecret}
      onClose={() => setEnrollmentSecret(undefined)}
    />
  );
```

`web/src/pages/AdminInvitationsPage.tsx`：把 `{secretUrl && (<SecretCard … />)}` 换成

```tsx
{secretUrl && (
  <SecretCeremony
    title="一次性邀请链接 · 只展示一次，不存任何地方"
    headline="现在就把链接发出去。我们没法再给你看一次。"
    body="令牌藏在 URL 的片段（#invite=）里，不会进入服务端访问日志，也不会出现在 Referer 头。"
    value={secretUrl}
    valueLabel="邀请链接"
    steps={[
      "把链接发给对方（IM、邮件都行）。",
      "对方打开链接、登录，即完成加入。",
      "这个人出现在「团队成员」里。",
    ]}
    recovery="什么都不会坏。吊销这条邀请、重新创建一条即可。"
    onClose={() => setSecretUrl(undefined)}
  />
)}
```

- [ ] **Step 4: 删除 SecretCard**

```bash
git rm web/src/components/SecretCard.tsx
```

Run: `npm --prefix web run build`
Expected: TypeScript 报出所有残留引用（应为 0 处）。若有残留，改完再继续。

- [ ] **Step 5: 更新三个测试文件的断言**

- `tests/access-tree.dom.test.tsx` 第 299 行：`screen.getByText("One-time Device Enrollment token (shown only once)")` → `screen.getByText("一次性设备注册令牌 · 只展示一次，不存任何地方")`
- `tests/devices.dom.test.tsx` 第 78–80 行：注释里的 "SecretCard" 改为 "仪式"；`{ name: "Copy client command" }` → `{ name: "复制接入命令" }`
- `tests/admin-invitations.dom.test.tsx`：若有断言邀请密钥卡文案的用例，按新文案更新

在 `tests/access-tree.dom.test.tsx` 追加一条新用例，钉住「仪式不再靠坐标比较、而靠坐标变更即关闭」这一行为变化。先读本文件里已有的「创建设备注册」用例（搜索 `One-time Device Enrollment` 附近那条），照它的 fixture 与点击路径写，只在结尾加导航与断言：

```ts
  it("产生令牌后钻到别人那里，仪式关闭而不是错绑过去", async () => {
    // 阶段 3 的密钥卡是内联的，靠「坐标比较渲染门控」避免错绑到别人的机器
    // 上；阶段 4 换成全屏仪式后，由「坐标变更即关闭」这一条 effect 保证。
    // 这条用例是那条 effect 的唯一守卫——去掉它，仪式会跟着用户钻遍整棵树。
    //
    // 步骤（照本文件既有的「创建设备注册」用例写前半段）：
    //   1. 以 owner 渲染 /management，点开「Connect a machine」创建注册
    //   2. 断言仪式出现：screen.getByText("tm_enroll_denr_01.secret")
    //   3. 点击第 1 层里另一个人的行（fixture 里的第二个 member）
    //   4. 断言 screen.queryByText("tm_enroll_denr_01.secret") 为 null
  });
```

实现者把这四步写成可运行的完整测试。断言用的令牌字符串照本文件既有 fixture 里的实际值。

- [ ] **Step 6: 跑测试确认绿**

Run: `npm --prefix web test`
Expected: 全绿

- [ ] **Step 7: 提交**

```bash
git add -A web/src web/tests
git commit -m "refactor(web): 四个一次性密钥调用点统一走全屏仪式，删除 SecretCard"
```

---

## Task 5: 发放接入令牌表单

**Files:**
- Create: `web/src/components/IssueAccessModal.tsx`
- Test: `web/tests/issue-access.dom.test.tsx`

**Interfaces:**
- Consumes: `createEnrollment(agentId, input)`、`CreateEnrollmentInput` from `web/src/api/actions.ts`；`apiError` from `web/src/api/client.ts`；`GRANTABLE_PERMISSIONS`、`EnrollmentSecret` from `web/src/api/types.ts`；`Modal`、`Button`、`Seg`、`useToast`
- Produces: `IssueAccessModal({ agentId, agentName, onClose, onCreated, onMaybeCreated })`
  - `onCreated: (secret: EnrollmentSecret) => void`
  - `onMaybeCreated: () => void`

**权限人类标签（原始名以小字并排显示，不隐藏）：**

| 原始名 | 标签 |
|---|---|
| `observe` | 记录它的会话 |
| `search` | 检索团队记忆 |
| `get` | 读取指定笔记 |
| `channel_send` | 发送给其他 Agent |
| `channel_receive` | 接收其他 Agent 的消息 |

**两个 Seg：** 认领窗口 `5m / 15m / 30m`（→ `expires_in_seconds` 300/900/1800，默认 900）；密钥有效期 `30 天 / 90 天 / 1 年 / 不过期`（→ `credential_expires_at` = 现在 + N，默认 90 天；「不过期」不发送该字段）。

**既有约束原样保留：** 该端点不支持 `Idempotency-Key`。4xx 保留表单让用户改；超时/5xx 关闭表单、刷新待认领列表、warn toast，**绝不自动重试**。

- [ ] **Step 1: 写失败测试**

`web/tests/issue-access.dom.test.tsx`：

```tsx
// 发放接入令牌表单。重点：人类标签映射回正确的原始权限名、两个 Seg 换算
// 正确、以及一次性密钥端点的「不盲目重试」纪律。
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { IssueAccessModal } from "../src/components/IssueAccessModal";
import { ToastProvider } from "../src/components/Toasts";
import { apiErrorResponse, jsonResponse, setupDomTest, stubFetch } from "./helpers";

setupDomTest();

function renderModal(overrides: {
  onCreated?: (s: { enrollment_id: string; token: string; expires_at: string }) => void;
  onMaybeCreated?: () => void;
} = {}) {
  return render(
    <ToastProvider>
      <IssueAccessModal
        agentId="agent-1"
        agentName="Alice Codex"
        onClose={() => {}}
        onCreated={overrides.onCreated ?? (() => {})}
        onMaybeCreated={overrides.onMaybeCreated ?? (() => {})}
      />
    </ToastProvider>,
  );
}

describe("IssueAccessModal", () => {
  it("默认勾选记录会话与检索记忆，提交时发出原始权限名", async () => {
    const user = userEvent.setup();
    const fetchMock = stubFetch(() =>
      jsonResponse({ enrollment_id: "enr_01", token: "tm_enroll_x.y", expires_at: "2099-01-01T00:00:00Z" }),
    );
    renderModal();

    await user.type(screen.getByLabelText(/它会在哪台机器上跑/), "mac-studio-01");
    await user.click(screen.getByRole("button", { name: "发放一次性令牌" }));

    const body = JSON.parse(String(fetchMock.mock.calls[0][1].body));
    expect(body.credential_label).toBe("mac-studio-01");
    expect(body.permissions).toEqual(["observe", "search"]);
    expect(body.expires_in_seconds).toBe(900);
    // 默认 90 天：只断言是个未来时刻，不钉死具体毫秒。
    expect(new Date(body.credential_expires_at).getTime()).toBeGreaterThan(Date.now());
  });

  it("人类标签勾选后映射回对应的原始权限名", async () => {
    const user = userEvent.setup();
    const fetchMock = stubFetch(() =>
      jsonResponse({ enrollment_id: "enr_01", token: "t", expires_at: "2099-01-01T00:00:00Z" }),
    );
    renderModal();

    await user.type(screen.getByLabelText(/它会在哪台机器上跑/), "mac-studio-01");
    await user.click(screen.getByLabelText(/发送给其他 Agent/));
    await user.click(screen.getByRole("button", { name: "发放一次性令牌" }));

    const body = JSON.parse(String(fetchMock.mock.calls[0][1].body));
    expect(body.permissions).toEqual(["observe", "search", "channel_send"]);
  });

  it("「不过期」不发送 credential_expires_at", async () => {
    const user = userEvent.setup();
    const fetchMock = stubFetch(() =>
      jsonResponse({ enrollment_id: "enr_01", token: "t", expires_at: "2099-01-01T00:00:00Z" }),
    );
    renderModal();

    await user.type(screen.getByLabelText(/它会在哪台机器上跑/), "mac-studio-01");
    await user.click(screen.getByRole("button", { name: "不过期" }));
    await user.click(screen.getByRole("button", { name: "发放一次性令牌" }));

    const body = JSON.parse(String(fetchMock.mock.calls[0][1].body));
    expect("credential_expires_at" in body).toBe(false);
  });

  it("认领窗口 5m 换算成 300 秒", async () => {
    const user = userEvent.setup();
    const fetchMock = stubFetch(() =>
      jsonResponse({ enrollment_id: "enr_01", token: "t", expires_at: "2099-01-01T00:00:00Z" }),
    );
    renderModal();

    await user.type(screen.getByLabelText(/它会在哪台机器上跑/), "mac-studio-01");
    await user.click(screen.getByRole("button", { name: "5 分钟" }));
    await user.click(screen.getByRole("button", { name: "发放一次性令牌" }));

    expect(JSON.parse(String(fetchMock.mock.calls[0][1].body)).expires_in_seconds).toBe(300);
  });

  it("机器名为空或权限全不选时本地拦下，不发请求", async () => {
    const user = userEvent.setup();
    const fetchMock = stubFetch(() => {
      throw new Error("不该发出请求");
    });
    renderModal();

    await user.click(screen.getByRole("button", { name: "发放一次性令牌" }));
    expect(screen.getByText(/得先说清楚它在哪台机器上跑/)).toBeDefined();

    await user.type(screen.getByLabelText(/它会在哪台机器上跑/), "mac-studio-01");
    await user.click(screen.getByLabelText(/记录它的会话/));
    await user.click(screen.getByLabelText(/检索团队记忆/));
    await user.click(screen.getByRole("button", { name: "发放一次性令牌" }));
    expect(screen.getByText(/至少要选一项/)).toBeDefined();

    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("4xx 保留表单让用户改", async () => {
    const user = userEvent.setup();
    stubFetch(() => apiErrorResponse(422, "invalid_argument", "bad label"));
    renderModal();

    await user.type(screen.getByLabelText(/它会在哪台机器上跑/), "mac-studio-01");
    await user.click(screen.getByRole("button", { name: "发放一次性令牌" }));

    expect(await screen.findByText(/被拒绝（HTTP 422）/)).toBeDefined();
    expect(screen.getByLabelText(/它会在哪台机器上跑/)).toBeDefined();
  });

  it("5xx 不重试：只发一次请求，走 onMaybeCreated", async () => {
    // 一次性密钥端点没有 Idempotency-Key，盲目重试可能凭空多出一条待认领
    // 记录（doc 3.3）。
    const user = userEvent.setup();
    const onMaybeCreated = vi.fn();
    const fetchMock = stubFetch(() => apiErrorResponse(503, "unavailable", "down"));
    renderModal({ onMaybeCreated });

    await user.type(screen.getByLabelText(/它会在哪台机器上跑/), "mac-studio-01");
    await user.click(screen.getByRole("button", { name: "发放一次性令牌" }));

    await vi.waitFor(() => expect(onMaybeCreated).toHaveBeenCalledTimes(1));
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("请求不带 Idempotency-Key", async () => {
    const user = userEvent.setup();
    const fetchMock = stubFetch(() =>
      jsonResponse({ enrollment_id: "enr_01", token: "t", expires_at: "2099-01-01T00:00:00Z" }),
    );
    renderModal();

    await user.type(screen.getByLabelText(/它会在哪台机器上跑/), "mac-studio-01");
    await user.click(screen.getByRole("button", { name: "发放一次性令牌" }));

    const headers = new Headers(fetchMock.mock.calls[0][1].headers);
    expect(headers.get("Idempotency-Key")).toBeNull();
  });
});
```

- [ ] **Step 2: 跑测试确认红**

Run: `npm --prefix web test -- issue-access`
Expected: FAIL —— 找不到 `../src/components/IssueAccessModal`

- [ ] **Step 3: 实现**

`web/src/components/IssueAccessModal.tsx`：

```tsx
// 发放一次性接入令牌。
//
// 权限用人类标签呈现，但原始名以小字并排显示——API 文档与审计日志用的是
// 原始名，藏起来会让用户没法把界面和文档对上。
//
// 这个端点返回一次性密钥且不支持 Idempotency-Key（doc 3.3）：
// 4xx 保留表单让用户改；超时/5xx 关闭表单、刷新待认领列表，绝不自动重试。

import { useState } from "react";
import { createEnrollment } from "../api/actions";
import { apiError } from "../api/client";
import type { EnrollmentSecret } from "../api/types";
import { GRANTABLE_PERMISSIONS } from "../api/types";
import { Button } from "./Button";
import { Modal } from "./Modal";
import { Seg } from "./Seg";
import { useToast } from "./Toasts";

/** 原始权限名 → 人类标签。键必须覆盖 GRANTABLE_PERMISSIONS 全集。 */
const PERMISSION_LABELS: Record<string, string> = {
  observe: "记录它的会话",
  search: "检索团队记忆",
  get: "读取指定笔记",
  channel_send: "发送给其他 Agent",
  channel_receive: "接收其他 Agent 的消息",
};

type ClaimWindow = "300" | "900" | "1800";
type KeyLifetime = "30" | "90" | "365" | "none";

const CLAIM_WINDOWS: { value: ClaimWindow; label: string }[] = [
  { value: "300", label: "5 分钟" },
  { value: "900", label: "15 分钟" },
  { value: "1800", label: "30 分钟" },
];

const KEY_LIFETIMES: { value: KeyLifetime; label: string }[] = [
  { value: "30", label: "30 天" },
  { value: "90", label: "90 天" },
  { value: "365", label: "1 年" },
  { value: "none", label: "不过期" },
];

const DAY_MS = 24 * 60 * 60 * 1000;

function credentialExpiry(lifetime: KeyLifetime): string | undefined {
  if (lifetime === "none") return undefined;
  return new Date(Date.now() + Number(lifetime) * DAY_MS).toISOString();
}

export function IssueAccessModal({
  agentId,
  agentName,
  onClose,
  onCreated,
  onMaybeCreated,
}: {
  agentId: string;
  agentName: string;
  onClose: () => void;
  onCreated: (secret: EnrollmentSecret) => void;
  onMaybeCreated: () => void;
}) {
  const toast = useToast();
  const [label, setLabel] = useState("");
  const [permissions, setPermissions] = useState<string[]>(["observe", "search"]);
  const [claimWindow, setClaimWindow] = useState<ClaimWindow>("900");
  const [lifetime, setLifetime] = useState<KeyLifetime>("90");
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | undefined>();

  const submit = async () => {
    if (!label.trim()) return setFormError("得先说清楚它在哪台机器上跑。");
    if (permissions.length === 0) {
      return setFormError("至少要选一项它能对团队记忆做的事。");
    }
    setFormError(undefined);
    setBusy(true);
    try {
      const secret = await createEnrollment(agentId, {
        credential_label: label.trim(),
        // 保持 GRANTABLE_PERMISSIONS 的顺序，请求体稳定、便于比对。
        permissions: GRANTABLE_PERMISSIONS.filter((p) => permissions.includes(p)),
        expires_in_seconds: Number(claimWindow),
        credential_expires_at: credentialExpiry(lifetime),
      });
      onCreated(secret);
    } catch (err) {
      if (apiError(err) && err.status < 500) {
        setFormError(`请求被拒绝（HTTP ${err.status}），检查一下填的内容。`);
      } else {
        toast(
          "warn",
          "请求失败，没有自动重试。列表已刷新：如果多出一条待认领记录，用它或取消它再重发一次。",
        );
        onMaybeCreated();
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal title={`给 ${agentName} 发放接入权限`} onClose={onClose}>
      <label htmlFor="ia-label">它会在哪台机器上跑？</label>
      <input
        id="ia-label"
        type="text"
        placeholder="mac-studio-01"
        value={label}
        onChange={(e) => setLabel(e.target.value)}
      />

      <label>它可以对团队记忆做什么？</label>
      {GRANTABLE_PERMISSIONS.map((p) => (
        <label key={p} className="ck">
          <input
            type="checkbox"
            checked={permissions.includes(p)}
            onChange={(e) =>
              setPermissions((prev) => (e.target.checked ? [...prev, p] : prev.filter((x) => x !== p)))
            }
          />
          {PERMISSION_LABELS[p] ?? p} <span className="small muted mono">{p}</span>
        </label>
      ))}

      <div className="field-row">
        <div>
          <label>认领窗口</label>
          <Seg
            label="认领窗口"
            options={CLAIM_WINDOWS}
            value={claimWindow}
            onChange={setClaimWindow}
          />
        </div>
        <div>
          <label>密钥有效期</label>
          <Seg label="密钥有效期" options={KEY_LIFETIMES} value={lifetime} onChange={setLifetime} />
        </div>
      </div>

      {formError && <div className="note bad">{formError}</div>}
      <div className="note small">
        令牌只在下一屏出现一次。这个端点<b>不支持重试保护</b>：如果请求超时，别重发——
        刷新待认领列表，确认没多出记录再操作。
      </div>
      <div className="row" style={{ justifyContent: "flex-end" }}>
        <Button variant="ghost" onClick={onClose} disabled={busy}>
          取消
        </Button>
        <Button variant="primary" disabled={busy} onClick={() => void submit()}>
          {busy ? "发放中…" : "发放一次性令牌"}
        </Button>
      </div>
    </Modal>
  );
}
```

注意 `permissions` 的 `filter` 返回 `readonly` 元组的成员类型，赋给 `string[]` 需要展开：写成 `[...GRANTABLE_PERMISSIONS].filter((p) => permissions.includes(p))`。

- [ ] **Step 4: 跑测试确认绿**

Run: `npm --prefix web test -- issue-access`
Expected: PASS（8 个用例）

- [ ] **Step 5: 变异验证**

把 5xx 分支临时改成重试一次（连发两次 `createEnrollment`），重跑：「5xx 不重试」用例必须红。改回。

- [ ] **Step 6: 提交**

```bash
git add web/src/components/IssueAccessModal.tsx web/tests/issue-access.dom.test.tsx
git commit -m "feat(web): 发放接入令牌表单改用人类语言与预设档位"
```

---

## Task 6: Identity 卡

**Files:**
- Create: `web/src/pages/agent/AgentIdentityCard.tsx`
- Modify: `web/src/styles/features/agent-detail.css`（追加 Identity 段样式）
- Test: `web/tests/agent-detail.dom.test.tsx`（本任务先建立新文件骨架，只测 Identity；后续任务继续往里加）

**Interfaces:**
- Consumes: `AgentAccess` from Task 1；`updateAgent(scope, agentId, patch, version)` from `web/src/api/actions.ts`；`apiError`；`validateDisplayName` from `web/src/lib/validation.ts`；`useErrorHandler`、`useToast`、`Button`、`Card`
- Produces: `AgentIdentityCard({ agent, access, onChanged, refetch })`
  - `agent: AgentProfile`、`access: AgentAccess`
  - `onChanged: (agent: AgentProfile) => void`
  - `refetch: () => Promise<AgentProfile>`

**行为：**
- 四个字段：显示名（`display_name`）、Runtime（`agent_type`，**保持自由文本框**——后端不枚举取值，单选框会谎称存在封闭集合）、它做什么（`description`）、列进团队目录（`directory_visible`）。
- 草稿在 `agent` 变化时重置（加载、保存成功、409 重取三种情形共用）。
- 409 `resource_version_conflict`：重取 → `onChanged(fresh)` → warn toast「有人改过它，已刷新到最新」，**不覆盖**。
- `access.canEdit` 为假时全部字段 disabled 且不渲染保存按钮。

- [ ] **Step 1: 写失败测试**

新建 `web/tests/agent-detail.dom.test.tsx`（**覆盖原文件**；原文件测的是即将删除的两个页面）：

```tsx
// Agent 详情页（阶段 4 合并版）的页面级 DOM 测试。
// 本文件按任务逐步长大：Task 6 加 Identity，Task 7 加 Lifecycle，
// Task 8 加 Keys，Task 9 加 Header/Behaviour，Task 10 加 scope 分支与降级。
import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import {
  callsTo,
  jsonResponse,
  makeAgent,
  makeMe,
  renderApp,
  setupDomTest,
} from "./helpers";

setupDomTest();

/** 详情 GET + 两条空的密钥腿；未预期的请求直接抛错。 */
export function agentFetch(
  scope: "me" | "admin",
  agent: ReturnType<typeof makeAgent> | Response,
  extra?: (path: string, init: RequestInit) => Response | undefined,
) {
  const base = `/v1/${scope}/agents/agent-1`;
  return (path: string, init: RequestInit) => {
    const fromExtra = extra?.(path, init);
    if (fromExtra) return fromExtra;
    if (path === base && (init.method ?? "GET") === "GET") {
      return agent instanceof Response ? agent : jsonResponse({ agent });
    }
    if (path.startsWith(`${base}/enrollments`)) return jsonResponse({ enrollments: [] });
    if (path.startsWith(`${base}/credentials`)) return jsonResponse({ credentials: [] });
    if (path.startsWith("/v1/admin/session-audit/activity")) return jsonResponse({ activity: [] });
    throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
  };
}

describe("Identity 卡", () => {
  it("member 看自己的 Agent：字段可编辑且有保存按钮", async () => {
    await renderApp({
      route: "/management/agents/agent-1",
      me: makeMe({ role: "member", membership_id: "mbr_01" }),
      fetch: agentFetch("me", makeAgent({ owner_membership_id: "mbr_01" })),
    });

    const name = (await screen.findByLabelText("显示名")) as HTMLInputElement;
    expect(name.value).toBe("Alice Codex");
    expect(name.disabled).toBe(false);
    expect((screen.getByLabelText("Runtime") as HTMLInputElement).value).toBe("codex");
    expect(screen.getByRole("button", { name: "保存改动" })).toBeDefined();
  });

  it("保存时 resource_version 同时进 body 与 If-Match", async () => {
    const { fetchMock, user } = await renderApp({
      route: "/management/agents/agent-1",
      me: makeMe({ role: "member", membership_id: "mbr_01" }),
      fetch: agentFetch("me", makeAgent({ owner_membership_id: "mbr_01" }), (path, init) => {
        if (path === "/v1/me/agents/agent-1" && init.method === "PATCH") {
          return jsonResponse({
            agent: makeAgent({ owner_membership_id: "mbr_01", display_name: "Renamed", resource_version: 8 }),
          });
        }
        return undefined;
      }),
    });

    const name = await screen.findByLabelText("显示名");
    await user.clear(name);
    await user.type(name, "Renamed");
    await user.click(screen.getByRole("button", { name: "保存改动" }));

    const patches = callsTo(fetchMock, "/v1/me/agents/agent-1", "PATCH");
    expect(patches).toHaveLength(1);
    expect(patches[0].headers.get("If-Match")).toBe('"7"');
    expect(JSON.parse(String(patches[0].init.body))).toMatchObject({
      display_name: "Renamed",
      resource_version: 7,
    });
  });

  it("409 冲突时重取并用服务端数据覆盖草稿，而不是反过来", async () => {
    let patched = false;
    const { user } = await renderApp({
      route: "/management/agents/agent-1",
      me: makeMe({ role: "member", membership_id: "mbr_01" }),
      fetch: agentFetch(
        "me",
        makeAgent({ owner_membership_id: "mbr_01" }),
        (path, init) => {
          if (path === "/v1/me/agents/agent-1" && init.method === "PATCH") {
            patched = true;
            return jsonResponse({ code: "resource_version_conflict", message: "stale" }, 409);
          }
          if (path === "/v1/me/agents/agent-1" && patched && (init.method ?? "GET") === "GET") {
            return jsonResponse({
              agent: makeAgent({
                owner_membership_id: "mbr_01",
                display_name: "Someone Else Renamed It",
                resource_version: 9,
              }),
            });
          }
          return undefined;
        },
      ),
    });

    const name = await screen.findByLabelText("显示名");
    await user.clear(name);
    await user.type(name, "My Local Draft");
    await user.click(screen.getByRole("button", { name: "保存改动" }));

    // 草稿被服务端数据替换——绝不静默覆盖别人的改动。
    expect(await screen.findByDisplayValue("Someone Else Renamed It")).toBeDefined();
  });

  it("admin 看别人的 Agent：字段只读且没有保存按钮", async () => {
    await renderApp({
      route: "/management/agents/agent-1",
      me: makeMe({ role: "admin", membership_id: "mbr_07" }),
      fetch: agentFetch("admin", makeAgent({ owner_membership_id: "mbr_99" })),
    });

    expect(((await screen.findByLabelText("显示名")) as HTMLInputElement).disabled).toBe(true);
    expect(screen.queryByRole("button", { name: "保存改动" })).toBeNull();
  });
});
```

**注意：** 这些用例要能跑通，需要 Task 10 才完成的页面装配。本任务只需实现 `AgentIdentityCard` 并在 `AgentDetailPage` 里**临时**渲染它（保留现有的取数与 404 逻辑，把旧的 `AgentDetailView` 调用替换为 Header 占位 + `AgentIdentityCard`），使这四条用例转绿；其余段落在后续任务补齐。若临时装配代价过大，允许本任务改用直接 `render(<AgentIdentityCard …/>)` 的组件级测试，并在 Task 10 补页面级用例——**两者取其一，不要都不做**。

- [ ] **Step 2: 跑测试确认红**

Run: `npm --prefix web test -- agent-detail`
Expected: FAIL

- [ ] **Step 3: 实现**

`web/src/pages/agent/AgentIdentityCard.tsx`：

```tsx
// Identity：Agent 的身份字段与乐观锁保存。
//
// Runtime（agent_type）保持自由文本框：后端不枚举也不校验取值，设计稿的
// Codex/Claude/Custom 单选会谎称存在一个封闭集合（spec §8 第 2 条）。
//
// 乐观锁：每次更新把 resource_version 同时放进 body 与 If-Match。
// 遇到 resource_version_conflict 就重取并重置表单——本地草稿绝不覆盖
// 别人已经落库的改动。

import { useEffect, useState } from "react";
import { updateAgent } from "../../api/actions";
import { apiError } from "../../api/client";
import type { AgentProfile } from "../../api/types";
import { Button } from "../../components/Button";
import { Card } from "../../components/Card";
import { useToast } from "../../components/Toasts";
import { useErrorHandler } from "../../lib/useErrorHandler";
import { validateDisplayName } from "../../lib/validation";
import type { AgentAccess } from "./agentScope";

export function AgentIdentityCard({
  agent,
  access,
  onChanged,
  refetch,
}: {
  agent: AgentProfile;
  access: AgentAccess;
  onChanged: (agent: AgentProfile) => void;
  refetch: () => Promise<AgentProfile>;
}) {
  const toast = useToast();
  const handleError = useErrorHandler();
  const [name, setName] = useState(agent.display_name);
  const [type, setType] = useState(agent.agent_type);
  const [description, setDescription] = useState(agent.description);
  const [visible, setVisible] = useState(agent.directory_visible);
  const [busy, setBusy] = useState(false);

  // 权威数据一变就重置草稿：加载、保存成功、409 重取三种情形共用。
  useEffect(() => {
    setName(agent.display_name);
    setType(agent.agent_type);
    setDescription(agent.description);
    setVisible(agent.directory_visible);
  }, [agent]);

  const disabled = !access.canEdit;

  const save = async () => {
    const nameError = validateDisplayName(name);
    if (nameError) return toast("warn", nameError);
    setBusy(true);
    try {
      const updated = await updateAgent(
        access.actScope,
        agent.agent_id,
        {
          display_name: name.trim(),
          description,
          agent_type: type.trim(),
          directory_visible: visible,
        },
        agent.resource_version,
      );
      onChanged(updated);
      toast("ok", `已保存（第 ${updated.resource_version} 版）`);
    } catch (err) {
      if (apiError(err, 409, "resource_version_conflict")) {
        const fresh = await refetch();
        onChanged(fresh);
        toast("warn", "有人在你之前改过它，已刷新到最新——你的改动没有提交。");
      } else {
        handleError(err);
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card title="Identity">
      <div className="field-row">
        <div>
          <label htmlFor="ag-name">显示名</label>
          <input
            id="ag-name"
            type="text"
            value={name}
            disabled={disabled}
            onChange={(e) => setName(e.target.value)}
          />
        </div>
        <div>
          <label htmlFor="ag-type">Runtime</label>
          <input
            id="ag-type"
            type="text"
            value={type}
            disabled={disabled}
            onChange={(e) => setType(e.target.value)}
          />
        </div>
      </div>
      <label htmlFor="ag-desc">它做什么</label>
      <textarea
        id="ag-desc"
        rows={2}
        value={description}
        disabled={disabled}
        onChange={(e) => setDescription(e.target.value)}
      />
      <label className="ck">
        <input
          type="checkbox"
          checked={visible}
          disabled={disabled}
          onChange={(e) => setVisible(e.target.checked)}
        />
        列进团队目录
        <span className="small muted"> —— 其他 Agent 能找到它并向它发送知识胶囊。</span>
      </label>
      {access.canEdit && (
        <div className="row between">
          <span className="small muted">
            第 <code>{agent.resource_version}</code> 版（提交时同时进 body 与 <code>If-Match</code>）
          </span>
          <Button variant="primary" size="sm" disabled={busy} onClick={() => void save()}>
            保存改动
          </Button>
        </div>
      )}
    </Card>
  );
}
```

- [ ] **Step 4: 跑测试确认绿**

Run: `npm --prefix web test -- agent-detail`
Expected: PASS（4 个用例）

- [ ] **Step 5: 提交**

```bash
git add web/src/pages/agent/AgentIdentityCard.tsx web/tests/agent-detail.dom.test.tsx web/src/pages/AgentDetailPage.tsx
git commit -m "feat(web): Agent Identity 卡与乐观锁保存"
```

---

## Task 7: Lifecycle 卡

**Files:**
- Create: `web/src/pages/agent/AgentLifecycleCard.tsx`
- Modify: `web/src/styles/features/agent-detail.css`（追加 `.ag-action` 行样式）
- Test: `web/tests/agent-governance.dom.test.tsx`（**覆盖原文件**）

**Interfaces:**
- Consumes: `AgentAccess` from Task 1；`KeyLeg` from Task 2；`updateAgent`、`retireAgent`、`transferAgent`、`beginAction` from `web/src/api/actions.ts`；`listAllMembers` from `web/src/api/queries.ts`；`ConfirmDialog`（已有 `children` 槽）
- Produces: `AgentLifecycleCard({ agent, access, pendingEnrollments, activeCredentials, onChanged, refetch })`
  - `pendingEnrollments: EnrollmentMetadata[] | undefined`
  - `activeCredentials: CredentialMetadata[] | undefined`

**四种动作，卡的可见性：**

| 卡 | 条件 | 后果文案 |
|---|---|---|
| 暂停这个 Agent | `canSuspend && status === "active"` | 它会立刻停止读写团队记忆。密钥被销毁，不是暂存。 |
| 恢复运行 | `canResume && status === "suspended"` | 恢复后它能重新读写，但旧密钥不会回来——你要重新发一次接入令牌。 |
| 永久退役 | `canRetire` | 终局。这个身份永远无法再启用，ID 也不能重用。 |
| 移交给别人 | `canTransfer` | 把身份交给另一个人，并吊销当前所有者签发的每一把密钥。 |

暂停与恢复按状态互斥，占同一个位置。

**确认弹窗：** 暂停与退役用 `ConfirmDialog` 的 `children` 槽渲染将被销毁的清单——**直接渲染传入的 `pendingEnrollments` / `activeCredentials` 数组**，不另取数、不自己算数。数组为 `undefined`（那条腿失败）时，清单位置显示「这条清单没取到，销毁范围可能比这里显示的多」，而不是显示 0。

**移交：** 打开弹窗时 `listAllMembers()` 取成员列表，用 `<select>` 选目标；确认后调 `transferAgent(agent.agent_id, targetMembershipId, agent.resource_version)`——**注意这个动作恒走 admin 端点**，与 `access.actScope` 无关。

- [ ] **Step 1: 写失败测试**

`web/tests/agent-governance.dom.test.tsx`（覆盖原文件）：

```tsx
// Lifecycle 三张卡与它们的确认弹窗。
//
// 最关键的一条：销毁类确认框里列出的密钥，就是页面上那两张卡渲染的同一个
// 数组——不是重新数的。这条由「同一个 props 数组」保证，测试用变异验证。
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { AgentLifecycleCard } from "../src/pages/agent/AgentLifecycleCard";
import { resolveAgentAccess } from "../src/pages/agent/agentScope";
import { ToastProvider } from "../src/components/Toasts";
import {
  callsTo,
  jsonResponse,
  makeAgent,
  makeCredential,
  makeEnrollment,
  makeMe,
  makeMember,
  setupDomTest,
  stubFetch,
} from "./helpers";

setupDomTest();

function renderCard(options: {
  me?: ReturnType<typeof makeMe>;
  agent?: ReturnType<typeof makeAgent>;
  credentials?: ReturnType<typeof makeCredential>[];
  enrollments?: ReturnType<typeof makeEnrollment>[];
}) {
  const me = options.me ?? makeMe({ role: "member", membership_id: "mbr_01" });
  const agent = options.agent ?? makeAgent({ owner_membership_id: "mbr_01" });
  render(
    <MemoryRouter>
      <ToastProvider>
        <AgentLifecycleCard
          agent={agent}
          access={resolveAgentAccess(me, agent)}
          pendingEnrollments={options.enrollments ?? []}
          activeCredentials={options.credentials ?? []}
          onChanged={() => {}}
          refetch={() => Promise.resolve(agent)}
        />
      </ToastProvider>
    </MemoryRouter>,
  );
}

describe("卡的可见性", () => {
  it("member 看自己的活跃 Agent：暂停 + 退役，没有移交", () => {
    renderCard({});
    expect(screen.getByRole("button", { name: "暂停" })).toBeDefined();
    expect(screen.getByRole("button", { name: "退役" })).toBeDefined();
    expect(screen.queryByRole("button", { name: "移交" })).toBeNull();
    expect(screen.queryByRole("button", { name: "恢复" })).toBeNull();
  });

  it("suspended 状态下暂停换成恢复", () => {
    renderCard({ agent: makeAgent({ owner_membership_id: "mbr_01", status: "suspended" }) });
    expect(screen.getByRole("button", { name: "恢复" })).toBeDefined();
    expect(screen.queryByRole("button", { name: "暂停" })).toBeNull();
  });

  it("owner 看别人的 Agent：四种动作都在", () => {
    renderCard({
      me: makeMe({ role: "owner", membership_id: "mbr_01" }),
      agent: makeAgent({ owner_membership_id: "mbr_99" }),
    });
    expect(screen.getByRole("button", { name: "暂停" })).toBeDefined();
    expect(screen.getByRole("button", { name: "退役" })).toBeDefined();
    expect(screen.getByRole("button", { name: "移交" })).toBeDefined();
  });

  it("admin 看别人的 Agent：只有暂停", () => {
    renderCard({
      me: makeMe({ role: "admin", membership_id: "mbr_07" }),
      agent: makeAgent({ owner_membership_id: "mbr_99" }),
    });
    expect(screen.getByRole("button", { name: "暂停" })).toBeDefined();
    expect(screen.queryByRole("button", { name: "退役" })).toBeNull();
    expect(screen.queryByRole("button", { name: "移交" })).toBeNull();
  });

  it("退役后三张卡都消失，只剩终态说明", () => {
    renderCard({
      agent: makeAgent({ owner_membership_id: "mbr_01", status: "retired", retired_at: "2026-08-01T00:00:00Z" }),
    });
    expect(screen.queryByRole("button", { name: "暂停" })).toBeNull();
    expect(screen.queryByRole("button", { name: "退役" })).toBeNull();
    expect(screen.getByText(/终态，无法恢复/)).toBeDefined();
  });
});

describe("销毁类确认框的后果清单", () => {
  it("列出的密钥就是卡片拿到的那两个数组", async () => {
    const user = userEvent.setup();
    renderCard({
      credentials: [
        makeCredential({ credential_id: "cred_a", label: "mac-studio-01" }),
        makeCredential({ credential_id: "cred_b", label: "linux-box" }),
      ],
      enrollments: [makeEnrollment({ enrollment_id: "enr_a", credential_label: "待认领的机器" })],
    });

    await user.click(screen.getByRole("button", { name: "暂停" }));
    const dialog = screen.getByRole("dialog");
    expect(dialog.textContent).toContain("mac-studio-01");
    expect(dialog.textContent).toContain("linux-box");
    expect(dialog.textContent).toContain("待认领的机器");
    expect(dialog.textContent).toContain("2 把活跃密钥");
    expect(dialog.textContent).toContain("1 张未认领令牌");
  });

  it("某条腿没取到时说「可能更多」，而不是显示 0", async () => {
    const user = userEvent.setup();
    const me = makeMe({ role: "member", membership_id: "mbr_01" });
    const agent = makeAgent({ owner_membership_id: "mbr_01" });
    render(
      <MemoryRouter>
        <ToastProvider>
          <AgentLifecycleCard
            agent={agent}
            access={resolveAgentAccess(me, agent)}
            pendingEnrollments={undefined}
            activeCredentials={undefined}
            onChanged={() => {}}
            refetch={() => Promise.resolve(agent)}
          />
        </ToastProvider>
      </MemoryRouter>,
    );

    await user.click(screen.getByRole("button", { name: "暂停" }));
    const dialog = screen.getByRole("dialog");
    expect(dialog.textContent).toContain("没取到");
    expect(dialog.textContent).not.toContain("0 把活跃密钥");
  });
});

describe("动作请求", () => {
  it("暂停走 PATCH status=suspended，带 If-Match", async () => {
    const user = userEvent.setup();
    const fetchMock = stubFetch(() =>
      jsonResponse({ agent: makeAgent({ owner_membership_id: "mbr_01", status: "suspended" }) }),
    );
    renderCard({});

    await user.click(screen.getByRole("button", { name: "暂停" }));
    await user.click(screen.getByRole("button", { name: "暂停并销毁密钥" }));

    const patches = callsTo(fetchMock, "/v1/me/agents/agent-1", "PATCH");
    expect(patches).toHaveLength(1);
    expect(JSON.parse(String(patches[0].init.body))).toMatchObject({
      status: "suspended",
      resource_version: 7,
    });
    expect(patches[0].headers.get("If-Match")).toBe('"7"');
  });

  it("退役走 DELETE 且带 Idempotency-Key", async () => {
    const user = userEvent.setup();
    const fetchMock = stubFetch(() =>
      jsonResponse({ agent: makeAgent({ owner_membership_id: "mbr_01", status: "retired" }) }),
    );
    renderCard({});

    await user.click(screen.getByRole("button", { name: "退役" }));
    await user.click(screen.getByRole("button", { name: "永久退役" }));

    const deletes = callsTo(fetchMock, "/v1/me/agents/agent-1", "DELETE");
    expect(deletes).toHaveLength(1);
    expect(deletes[0].headers.get("Idempotency-Key")).toBeTruthy();
  });

  it("移交恒走 admin 端点，即使动作 scope 是 me", async () => {
    // owner 移交自己的 Agent：页面其余动作走 /v1/me/*，移交没有 me 端点。
    const user = userEvent.setup();
    const fetchMock = stubFetch((path) => {
      if (path.startsWith("/v1/admin/members")) {
        return jsonResponse({ members: [makeMember({ membership_id: "mbr_99", display_name: "Bob" })] });
      }
      return jsonResponse({ agent: makeAgent({ owner_membership_id: "mbr_99" }) });
    });
    renderCard({
      me: makeMe({ role: "owner", membership_id: "mbr_01" }),
      agent: makeAgent({ owner_membership_id: "mbr_01" }),
    });

    await user.click(screen.getByRole("button", { name: "移交" }));
    await user.selectOptions(await screen.findByLabelText("交给谁"), "mbr_99");
    await user.click(screen.getByRole("button", { name: "移交并吊销密钥" }));

    const posts = callsTo(fetchMock, "/v1/admin/agents/agent-1/transfer", "POST");
    expect(posts).toHaveLength(1);
    expect(JSON.parse(String(posts[0].init.body))).toMatchObject({
      target_membership_id: "mbr_99",
      resource_version: 7,
    });
  });
});
```

- [ ] **Step 2: 跑测试确认红**

Run: `npm --prefix web test -- agent-governance`
Expected: FAIL —— 找不到 `AgentLifecycleCard`

- [ ] **Step 3: 实现**

`web/src/pages/agent/AgentLifecycleCard.tsx`（完整实现，注意四点）：

1. `pending` 状态形如 `{ kind: "suspend" | "resume" | "retire" | "transfer"; key: string }`，`key` 来自 `beginAction()`，一个弹窗一个键、重试复用同一个键。
2. 后果清单直接映射传入数组；`undefined` 走「没取到」分支。
3. 移交在 `pending.kind === "transfer"` 时用 `useEffect` 触发 `listAllMembers()`，选中项存在本地 state。
4. 409 `resource_version_conflict` → 关弹窗 + `refetch()` + `onChanged` + warn toast。

卡片行结构：

```tsx
<div className="ag-action">
  <div>
    <div className="ag-action-name">暂停这个 Agent</div>
    <div className="ag-action-why">它会立刻停止读写团队记忆。密钥被销毁，不是暂存。</div>
  </div>
  <Button size="sm" onClick={() => setPending({ kind: "suspend", key: beginAction() })}>
    暂停
  </Button>
</div>
```

后果清单渲染（暂停与退役共用一个函数）：

```tsx
function DestructionPreview({
  enrollments,
  credentials,
}: {
  enrollments?: EnrollmentMetadata[];
  credentials?: CredentialMetadata[];
}) {
  if (enrollments === undefined || credentials === undefined) {
    return (
      <p className="small muted">
        这台 Agent 的密钥清单没取到，销毁范围可能比这里显示的多。
      </p>
    );
  }
  return (
    <div className="small">
      <p>
        这会销毁 <b>{credentials.length} 把活跃密钥</b> 和{" "}
        <b>{enrollments.length} 张未认领令牌</b>：
      </p>
      <ul>
        {credentials.map((c) => (
          <li key={c.credential_id}>{c.label}</li>
        ))}
        {enrollments.map((e) => (
          <li key={e.enrollment_id}>{e.credential_label}（待认领）</li>
        ))}
      </ul>
    </div>
  );
}
```

样式追加到 `web/src/styles/features/agent-detail.css`：

```css
.ag-action { display: flex; align-items: center; gap: var(--space-4); border: 1px solid var(--color-divider); padding: var(--space-3) var(--space-4); }
.ag-action > div:first-child { flex: 1; }
.ag-action-name { font-family: var(--font-heading); font-weight: var(--font-heading-weight); font-size: 14px; }
.ag-action-why { font-size: 12px; opacity: 0.65; }
.ag-actions { display: grid; gap: var(--space-2); }
```

确认弹窗按钮文案（测试依赖，必须逐字一致）：暂停 → `暂停并销毁密钥`；恢复 → `恢复运行`；退役 → `永久退役`；移交 → `移交并吊销密钥`。移交的目标选择 `<label htmlFor>` 文案为 `交给谁`。

- [ ] **Step 4: 跑测试确认绿**

Run: `npm --prefix web test -- agent-governance`
Expected: PASS（10 个用例）

- [ ] **Step 5: 变异验证**

把 `DestructionPreview` 的 `credentials` 临时改成 `credentials.slice(0, 1)`，重跑：「列出的密钥就是卡片拿到的那两个数组」必须红（`linux-box` 与「2 把活跃密钥」两处都该失败）。改回。

- [ ] **Step 6: 提交**

```bash
git add web/src/pages/agent/AgentLifecycleCard.tsx web/src/styles/features/agent-detail.css web/tests/agent-governance.dom.test.tsx
git commit -m "feat(web): Agent 生命周期三卡与销毁后果同源预览"
```

---

## Task 8: 密钥两卡

**Files:**
- Create: `web/src/pages/agent/AgentKeysSection.tsx`
- Modify: `web/src/styles/features/agent-detail.css`
- Test: `web/tests/agent-artifacts.dom.test.tsx`（**覆盖原文件**）

**Interfaces:**
- Consumes: `AgentKeys`、`KeyLeg` from Task 2；`AgentAccess` from Task 1；`IssueAccessModal` from Task 5；`SecretCeremony` from Task 3；`revokeEnrollment`、`revokeCredential`、`beginAction`；`usePagedList`、`listEnrollments`、`listCredentials`（历史用）；`ConfirmDialog`、`RegionError`、`EmptyState`、`Countdown`、`Badge`
- Produces: `AgentKeysSection({ agent, access, keys })`，`keys: AgentKeys`

**两张卡：**

1. **Waiting to be claimed** —— 渲染 `keys.enrollments`。卡头右侧小字「一次性令牌 · 从不存储，也不会再次发送」；`access.canIssue` 时卡头有「发放接入权限」按钮。每行：标签、权限、`enrollment_id`、倒计时、取消按钮（`access.canRevoke`）。
2. **Active keys** —— 渲染 `keys.credentials`。表头：在哪运行 / 能做什么 / 最近使用 / 到期 / （吊销）。

**每张卡三态：** `loading` → 「加载中…」；`error` → `<RegionError onRetry={keys.reload} />`；空 → `<EmptyState>`。

**历史开关：** 每张卡底部一个「显示历史」按钮，点开后**才**挂 `usePagedList`（分别是 `listEnrollments(scope, id, {cursor})` 与 `listCredentials(scope, id, {cursor})`，不带 status，取全部状态）。历史区只读，不放吊销按钮。

**发放流程：** 「发放接入权限」→ `IssueAccessModal` → `onCreated(secret)` 关表单、`setSecret(secret)`、`keys.reload()`；仪式关闭时 `setSecret(undefined)` 并 toast 提示「令牌不再可见，但它仍在有效期内可被兑换」。

- [ ] **Step 1: 写失败测试**

`web/tests/agent-artifacts.dom.test.tsx`（覆盖原文件）。至少覆盖：

```tsx
// 密钥两卡：三态、吊销、历史开关、发放到仪式的完整链路。
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AgentKeysSection } from "../src/pages/agent/AgentKeysSection";
import { resolveAgentAccess } from "../src/pages/agent/agentScope";
import { ToastProvider } from "../src/components/Toasts";
import {
  callsTo,
  jsonResponse,
  makeAgent,
  makeCredential,
  makeEnrollment,
  makeMe,
  setupDomTest,
  stubFetch,
} from "./helpers";

setupDomTest();

function renderKeys(keys: {
  enrollments: { items?: unknown[]; error?: unknown; loading: boolean };
  credentials: { items?: unknown[]; error?: unknown; loading: boolean };
  reload?: () => void;
}, me = makeMe({ role: "member", membership_id: "mbr_01" })) {
  const agent = makeAgent({ owner_membership_id: "mbr_01" });
  return render(
    <ToastProvider>
      <AgentKeysSection
        agent={agent}
        access={resolveAgentAccess(me, agent)}
        keys={{ reload: keys.reload ?? (() => {}), ...keys } as never}
      />
    </ToastProvider>,
  );
}

describe("三态", () => {
  it("加载中两张卡各显示加载态", () => {
    renderKeys({ enrollments: { loading: true }, credentials: { loading: true } });
    expect(screen.getAllByText("加载中…").length).toBe(2);
  });

  it("一条腿失败不影响另一条", () => {
    renderKeys({
      enrollments: { error: new Error("boom"), loading: false },
      credentials: { items: [makeCredential({ label: "mac-studio-01" })], loading: false },
    });
    expect(screen.getByRole("button", { name: "Retry" })).toBeDefined();
    expect(screen.getByText("mac-studio-01")).toBeDefined();
  });

  it("空列表走正向空态而不是错误", () => {
    renderKeys({
      enrollments: { items: [], loading: false },
      credentials: { items: [], loading: false },
    });
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
    expect(screen.getByText(/还没有待认领的令牌/)).toBeDefined();
    expect(screen.getByText(/还没有任何密钥/)).toBeDefined();
  });
});

describe("吊销", () => {
  it("吊销密钥带 Idempotency-Key 并走动作 scope", async () => {
    const user = userEvent.setup();
    const fetchMock = stubFetch(() => jsonResponse({ credential: makeCredential() }));
    renderKeys({
      enrollments: { items: [], loading: false },
      credentials: { items: [makeCredential({ credential_id: "cred_01", label: "mac-studio-01" })], loading: false },
    });

    await user.click(screen.getByRole("button", { name: "吊销" }));
    await user.click(screen.getByRole("button", { name: "确认吊销" }));

    const calls = callsTo(fetchMock, "/v1/me/agents/agent-1/credentials/cred_01", "DELETE");
    expect(calls).toHaveLength(1);
    expect(calls[0].headers.get("Idempotency-Key")).toBeTruthy();
  });
});

describe("历史", () => {
  it("默认不请求历史，点开后才请求且不带 status", async () => {
    const user = userEvent.setup();
    const fetchMock = stubFetch(() => jsonResponse({ credentials: [makeCredential({ revoked_at: "2026-01-01T00:00:00Z" })] }));
    renderKeys({
      enrollments: { items: [], loading: false },
      credentials: { items: [], loading: false },
    });

    expect(fetchMock).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "显示密钥历史" }));

    await screen.findByText(/revoked/);
    const url = String(fetchMock.mock.calls[0][0]);
    expect(url).toContain("/v1/me/agents/agent-1/credentials");
    expect(url).not.toContain("status=");
  });
});

describe("发放到仪式", () => {
  it("发放成功后进入全屏仪式，关闭时提示令牌仍可兑换", async () => {
    const user = userEvent.setup();
    stubFetch(() =>
      jsonResponse({ enrollment_id: "enr_01", token: "tm_enroll_x.secret-value", expires_at: "2099-01-01T00:00:00Z" }),
    );
    renderKeys({
      enrollments: { items: [], loading: false },
      credentials: { items: [], loading: false },
    });

    await user.click(screen.getByRole("button", { name: "发放接入权限" }));
    await user.type(screen.getByLabelText(/它会在哪台机器上跑/), "mac-studio-01");
    await user.click(screen.getByRole("button", { name: "发放一次性令牌" }));

    expect(await screen.findByText("tm_enroll_x.secret-value")).toBeDefined();
    await user.click(screen.getByRole("button", { name: "我已保存，关闭" }));
    await user.click(screen.getByRole("button", { name: "确定关闭" }));

    expect(screen.queryByText("tm_enroll_x.secret-value")).toBeNull();
    expect(JSON.stringify(sessionStorage)).not.toContain("secret-value");
    expect(JSON.stringify(localStorage)).not.toContain("secret-value");
  });

  it("admin 看别人的 Agent 时没有发放按钮", () => {
    const me = makeMe({ role: "admin", membership_id: "mbr_07" });
    const agent = makeAgent({ owner_membership_id: "mbr_99" });
    render(
      <ToastProvider>
        <AgentKeysSection
          agent={agent}
          access={resolveAgentAccess(me, agent)}
          keys={{ enrollments: { items: [], loading: false }, credentials: { items: [], loading: false }, reload: () => {} }}
        />
      </ToastProvider>,
    );
    expect(screen.queryByRole("button", { name: "发放接入权限" })).toBeNull();
  });
});
```

- [ ] **Step 2: 跑测试确认红**

Run: `npm --prefix web test -- agent-artifacts`
Expected: FAIL

- [ ] **Step 3: 实现**

`web/src/pages/agent/AgentKeysSection.tsx`。要点：

- 两张卡各用 `Card`，卡头右侧的说明文字用 `meta` 槽或卡内 `.row between`。
- 三态判断顺序：`leg.loading` → `leg.error` → `leg.items.length === 0` → 表格。
- 吊销确认用 `ConfirmDialog`，`consequences` 为：
  - 令牌：`["这张一次性令牌立刻失效，正在进行的客户端接入会失败", "无法撤销；需要的话重新发一张"]`，`confirmLabel="确认取消"`
  - 密钥：`["这把 API 密钥立刻失效，持有它的客户端立即失去访问", "无法撤销；需要的话重新发一次接入权限"]`，`confirmLabel="确认吊销"`
- 历史区各自一个 `showHistory` state；为真时才渲染一个内部子组件（`EnrollmentHistory` / `CredentialHistory`），子组件内部调 `usePagedList`——**hook 必须在子组件里，不能条件调用**。
- 仪式关闭：`onClose={() => { setSecret(undefined); toast("ok", "令牌不再可见。它仍在有效期内可被兑换——要作废就在「待认领」里取消它。"); }}`

样式追加：

```css
.ag-keys-note { margin-left: auto; font-size: 11px; opacity: 0.5; }
.ag-enrollment { display: grid; grid-template-columns: 1fr auto auto; gap: var(--space-4); align-items: center; border-top: 1px solid var(--color-divider); padding: var(--space-3) 0; }
.ag-enrollment-id { font-family: var(--font-mono); font-size: 11px; opacity: 0.45; }
```

- [ ] **Step 4: 跑测试确认绿**

Run: `npm --prefix web test -- agent-artifacts`
Expected: PASS（8 个用例）

- [ ] **Step 5: 变异验证**

把「历史」的 `usePagedList` 改成无条件挂载（不看 `showHistory`），重跑：「默认不请求历史」必须红。改回。

- [ ] **Step 6: 提交**

```bash
git add web/src/pages/agent/AgentKeysSection.tsx web/src/styles/features/agent-detail.css web/tests/agent-artifacts.dom.test.tsx
git commit -m "feat(web): Agent 密钥两卡、历史懒加载与发放到仪式的链路"
```

---

## Task 9: 页头与 Recent behaviour

**Files:**
- Create: `web/src/pages/agent/AgentHeader.tsx`、`web/src/pages/agent/AgentBehaviourCard.tsx`
- Modify: `web/src/styles/features/agent-detail.css`
- Test: `web/tests/agent-behaviour.dom.test.tsx`

**Interfaces:**
- Consumes: `AgentAccess`；`getDevice(credentialId)` from `web/src/api/queries.ts`；`listSessionAuditActivity(filter)` from `web/src/api/queries.ts`；`can`、`hasServerCapability` from `web/src/lib/capabilities.ts`；`Crumbs`、`Badge`、`ProvisionedByBadge`、`Button`、`Card`、`MetricTile`
- Produces:
  - `AgentHeader({ agent, access, me })`
  - `AgentBehaviourCard({ agentId })`
  - `sumActivity(days: SessionAuditActivityDay[]): { toolCalls: number; highRisk: number; sessions: number }`（从 `AgentBehaviourCard.tsx` 导出，便于单测）
  - `activityWindow(now: Date): { from_day: string; to_day: string }`（同上）

**AgentHeader：**
- 面包屑：`访问树`（→ `/management`）/ Agent 名。
- 标题行：名字、状态 `Badge`、`agent_type` 标签。
- 归属行：`agent_id`（mono）、`归 <owner_membership_id> 所有`、以及机器归属。
- **机器归属：** `agent.provisioned_by` 存在且 `access.readScope === "admin"` 时调 `getDevice(provisioned_by)` 换机器名，成功显示「在 <机器名> 上自助注册 · 继承那台机器的授权」；失败或 member 视角退回既有的 `ProvisionedByBadge`。
- 两个按钮（各自门控）：`can(me.role, "view.audit")` → 「查看它的会话」链到 `/governance/sessions?agent=<id>`；`hasServerCapability(me, "view.team-memory")` → 「查看它的记忆」链到 `/governance/memory?agent=<id>`。

**AgentBehaviourCard：**
- 只在调用方判定有 `view.audit` 时才渲染（组件自身不做门控，由 `AgentDetailPage` 决定挂不挂）。
- 窗口：`from_day = 今天 − 6 天`、`to_day = 今天`，按浏览器本地日期算出 `YYYY-MM-DD`。
- 三格：工具调用 · 7 天 / 高危 · 7 天 / 会话 · 7 天，值为各天求和。
- 失败 → 一行说明「近期行为没取到」，不影响其余段落。

- [ ] **Step 1: 写失败测试**

`web/tests/agent-behaviour.dom.test.tsx`：

```tsx
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { AgentBehaviourCard, activityWindow, sumActivity } from "../src/pages/agent/AgentBehaviourCard";
import { AgentHeader } from "../src/pages/agent/AgentHeader";
import { resolveAgentAccess } from "../src/pages/agent/agentScope";
import { jsonResponse, makeAgent, makeDevice, makeMe, setupDomTest, stubFetch } from "./helpers";

setupDomTest();

describe("activityWindow", () => {
  it("是含两端的 7 天窗口，按本地日期算", () => {
    expect(activityWindow(new Date(2026, 7, 6))).toEqual({
      from_day: "2026-07-31",
      to_day: "2026-08-06",
    });
  });
});

describe("sumActivity", () => {
  it("把各天的三个计数分别求和", () => {
    expect(
      sumActivity([
        { user_id: "u", agent_id: "a", day: "2026-08-05", event_count: 9, tool_call_count: 10, high_risk_count: 1, session_count: 2, tool_breakdown: {} },
        { user_id: "u", agent_id: "a", day: "2026-08-06", event_count: 4, tool_call_count: 5, high_risk_count: 0, session_count: 3, tool_breakdown: {} },
      ]),
    ).toEqual({ toolCalls: 15, highRisk: 1, sessions: 5 });
  });
});

describe("AgentBehaviourCard", () => {
  it("渲染三格并带上 agent_id 与 7 天窗口", async () => {
    const fetchMock = stubFetch(() =>
      jsonResponse({
        activity: [
          { user_id: "u", agent_id: "agent-1", day: "2026-08-06", event_count: 1, tool_call_count: 318, high_risk_count: 2, session_count: 7, tool_breakdown: {} },
        ],
      }),
    );
    render(<AgentBehaviourCard agentId="agent-1" />);

    expect(await screen.findByText("318")).toBeDefined();
    expect(screen.getByText("2")).toBeDefined();
    expect(screen.getByText("7")).toBeDefined();
    const url = String(fetchMock.mock.calls[0][0]);
    expect(url).toContain("agent_id=agent-1");
    expect(url).toContain("from_day=");
    expect(url).toContain("to_day=");
  });

  it("取数失败时只塌自己这一格", async () => {
    stubFetch(() => jsonResponse({ code: "unavailable", message: "down" }, 503));
    render(<AgentBehaviourCard agentId="agent-1" />);
    expect(await screen.findByText(/近期行为没取到/)).toBeDefined();
  });
});

describe("AgentHeader", () => {
  it("admin 视角把 provisioned_by 换成机器名", async () => {
    stubFetch(() => jsonResponse({ device: makeDevice({ device_name: "todd-macbook-air" }), agents: [] }));
    const me = makeMe({ role: "admin", membership_id: "mbr_07" });
    const agent = makeAgent({ owner_membership_id: "mbr_99", provisioned_by: "dev_01" });
    render(
      <MemoryRouter>
        <AgentHeader agent={agent} access={resolveAgentAccess(me, agent)} me={me} />
      </MemoryRouter>,
    );
    expect(await screen.findByText(/todd-macbook-air/)).toBeDefined();
  });

  it("机器名取不到时静默退回 device-provisioned 标签", async () => {
    stubFetch(() => jsonResponse({ code: "not_found", message: "gone" }, 404));
    const me = makeMe({ role: "admin", membership_id: "mbr_07" });
    const agent = makeAgent({ owner_membership_id: "mbr_99", provisioned_by: "dev_01" });
    render(
      <MemoryRouter>
        <AgentHeader agent={agent} access={resolveAgentAccess(me, agent)} me={me} />
      </MemoryRouter>,
    );
    expect(await screen.findByText("device-provisioned")).toBeDefined();
  });

  it("member 视角不请求 /v1/admin/devices，只显示标签", async () => {
    const fetchMock = stubFetch(() => {
      throw new Error("member 不该请求 admin 端点");
    });
    const me = makeMe({ role: "member", membership_id: "mbr_01" });
    const agent = makeAgent({ owner_membership_id: "mbr_01", provisioned_by: "dev_01" });
    render(
      <MemoryRouter>
        <AgentHeader agent={agent} access={resolveAgentAccess(me, agent)} me={me} />
      </MemoryRouter>,
    );
    expect(screen.getByText("device-provisioned")).toBeDefined();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("两个跳转按钮各自按能力门控，且带上 agent 参数", () => {
    const me = makeMe({ role: "owner", membership_id: "mbr_01", capabilities: ["view.team-memory"] });
    const agent = makeAgent({ owner_membership_id: "mbr_01" });
    render(
      <MemoryRouter>
        <AgentHeader agent={agent} access={resolveAgentAccess(me, agent)} me={me} />
      </MemoryRouter>,
    );
    expect(screen.getByRole("link", { name: "查看它的会话" }).getAttribute("href")).toBe(
      "/governance/sessions?agent=agent-1",
    );
    expect(screen.getByRole("link", { name: "查看它的记忆" }).getAttribute("href")).toBe(
      "/governance/memory?agent=agent-1",
    );
  });

  it("member 看不到这两个跳转", () => {
    const me = makeMe({ role: "member", membership_id: "mbr_01" });
    const agent = makeAgent({ owner_membership_id: "mbr_01" });
    render(
      <MemoryRouter>
        <AgentHeader agent={agent} access={resolveAgentAccess(me, agent)} me={me} />
      </MemoryRouter>,
    );
    expect(screen.queryByRole("link", { name: "查看它的会话" })).toBeNull();
    expect(screen.queryByRole("link", { name: "查看它的记忆" })).toBeNull();
  });
});
```

- [ ] **Step 2: 跑测试确认红**

Run: `npm --prefix web test -- agent-behaviour`
Expected: FAIL

- [ ] **Step 3: 实现 AgentBehaviourCard**

```tsx
// Recent behaviour：近 7 天的三个计数。
//
// 设计稿是「工具调用 · 24h / 高危 / 未批准」。实际改动两处（spec §8）：
// - activity 端点按 YYYY-MM-DD 天聚合，凑不出滚动 24 小时，写 24h 是谎报口径
// - 未批准数只能靠拉一页 tool-calls 数条数，会被 limit 截成假数字；
//   activity 直接给 session_count，精确

import { useEffect, useState } from "react";
import { listSessionAuditActivity } from "../../api/queries";
import type { SessionAuditActivityDay } from "../../api/types";
import { Card } from "../../components/Card";
import { MetricTile } from "../../components/MetricTile";

const DAY_MS = 24 * 60 * 60 * 1000;

/** 本地日期的 YYYY-MM-DD。刻意不用 toISOString——那是 UTC。 */
function dayKey(date: Date): string {
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${date.getFullYear()}-${month}-${day}`;
}

/** 含两端的 7 天窗口。 */
export function activityWindow(now: Date): { from_day: string; to_day: string } {
  return { from_day: dayKey(new Date(now.getTime() - 6 * DAY_MS)), to_day: dayKey(now) };
}

export function sumActivity(days: SessionAuditActivityDay[]): {
  toolCalls: number;
  highRisk: number;
  sessions: number;
} {
  return days.reduce(
    (acc, d) => ({
      toolCalls: acc.toolCalls + d.tool_call_count,
      highRisk: acc.highRisk + d.high_risk_count,
      sessions: acc.sessions + d.session_count,
    }),
    { toolCalls: 0, highRisk: 0, sessions: 0 },
  );
}

export function AgentBehaviourCard({ agentId }: { agentId: string }) {
  const [totals, setTotals] = useState<ReturnType<typeof sumActivity> | undefined>();
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let cancelled = false;
    const window = activityWindow(new Date());
    listSessionAuditActivity({ agent_id: agentId, ...window })
      .then((days) => {
        if (!cancelled) setTotals(sumActivity(days));
      })
      .catch(() => {
        if (!cancelled) setFailed(true);
      });
    return () => {
      cancelled = true;
    };
  }, [agentId]);

  return (
    <Card title="Recent behaviour">
      {failed ? (
        <p className="small muted">近期行为没取到。页面其余部分不受影响。</p>
      ) : totals === undefined ? (
        <p className="small muted">加载中…</p>
      ) : (
        <div className="ag-metrics">
          <MetricTile label="工具调用 · 7 天" value={String(totals.toolCalls)} />
          <MetricTile label="高危 · 7 天" value={String(totals.highRisk)} />
          <MetricTile label="会话 · 7 天" value={String(totals.sessions)} />
        </div>
      )}
    </Card>
  );
}
```

`MetricTile` 的签名已核对为 `{ label: string; value: ReactNode; unit?: string; note?: ReactNode }`，上面的调用与之相符。

- [ ] **Step 4: 实现 AgentHeader**

按上文「AgentHeader」小节的要求实现。机器名解析用一个局部 `useEffect`，只在 `agent.provisioned_by !== undefined && access.readScope === "admin"` 时发起；`.catch()` 里只 `setDeviceName(undefined)`，**不调 `useErrorHandler`**——这是静默降级，不该弹全局错误。

样式追加：

```css
.ag-head { display: flex; align-items: flex-start; justify-content: space-between; gap: var(--space-5); padding: var(--space-5) var(--space-4) var(--space-4); }
.ag-head-facts { display: flex; flex-wrap: wrap; gap: var(--space-4); margin-top: var(--space-2); font-size: 12px; align-items: center; }
.ag-metrics { display: grid; grid-template-columns: repeat(3, 1fr); border: 1px solid var(--color-divider); }
```

- [ ] **Step 5: 跑测试确认绿**

Run: `npm --prefix web test -- agent-behaviour`
Expected: PASS（9 个用例）

- [ ] **Step 6: 变异验证**

把 `activityWindow` 的 `6 * DAY_MS` 改成 `0`，重跑：`activityWindow` 用例必须红。改回。

- [ ] **Step 7: 提交**

```bash
git add web/src/pages/agent/AgentHeader.tsx web/src/pages/agent/AgentBehaviourCard.tsx web/src/styles/features/agent-detail.css web/tests/agent-behaviour.dom.test.tsx
git commit -m "feat(web): Agent 页头归属解析与近 7 天行为三格"
```

---

## Task 10: 页面装配、路由与旧文件删除

**Files:**
- Rewrite: `web/src/pages/AgentDetailPage.tsx`
- Modify: `web/src/app/routes.tsx`
- Delete: `web/src/pages/AdminAgentDetailPage.tsx`、`web/src/components/AgentDetailView.tsx`、`web/src/components/AgentGovernanceCard.tsx`、`web/src/components/AgentArtifacts.tsx`、`web/src/components/artifacts/`（整目录）
- Test: `web/tests/agent-detail.dom.test.tsx`（补齐 scope 分支与降级）、`web/tests/a11y-controls.dom.test.tsx`、`web/tests/provisioned-by.dom.test.tsx`

**Interfaces:**
- Consumes: Task 1、2、6、7、8、9 的全部产出；`useAgentDetail`、`getOwnAgent`、`getAdminAgent`
- Produces: `AgentDetailPage({ me }: { me: HumanMe })`

**装配要点：**

```tsx
export function AgentDetailPage({ me }: { me: HumanMe }) {
  const { agentId = "" } = useParams();
  const readScope = readScopeFor(me);
  // fetcher 必须 memo：useAgentDetail 把它放进 effect deps。
  const fetcher = useMemo(
    () => (readScope === "admin" ? getAdminAgent : getOwnAgent),
    [readScope],
  );
  const { agent, setAgent, notFound, refetch } = useAgentDetail(fetcher, agentId);
  …
}
```

- `notFound` → not-found 卡 + 「回到访问树」链接（`/management`）。
- `agent === undefined` → 加载态。**不要用字面量 `"Loading…"`**——`renderApp` 等的就是全局 boot 占位符那个字符串消失，同名会让深链测试挂死。用「正在打开这个 Agent…」。
- `agent` 就绪后：`const access = resolveAgentAccess(me, agent)`，`const keys = useAgentKeys(access.actScope, agent.agent_id)`。
  **注意 hook 顺序：** `useAgentKeys` 不能在 `agent` 就绪后才调用。把它放在组件顶层，用 `access` 尚不可知时的保守 scope——正确做法是把「Identity + Lifecycle + Keys + Behaviour」整块抽成一个内部组件 `<LoadedAgent agent={agent} me={me} … />`，只有 `agent` 存在时才挂载它，hook 全在那个组件里。外层 `AgentDetailPage` 只负责取数与三态分派。
- 两列布局：左 `AgentIdentityCard` + `AgentLifecycleCard`，右 `AgentKeysSection` + （有 `view.audit` 时）`AgentBehaviourCard`。
- `AgentBehaviourCard` 的挂载条件写在 `LoadedAgent` 里：`can(me.role, "view.audit") && <AgentBehaviourCard agentId={agent.agent_id} />`。

**路由：**

```tsx
<Route path="/management/agents/:agentId" element={<AgentDetailPage me={me} />} />
```

删掉 `adminLike ? … : …` 三元与两个旧 import；若 `adminLike` 在文件里没有其它用途，一并删掉它的定义。

- [ ] **Step 1: 往 agent-detail 测试文件补 scope 分支与降级用例**

在 `web/tests/agent-detail.dom.test.tsx` 追加：

```tsx
describe("scope 分派", () => {
  it("member 只打 /v1/me/*，一个 admin 请求都不发", async () => {
    const { fetchMock } = await renderApp({
      route: "/management/agents/agent-1",
      me: makeMe({ role: "member", membership_id: "mbr_01" }),
      fetch: agentFetch("me", makeAgent({ owner_membership_id: "mbr_01" })),
    });

    await screen.findByLabelText("显示名");
    const adminCalls = fetchMock.mock.calls.filter(([url]) => String(url).startsWith("/v1/admin/"));
    expect(adminCalls).toHaveLength(0);
  });

  it("admin 看自己的 Agent：详情走 admin，密钥与发放走 me", async () => {
    // 本阶段修的洞：createEnrollment 只有 me scope。
    const { fetchMock } = await renderApp({
      route: "/management/agents/agent-1",
      me: makeMe({ role: "admin", membership_id: "mbr_07" }),
      fetch: (path: string, init: RequestInit) => {
        if (path === "/v1/admin/agents/agent-1" && (init.method ?? "GET") === "GET") {
          return jsonResponse({ agent: makeAgent({ owner_membership_id: "mbr_07" }) });
        }
        if (path.startsWith("/v1/me/agents/agent-1/enrollments")) return jsonResponse({ enrollments: [] });
        if (path.startsWith("/v1/me/agents/agent-1/credentials")) return jsonResponse({ credentials: [] });
        if (path.startsWith("/v1/admin/session-audit/activity")) return jsonResponse({ activity: [] });
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    });

    expect(await screen.findByRole("button", { name: "发放接入权限" })).toBeDefined();
    expect(callsTo(fetchMock, "/v1/me/agents/agent-1/credentials")).not.toHaveLength(0);
  });

  it("admin 看别人的 Agent：没有发放按钮，密钥走 admin scope", async () => {
    const { fetchMock } = await renderApp({
      route: "/management/agents/agent-1",
      me: makeMe({ role: "admin", membership_id: "mbr_07" }),
      fetch: agentFetch("admin", makeAgent({ owner_membership_id: "mbr_99" })),
    });

    await screen.findByLabelText("显示名");
    expect(screen.queryByRole("button", { name: "发放接入权限" })).toBeNull();
    expect(callsTo(fetchMock, "/v1/admin/agents/agent-1/credentials")).not.toHaveLength(0);
  });
});

describe("三态与降级", () => {
  it("404 渲染 not-found 卡并链回访问树", async () => {
    await renderApp({
      route: "/management/agents/agent-1",
      me: makeMe({ role: "member", membership_id: "mbr_01" }),
      fetch: agentFetch("me", apiErrorResponse(404, "not_found", "no such agent")),
    });

    await screen.findByText(/这个 Agent 不存在，或者你看不到它/);
    expect(screen.getByRole("link", { name: "回到访问树" }).getAttribute("href")).toBe("/management");
  });

  it("密钥两条腿都失败，Identity 与 Lifecycle 照常渲染", async () => {
    await renderApp({
      route: "/management/agents/agent-1",
      me: makeMe({ role: "member", membership_id: "mbr_01" }),
      fetch: (path: string, init: RequestInit) => {
        if (path === "/v1/me/agents/agent-1" && (init.method ?? "GET") === "GET") {
          return jsonResponse({ agent: makeAgent({ owner_membership_id: "mbr_01" }) });
        }
        if (path.startsWith("/v1/me/agents/agent-1/")) {
          return jsonResponse({ code: "unavailable", message: "down" }, 503);
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    expect(await screen.findByLabelText("显示名")).toBeDefined();
    expect(screen.getByRole("button", { name: "暂停" })).toBeDefined();
  });

  it("member 不挂 Recent behaviour（零 session-audit 请求）", async () => {
    const { fetchMock } = await renderApp({
      route: "/management/agents/agent-1",
      me: makeMe({ role: "member", membership_id: "mbr_01" }),
      fetch: agentFetch("me", makeAgent({ owner_membership_id: "mbr_01" })),
    });

    await screen.findByLabelText("显示名");
    expect(
      fetchMock.mock.calls.filter(([url]) => String(url).includes("session-audit")),
    ).toHaveLength(0);
  });
});
```

（`apiErrorResponse` 需要加进本文件的 import。）

- [ ] **Step 2: 跑测试确认红**

Run: `npm --prefix web test -- agent-detail`
Expected: FAIL

- [ ] **Step 3: 重写 AgentDetailPage 并改路由**

按「装配要点」实现。特别注意 `LoadedAgent` 内部组件的拆分——这是让 hook 顺序合法的唯一干净办法。

- [ ] **Step 4: 删除旧文件**

```bash
git rm web/src/pages/AdminAgentDetailPage.tsx web/src/components/AgentDetailView.tsx web/src/components/AgentGovernanceCard.tsx web/src/components/AgentArtifacts.tsx
git rm -r web/src/components/artifacts
```

Run: `npm --prefix web run build`
Expected: TypeScript 报出所有残留引用。逐个改掉直到 build 干净。

- [ ] **Step 5: 修受牵连的两个测试文件**

- `tests/a11y-controls.dom.test.tsx` 第 114–130 行：删掉「dispatches AdminAgentDetailPage…」那段过时注释；按新页面的控件名调整断言。
- `tests/provisioned-by.dom.test.tsx`：两处 `/management/agents/:id` 路由的 fetch stub 要补上密钥两条腿与 session-audit（admin 视角），断言按新页头调整。

- [ ] **Step 6: 跑全量测试与构建**

Run: `npm --prefix web test && npm --prefix web run build`
Expected: 全绿；build 只剩既有的 chunk-size 警告

- [ ] **Step 7: 提交**

```bash
git add -A web/src web/tests
git commit -m "feat(web)!: Agent 详情合并为一个按归属判定 scope 的页面"
```

---

## Task 11: 两个 Governance 页读 URL 上的 agent 参数

**Files:**
- Modify: `web/src/pages/AdminSessionAuditPage.tsx`（三处 `const [agentId, setAgentId] = useState("")`，约第 148、284、454 行）
- Modify: `web/src/pages/AdminExplorerPage.tsx`（第 19 行附近）
- Test: `web/tests/agent-deeplink.dom.test.tsx`（新建）

**Interfaces:**
- Consumes: `useSearchParams` from `react-router-dom`
- Produces: 无新导出。行为：`?agent=<agent_id>` 作为筛选框的**初始值**。

**范围纪律：** 只改初始值，不改布局、不改筛选器、不改分页，也**不做**双向绑定（用户之后改筛选框不写回 URL）。这两页在阶段 5 会被重画，冲突面越小越好。

`AdminSessionAuditPage` 的三个视图各自独立 `useState`，三处都要改。取参数的写法：

```tsx
const [searchParams] = useSearchParams();
const [agentId, setAgentId] = useState(searchParams.get("agent") ?? "");
```

`AdminSessionAuditPage` 顶层已经有一个 `useSearchParams`（第 563 行用于 `view`），但那是在另一个组件里；三个视图组件各自调用 `useSearchParams()` 是正常的，React Router 允许多处调用。

- [ ] **Step 1: 写失败测试**

`web/tests/agent-deeplink.dom.test.tsx`：

```tsx
// Agent 详情页头的两个跳转要真的带上过滤，否则点过去是一张空表、用户还得
// 自己粘 agent_id。这里钉住「URL 参数进筛选框，并进首次请求」。
import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

describe("/governance/sessions?agent=", () => {
  it("参数进筛选框并进首次请求", async () => {
    const { fetchMock } = await renderApp({
      route: "/governance/sessions?agent=agent-1",
      me: makeMe({ role: "owner" }),
      fetch: (path: string) => {
        if (path.startsWith("/v1/admin/session-audit/findings")) return jsonResponse({ findings: [] });
        if (path.startsWith("/v1/admin/session-audit/tool-calls")) return jsonResponse({ tool_calls: [] });
        if (path.startsWith("/v1/admin/session-audit/activity")) return jsonResponse({ activity: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    expect(((await screen.findByPlaceholderText("agent_id")) as HTMLInputElement).value).toBe("agent-1");
    expect(
      fetchMock.mock.calls.some(([url]) => String(url).includes("agent_id=agent-1")),
    ).toBe(true);
  });

  it("没有参数时筛选框为空", async () => {
    await renderApp({
      route: "/governance/sessions",
      me: makeMe({ role: "owner" }),
      fetch: (path: string) => {
        if (path.startsWith("/v1/admin/session-audit/")) return jsonResponse({ findings: [], tool_calls: [], activity: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    expect(((await screen.findByPlaceholderText("agent_id")) as HTMLInputElement).value).toBe("");
  });
});

describe("/governance/memory?agent=", () => {
  it("参数进筛选框并进首次请求", async () => {
    const { fetchMock } = await renderApp({
      route: "/governance/memory?agent=agent-1",
      me: makeMe({ role: "owner", capabilities: ["view.team-memory"] }),
      fetch: (path: string) => {
        if (path.startsWith("/v1/admin/team-notes")) return jsonResponse({ notes: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByText(/Memory/);
    expect(
      fetchMock.mock.calls.some(([url]) => String(url).includes("agent_id=agent-1")),
    ).toBe(true);
  });
});
```

实现者需先读这两个页面，确认筛选框的 `placeholder` 与 explorer 的实际列表端点路径，把上面的 stub 与断言对齐到真实值——**不要为了让测试通过去改页面的 placeholder 或端点**。

- [ ] **Step 2: 跑测试确认红**

Run: `npm --prefix web test -- agent-deeplink`
Expected: FAIL —— 筛选框初始值是空串

- [ ] **Step 3: 实现**

四处 `useState("")` 改成 `useState(searchParams.get("agent") ?? "")`，各自在组件内加 `const [searchParams] = useSearchParams();`。

- [ ] **Step 4: 跑全量测试与构建**

Run: `npm --prefix web test && npm --prefix web run build`
Expected: 全绿；build 干净

- [ ] **Step 5: 提交**

```bash
git add web/src/pages/AdminSessionAuditPage.tsx web/src/pages/AdminExplorerPage.tsx web/tests/agent-deeplink.dom.test.tsx
git commit -m "feat(web): 会话审计与记忆浏览接受 URL 上的 agent 参数"
```

---

## 收尾检查（最后一个任务完成后）

- [ ] `npm --prefix web test` 全绿
- [ ] `npm --prefix web run build` 干净（只剩既有的 chunk-size 警告）
- [ ] `git diff --stat main` 确认改动只在 `web/` 与 `docs/` 下，Go 侧零改动
- [ ] `grep -rn "\.btn\.ghost\|btn ghost" web/src` —— 本次新增代码里应为 0 处
- [ ] `grep -rnE "var\(--(bg|muted|accent|text|border|surface|mono)\)" web/src/styles/features/agent-detail.css` —— 应为 0 处（兼容别名禁用）
- [ ] `grep -rn "SecretCard" web/src` —— 应为 0 处
- [ ] **三主题（beige / dark / arcade）浏览器验证仍未做**：与阶段 3 同样待办。`agent-detail.css` 新引入多处 `color-mix(... var(--color-accent-100) ...)` 表面从未在浏览器里渲染过；CSS 变量引用错名不报错，症状是静默掉回默认样式。如实记入 PR 描述，不要假装做过。
