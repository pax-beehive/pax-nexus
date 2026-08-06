# Modernist Portal 阶段 3 · Management 访问树

日期：2026-08-06
上位设计：`docs/superpowers/specs/2026-08-04-portal-modernist-redesign-design.md`（七阶段总设计，本文是其阶段 3 的落地设计）
前序：阶段 1（PR #78→#77）、阶段 2a（PR #81）、阶段 2b（PR #82）均已合并

---

## 1. 目标与非目标

**目标**

1. `/management` 落地**访问树**：一棵体现「人 → 机器 → Agent」权限继承链的三层下钻视图，取代当前顶替用的 `MyAgentsPage`。
2. member 的 `/management`（同时是 member 的落地页）成为按 Modernist 重画的本人 Agent 列表。
3. Members / Devices / Agents / Invitations 四张平表按 Modernist 重画，保留扁平视图与现有筛选。
4. 把已有的级联吊销弹窗提取为共用组件，让树上的机器行可直接调起。

**非目标**

- 不新增任何后端端点，不改任何写路径语义。本阶段是纯前端。
- 不合并 `AgentDetailPage` / `AdminAgentDetailPage`（阶段 4）。
- 不动四张平表的功能与筛选器语义，只动版式。
- 不引入前端运行时依赖。

---

## 2. 屏与路由

| 路由 | 阶段 3 后 | 备注 |
|---|---|---|
| `/management` | 新 `AccessTreePage` | 按角色分叉 |
| `/management/members` | `AdminMembersPage`（重画） | 功能不变 |
| `/management/devices` | `AdminDevicesPage`（重画） | 功能不变 |
| `/management/agents` | `AdminAgentsPage`（重画） | 功能不变 |
| `/management/invitations` | `AdminInvitationsPage`（重画） | 功能不变 |
| `/management/devices/:credentialId` | `AdminDeviceDetailPage` | 仅改为引用提取后的 `RevokeDeviceModal` |
| `/management/agents/:agentId` | 不动 | 两页合一是阶段 4 |

**路由表不新增条目。** 下钻位置进 query 参数：

```
/management                                  第 1 层（人）
/management?person=<membership_id>           第 2 层（该人的机器）
/management?person=<...>&machine=<credential_id>   第 3 层（该机器的 Agent）
```

选择 query 而非 path 段，是因为 `/management/members` 等四条平表路由已经占据同一层级，
path 方案（`/management/people/:id/machines/:id`）要改路由表与 legacy 重定向表，收益相同。

`navModel.ts:33` 的 subnav 首项 "Access tree" 已指向 `/management`，无需改动。

### 2.1 角色分叉

- **admin+**（`can(me.role, "view.members")` 为真）：三层访问树。
- **member**：本人 Agent 的扁平列表。无面包屑（没有上一层可回），不读 query 参数，
  **不发任何 `/v1/admin/*` 请求**，数据来自 `listMyAgents()`。

member 分叉必须保留 `MyAgentsPage` 现有的 `CreateAgentModal` 与「+ Create Agent」入口。
注册个人 Agent 的入口全站仅此一处（现 `routes.tsx` 的注释已就此立警示），丢掉它会导致
owner / admin / member 三种角色在整个门户里都无法注册自己的 Agent。

**`MyAgentsPage.tsx` 在本阶段被删除**：其列表与 `CreateAgentModal` 迁入
`pages/management/MyAgentsLevel.tsx` 并顺带重画。删除后 `/management` 是它唯一的历史挂载点，
不留死代码。

**注意 admin+ 也需要「我的 Agent」**：owner / admin 走的是访问树分叉，而访问树第 1 层是
**团队全员**，其中包含他们自己——他们注册个人 Agent 的路径是「在人员层点自己 → 进入自己的
机器层」。因此 `MyAgentsLevel` 的「+ Create Agent」入口必须同时挂在第 2 层的人头部上
（当该人是当前用户时），否则删掉 `MyAgentsPage` 就复现了 `routes.tsx` 注释警告的那个坑。
`web/tests/app-shell.dom.test.tsx:22` 已就此立了回归断言，本阶段必须让它继续成立。

