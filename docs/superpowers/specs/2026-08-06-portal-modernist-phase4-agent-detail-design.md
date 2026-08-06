# Modernist Portal 阶段 4 · Agent 详情 + 令牌仪式

Date: 2026-08-06
Status: Accepted

七阶段重构的第 4 阶段。把两个近乎重复的 Agent 详情页合并成一个 scope 自适应的页面，
按设计稿重画为五段式；把一次性令牌的展示从一张卡升级为不可误关的**全屏仪式**，
并让四个一次性密钥展示点统一走这套仪式。

上位设计：`docs/superpowers/specs/2026-08-04-portal-modernist-redesign-design.md` §阶段 4。
前一阶段：`docs/superpowers/specs/2026-08-06-portal-modernist-phase3-management-design.md`。
设计稿：`docs/w.html`（`isAgent` 分支、`issueOpen` / `secretOpen` / `confirmSuspend` 三个覆盖层）。
API 契约：`docs/on-prem-identity-frontend-integration.md`。

---

## 1. 目标与非目标

**目标**

1. `pages/AgentDetailPage.tsx` 与 `pages/AdminAgentDetailPage.tsx` 合并为一个页面，
   scope 按**归属**而非角色判定。
2. 详情页按设计稿重画为五段：Identity / Lifecycle / Waiting to be claimed /
   Active keys / Recent behaviour。
3. 生命周期改为「暂停 / 退役 / 移交」三张卡，后果文案从 API 术语改写为人话；
   移交是本阶段新增的 UI（后端 `transferAgent` 已存在）。
4. `SecretCard` 升级为全屏 `SecretCeremony`（倒计时 + 双复制 + 三步说明 + 关闭二次确认），
   四个调用点全部迁移。
5. 发放接入令牌的表单改写为人类语言。

**非目标**

- **零后端改动。** 本阶段不新增、不修改任何端点或 IDL。
- 不做无障碍专项；现有 `aria-*` / `role` 不允许倒退。
- 不引入任何前端运行时依赖（生产依赖仍限于 react / react-dom / react-router-dom /
  react-markdown / remark-gfm）。
- 不重画 Governance 四屏（阶段 5）。本阶段只让其中两页多读一个 URL 参数，
  不动它们的布局。

---

## 2. scope 与权限

### 2.1 合并后的判定

路由 `/management/agents/:agentId` 现在按 `me.role` 一刀切地渲染两个页面之一。
合并后由一个页面按**目标 Agent 的归属**判定：

| 角色 | 读 | 动作 | 可发放接入令牌 |
|---|---|---|---|
| member | `/v1/me/agents/:id` | me | 是 |
| admin+，`owner_membership_id === me.membership_id` | `/v1/admin/agents/:id` | **me** | **是** |
| admin+，别人的 Agent | `/v1/admin/agents/:id` | admin | 否 |

读用一个请求即可拿到 `owner_membership_id`，动作 scope 由它决定——不需要额外探测。

第二行是本阶段修掉的能力缺口：`createEnrollment` 只有 me scope
（`POST /v1/me/agents/:id/enrollments`），而现状下 admin+ 恒走 admin scope，
于是**管理员给自己的 Agent 发不了接入令牌**。这与阶段 3 修掉的
「owner/admin 无创建 Agent 路径」是同一类洞。

`me.membership_id` 在 `HumanMe` 上；`owner_membership_id` 在 `AgentProfile` 上是
可选字段。两者任一缺失时**判定为「不是自己的」**（保守：宁可少给权限，
后端仍是唯一执法者）。

### 2.2 动作权限矩阵

不新增 capability，沿用 `lib/capabilities.ts`：

| 动作 | member（自己的） | admin（自己的） | admin（别人的） | owner（别人的） |
|---|---|---|---|---|
| 编辑 Identity | ✅ | ✅ | ❌ | ✅ `govern.any-agent` |
| 暂停 | ✅ | ✅ | ✅ `suspend.any-agent` | ✅ |
| 恢复 | ✅ | ✅ | ❌ | ✅ `govern.any-agent` |
| 退役 | ✅ | ✅ | ❌ | ✅ `govern.any-agent` |
| 移交 | ❌ | ❌ | ❌ | ✅ `govern.any-agent`（admin scope 专有端点） |
| 发放接入令牌 | ✅ | ✅ | ❌ | ❌ |
| 吊销令牌 / 密钥 | ✅ | ✅ | ✅ | ✅ |

