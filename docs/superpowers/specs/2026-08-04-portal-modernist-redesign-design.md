# Human Portal 按 Modernist 设计重构

Date: 2026-08-04
Status: Accepted

把 `web/`（Human Portal）从当前的侧边栏 IA + 手写 beige 主题，重构为 `docs/w.html`
交付的 **Modernist** 设计系统与信息架构。设计稿是一份自解压的高保真交互原型；
本文是它与现有代码库、API 契约之间的落地约定。

参考：
- 设计稿：`docs/w.html`（bundle，内含 Modernist token/组件 CSS 与全产品原型）
- 现状与痛点：`docs/frontend-redesign-brief.md`
- 前端约定：`AGENTS.md` §Web frontend
- API 契约：`docs/on-prem-identity-frontend-integration.md`、`docs/on-prem-operations-frontend-integration.md`

---

## 1. 目标与非目标

**目标**

1. 落地 Modernist 设计系统（Archivo 字族、朱红 `#ec3013` 强调色、零圆角、2px 分隔线、OKLCH 九级色阶）。
2. 把 IA 从「侧边栏 5 组」改为「顶栏 5 分区 + 二级 subnav」，并新增产品当前缺失的 **Overview** 落地页。
3. 把 Members / Devices / All Agents 三张平行表统一成一棵体现权限继承的 **访问树**。
4. 把界面文案从 API 术语改写为人类语言。
5. 把一次性令牌的展示升级为不可误关的**仪式**。
6. 全站响应式（桌面 → 平板 → 手机）。

**非目标**

- 不改任何写路径的后端语义。唯一的后端改动是新增一个只读聚合端点。
- 不做无障碍专项。现有 `aria-*` / `role` 属性不允许倒退，但不新增系统性 a11y 工作。
- 不引入任何前端运行时依赖。生产依赖仍限于 react / react-dom / react-router-dom / react-markdown / remark-gfm。

---

## 2. 推进策略

**新壳并行 + 逐屏迁移。** 先落设计系统与新外壳，旧页面原样挂进新壳继续可用，
再一屏一屏重画。每个阶段都可发布、可验收，回归风险分散。

组件层走**原地换血**：`web/src/styles/` 重写为 Modernist；`components/` 下现有组件的
**外部 API 一个不变**，只把内部输出的 class 重绑到新类名；缺的组件补齐。
`AGENTS.md` 既有的三条硬约定（按钮必须走 `Button.tsx`、只引用 CSS 变量、用布局工具类
而非 inline 间距）继续成立。设计原型自身大量使用 inline style，那是原型产物，不照搬。

---

## 3. 样式与组件层

### 3.1 `web/src/styles/` 重组

现在按「页面」分（base/themes/components/operations/wiki/pulse/session-audit/apps/teams），
改为按「层」分：

```
styles/
  tokens.css       Modernist 变量单一真源
  themes.css       beige(默认) / dark(反转) / arcade(朱红铺底)
  base.css         reset、排版刻度、链接、焦点环、选区
  components.css   .btn .card .tag .table .seg .input .radio .field .dialog .hr .elev-*
  layout.css       .app-shell .topbar .subnav .page .toolbar .stack .row .section
  features/        仅限确实特殊的：access-tree.css wiki.css overview-chart.css palette.css
```

核心 token（取自设计稿，不再自行发挥）：

| token | 值 |
|---|---|
| `--color-bg` / `--color-surface` / `--color-text` | `#f3f2f2` / `#eae9e9` / `#201e1d` |
| `--color-accent` / `--color-accent-2` | `#ec3013` / `#e15b47` |
| `--color-divider` | `color-mix(in srgb, #201e1d 40%, transparent)` |
| neutral / accent / accent-2 | 各 9 级（100…900），OKLCH 同一亮度刻度 |
| `--font-heading` / `--font-body` | `Archivo`，heading 字重 800 |
| `--space-1..8` | 4 / 8 / 12 / 16 / 24 / 32 |
| `--radius-sm/md/lg` | **全部 0** |
| `--shadow-sm/md/lg` | ink 混色阴影三级 |