---

## 3. 组件结构

```
pages/AccessTreePage.tsx           路由入口：角色分叉、query 参数解析、层级判定
pages/management/
  useAccessSnapshot.ts             三个列表 → 一份快照 + 派生索引
  AccessSummary.tsx                三格汇总条
  AccessCrumbs.tsx                 面包屑 + 右侧 level hint
  PeopleLevel.tsx                  第 1 层
  MachinesLevel.tsx                第 2 层（人头部 + 机器行 + 散装 Agent 分组）
  DeviceAgentsLevel.tsx            第 3 层（机器头部 + Agent 行 + Revoke）
  MyAgentsLevel.tsx                member 根层
components/RevokeDeviceModal.tsx   从 AdminDeviceDetailPage.tsx 提取
```

`AccessTreePage` 是唯一持有下钻状态的地方（来源是 URL，不是 `useState`）。各层组件是纯展示 +
回调，接受已派生好的数据，不自行取数——第 3 层的详情取数由 `AccessTreePage` 发起。

`RevokeDeviceModal` 是**原样搬迁**：它已带级联表格预览、per-dialog `Idempotency-Key`、
不可撤销文案。`AdminDeviceDetailPage` 改为引用它，行为不变。

---

## 4. 取数与派生

### 4.1 快照

根层一次取三个列表，构成一份**时点一致**的快照：

| 列表 | 函数 | 状态 |
|---|---|---|
| 成员 | `listAllMembers()` | 已存在；内部翻完全部页，带重复游标保护 |
| 机器 | `listAllDevices()` | **新增**，照 `listAllMembers` 逐字同构 |
| Agent | `listAllAgents()` | **新增**，照 `listAllMembers` 逐字同构 |

后端 `queryLimit` 把每页硬顶在 100（`identity_registry_endpoints.go:907-917`），
因此只有翻页才能得到真实计数。既有的 `listAllMembers` 已经是这个模式的实现与先例，
新增两个函数复用同一形状，包括重复游标保护。

### 4.2 派生（第 1、2 层零额外请求）

| 派生量 | 来源 |
|---|---|
| 某人的机器 | devices 按 `created_by_membership_id` 筛 |
| 某机器的存活 Agent 数 | `DeviceSummary.provisioned_agent_count` |
| 某人的散装（手工注册）Agent | agents 里 `owner_membership_id === 此人` 且 `provisioned_by` **缺失** |

`provisioned_agent_count` 是后端 `count(DISTINCT agent_id) WHERE provisioned_by = $1 AND
revoked_at IS NULL`（`postgres/registry.go:900-904`），即存活口径，与级联吊销的口径同源。

`provisioned_by` 按**字段存在性**判断，不按真值——人工注册的 Agent 整个省略该字段
（`api/types.ts:100-106` 已就此立注释）。

### 4.3 第 3 层的一次详情取数

进入第 3 层时取一次 `getDevice(credential_id)`（`GET /v1/admin/devices/:credential_id`），
用 `aliveProvisionedAgents()` 过滤。这一次取数买到三样东西：

1. Agent 行的 `credential_id` 与最后使用时间（agents 列表不携带 credential 信息）；
2. revoked 历史的正确处理（同一 agent_id 可能有多行轮换记录）；
3. **级联预览与展示行同源**——spec 的验收标准「吊销机器时预览行数 = 实际级联数」
   因此是结构性成立，而非靠测试盯住两个数据源不漂移。

这一层与 `AdminDeviceDetailPage` 的 Agent 表同源，行组件共用。

---

## 5. 汇总条与各层行

### 5.1 三格汇总条

全部从快照算，不额外取数：

