# Team Memory Portal — 前端重设计 Brief

面向外部设计（Claude Design 等）的产品说明：**现有功能全集、用户动线、API 契约、以及现状 UI 的约束与痛点**。

写作依据是当前代码（`web/src/`、`internal/teamnote/transport/httpapi/router/`、`idl/*.thrift`），不是规划文档 —— 下文描述的每个页面、按钮、端点都在 `main` + `feat/saas-team-devices-r2` 上真实存在。

---

## 1. 这个产品是什么

**PAX Nexus / Team Memory** 是「AI Agent 团队的共享记忆基础设施」。

一句话的心智模型：

```
Agent 干活时产生的 session 证据
    ↓ 落进不可变的 Session Lake（Evidence Lake）
    ↓ 两条独立的产品管线各自消费同一批证据
    ├── Team Note   短生命周期、带证据引用的协作事实（谁在做什么、卡在哪、交接给谁）
    │                 → 被动召回：另一个 Agent 开工时自动拿到相关上下文
    └── PageWiki    长期沉淀、逐句引用来源的百科页面（决策、系统、概念）
                      → 人类阅读；也可作为 Agent 的检索面
    ↓ 在其之上长出「预置应用」
    └── Todos       从 Team Note 的 blocker / handoff 自动推导待办建议
```

Portal（本文的主角）是这套后端的**人类控制面**。它做四件事：

1. **身份与准入** —— 谁能进这个团队、以什么角色。
2. **Agent 与凭证的生命周期** —— 注册 Agent 身份、发一次性令牌、机器整机接入、吊销、移交。
3. **知识消费** —— 读 Wiki、用 Todos。
4. **可观测与审计** —— 记忆管线跑得怎么样、Agent 在干什么、谁动了什么、Agent 的工具调用有没有越权。

> 关键区分：**人类不生产内容**。Wiki 页面、Team Note、Todo 建议全部由后端从 Agent 会话证据里抽取生成。Portal 里几乎没有"编辑器"，只有**观察 / 配置 / 治理**。这是这个产品和 Notion/Confluence 类工具最本质的差别，也是重设计时最该被表达出来的东西。

### 两种部署形态

同一份前端代码，运行时探测：

| | **SaaS**（`app.<domain>`，GCP + Cloudflare + WorkOS OIDC） | **On-prem**（单机 / 工作站 Compose 部署） |
|---|---|---|
| 一个用户属于 | 多个 team，可切换 | 恰好一个 membership |
| 入场方式 | 创建 team 或接受邀请 | 用 operator 给的 bootstrap secret 领取首个 Owner |
| 侧边栏 | 有 Team Switcher、Team settings | 无 |
| 探测方式 | `GET /v1/teams` 返回 200 | `GET /v1/teams` 返回 501 `not_configured` |

前端用 `AuthContext` 把 `GET /v1/me` 归类成 6 种状态，路由完全由它分叉：
`loading` / `not-configured` / `unauthenticated` / `no-membership` / `active` / `suspended`。

### 角色与能力

三个角色：`owner` > `admin` > `member`。

| 能力 | owner | admin | member |
|---|---|---|---|
| 管理自己的 Agent（建 / 改 / 发凭证 / 退役） | ✅ | ✅ | ✅ |
| 查看成员、邀请 member | ✅ | ✅ | ❌ |
| 邀请 admin | ✅ | ❌ | ❌ |
| 查看全部 Agent / Devices / 审计 | ✅ | ✅ | ❌ |
| 挂起任意 Agent | ✅ | ✅ | ❌ |
| 恢复 / 退役 / 移交他人的 Agent | ✅ | ❌ | ❌ |
| Operations 控制台、Team Memory Explorer | 由**服务端下发的 capability** 决定，不由角色矩阵决定 | | |

前端有一份客户端角色矩阵（`lib/capabilities.ts`）**只用来隐藏按钮**；后端每个请求独立鉴权。另有两个**服务端下发**的 capability：`view.operations`、`view.team-memory`（出现在 `/v1/me` 的 `capabilities[]` 里），没有它对应路由直接重定向。