排版刻度：body 15px/1.55；h1 42 / h2 32 / h3 25 / h4 20 / h5 16 / h6 13（h6 大写 + `0.08em` 字距，
用作 kicker）。`2px` 主分隔线、`1px` 次分隔线是这套设计的骨架特征，定义在 `layout.css`，
不由各页面自行绘制。

Archivo 字体文件随仓库自托管（设计稿已内嵌 3 个 woff2 子集：latin / latin-ext / vietnamese），
不引外部 CDN。

### 3.2 主题

三个主题名保留，全部重锚到 Modernist 调色板：

| 主题 | bg | text |
|---|---|---|
| beige（默认） | `#f3f2f2` | `#201e1d` |
| dark | `#201e1d` | `#f3f2f2` |
| arcade | `#dd2b0f` | `#fff` |

arcade 用 `--color-accent-600` 而不是设计稿原本的 `--color-accent-500`（`#ec3013`）：
后者配白字实测只有 4.20:1，达不到 WCAG AA 的 4.5；降一档后是 4.74:1，色相差别
几乎不可分辨。这是 §9 风险表「arcade 对比度」那条的实际处置结果。

仍通过 `<html data-theme>` 切换、由 `lib/theme.ts` 持久化，机制不变。
主题切换入口从侧边栏底部的 `<select>` 迁到 `Settings › Appearance`。

### 3.3 组件

| 保留、外部 API 不变、仅重绑类名 | 新增 |
|---|---|
| `Button` `Badge`/`RoleBadge` `Modal` `ConfirmDialog` `Toasts` `SecretCard` `PagedListCard` `Countdown` `ErrorBoundary` `RegionError` `TeamSwitcher` | `Card`（kicker/title/body/meta 四槽）`Tag` `Seg` `Field` `DataTable` `MetricTile` `Kicker` `CommandPalette` `Crumbs` `EmptyState` |

两处必要的能力扩展：

- `ConfirmDialog` 增加 `preview` 槽。设计里级联吊销把受影响的 Agent 表格直接摊在弹窗内，
  现有的 `consequences: string[]` 装不下。
- `SecretCard` 升级为 `SecretCeremony`：全屏态、倒计时、复制 token / 复制客户端命令双按钮、
  「接下来会发生什么」三步、「丢了怎么办」、关闭前二次确认。

---

## 4. 信息架构与路由

### 4.1 顶栏

```
PAX Nexus │ [团队切换器] │ Overview  Management  Governance  Apps  Settings │ ⌘K │ 用户+角色
                          └─ 二级 subnav（sticky，随分区变化）
```

| 顶栏项 | 可见条件 | 二级 |
|---|---|---|
| Overview | 服务端 capability `view.operations` | — |
| Management | 总是可见 | Access tree · Members · Devices · Agents · Invitations（后四项 admin+） |
| Governance | 任一子项可见时 | Audit trail · Session audit（`view.audit`）／ Pipeline health（`view.operations`）／ Memory explorer（`view.team-memory`） |
| Apps | 总是可见 | Wiki · Todos |
| Settings | 总是可见 | Team（仅 SaaS）· Memory rules · Model usage · Appearance |

`Settings › Memory rules` 里的 **Reset & rebuild 仍然是 owner-only**（沿用现状），
其余设置项对全体成员可见。

**落地页**：admin+ 落 Overview；member 只看到 `Management / Apps / Settings` 三项，落 Management。

### 4.2 路由表与重定向

```
/overview                            ←  /admin/pulse
/management                          ←  /agents
/management/members                  ←  /admin/members
/management/devices[/:credentialId]  ←  /admin/devices[/:credentialId]
/management/agents[/:agentId]        ←  /admin/agents[/:agentId] 与 /agents/:agentId
/management/invitations              ←  /admin/invitations
/governance/audit                    ←  /admin/audit
/governance/sessions                 ←  /admin/session-audit
/governance/pipeline                 ←  /admin/operations
/governance/memory[/:noteId]         ←  /admin/explorer[/notes/:noteId]
/apps/wiki[/:slug]                   ←  /wiki/browse?page=<slug>
/apps/todos                          ←  /todo
                                     ←  /apps（旧启动器，重定向到 /apps/wiki）
/settings/team                       ←  /team
/settings/memory                     ←  /wiki（ingestion + rebuild + voice 部分）
/settings/usage                      ←  /wiki（LLM 用量卡部分）
/settings/appearance                 ←  侧边栏主题下拉
```