| 格 | 主数 | 副文案 |
|---|---|---|
| People | 成员总数 | `N owner · N admin · N members` |
| Machines | 机器总数 | `N connected · N revoked · N 人没有机器` |
| Agents | Agent 总数 | `N active · N suspended · N retired` |

`DeviceStatus` 只有 `active` / `revoked`；`AgentStatus` 是 `active` / `suspended` / `retired`
（`api/types.ts:10-13,140`）。副文案用人话，不照搬枚举名。

设计稿的第四格 **Unclaimed tokens 不做**——见 §8.3。

### 5.2 各层行

- **第 1 层（人）**：姓名 + 角色 kicker、邮箱、机器数、Agent 数、状态备注、行尾 `→`。
- **第 2 层（机器）**：人头部、机器行（名称/credential id/状态 tag/Agent 数/最后使用/Revoke/`→`）、
  散装 Agent 分组（标题说明这些是手工逐个签发的密钥）。

  人头部的动作按钮**按「这个人是不是你」分叉**，这是设计稿没有区分、而后端强制的：

  | 动作 | 条件 | 去向 |
  |---|---|---|
  | Change access | 恒有（需 `view.members`） | `/management/members`，改角色/状态在那里 |
  | Connect a machine | **仅当该人是当前用户** | 开 `CreateDeviceEnrollmentModal` |
  | + Create Agent | **仅当该人是当前用户** | 开 `CreateAgentModal`（见 §2.1） |

  设备注册端点是 `POST /v1/me/device-enrollments`（`api/actions.ts:303-311`），
  入参只有 `device_name` 与 `expires_in_seconds`，**永远为调用者本人注册**。
  admin 无法代 Sam 连一台机器，所以在别人的头部渲染这个按钮是在承诺做不到的事。
- **第 3 层（Agent）**：机器头部（名称/状态/id/最后使用/「trusted by <人名>」+「Revoke this machine」）、
  Agent 行（名称/agent id/类型/状态/最后活跃/`→` 通往 Agent 详情）。

**Agent 行不携带 writes / notes / recalls 三个活动计数**——见 §8.4。

---

## 6. 错误处理与边界

- **members 是脊柱**：这条腿失败即整页 error + 重试按钮（没有人就没有树）。
- **devices / agents 降级**：任一失败时，相关计数显示 `—`，进入对应层时行内报错并可单独重试，
  不整页白屏。这是「区块级错误隔离」在单块页面上的落法。
- **失效的 query 参数**：`?person=` 指向的人已离职、`?machine=` 指向的机器已删或不属于该人时，
  回退到最近的有效层，并在页内说明发生了什么——不静默重置到根层。
- **member 分叉零 admin 请求**：必须有测试盯住。否则 403 噪音只在生产可见。
- 第 3 层详情取数失败：该层报错可重试，面包屑与上层仍可用。

---

## 7. 四张平表的重画

四页均已走 `PagedListCard`，className 只使用 Modernist 期的工具类，
**无一处** `.badge` / `.b-*` / `.tabs` 遗留（那些债集中在 explorer / operations / wiki 三页，
按总设计在阶段 6 清理）。因此本阶段对它们是版式工作：

- 主列使用标题字族与大写 kicker；
- 2px 分隔线与行密度对齐树的行原语；
- 可下钻的行补 `→` 可供性；
- 空态走 `EmptyState`。

行原语与访问树共用一套，四表因此边际成本低。**功能、筛选器、分页、权限门控一律不动。**

---

## 8. 与上位 spec / 设计稿的记账偏离

每条都是有意为之，列此以免后来者当作实现遗漏。

1. **总设计 §5.2「进入 Management 只打人 + 机器两个列表请求」→ 实际三个。**
   人均 Agent 数与散装 Agent 分组是总设计自己要求的功能，两个请求拿不到。
2. **总设计 §5.2「下钻懒加载 + 同节点结果会话内缓存」→ 只有第 3 层懒加载。**
   前两层的数据本就在快照里，没有可缓存的对象。