---

## 2. 信息架构（现状）

### 全屏 / 无外壳路由

| 路径 | 页面 | 出现条件 |
|---|---|---|
| `/` (catch-all) | **LoginPage** — 一个 "Continue with OIDC" 按钮 | 未登录 |
| `/join` | **JoinPage** — 接受邀请（token 来自 URL fragment） | 任何状态都可达 |
| `/bootstrap` | **BootstrapPage** — 输入 bootstrap secret 领 Owner | on-prem + 无 membership |
| `/` (catch-all) | **EntryPage** — 贴邀请码 / 去 bootstrap | on-prem + 无 membership |
| `/onboarding` | **OnboardingPage** — 创建 team ⇄ 用邀请加入（分段控件切换） | SaaS + 无 membership，或已登录用户主动新建 team |
| `/` (catch-all) | **SuspendedPage** — 账号被停用的死胡同页 | membership 被 suspend |
| `/` | **NotConfiguredPage** — 部署未配置身份 | 后端 501 |
| `/wiki/browse` | **Wiki 阅读器**（全屏，脱离侧边栏） | active |
| `/todo` | **Todos**（全屏，脱离侧边栏） | active |

### PortalShell（侧边栏 + 内容区）

侧边栏可整体折叠（`«`/`»`，状态存 localStorage），导航按**可折叠分组**组织，每组的展开状态独立持久化；当前路由所在的组强制展开。

```
Team Memory Portal          ← 品牌区
[Team Switcher]             ← 仅 SaaS；折叠时退化成头像

▾ Personal
    My Agents               /agents            所有人
▾ Knowledge
    Apps                    /apps              所有人
▸ Directory                                    （SaaS 或 admin+ 才出现整组）
    Team settings           /team              SaaS 全体成员
    Members                 /admin/members     admin+
    Invitations             /admin/invitations admin+
▸ Fleet                                        （admin+ 才出现整组）
    All Agents              /admin/agents
    Devices                 /admin/devices
▸ Insights                                     （admin+ 才出现整组）
    Pulse                   /admin/pulse       需 view.operations
    Explorer                /admin/explorer    需 view.team-memory
    Audit Events            /admin/audit
    Session Audit           /admin/session-audit
    Operations              /admin/operations  需 view.operations

──────────────
[主题选择：Beige / Dark / Arcade]
user@example.com
[role badge]              [Sign out]
```

内容区路由（全部在 shell 内）：

| 路径 | 页面 | 一句话 |
|---|---|---|
| `/agents` | My Agents | 我拥有的 Agent 身份卡片墙 + 建 Agent |
| `/agents/:agentId` | Agent Detail | 档案编辑 + 生命周期 + 凭证/enrollment 管理 |
| `/apps` | Apps | 应用启动器（纯导航，无数据请求） |
| `/wiki` | Wiki 策略页 | ingestion 开关、生成语言/自定义指令、LLM token 用量、Reset & rebuild |
| `/team` | Team settings | 只读团队信息 + 通往 Members/Invitations |
| `/admin/members` | Members | 成员列表，改角色/状态 |
| `/admin/invitations` | Invitations | 发/撤邀请，一次性 join URL |
| `/admin/agents` `/admin/agents/:id` | All Agents | 全团队 Agent 治理：挂起/恢复/退役/移交 |
| `/admin/devices` `/admin/devices/:credentialId` | Devices | 整机接入凭证 + 级联吊销 |
| `/admin/audit` | Audit Events | 不可变审计流水，可展开详情 |
| `/admin/session-audit` | Session Audit | 三视图：Findings / Tool calls / Activity |
| `/admin/operations` | Operations | 活动汇总、管线健康、存储、近期事件 |
| `/admin/pulse` | Team Pulse | 10s 轮询的实时 Agent 活动看板 |
| `/admin/explorer` `/admin/explorer/notes/:noteId` | Team Memory Explorer | 一条 Team Note 的全链路溯源 |

**默认落地页是 `/agents`。产品没有首页 / dashboard / 概览。**

---

## 3. 用户动线