「自己的」列一律走 me scope，所以权限与 member 相同——这正是 2.1 的效果。

移交只有 admin scope 端点（`POST /v1/admin/agents/:id/transfer`），所以即使是
自己的 Agent，移交也走 admin scope 且要求 owner 角色。owner 移交自己的 Agent 时，
页面的动作 scope 是 me，移交这一个动作单独用 admin scope——这是唯一的例外，
在代码里显式注明。

### 2.3 路由

`/management/agents/:agentId` 保持一条路由、一个元素，不再有 `adminLike` 三元分派。
不加 `RequireRole` 包装：member 访问别人的 Agent 时 `/v1/me/agents/:id` 返回 404，
页面渲染既有的 not-found 卡，这是正确且信息量更小的结果。

---

## 3. 页面结构

### 3.1 文件

```
pages/AgentDetailPage.tsx           唯一入口，组合下述部件
pages/agent/useAgentScope.ts        归属判定 → { readScope, actScope, powers }
pages/agent/useAgentKeys.ts         pending enrollments + active credentials 全量取
pages/agent/AgentHeader.tsx         面包屑、标题、状态标签、归属行、两个跳转按钮
pages/agent/AgentIdentityCard.tsx   Identity 表单 + 乐观锁保存
pages/agent/AgentLifecycleCard.tsx  三张动作卡 + 三个确认弹窗 + 移交目标选择
pages/agent/AgentKeysSection.tsx    Waiting to be claimed + Active keys
pages/agent/AgentBehaviourCard.tsx  Recent behaviour 三格
components/SecretCeremony.tsx       全屏一次性密钥仪式
components/IssueAccessModal.tsx     发放接入令牌表单
styles/features/agent-detail.css    新建特性样式文件
```

`useAgentDetail`（`lib/useAgentDetail.ts`）保留不动——它已经是 scope 无关的
「取一个 Agent + 404 + refetch」。

### 3.2 删除

| 删除 | 去向 |
|---|---|
| `pages/AdminAgentDetailPage.tsx` | 合并进 `AgentDetailPage` |
| `components/AgentDetailView.tsx` | 合并进 `AgentDetailPage` |
| `components/AgentGovernanceCard.tsx` | 拆成 `AgentIdentityCard` + `AgentLifecycleCard` |
| `components/AgentArtifacts.tsx` | 拆成 `AgentKeysSection` + `useAgentKeys` |
| `components/artifacts/EnrollmentsCard.tsx` | → `AgentKeysSection` 的「Waiting to be claimed」 |
| `components/artifacts/CredentialsCard.tsx` | → `AgentKeysSection` 的「Active keys」 |
| `components/artifacts/IssueEnrollmentModal.tsx` | → `components/IssueAccessModal.tsx` |
| `components/artifacts/EnrollmentSecretCard.tsx` | → `SecretCeremony` 的调用点 |
| `components/artifacts/Tabs.tsx`、`LoadMore.tsx` | 仅被上面两张卡使用，随之消失 |
| `components/SecretCard.tsx` | 四个调用点全部改用 `SecretCeremony` |

`components/artifacts/` 目录整体消失。

### 3.3 布局

设计稿是两列（左 Identity + Lifecycle，右 三段密钥与行为）。
落地为 `.agent-detail` 的两列网格，窄屏折成一列，顺序为
Identity → Lifecycle → Waiting → Active keys → Recent behaviour。
栅格与断点复用 `layout.css` 已有的工具类，特性样式只描述这一页特有的分栏与卡内排版。

---

## 4. 取数与降级

页面共四条取数腿，各自独立：