3. **设计稿汇总条第四格「Unclaimed tokens」→ 砍掉。**
   无数据源：enrollment 只能按 Agent 逐个列（`/v1/admin/agents/:id/enrollments`）；
   团队级的 `ListExpiringEnrollments` 只在 store 层，且仅经 Overview 聚合端点暴露、
   24h 窗口、`view.operations` 门控。另立议题。
4. **设计稿 Agent 行的 writes / notes / recalls → 砍掉。**
   数据源 `/v1/admin/operations/agents` 受 `view.operations` 门控；Management 是 member
   也要天天看的分区，把能力门控的数据放进行里会让同一张表对不同角色少三列。
   活动数据留在它本来的位置：Overview 的「谁在写」与 Agent 详情页。
5. **总设计阶段 3 的「级联吊销弹窗带表格预览」→ 已存在。**
   `AdminDeviceDetailPage.tsx:19` 的 `RevokeDeviceModal` 已完整实现，
   `ConfirmDialog` 的 `children` 预览槽阶段 1 已补。本阶段只做提取共用。
6. **设计稿在任意人员头部都画了「Connect a machine」→ 只在本人头部渲染。**
   见 §5.2：注册端点是 `/v1/me/device-enrollments`，永远为调用者本人注册。

---

## 8bis. 需要一并更新的既有测试

四个测试文件写死了「`/management` 在阶段 3 前对所有角色渲染 `MyAgentsPage`」这一临时事实，
本阶段必须随之更新（不是删除断言，是改成新的正确行为）：

| 文件 | 现有假设 |
|---|---|
| `web/tests/app-shell.dom.test.tsx:22` | 「+ Create Agent」全站唯一入口的回归护栏——**必须继续成立**，见 §2.1 |
| `web/tests/a11y-controls.dom.test.tsx:78` | owner 落到 `MyAgentsPage`，与 member 相同 |
| `web/tests/onboarding.dom.test.tsx:21` | `/management` 是各角色统一的 member-rooted 视图 |
| `web/tests/admin-operations-access.dom.test.tsx:90,124` | 同上，两处 |

---

## 9. 验收

1. 三种角色（owner / admin / member）各走通一次完整下钻。
2. 吊销机器时，预览行数 = 展示的 Agent 行数（同源，结构性成立）。
3. 三层深链直接刷新进入可用，面包屑标签从快照查得，不额外取数。
4. 失效的 `?person=` / `?machine=` 回退到最近有效层并给出说明。
5. member 分叉不发出任何 `/v1/admin/*` 请求。
6. 带散装 Agent 的人，在第 2 层出现「手工注册」分组，且其 Agent 计数包含这些 Agent。
7. `npm run build && npm test` 全绿；三主题各过一遍。

---

## 10. 测试计划

| 测试 | 断言 |
|---|---|
| 角色分叉 | member 根层渲染 Agent 列表与「+ Create Agent」；admin 根层渲染人员行 |
| member 零 admin 请求 | fetch stub 对任何 `/v1/admin/*` 抛错，member 分叉仍渲染成功 |
| 三层深链 | 带完整 query 直接挂载，面包屑标签正确且无额外请求 |
| 失效 person id | 回退到根层并渲染说明文案 |
| 级联同源 | 预览表行数 === 第 3 层展示的 Agent 行数 |
| 散装 Agent | 无 `provisioned_by` 的 Agent 出现在「手工注册」分组，且计入该人的 Agent 数 |
| 降级 | devices 腿失败时页面仍渲染人员行，机器计数显示 `—` |
| 翻页 | 超过 100 的列表被 `listAllDevices` / `listAllAgents` 完整翻页，计数为总数 |
| 人头部 CTA 分叉 | 自己的头部有「Connect a machine」与「+ Create Agent」；别人的头部两者都无，只有「Change access」 |
| Create Agent 护栏 | `app-shell.dom.test.tsx` 的全站唯一入口断言在删除 `MyAgentsPage` 后仍成立（owner 经人员层进入自己的机器层） |