### J1 · 首次进入（SaaS）

```
访问 app.<domain>
  → 未登录 → LoginPage：单按钮 "Continue with OIDC →"
     （这是顶层跳转 GET /v1/auth/login → 302 到 WorkOS，不是 fetch）
  → WorkOS 登录 → 回调固定落到 Portal URL
  → GET /v1/me 无 membership → OnboardingPage
     ├─ 「Create a team」：输入队名 → 实时展示派生 slug → 创建 → 自动切换当前 team → /agents
     └─ 「Join with invitation」：粘贴 token 或整条 join 链接 → 接受 → /agents
```

**continuation 机制**：登录前若停在某个内部路径，会存 `return_url`；若手上有邀请 token，则存 pending invitation。回来后**邀请优先于 return_url**，两者永不混用。

### J1' · 首次进入（on-prem）

```
LoginPage → OIDC → 无 membership → EntryPage
  ├─「我有邀请码」→ 贴 token → /join → 接受
  └─「首次安装」→ /bootstrap → 输入 operator 给的 bootstrap secret
       → 成为首个 Owner，bootstrap 永久关闭，旧的静态 admin key 失效
```

Bootstrap secret 只走 `X-PAX-Bootstrap-Secret` 请求头，请求一结束立刻清空输入框，永不进 URL / storage / 日志，**永不自动重试**。

### J2 · 接受邀请（`/join`）

邀请链接形如 `https://app.example.com/join#invite=tm_invite_inv_01.xxxx`。token 在 URL **fragment** 里，页面首帧之前就搬进 sessionStorage 并抹掉地址栏 —— 保证它不进 access log、不进 Referer。

页面按 auth 状态自己分叉：未登录 → 「登录并继续」；已有 membership（on-prem）→ 「你已有身份，邀请不能覆盖」；正常 → 展示 token 与目标邮箱 → Accept。

**所有失败（过期 / 撤销 / 已用 / 邮箱不符 / 格式错）折叠成同一句话**，防止泄露邀请状态。

### J3 · 注册 Agent 并发凭证 ★核心动线

```
/agents → 「+ Create Agent」
  弹窗字段：agent_id（不可变、全局唯一）、display_name、description、
            agent_type（codex / claude / custom）、directory_visible 勾选
  → 创建成功直接跳到 /agents/:agentId

Agent Detail 页三段：
  ① Profile 卡  ── 编辑 display_name / type / description / directory_visible
                    带 resource_version 乐观锁；底部是 Suspend / Resume / Retire
  ② Enrollments 卡 ── 「Issue enrollment」→ 选 credential_label、权限勾选
                       (observe / search / get / channel_send / channel_receive)、
                       令牌有效期、凭证有效期
                    → 返回**一次性 token**，用醒目的 SecretCard 展示 + 复制客户端命令
  ③ Credentials 卡 ── 已兑换出的长期凭证，可单条吊销
```

一次性令牌的处理规则（贯穿整个产品）：只在响应里出现一次、只存在于内存、不写 storage/日志/分析、**不支持 Idempotency-Key 因此绝不自动重试**；超时的正确做法是刷新列表看是否已产生记录。

### J4 · 整机接入（Devices）

比逐个 Agent 发凭证更高一层的抽象：

```
/admin/devices → 「+ Create Device Enrollment」
  只填 device_name + 令牌有效期（5/15/30 分钟）—— 没有权限矩阵，
  因为 Device 凭证固定只有 agent_provision 一种权限
  → 一次性 token + 「Copy client command」（paxl device connect ...）
  → 在目标机器上执行 → 该机器上的 Agent 此后可自助注册身份、自助 mint 凭证

/admin/devices/:credentialId
  → 该 Device provisioned 出来的 Agent 清单（含轮换历史）
  → 「Revoke Device」：弹窗直接把级联预览表格摊开
     （会连带吊销的 N 条 Agent 凭证，含 agent、credential id、last used）
```

由 Device 自助注册的 Agent 在全站都带一个 `provisioned by` 徽标。

### J5 · 团队管理