| 腿 | 请求 | 失败表现 |
|---|---|---|
| Agent 本体 | `getOwnAgent` / `getAdminAgent` | **整页 error**（脊柱），404 走 not-found 卡 |
| 密钥现状 | `listEnrollments(status=pending)` + `listCredentials(status=active)`，各自全量翻页 | 两张卡各自塌成可重试的区块错误 |
| 归属机器名 | `getDevice(provisioned_by)`，仅 admin+ 且 `provisioned_by` 存在时 | 退回显示 `credential_id`，不报错 |
| Recent behaviour | `listSessionAuditActivity({agent_id, from_day, to_day})`，`view.audit` 门控 | 只塌这一格，其余照常 |

「脊柱失败即整页 error，其余独立降级」沿用阶段 2 与阶段 3 的同一契约。

### 4.1 密钥现状为什么全量翻页

暂停与退役的确认弹窗要告诉用户「这会销毁 N 把活跃密钥和 M 张未认领令牌」。
这个数字必须精确——destructive 确认框里给一个被分页截断的数字比不给更糟。

`useAgentKeys` 用与 `listAllMembers` 同构的 do/while + `seenCursors` 守卫，
把 `status=pending` 的 enrollments 和 `status=active` 的 credentials 各自取全。
返回的**同一个数组引用**同时喂给：

- 「Waiting to be claimed」/「Active keys」两张卡的行
- 暂停 / 退役确认弹窗的后果预览

所以「预览计数 = 页面行数」是结构性成立的，不靠测试盯两个数据源不漂移——
两者分叉在代码里无法表达。这与阶段 3 的级联吊销预览是同一手法。

一个 Agent 的活跃密钥数量级是「每台机器一把」，全量翻页的实际代价接近一次请求。

`credential` 没有服务端 status 字段（`lib/credentials.ts` 的注释），
`status=active` 是服务端过滤；客户端**再用 `deriveCredentialStatus` 过一遍**，
只有两者都判定 active 的才计入确认框。宁可少算，不可多算。

### 4.2 历史

consumed / revoked / expired 的记录收在两张卡各自的「显示历史」开关后面，
展开时才挂 `usePagedList`（沿用现有的游标分页与筛选）。
确认弹窗**永远读 4.1 的实时数组**，不读历史路径。

### 4.3 Recent behaviour

`GET /v1/admin/session-audit/activity?agent_id=&from_day=&to_day=` 返回按天聚合的
`{ tool_call_count, high_risk_count, session_count, ... }`。窗口取
`from_day = 今天 − 6 天`、`to_day = 今天`（两端都含，按浏览器本地日期算出
`YYYY-MM-DD`），把返回的各天求和，渲染三格：
**工具调用 · 7 天** / **高危 · 7 天** / **会话 · 7 天**。

只在 `can(role, "view.audit")` 时挂载这个 hook；member 页面上这一格根本不存在
（而不是渲染成错误卡）。

---

## 5. 令牌仪式

### 5.1 形态

全屏覆盖层（`position: fixed; inset: 0`），朱红铺底、白字，三段式：

```
┌─ 顶栏 ── ONE-TIME TOKEN · 只展示一次，不存任何地方 ──── 可认领 04:58 ─┐
│                                                                      │
│  现在就复制。我们没法再给你看一次。          接下来会发生什么          │
│  这串令牌让 <name> 在 <where> 换取一把长期     1. 在目标机器上执行命令   │
│  密钥。它只存在于这个屏幕上。                 2. 令牌兑换成密钥并自毁    │
│                                              3. 密钥出现在「活跃密钥」  │
│  ┌────────────────────────────────────┐      ───────────────────────  │
│  │ tm_enroll_....                     │      丢了怎么办                │
│  └────────────────────────────────────┘      什么都不会坏。取消这张待   │
│  [复制令牌] [复制接入命令]                    认领的记录、重新发一张即可 │
│  $ paxl channel connect onprem --enrollment-token …                   │
│                                              enr_… · 签发于 …          │
├──────────────────────────────────────────────────────────────────────┤
│  确定关闭？令牌无法再展示。      [先别关]  [我已保存，关闭]            │
└──────────────────────────────────────────────────────────────────────┘
```

关闭是两段的：第一次点「我已保存，关闭」把底栏切成确认态（出现「先别关」，
按钮文案变成「确定关闭」，左侧提示「关掉后这串令牌就再也看不到了」），
第二次点才真的关。`Esc` 与点击遮罩**不关闭**——一次性密钥不该被误触销毁。