壳外路由：`/login` `/join` `/welcome`（onboarding）`/bootstrap` `/suspended` `/not-configured`。

重定向表写成单一数据结构（`lib/legacyRoutes.ts`），由单测遍历断言每一条，避免漏项 404。
`?page=` / `?revision=` 形式的旧 wiki 深链在重定向时转换为 path + query。

`/management/agents/:agentId` **一个路由服务两种 scope**：页面按 `me.role` 与目标 Agent 的
`owner_membership_id` 判定走 `/v1/me/agents/*` 还是 `/v1/admin/agents/*`，
取代现有 `AgentDetailPage` / `AdminAgentDetailPage` 两个近乎重复的文件。

### 4.3 on-prem 差异

无团队切换器；`Settings › Team` 隐藏。其余完全一致——访问树与 Overview 都依赖
capability 而非部署形态。

### 4.4 被删除的东西

| 删除 | 去向 |
|---|---|
| `AdminPulsePage` + `pages/pulse/`（AgentGrid / KnowledgeFlow / LiveEventsFeed）+ `pulse.css` | 内容并入 Overview |
| `AppsPage`（`/apps` 卡片墙启动器） | Apps 成为顶栏分区 |
| 全屏逃逸模式（`app-fullscreen`、`app-back` 返回链） | Wiki 与 Todos 回到壳内 |
| `WikiStatusPage`（`/wiki` 策略页） | 拆为 Settings › Memory rules 与 Settings › Model usage |
| 侧边栏（`PortalShell` 的 aside、折叠状态、nav 分组持久化） | 顶栏 + subnav |

---

## 5. 数据流

### 5.1 唯一的后端新增

```
GET /v1/admin/overview?window=1h|24h|7d          门控：capability view.operations
{
  from_time, to_time, generated_at,
  metrics:   { evidence_captured, live_notes, notes_expiring_today,
               recalls_served, recall_accept_rate, p50_ms, p95_ms, attention_count },
  series:    [ { bucket_at, evidence, facts, recalls } ],
  note_mix:  [ { kind, count, pct } ],
  attention: [ { kind, severity, title, body, ref, target } ]
}
```

分桶粒度由 `window` 决定，服务端固定：`1h` → 10 分钟桶（6 个）；
`24h` → 3 小时桶（8 个）；`7d` → 1 天桶（7 个）。前端不参与分桶。

只读，不触碰任何写路径。`metrics` 与 `series` 复用 operations 的既有计数来源；
`note_mix` 是 team-note 按 kind 的分组计数；`attention` 是四个既有来源的汇流：

1. session-audit 的高危 finding
2. 抽取 quarantine
3. 即将过期的 pending 邀请
4. 即将过期且未认领的 enrollment

3 与 4 的「即将过期」阈值统一为 **24 小时内**。

每条 `attention` 携带 `target`，前端直接拼成站内链接，使「看到问题 → 点进去处理」闭环成立。

窗口校验复用 operations 既有规则（不得超过部署保留期）。

### 5.2 访问树取数（无新端点）

| 层 | admin+ | member |
|---|---|---|
| 人 | `GET /v1/admin/members` | 自己（`GET /v1/me`） |
| 机器 | `GET /v1/admin/devices`，按 `created_by_membership_id` 客户端分组 | **无此层** |
| Agent | 展开机器时 `GET /v1/admin/devices/:credential_id`（响应含 `agents[]`）；某人的散装 Agent 用 `GET /v1/admin/agents?owner_membership_id=` | `GET /v1/me/agents` |
| 散装（手工注册） | `provisioned_by` 缺失即为手工注册 | 同左 |