- **Members**：按状态/角色筛选的表格；编辑弹窗改 `role` + `status`。系统始终保证至少一个活跃 Owner，破坏该约束的改动被后端以 `last_active_owner` 拒绝。挂起或移除会**吊销该用户的人类会话、全部 Agent 凭证、待处理 enrollment**；恢复不会还原旧密钥。`removed` 是终态，只能重新邀请。
- **Invitations**：发邀请（邮箱 + 角色 + 24h/2d/7d 有效期）→ 一次性 join URL 卡片；列表里 pending 的显示**倒计时**；owner 可撤销任意邀请，admin 只能撤 member 级的。

### J6 · Fleet 治理（All Agents）

搜索 + Owner 下拉 + 状态分段过滤的表格。每个治理动作都走 `ConfirmDialog`，**弹窗里逐条列出后果**，例如挂起：

> - 立即吊销该 Agent 的全部凭证与待处理 enrollment
> - 恢复为 active 不会还原旧密钥，必须重新发一次 enrollment

移交（transfer）会把旧 Owner 名下的凭证与 enrollment 全部吊销。退役（retire）是**不可逆终态**。

### J7 · 读 Wiki（`/wiki/browse`，全屏）

```
顶部：← All apps ｜ Wiki 标题 ｜ 搜索框
左侧：主题树导航栏（可下钻的层级 topic + 页面），显示总页数
中间：文章
   - 归档页顶部有 banner + 「See successor page」
   - Current / Historical 徽标
   - 标题 + entity_type 徽标 + 摘要 + slug/revision id
   - Markdown 正文，行内渲染引用与页面间链接（Xanadu links，可点击跳转）
   - Revision history：r1 / r2 / ... 逐版切换（写进 URL query，可分享）
   - Xanadu links：Outgoing / Incoming 双栏
搜索：命中当前版本的段落，显示 page title + passage + section + score
空态：「Your wiki is ready for its first page」
```

auto-inject 开着时，导航树每 3 秒静默刷新，新页面自己冒出来。

### J8 · Wiki 运营（`/wiki`，在 shell 内）

四张卡：**进度**（pending sessions、last processed、rebuild 状态）、**Ingestion**（auto-inject 开关；手动注入指定 session id；Owner 才有的 **Reset & rebuild**，可选起始日期，返回 202 后台执行）、**Generation settings**（输出语言：跟随证据 / 简体中文 / English / 自定义；自定义指令 textarea；提示"只影响未来运行，要重刷已有页面请用 rebuild"）、**LLM token usage**（按 component × model 的 calls / input / cache hit / cache miss / output tokens，可切 7/N 天窗口）。

### J9 · Todos（`/todo`，全屏）

```
「Check team memory」按钮 → 后端扫团队记忆产出建议
Suggestions 区：每条带 kind 徽标（blocker / handoff…）、标题、正文 → Accept / Dismiss
Todos 区：输入框直接加 todo；开放项逐条 Complete；已完成折叠在 <details> 里
```

所有变更都是**先等服务端、再整体重取**，没有乐观更新。

### J10 · Insights（四个截然不同的观察面）

- **Team Pulse** —— 唯一一个"活的"页面。10 秒轮询，三个区：知识流动图、Agent 卡片网格（写入数、产出笔记、召回数、capsule 流量、最近笔记、状态点、相对活跃时间）、实时事件流。新数据带淡入/滑入动画；用户正在往下滚时，更新会**挂起在提示条后面**而不是在眼皮底下抽动列表。
- **Operations** —— 只读运维台。时间窗（1h/24h/7d）+ Agent 过滤；活动汇总卡、管线健康（抽取 quarantine/failed、未抽取事件积压、最老未抽取时间、p50/p95 延迟）、存储快照与历史趋势、可按 operation kind/outcome 过滤的近期事件表。**每个区块独立管理 loading/ready/error/stale 状态**，一个区挂掉不影响其他区。明确不展示：原始 query、内容文本、凭证、幂等键、原始错误。
- **Team Memory Explorer**（Owner 级）—— 搜 Team Note，点进去看**全链路溯源**：当前笔记 → 关系 → provenance 时间线（源事件 → 抽取运行 → 候选 → 准入/驳回 → 版本 → 投递）→ 召回决策（每次被召回时的 disposition、驳回原因、预算丢弃、硬门失败）。
- **Audit Events** —— 不可变审计流水。actor_kind / target_kind / action / target_id 过滤，行内展开详情。ID 到人名的映射是**非权威的前端增强**，原始 ID 永远保留可见（对象删除后仍可读）。
- **Session Audit** —— 三视图分段切换（视图写进 URL）：**Findings**（`high_risk_unapproved` / `denied_tool_executed` / `visibility_unknown` / `attribution_missing`，带 severity 和证据事件 id）、**Tool calls**（工具名、输入摘要、风险等级 + 理由、审批状态）、**Activity**（按天的事件数/工具调用数/高风险数/会话数 + 工具分布）。