关闭**不取消**这条待认领记录：令牌本身仍在有效期内可被兑换，只是不再可见。
关闭后的 toast 说明这一点，并指向「Waiting to be claimed」——用户若想作废，
要去那里显式取消。

### 5.2 接口

```ts
export function SecretCeremony(props: {
  title: string;          // 顶栏 kicker
  headline: string;       // 大标题
  body: ReactNode;        // 标题下的解释段
  value: string;          // 令牌本身
  valueLabel: string;     // 「复制令牌」按钮里的名词
  expiresAt?: string;     // 倒计时
  steps: string[];        // 「接下来会发生什么」
  recovery: string;       // 「丢了怎么办」
  meta?: ReactNode;       // 右下角签发元信息
  command?: string;       // 客户端接入命令（有则渲染命令块与第二个复制按钮）
  onClose: () => void;
}): JSX.Element
```

`SecretCard` 的四个调用点各自提供自己的文案：

| 调用点 | title | command |
|---|---|---|
| Agent 接入令牌（本页） | 一次性接入令牌 | `enrollmentConnectCommand(token, origin)` |
| 设备注册令牌（`AccessTreePage`） | 一次性设备注册令牌 | `deviceConnectCommand(token, origin, name)` |
| 设备注册令牌（`AdminDevicesPage`） | 同上 | 同上 |
| 成员邀请令牌（`AdminInvitationsPage`） | 一次性邀请令牌 | 无（人类走浏览器，不走 CLI） |

`lib/enrollment.ts` 的两个命令构造函数原样复用，不改。

### 5.3 安全不变量

令牌只存在于组件 state 中。仪式关闭后：

- `localStorage` 与 `sessionStorage` 不含令牌子串
- `location.href` 不含令牌子串
- 令牌不进任何 logger

复制走既有的 `lib/clipboard.ts`；剪贴板不可用时退回 `window.prompt`，
令牌仍然不落存储。

### 5.4 附带修掉的阶段 3 隐患

阶段 3 的设备注册密钥卡是**内联**渲染在访问树里的，于是「树内导航不卸载」导致
密钥卡会挂到别人的机器上方；当时用「坐标键清理 effect + 渲染门控」双保险修掉。
全屏仪式让这个问题在结构上消失：仪式是覆盖层，出现时它是页面上唯一可交互的东西，
不存在「挂在哪个节点下」的语义。阶段 3 那两道保险随之简化为一条
「导航即关闭仪式」，在 spec §8 记账。

---

## 6. 生命周期

三张动作卡，每张是「动作名 + 一句后果 + 按钮」：

| 卡 | 后果文案 | 可见条件 |
|---|---|---|
| 暂停 | 它会立刻停止读写团队记忆。密钥被销毁，不是暂存。 | 状态 active 且有暂停权 |
| 恢复 | 恢复后它能重新读写，但旧密钥不会回来——你要重新发一次接入令牌。 | 状态 suspended 且有恢复权 |
| 退役 | 终局。这个身份永远无法再启用，ID 也不能重用。 | 未退役且有退役权 |
| 移交 | 把身份交给另一个人，并吊销当前所有者签发的每一把密钥。 | owner 且未退役 |

暂停与恢复共用同一张卡的位置（按状态互斥），所以 owner 看到的是设计稿那三张
（暂停|恢复 / 退役 / 移交）；member 与 admin 看自己的 Agent 时是两张（无移交）；
admin 看别人的 Agent 时只有暂停一张。

确认弹窗用 `ConfirmDialog` 现有的 `children` 槽渲染后果预览：

- 暂停 / 退役：列出将被销毁的活跃密钥与未认领令牌（4.1 的同一数组），
  并在末尾以小字给出 `agent_id · resource_version N`
- 移交：先选目标成员（`listAllMembers` 的下拉），再确认

`retired` 是终态：退役后 Identity 表单只读，三张动作卡换成一句
「已退役 · 终态，无法恢复」。