member 没有机器层，因为 `CreateDeviceEnrollment` 在后端要求 Owner/Admin
（`internal/deployment/onprem/registry.go`，由 `registry_test.go:425` 锁定）。
member 的 Agent 若带 `provisioned_by`，显示机器名但不可下钻——他无权读 `/v1/admin/devices/:id`。

下钻**懒加载**：进入 Management 只打人 + 机器两个列表请求；展开某节点才取其子层。
计数摘要来自列表响应本身，不逐个探测。同一节点的展开结果在本次会话内缓存。

### 5.3 ⌘K 命令面板

不加端点，三路联合：本地静态导航动作 + Agent（`?q=`）+ Wiki（`GET /v1/wiki/search`）。
200ms debounce，`AbortController` 取消在途请求。

### 5.4 轮询与区块状态

沿用现有 `usePolling` / `usePolledRegion` 契约，不做改动：页面可见性门控、
唤醒时单次刷新、`loading | ready | error` 三态加「自动刷新失败，数据可能过期」。
Overview 轮询 **10s**（对齐现有 Pulse；不采用原型的 4s）。

---

## 6. 错误处理

沿用现有契约，三条新增规则：

- `/v1/admin/overview` 失败时整页不塌。五个区块各自持有 region 状态，
  失败的区块显示「这一块没刷新」+ 重试，其余照常工作。
- `attention` 为空是**正向空态**（"Nothing needs you right now"），
  既不是错误态也不是加载态。
- ⌘K 的 Agent / Wiki 检索失败**静默降级**为只剩导航动作，不弹 toast。

### 6.1 每阶段红线 checklist

每个阶段的 PR 描述逐条勾选（源自 `docs/frontend-redesign-brief.md` §7）：

- [ ] 能力门控：无权限项不渲染，无权限路由重定向
- [ ] 一次性密钥仍然只出现一次，且不进任何持久化通道
- [ ] 四个不支持 `Idempotency-Key` 的创建操作仍然不自动重试
- [ ] 乐观锁冲突走「重取而非覆盖」
- [ ] 级联后果在确认前完整披露
- [ ] 终态（retired / removed）不可逆且动作区消失
- [ ] 区块级错误隔离
- [ ] 审计页原始 ID 永远可见

---

## 7. 迁移阶段

响应式不单列阶段——每个阶段交付时自带断点。

**每个阶段各自生成一份实施计划并独立交付**，不把七个阶段压成一份计划。
本文是七个阶段共同的设计依据。

### 阶段 1 · 设计系统 + 外壳

`styles/` 五文件重组；组件重绑 + 10 个新组件；`AppShell`（顶栏 / sticky subnav / ⌘K）；
新路由表 + 旧路由全量重定向；现有页面原样挂进新壳（仅移除各自重复的品牌区）。

**过渡期的新路由 → 旧页面映射**（两个尚不存在的新页面暂由旧页面顶替，
分别在阶段 2、3 被替换）：

| 新路由 | 阶段 1 渲染 | 何时替换 |
|---|---|---|
| `/overview` | 现有 `AdminPulsePage` | 阶段 2 |
| `/management` | member → 现有 `MyAgentsPage`；admin+ → 现有 `AdminAgentsPage` | 阶段 3 |
| 其余全部 | 与旧路由一一对应的现有页面 | 阶段 4–7 |

验收：`npm run build && npm test` 绿；三主题各过一遍；每条旧 URL 都能落到新 URL；
owner / admin / member 三种角色顶栏项正确。

### 阶段 2 · Overview

后端 `GET /v1/admin/overview`（IDL + handler + 聚合查询 + Go 测试，走 80% 覆盖率门禁）；
前端五区块（指标行 / 吞吐图 / 记忆构成 / 谁在写 / attention 队列 / 事件流），
事件流保留「新事件挂起」模式；删除 `AdminPulsePage` + `pages/pulse/` + `pulse.css`。

验收：断掉任一区块端点其余仍可用；1h/24h/7d 切换正确；
无 `view.operations` 时顶栏无该项且直连 `/overview` 被重定向。