### J11 · 切换 team（仅 SaaS）

侧边栏 Team Switcher → 选另一个 team → `POST /v1/me/current-team` 返回重新 scope 的 principal → **整个内容区按 `current_team_id` 重新挂载**，所有视图对新 team 重新取数。

---

## 4. 贯穿全站的交互约束（重设计必须保留的语义）

这些不是装饰，是后端契约在 UI 上的投影。重画可以，语义不能丢。

| 约束 | 现状表现 | 为什么存在 |
|---|---|---|
| **一次性密钥** | 醒目的 SecretCard，带复制按钮 / 复制客户端命令 / 过期倒计时 / 明确的"丢了只能重发"文案 | 令牌只返回一次，绝不落库 |
| **不可重试的创建** | 邀请、enrollment、device enrollment、bootstrap 的失败提示是"已刷新列表，若已产生记录请使用或撤销后重建" | 这几个接口不支持 Idempotency-Key |
| **乐观锁** | 编辑表单展示 `resource_version`；冲突时提示"他人已修改，已载入最新数据"并重置表单 | 更新同时带 body 里的 version 和 `If-Match` |
| **后果前置的确认框** | ConfirmDialog 把级联影响逐条列出（尤其是 Device 吊销，直接摊开受影响表格） | 大量操作是不可逆的级联吊销 |
| **终态** | `retired` / `removed` 行显示为 "Terminal"，动作区消失 | 后端无恢复路径 |
| **区块级错误** | 每个数据区独立 loading / ready / error / "自动刷新失败，数据可能过期"，配 Retry | 轮询页面不能因一个区挂掉而整页崩 |
| **能力门控** | 无权限的导航项/按钮**不渲染**；无权限的路由重定向到 `/agents` | 前端隐藏只是便利，后端逐请求鉴权 |
| **不自动重试 429** | `Retry-After` 展示给用户，由用户决定 | 契约禁止自动重试 |
| **轮询只在页面可见时进行** | `usePolling` 统一处理可见性门控与唤醒时的单次刷新 | |
| **无全局 401 跳转** | 各页面上报 401 → 丢弃身份 → 路由守卫接管 | 避免 OIDC 重定向循环 |

---

## 5. API 契约

### 5.1 传输层

- 同源相对路径 `/v1/...`，`credentials: "include"`。**无 CORS**（每个环境一个域名）。
- 会话：HttpOnly cookie（OIDC 换取）。CSRF：double-submit —— 读 `tm_csrf` cookie，写进 `X-CSRF-Token` 头，所有非 GET/HEAD/OPTIONS 请求必带。
- 错误体：`{ "code": "...", "message": "..." }`。**`code` 驱动流程分支，`message` 只作诊断展示**。
- 分页：所有 list 端点用 `?limit=&cursor=`，响应带 `next_cursor`；cursor 是不透明字符串，原样回传。
- 幂等：支持的写操作带 `Idempotency-Key` 头（每个"用户动作实例"一个 UUID，网络重试复用、重新发起换新）。
- 乐观锁：`PATCH` 同时在 body 里带 `resource_version` 和头里带 `If-Match: "7"`。