乐观锁沿用现状：更新同时带 body 里的 `resource_version` 与 `If-Match`；
409 `resource_version_conflict` 时重取并重置表单，toast 提示
「有人改过它，已刷新到最新」——**不覆盖**。

---

## 7. 发放接入令牌

`IssueAccessModal` 取代 `IssueEnrollmentModal`：

| 字段 | 现状 | 改为 |
|---|---|---|
| 标签 | `credential_label` 文本框 | 「它会在哪台机器上跑？」文本框 |
| 权限 | 五个原始名 checkbox | 人类标签 + 小字原始名（见下） |
| 令牌时效 | `<select>` 5/15/30 分钟 | `<Seg>` 5m / 15m / 30m，默认 15m |
| 密钥有效期 | `datetime-local` 可选 | `<Seg>` 30d / 90d / 1y / 不过期，默认 90d |

权限映射：

| 原始名 | 人类标签 |
|---|---|
| `observe` | 记录它的会话 |
| `search` | 检索团队记忆 |
| `get` | 读取指定笔记 |
| `channel_send` | 发送给其他 Agent |
| `channel_receive` | 接收其他 Agent 的消息 |

原始权限名以小字并排显示，因为 API 文档与审计日志用的是原始名，隐藏它会让
用户无法把界面与文档对上。可选集合仍来自 `GRANTABLE_PERMISSIONS`，
默认勾选 `observe` + `search`（与现状一致）。

密钥有效期的 seg 选项换算成 `credential_expires_at`（now + N），
「不过期」则不发送该字段——保住现状的能力。

**既有约束原样保留**：该端点不支持 `Idempotency-Key`，超时/5xx 时不自动重试，
关闭表单并刷新待认领列表让用户自己判断（`docs/on-prem-identity-frontend-integration.md` 3.3）。
4xx 时保留表单让用户改。

---

## 8. 与设计稿的记账偏离

均为有意，列此以免当作实现遗漏：

1. **「3 of 5 seats used」砍掉。** 后端没有席位概念，任何数字都是编的。
2. **Runtime 的 Codex / Claude / Custom 三选一 → 保持文本框。** `agent_type` 是
   自由文本，后端不枚举也不校验取值；单选框会谎称存在一个封闭集合。
   标签改成人类语言的「Runtime」。
3. **「未批准」格 → 换成「会话」。** 未批准数只能靠拉一页 tool-calls 数条数，
   会被 `limit` 截断成假数字。`activity` 端点直接给 `session_count`，精确。
4. **「工具调用 · 24h」→「· 7 天」。** `activity` 按 `YYYY-MM-DD` 天聚合，
   凑不出滚动 24 小时窗口；写 24h 是谎报口径。
5. **member 看不到归属机器名。** `GET /v1/admin/devices/:id` 是 admin 门控，
   member 只保留既有的 `device-provisioned` 标签（`title` 里带 credential_id）。
6. **移交对自己的 Agent 也走 admin scope。** 只有 `/v1/admin/agents/:id/transfer`
   一个端点，且要求 owner 角色。页面其余动作走 me scope 时这一个是例外。
7. **Esc 与点遮罩不关闭仪式。** 与站内其他 `Modal` 的行为不一致，但一次性密钥
   被误触销毁的代价远高于一致性。
8. **阶段 3 的密钥坐标绑定双保险简化为一条。** 见 §5.4。

---

## 9. 需要顺带改的两个页面

Recent behaviour 与页头的两个跳转要真的带上过滤，否则点过去是一张空表、
用户还得自己粘 `agent_id`。两个目标页当前把 agent 过滤放在组件内 `useState`：

- `pages/AdminSessionAuditPage.tsx`：三个视图各有一个 `agentId` state
- `pages/AdminExplorerPage.tsx`：一个 `agentId` state

改动限于**把 URL 查询参数 `?agent=<agent_id>` 作为该 state 的初始值**
（两页用同一个参数名），其余布局、筛选器、分页一律不动。用户随后手动改动
筛选框不写回 URL——本阶段不做双向绑定。这两页在阶段 5 会被重画，刻意不多碰。

页头两个按钮各自门控：「查看它的会话」需 `view.audit`，
「查看它的记忆」需服务端能力 `view.team-memory`。