### 阶段 3 · Management

`AccessTree`（三层下钻 + 面包屑 + 计数摘要 + loose 分组）；member 自根 / admin+ 团队根；
Members / Devices / Agents / Invitations 四张平表按 Modernist 重画（保留为扁平视图）；
级联吊销弹窗带表格预览。

验收：三种角色各走通一次完整下钻；吊销机器时预览行数 = 实际级联数。

### 阶段 4 · Agent 详情 + 令牌仪式

两个 AgentDetailPage 合一（scope 自适应）；五段式详情
（Identity / Lifecycle / Waiting to be claimed / Active keys / Recent behaviour）；
`SecretCeremony` 全屏 + 关闭二次确认；生命周期三个动作的后果文案按设计重写。

验收：乐观锁冲突走「重取不覆盖」；仪式结束后 `localStorage`、`sessionStorage`、
`location` 三处均无 token；退役终态不可逆。

### 阶段 5 · Governance

Audit trail / Session audit（Findings·Tool calls·By day 三视图）/ Pipeline health /
Memory explorer 四屏。

验收：区块级错误隔离仍成立；Explorer 溯源链完整（源事件 → 抽取 → 候选 → 版本 → 投递 → 召回决策）。

### 阶段 6 · Apps + Settings

Wiki 回壳内、slug 进 path（legacy `?page=` 重定向）、Todos；
`/wiki` 策略页拆成 Settings › Memory rules 与 Settings › Model usage；Team / Appearance。

验收：全屏逃逸模式彻底消失；旧 wiki 深链可用。

### 阶段 7 · 入场动线 + 清理

Login / Join / Onboarding / Bootstrap / Entry / Suspended / NotConfigured 按 Modernist 重画；
SaaS 与 on-prem 两条入场统一为 `/welcome`，按 profile 分叉；删死代码；
更新 `AGENTS.md` §Web frontend 与两份 frontend-integration 文档里的路由引用。

验收：两种部署形态各走一遍首次进入；`AGENTS.md` 与实际结构一致。

---

## 8. 测试

**前端（vitest + testing-library）**

- 路由重定向表：遍历 `legacyRoutes` 数据结构逐条断言
- `AccessTree` 三层下钻与 member / admin 分叉
- capability 门控：顶栏项渲染与直连路由的重定向
- `CommandPalette` 键盘路径（↑↓ / ↵ / esc）
- `SecretCeremony` 结束后三处存储无 token

**后端（Go）**

- overview handler 的窗口校验、capability 门控、空数据形态
- 聚合查询的 Postgres 适配器测试
- 走现有 80% 手写代码覆盖率门禁

---

## 9. 风险

| 风险 | 缓解 |
|---|---|
| 阶段 1 结束是「新皮 + 旧骨架」的过渡态 | 阶段 1 只动壳与样式基建、不动页面内部结构；尽快进入阶段 2；不在此状态做外部演示 |
| 访问树懒加载仍可能 N+1（一人 5 台机器 = 5 个请求） | 计数摘要来自列表响应本身；同一节点展开结果本次会话内缓存；实测超阈值再考虑后端聚合 |
| overview 聚合查询在大表上慢 | 复用 operations 的窗口上限校验；上线前用 staging 数据量实测，必要时补索引 |
| 路由重定向漏项导致 404 | 重定向表写成单一数据结构 + 单测遍历 |
| arcade 主题朱红铺底可能不过对比度 | 阶段 1 对三主题做一次 WCAG AA 校验；不合格则把正文压到 `--color-accent-700` |

---

## 10. 需要同步更新的文档

- `AGENTS.md` §Web frontend：`styles/` 文件清单、「全屏应用渲染在 PortalShell 之外」
  这条规则删除、组件清单补充
- `docs/frontend-redesign-brief.md`：重构完成后现状描述失效，标注为历史文档
- `docs/on-prem-identity-frontend-integration.md`、`docs/on-prem-operations-frontend-integration.md`：
  其中引用的 Portal 路由路径