常见 `code`：`not_configured`(501) · `resource_version_conflict`(409) · `idempotency_conflict`(409) · `last_active_owner` · `team_slug_conflict`(409) · `not_team_member`(403) · `membership_conflict` · `bootstrap_closed` · `agent_id_conflict` · `invalid_transition`(409) · `storage_not_available`。邀请类失败统一 410。

### 5.2 Portal 端点（人类会话）

**身份 / 会话**

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/v1/auth/login` | 顶层跳转，302 到 OIDC；不可用 fetch |
| GET | `/v1/auth/callback` | OIDC 回调 |
| POST | `/v1/auth/logout` | |
| GET | `/v1/me` | 返回 `HumanMe`：user_id / email / role / membership_status / **capabilities[]** / （SaaS）teams[] + current_team_id |
| POST | `/v1/bootstrap/claim` | header `X-PAX-Bootstrap-Secret`，无幂等键 |
| POST | `/v1/invitations/accept` | body `{token}`，支持幂等键 |

**Teams（SaaS；on-prem 返回 501）**

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/v1/teams` | 也被前端用作**部署形态探测** |
| POST | `/v1/teams` | `{name}`；slug 服务端派生；409 `team_slug_conflict` |
| POST | `/v1/me/current-team` | `{team_id}` → 返回重新 scope 的 `HumanMe` |

**自有 Agent**

| 方法 | 路径 |
|---|---|
| GET / POST | `/v1/me/agents`（`?status=&limit=&cursor=`） |
| GET / PATCH / DELETE | `/v1/me/agents/:agent_id`（DELETE 即 retire，version 走 query） |
| GET / POST | `/v1/me/agents/:agent_id/enrollments` |
| DELETE | `/v1/me/agents/:agent_id/enrollments/:enrollment_id` |
| GET | `/v1/me/agents/:agent_id/credentials` |
| DELETE | `/v1/me/agents/:agent_id/credentials/:credential_id` |
| POST | `/v1/me/device-enrollments` |

**管理面**（把上面 `me` 换成 `admin` 即为治理版；下列是额外的）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/v1/admin/members`（`?role=&status=`） / `/:membership_id` | |
| PATCH | `/v1/admin/members/:membership_id` | 改 role/status |
| GET / POST | `/v1/admin/invitations`（`?status=`） | |
| DELETE | `/v1/admin/invitations/:invitation_id` | |
| GET | `/v1/admin/agents`（`?owner_membership_id=&status=&q=`） | |
| POST | `/v1/admin/agents/:agent_id/transfer` | `{target_membership_id, resource_version}` |
| GET | `/v1/admin/devices`（`?status=`） / `/:credential_id` | detail 内含 `agents[]` 级联预览 |
| DELETE | `/v1/admin/devices/:credential_id` | 级联吊销，无 version 字段 |
| GET | `/v1/admin/audit-events`（`?actor_kind=&action=&target_kind=&target_id=`） / `/:id` | |

**Insights**

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/v1/admin/operations/summary`（`?from=&to=&agent_id=`） | observations / extraction / recalls / latency / errors 五组计数 |
| GET | `/v1/admin/operations/events`（`+?operation_kind=&outcome=`） | 分页事件流 |
| GET | `/v1/admin/operations/agents`（`?from=&to=`） | 每 Agent 聚合 + 最近 5 条笔记 |
| GET | `/v1/admin/operations/storage` · `/storage/history` | 组件级 logical/physical bytes |
| GET | `/v1/admin/operations/recalls/:observation_id` | 隐私安全的召回诊断（lanes、candidates、disposition/rejection 计数） |
| GET | `/v1/admin/team-notes`（`?q=&kind=&state=&agent_id=&task_ref=&thread_ref=`） / `/:note_id` | Explorer |
| GET | `/v1/admin/diagnostics/extractions/:run_id` · `/channels/:envelope_id` | |
| GET | `/v1/admin/session-audit/findings`（`?user_id=&agent_id=&session_id=&kind=&severity=&limit=`） | **只有 limit，无 cursor** |
| GET | `/v1/admin/session-audit/tool-calls`（`+?risk_level=&approval_state=`） | 同上 |
| GET | `/v1/admin/session-audit/activity`（`?from_day=&to_day=`） | 同上 |