---

## 10. 错误处理

| 情形 | 表现 |
|---|---|
| Agent 404 / 无权见 | not-found 卡 + 返回 `/management` 的链接 |
| Agent 取数失败（非 404） | 整页可重试的错误卡 |
| 密钥列表任一腿失败 | 该卡塌成区块错误 + 重试，另一张卡与其余段照常 |
| 机器名解析失败 | 静默退回显示 `credential_id` |
| Recent behaviour 失败 | 该格塌成一行说明，不影响其余 |
| 409 乐观锁冲突 | 重取 + 重置表单 + warn toast，不覆盖 |
| 发放令牌 4xx | 保留表单 + 内联错误 |
| 发放令牌超时 / 5xx | 关闭表单 + 刷新待认领列表 + warn toast，**不重试** |
| 退役后 | 表单只读，动作卡换成终态说明 |

---

## 11. 测试

**scope 判定**（`useAgentScope` 单测 + 页面 DOM 测试）

- member 看自己的 → me scope，有发放按钮
- admin 看自己的 → 读 admin、动作 me，**有发放按钮**（这一条是本阶段修的洞）
- admin 看别人的 → 只有暂停，无发放 / 编辑 / 退役
- owner 看别人的 → 编辑 / 恢复 / 退役 / 移交齐全
- `owner_membership_id` 或 `me.membership_id` 缺失 → 判为「不是自己的」

**同源计数**

- 确认弹窗列出的密钥行数 = 「Active keys」卡的行数；把喂给弹窗的数组截断一条，
  测试必须变红
- 全量翻页：两页游标的 fixture，断言两页都进了数组
- 服务端返回一条 `expires_at` 已过期的「active」credential → 不计入确认框

**仪式**

- 关闭后 `localStorage` / `sessionStorage` / `location.href` 三处均无令牌子串
- 第一次点关闭不关闭，出现「先别关」；第二次才关
- `Esc` 与点遮罩不关闭
- 四个调用点各渲染一次，断言各自的 title 与命令块存在/不存在

**生命周期**

- 409 → 重取且表单值来自新数据（不是本地草稿）
- 退役后表单只读且三张动作卡消失
- 移交：选定成员后调用 admin scope 端点

**降级**

- 四条腿各自失败一次，断言其余段仍渲染
- member 页面上 Recent behaviour 根本不挂载（计数 admin 请求为 0）

**回归**

- 旧路由 `/agents/:id` 与 `/admin/agents/:id` 仍重定向到 `/management/agents/:id`
- `npm --prefix web test` 与 `npm --prefix web run build` 全绿

---

## 12. 全局约束

- 纯前端，零后端改动
- 不新增前端运行时依赖
- 按钮一律走 `components/Button.tsx`，禁止 `.btn.ghost` 这类点分写法
- 样式只引用 `--color-*` / `--space-*` / `--font-*`；**禁止**使用
  `tokens.css` 第二个 `:root` 块里的兼容别名（`--bg` `--muted` `--accent`
  `--text` `--border` `--surface` `--mono` …）
- 新特性样式写进新建的 `styles/features/agent-detail.css`，不追加到已有文件
- 间距用 `layout.css` 的工具类，不用 inline style
- 一次性密钥只展示一次，永不进入 `localStorage` / `sessionStorage` / URL / 日志
- 提交信息用中文

---

## 13. 风险

| 风险 | 缓解 |
|---|---|
| 全屏仪式在三主题下未验证（阶段 3 同样待验） | 与阶段 3 的三主题验证合并进行；仪式自带朱红铺底，dark / arcade 下的白字对比度需实测 |
| 四个调用点同时迁移，回归面比看上去大 | 每个调用点各有 DOM 测试断言其 title 与命令块；`SecretCard` 删除后编译期即可发现漏改 |
| 密钥全量翻页在异常数据下可能循环 | 复用 `listAllMembers` 的 `seenCursors` 重复游标守卫 |
| 顺带改的两个 Governance 页在阶段 5 会重画 | 改动限定为「读 URL 参数作为初始值」，不碰布局，冲突面最小 |