**Wiki**

| 方法 | 路径 |
|---|---|
| GET | `/v1/wiki/navigation` · `/v1/wiki/search?q=` |
| GET | `/v1/wiki/pages/:slug` · `/revisions` · `/revisions/:revision` · `/backlinks` |
| GET / PUT | `/v1/wiki/ingestion`（`{auto_inject}`） |
| GET / PUT | `/v1/wiki/settings`（`{language, custom_instructions}`） |
| POST | `/v1/wiki/rebuild`（`{since?}` → **202**，后台执行，进度经 ingestion 状态轮询） |
| POST | `/v1/wiki/sessions/:session_id/inject` |
| GET | `/v1/llm-usage?days=` |

**Todos**

| 方法 | 路径 |
|---|---|
| GET / POST | `/v1/todo/todos`（`?status=open\|done`） |
| POST | `/v1/todo/todos/:todo_id/complete` |
| GET | `/v1/todo/suggestions` |
| POST | `/v1/todo/suggestions/refresh` · `/:suggestion_id/accept` · `/:suggestion_id/dismiss` |

### 5.3 Agent 端点（不属于 Portal，供 SDK/CLI 使用，凭 API key）

`POST /v1/observations` · `/v1/session-batches` · `/v1/stream-batches`（证据摄取）；`POST /v1/memory/search` · `/v1/memory/get` · `/v1/notes/recall`（召回）；`/v1/channel/agents` · `/v1/channel/envelopes`（Agent 间 Knowledge Capsule 收发、accept、archive）；`GET /v1/agent-identity`；`POST /v1/agent-enrollments/exchange`；`POST /v1/agent-credentials/rotate`；`POST|GET /v1/device/agent-provisions`（Device 自助注册 Agent）。

**这些端点定义了 Portal 观察到的一切**：设计 Insights 类页面时，"事件"就来自这里。

### 5.4 核心数据模型（简版）

```ts
HumanMe        { user_id, email?, role?, membership_status?, capabilities[], teams?[], current_team_id? }
TeamSummary    { team_id, name, slug, role, membership_id }
Member         { membership_id, email?, display_name, role, status, joined_at, resource_version }
Invitation     { invitation_id, token?（仅创建时）, target_email, role, status, expires_at }
AgentProfile   { agent_id, display_name, description, agent_type, status,
                 directory_visible, resource_version, owner_membership_id?, provisioned_by? }
EnrollmentMeta { enrollment_id, credential_label, permissions[], status, expires_at }
CredentialMeta { credential_id, label, permissions[], expires_at?, revoked_at?, last_used_at? }
DeviceSummary  { credential_id, device_name, status, provisioned_agent_count,
                 grantable_permissions[], last_used_at? }
WikiPage       { slug, title, entity_type?, status?, successor_slug?, revision{ sections[], markdown, citations[], links[] } }
TeamNoteSummary{ note_id, kind, subject, state, origin_agent_id, audience_agent_ids[],
                 revision, soft_expires_at, hard_expires_at }
OperationEvent { operation_kind, outcome, actor_agent_id?, duration_ms,
                 input/accepted/duplicate/result/delivered/evidence/hint/reference_items, tokens? }
SessionAuditFinding { kind, severity, summary, evidence_event_ids[], agent_id, session_id }
```

状态词表：Agent `active|suspended|retired` · Membership `active|suspended|removed` · Invitation `pending|accepted|revoked|expired` · Enrollment `pending|consumed|revoked|expired` · Device `active|revoked` · 风险等级 `low|medium|high|critical` · 审批态 `unknown|approved|denied|auto`。

---

## 6. 现有前端技术底子

- **React 18 + TypeScript + Vite + react-router v6**，SPA。
- **零 UI 依赖**：没有组件库、没有 CSS 框架、没有图表库、没有图标库。全部手写 CSS（`web/src/styles/` 下 9 个文件），自建的 `Button / Badge / Modal / ConfirmDialog / Toasts / PagedListCard / SecretCard / ErrorBoundary / Countdown`。生产依赖只有 react、react-router、react-markdown、remark-gfm。
- **主题走 CSS 变量**：`:root` 是默认 beige 亮色，`[data-theme=dark]`、`[data-theme=arcade]` 覆盖同一组 token（`--bg / --surface / --surface-2 / --border / --text / --muted / --faint / --accent / --ok / --warn / --bad / --info / --input-bg` 等）。重设计**应该继续用这套 token 体系**，只换值不换结构最省事。
- 动画：纯 CSS keyframes + 一个 rAF 计数上升 hook，尊重 `prefers-reduced-motion`。
- **响应式基本没做**：全站只有 `base.css` 里一条 `max-width: 820px`，加上 wiki 自己的三条断点。Pulse / Operations / 所有 admin 表格页在窄屏下没有专门处理。
- 有 vitest + testing-library 的少量组件测试。

---

## 7. 给设计的现状诊断

以下是从代码里能客观读出来的问题，不是主观吐槽 —— 也正是重设计最有价值的着力点。

1. **没有首页。** 默认落地 `/agents`。一个 owner 登进来，第一眼看到的是"我自己的 Agent 卡片墙"，而不是"团队现在怎么样"。Pulse 和 Operations 里已有的数据完全够拼一个真正的概览页。

2. **API 术语大量泄漏到界面上。** 界面上直接出现 `resource_version`、`Idempotency-Key`、`agent_id (immutable after creation)`、`directory_visible`、`actor_kind`、`target_kind`、`storage_not_available`，甚至有把内部文档章节号写进用户可见文案的地方。工程正确，但对非工程用户不可读。

3. **信息架构扁平且同质。** 12 个 admin 页几乎都是"筛选条 + 分段控件 + 表格 + Load more"。Members、Invitations、All Agents、Devices、Audit、Explorer 长得几乎一样，但它们的**使用节奏完全不同**（配置 vs 排障 vs 审计）。

4. **两个"应用"是逃逸出去的全屏页。** Wiki 和 Todos 通过 `/apps` 启动器跳到全屏、脱离侧边栏，靠一个 "← All apps" 返回。壳内壳外两套导航心智。

5. **Wiki 有两个入口且概念混淆。** `/wiki` 是策略/运维页，`/wiki/browse` 才是阅读器；名字和位置都容易误导。

6. **一次性密钥的展示时刻是最高风险时刻**，现在只是一张卡片。这是整个产品最该被设计好好对待的瞬间之一（复制、过期倒计时、"关掉就没了"的不可逆感）。

7. **五个 Insights 页之间没有导航连续性。** Pulse 看到某 Agent 异常 → 想查它的 operation events → 想看某条 Team Note 的溯源 → 想看它的 session audit findings，这条排障路径在设计上是断的（少数几个链接除外）。

8. **Onboarding 的两条形态分叉在视觉上是两套东西**（SaaS 的 OnboardingPage vs on-prem 的 EntryPage + BootstrapPage），可以统一。

9. **窄屏基本没管**。

10. **三个主题（Beige / Dark / Arcade）的存在感很弱**：只是侧边栏底部一个 `<select>`。

### 建议设计优先处理的顺序

**第一梯队（高频 + 高价值）**：概览首页 · Agent 详情与凭证发放（含一次性密钥时刻）· Wiki 阅读器 · Team Pulse。
**第二梯队**：Onboarding/加入的三条入场动线 · Members/Invitations · Devices 与级联吊销确认。
**第三梯队**：Operations / Explorer / Audit / Session Audit —— 它们更像专业排障工具，密度和信息层级比美观更重要。

### 不能改的东西

角色与 capability 门控规则、一次性密钥的一次性、不可自动重试的那几个创建操作、乐观锁冲突的"重取而非覆盖"、级联后果的前置披露、终态的不可逆、区块级错误隔离、审计页面里"原始 ID 永远可见"。这些是安全与正确性契约，视觉可以变，语义必须留。
