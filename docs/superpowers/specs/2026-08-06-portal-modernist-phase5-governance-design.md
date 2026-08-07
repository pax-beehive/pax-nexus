# Modernist Portal 阶段 5 · Governance 四屏

Date: 2026-08-06
Status: Accepted

七阶段重构的第 5 阶段。Governance 分区的四屏（Audit trail / Session audit /
Pipeline health / Memory explorer）按 Modernist 重画，并把 Memory explorer 从
「列表页 + 独立详情路由」改成体现**溯源链**的双栏。

上位设计：`docs/superpowers/specs/2026-08-04-portal-modernist-redesign-design.md` §阶段 5。
前一阶段：`2026-08-06-portal-modernist-phase4-agent-detail-design.md`。
设计稿：`docs/w.html`（`isAudit` / `isSessions` / `isPipeline` / `isExplorer` 四个分支）。

**本文的裁定由作者独立做出**——用户在 2026-08-06 出门前授权独立闭环完成剩余阶段，
本阶段未经用户逐条确认。所有可能引起异议的取舍在 §8 逐条记账。

---

## 1. 目标与非目标

**目标**

1. 四屏按 Modernist 重画：页头 kicker + 大标题 + 一句人话说明，筛选器统一为 `<Seg>`。
2. Memory explorer 改为**双栏**（左列表 / 右详情），详情页展示完整的六段溯源链：
   源事件 → 抽取 → 候选 → 版本 → 投递 → 召回决策。
3. 界面文案从 API 术语改写为人类语言。
4. 区块级错误隔离（Pipeline health 的既有属性）在重画后仍然成立。

**非目标**

- **零后端改动。** 不新增、不修改任何端点或 IDL。
- 不引入任何前端运行时依赖。
- 不做无障碍专项；现有 `aria-*` / `role` 不允许倒退。
- 不改任何写路径——Governance 四屏全部只读。

---

## 2. 四屏各自要做什么

### 2.1 Audit trail（`/governance/audit`）

页头：kicker `Governance · 审计流水`，标题「发生过的一切，未经编辑」，
说明「只追加。名字是我们替你查出来的方便——原始标识符留在行上，
所以人、Agent 或机器消失之后，条目仍然读得通。」

**筛选器**：`actor_kind` 的下拉换成 `<Seg>`：全部 / 人 / Agent / 系统。
后端的 `actor_kind` 取值是 `bootstrap` / `human` / `agent` / `system`
（`AdminAuditPage.tsx:11`）——四个 Seg 档位映射为
`""` / `human` / `agent` / `system`，**`bootstrap` 并入「系统」**：它只在首次安装时
出现一次，单独占一格是浪费。`action` 与 `target_id` 两个自由文本筛选框保留。

**行**：时间（mono）/ 类别 tag / 操作者 / 动作（heading 字族）/ 目标（mono）。
整行是 `<button>`，点开就地展开详情（`getAuditEvent`），与现状的展开机制一致。

**名字解析已经存在**（`AdminAuditPage.tsx:14-38` 的 `LabelDirectory`），
本阶段只重画它的呈现：名字在前、原始 ID 以小字括号跟随。设计稿明确要求
「原始标识符留在行上」，不允许只显示名字。

### 2.2 Session audit（`/governance/sessions`）

页头 kicker `Governance · 会话审计`，标题「Agent 到底做了什么」，
说明「Finding 由证据推导，从不来自 prompt 或内容。每条都会点名它取材的事件，
你可以自己去读源头。」

三视图切换从现有的自绘 `.seg` 换成 `<Seg>` 组件：**Findings / 工具调用 / 按天**。
视图选择继续走 URL 的 `?view=` 参数（现状如此，不动）。

- **Findings**：从表格改成**行卡**——左侧 severity tag，中间标题 + 摘要 +
  `kind · 证据 evt_…`，右侧 Agent / session，最右一个「看这些调用」按钮，
  点了切到工具调用视图并带上该 session 的过滤。
- **工具调用**：保持表格。列：时间 / Agent / 工具 / 输入 / 风险（tag + 一行原因）/ 批准状态。
- **按天**：从表格改成**柱状图** + 每根柱子下方的一组数字（会话数 · 调用数 · 高危数）。
  纯 CSS 柱子，不引依赖。

### 2.3 Pipeline health（`/governance/pipeline`）

页头 kicker `Governance · 管道健康`，标题「记忆跟得上吗？」，
说明「每块自己加载。一块失败时其余照常——你会看到哪块过期了，而不是一整页空白。
这里从不显示任何查询、内容或密钥。」

**顶部六格指标条**（取自 `getOperationsSummary`，`OperationsSummary` 类型）：

| 格 | 取值 | 副标 |
|---|---|---|
| 扣下待查 | `extraction.quarantined` | — |
| 失败 | `extraction.failed` | 「抽取失败；全部操作出错 N 次」（`errors`） |
| 排队中 | `extraction.unextracted_events` | `oldest_unextracted_at` 换算的相对时间 |
| 典型延迟 | `latency.p50_ms` | `p50` |
| 最坏情况 | `latency.p95_ms` | `p95` |
| 空手而归 | `recalls.empty` | 「证据不足或预算不够」 |

（此表已按实现校准，见 §8 裁定 3——`oldest_unextracted_at` 挂在「排队中」而不是
「扣下待查」，「失败」的副标带上了 `errors` 这个跨操作类型的更广口径计数。）

下方两栏：左 Storage（既有 `StorageSnapshotView` / `StorageHistoryView`），
右「最近的活儿」（既有事件流）。**区块级错误隔离照搬现状**——
`useSummaryRegion` / `useEventsRegion` / `useStorageRegion` 三个 hook 一个不动，
只换呈现。失败的区块渲染既有的 `RegionError`（可重试），其余区块照常。

### 2.4 Memory explorer（`/governance/memory[/:noteId]`）

页头 kicker `Governance · 追一条事实`，标题「这是从哪来的？」，
说明「挑一条团队正在流传的事实，顺着它回到产生它的那次会话——
再往前看它被交给过哪些 Agent。」

**改成双栏**：左侧 340px 的笔记列表（搜索框 + 筛选 + 可滚动的卡片行），
右侧详情。`/governance/memory` 渲染左栏 + 右侧空态；
`/governance/memory/:noteId` 渲染左栏 + 该笔记的详情。**一个组件服务两条路由。**

`AdminTeamNoteDetailPage.tsx` 作为独立页面**删除**，内容并入右栏。

---

## 3. 溯源链（本阶段的核心）

### 3.1 数据来源：一次请求

`getTeamNote(noteId)` 返回 `TeamNoteDetail`：

```
{ note, related_notes, revisions[], recall_observations[] }
```

而 `ExplorerRevision` **每一项自带整条链**：

```
{ revision, candidate_id, operation, body, created_at,
  extraction: ExplorerExtractionRun,   ← 抽取
  evidence:   ExplorerSourceEvent[],   ← 源事件
  candidate:  ExplorerCandidate,       ← 候选
  deliveries: ExplorerDelivery[] }     ← 投递
```

顶层的 `recall_observations: ExplorerRecallUse[]` 是 ← 召回决策。

**所以六段链条由一次请求完全构成，零额外请求。** 这是本阶段最重要的取数事实：
不需要 N+1，也不需要 `getExtractionDiagnostic` / `getChannelDiagnostic` /
`getRecallDiagnostic` 三个诊断端点参与主链——它们退居为可选的下钻（§3.3）。

### 3.2 六段的呈现

右栏从上到下三块：

1. **笔记头**：subject（h4）、写它的 Agent、受众、有效期、`note_id · rev N · state`（mono）。
2. **「它是怎么来的」**：按 `revisions` 逆序（最新在上），每个版本渲染成一条
   两列的时间线行——左列是**阶段名**（小号大写、朱红），右列是标题 + 一句说明 + mono 的引用。
   一个版本展开为六行：

   | 阶段 | 标题 | 说明 | 引用 |
   |---|---|---|---|
   | 源事件 | `evidence.length` 条会话事件 | 取自 `session_id` 的第 `from_sequence`–`to_sequence` 条 | `evt_…`（多条则「等 N 条」） |
   | 抽取 | 模型 `model` · prompt `prompt_version` | 状态 `status`，用了 `input_tokens`+`output_tokens` token | `run_id` |
   | 候选 | `candidate.action` 一条 `candidate.kind` | `admission_status`；被拒时给出 `rejection_reason` | `candidate_id` |
   | 版本 | 第 `revision` 版 · `operation` | 正文摘要（首行截断） | `created_at` |
   | 投递 | 投给 `deliveries.length` 个 Agent | 汇总共几次投递、消耗多少 `context_tokens` | 接收方 `recipient_agent_id` 列表（`formatRefList`，多条则「等 N 条」） |
   | 召回决策 | 见下 | | |

   （投递行已按实现校准：`ExplorerDelivery`——`types.ts:453-459`——既没有
   `envelope_id` 也没有 status 字段，所以「逐条列出接收方与状态 / 引用
   `envelope_id`」是本文档原先的错误，不是实现的偏离。实际渲染的是一句汇总
   加接收方 Agent ID 的 ref 列表，见 `provenance.ts` 的 `buildDeliveryStep`。）

   **召回决策不在版本内**（它挂在笔记上，不挂在某个版本上），所以它作为
   第三块单独渲染。

3. **「每一次被端到 Agent 面前」**：`recall_observations` 的表格——
   时间 / Agent / 结果（tag：投递了 / 被丢弃）/ 为什么
   （`rejection_reasons` + `budget_drop_reasons` + `hard_gate_failures` 三个数组
   拼成一句人话；三者都空且 `delivered` 为真时写「命中并投递」）。

### 3.3 可选下钻

`ExplorerDiagnosticDrawer`（现有组件）与三个诊断端点保留，作为每段引用旁的
「看原始记录」链接。**它们不参与主链的渲染**，失败也不影响链条本身。

### 3.4 缺段怎么办

链条的任何一段都可能缺失（老数据、抽取失败、从未被召回过）。
每段各自渲染「这一段没有记录」而**不是**整条链报错。
`revisions` 为空数组时，「它是怎么来的」整块渲染正向空态
（「这条笔记没有留下版本记录——它可能早于溯源功能上线」），而不是错误。

---

## 4. 取数与降级

| 屏 | 请求 | 失败表现 |
|---|---|---|
| Audit | `listAuditEvents`（分页）+ 展开时 `getAuditEvent` | 列表失败 → 整页可重试错误；名字解析（members/agents）失败 → 静默退回原始 ID |
| Session audit | 三视图各自的 `listSessionAudit*` | 各视图独立；当前视图失败只塌当前视图 |
| Pipeline | `useSummaryRegion` / `useEventsRegion` / `useStorageRegion` | **区块级隔离，照搬现状**，各自 `RegionError` + 重试 |
| Explorer 左栏 | `listTeamNotes`（分页） | 左栏塌成可重试错误，右栏若已有内容则保持 |
| Explorer 右栏 | `getTeamNote(noteId)` | 右栏塌成可重试错误，左栏照常 |

**「区块级错误隔离仍成立」是上位 spec 给本阶段的验收项**，四屏都要满足：
任何一个数据源失败，页面其余部分必须仍然可用。

---

## 5. 路由

不新增路由。既有四条不变：

```
/governance/audit
/governance/sessions          （?view= 选视图，?agent= 预填过滤——阶段 4 加的）
/governance/pipeline
/governance/memory[/:noteId]  （本阶段起一个组件服务两条）
```

Explorer 左栏的笔记链接现在指向 legacy 的 `/admin/explorer/notes/:id`
（`AdminExplorerPage.tsx:104`），本阶段改为 `/governance/memory/:id`，
少一跳重定向。

---

## 6. 文件结构

**新建**

| 文件 | 职责 |
|---|---|
| `web/src/pages/governance/AuditRow.tsx` | 审计单行 + 就地展开的详情 |
| `web/src/pages/governance/SessionFindings.tsx` | Findings 行卡视图 |
| `web/src/pages/governance/SessionDays.tsx` | 按天柱状图 |
| `web/src/pages/governance/PipelineMetrics.tsx` | 顶部六格指标条 |
| `web/src/pages/governance/NoteList.tsx` | Explorer 左栏 |
| `web/src/pages/governance/NoteProvenance.tsx` | 六段溯源链 |
| `web/src/pages/governance/NoteRecalls.tsx` | 召回决策表 |
| `web/src/pages/governance/provenance.ts` | 从 `TeamNoteDetail` 派生链条的纯函数 |
| `web/src/styles/features/governance.css` | 本阶段特性样式（**新建文件**） |

**重写**：`AdminAuditPage.tsx`、`AdminSessionAuditPage.tsx`、`AdminOperationsPage.tsx`、
`AdminExplorerPage.tsx`

**删除**：`AdminTeamNoteDetailPage.tsx`（并入 Explorer 右栏）

**不动**：`pages/operations/` 下的九个既有组件与 `hooks.ts`——
区块级隔离的机制一个不改，只在 `AdminOperationsPage` 里换外壳与呈现。

---

## 7. 测试

**纯函数**（`provenance.ts`）
- 从一个完整的 `TeamNoteDetail` 派生出六段，段序正确
- 任一段缺失时该段标记为「无记录」而不抛错
- `revisions` 为空时返回空链（调用方渲染空态）
- 多版本时按 revision 逆序

**Audit**
- 四个 Seg 档位各自发出正确的 `actor_kind`（「系统」档要同时覆盖 `system` 与 `bootstrap`——
  若后端只接受单值，则「系统」档发 `system`，`bootstrap` 的处理方式在实现时按实际接口确定并记账）
- 名字解析失败时行仍渲染且显示原始 ID（stub 让 members/agents 请求 403）
- 展开一行只打一次 `getAuditEvent`

**Session audit**
- 三视图切换写 URL `?view=`，刷新后停在同一视图
- Findings 的「看这些调用」跳到工具调用视图并带上 session 过滤
- 按天视图的柱高与数据成比例（断言 style 里的高度百分比）

**Pipeline**
- 三个区块各自失败一次，其余两块仍渲染（**这是上位 spec 的验收项**）
- 六格的每一格取自正确的字段（改一个字段名，对应格必须变红）

**Explorer**
- `/governance/memory` 渲染左栏 + 右侧空态，**不发** `getTeamNote`
- `/governance/memory/:noteId` 一次 `getTeamNote` 撑起整条链（断言只有一次请求）
- 溯源链六段齐全时全部渲染；缺段时该段显示「没有记录」
- 左栏失败不影响右栏，反之亦然
- 左栏链接指向 `/governance/memory/:id` 而非 `/admin/explorer/...`

**回归**
- 旧路由 `/admin/audit` `/admin/session-audit` `/admin/operations` `/admin/explorer`
  `/admin/explorer/notes/:id` 仍重定向
- 阶段 4 加的 `?agent=` 深链在重画后仍生效
- `npm --prefix web test` 与 `npm --prefix web run build` 全绿

---

## 8. 记账偏离与作者裁定

用户不在场，以下取舍由作者独立判定，列此以便事后复核：

1. **`bootstrap` 并入「系统」Seg 档。** 它只在首次安装时出现一次，单独占一格是浪费。
   代价（终审修复时改写：原文写错了）：不是「无法单独筛 bootstrap 事件」，而是**选
   『系统』会静默漏掉 bootstrap 事件**——档位名承诺的集合是「bootstrap/human/agent/
   system 里非人非 Agent 的那些」，它实际发出的查询却只是 `actor_kind=system`，不含
   `bootstrap`。也没有真正的兜底：`action` 自由文本框筛的是动作名，筛不了
   `actor_kind`，筛不出 bootstrap 事件。
2. **Pipeline 第六格「Recalls refused / budget or hard gate」→「空手而归」。**
   `OperationsSummary` 没有「因预算或硬门禁被拒」的直接计数；`recalls.empty` 是
   最接近的诚实指标。副标写「证据不足或预算不够」而不是断言只有这两种原因。
3. **Pipeline 六格副标的实际分布，与本文档 §2.3 原表不同**（终审修复时改写：原表
   已按实现校准，见上）。`oldest_unextracted_at`（最老的未抽取事件）实际挂在第三格
   「排队中」，而不是设计稿原定的第一格「扣下待查」——那个时间戳描述的是**还没被
   抽取的事件积压**（unextracted backlog），不是**被扣下等待人工复核的候选**
   （quarantine），两者是不同的概念，挂在「排队中」在语义上才对得上；第一格「扣下
   待查」的副标因此是「—」。挪位本身是对的，只是本文档没跟上实现。另外，第二格
   「失败」的副标在终审 I2 里追加了 `errors`（跨 recall / observation / extraction
   三种操作类型、口径更广的出错计数），写成「抽取失败；全部操作出错 N 次」——
   `errors` 在旧版 SummaryCards / PipelineHealthCard 消失后没有地方安放，这里复用了
   一个原本是空「—」的副标位，也是对本文档原表的偏离。
4. **三个诊断端点退出主链——但「降级为可选下钻」只做了一半。** 设计稿把溯源画成一条
   链，而 `getTeamNote` 一次就能给出全部六段；让诊断端点参与主链只会引入 N+1 和更多
   失败面，这半句照做了，也被测试钉死（`governance-explorer.dom.test.tsx` 断言诊断
   端点零请求）。但「它们降级为可选下钻」没有兑现——`NoteProvenance.tsx` 里没有任何
   「看原始记录」链接，`ExplorerDiagnosticDrawer` 也没有被接进 Explorer 右栏。本次
   终审修复的范围不包含补上这个下钻，但要如实记账：相对旧的
   `AdminTeamNoteDetailPage`，右栏压缩掉了下面这些信息，且**没有任何入口能找回来**：
   - 每条源事件的正文（`event.content`）——旧页面逐条渲染，新版只给
     `evidence.length` 和一份 event_id 的 ref 列表
   - candidate 的 `subject` / `body`——新版只给 `admission_status` 与拒绝原因
   - 投递的逐条明细表（Agent / Session / Tokens / Time）——新版只给一句汇总（见
     §3.2 的校准）
   - 到抽取运行诊断（`/admin/operations?detail=extraction_run&id=...`）的链接——
     完全没有了
   - 召回表的 `disposition` 列——新版的 `NoteRecalls` 表格没有这一列（见
     §「本阶段丢掉的旧信息」）

   最扎眼的一条：**源事件正文现在在 Explorer 里完全不可达**，而这一屏的 slogan
   恰恰是「顺着它回到产生它的那次会话」——顺到 `session_id` 和 `event_id` 就到头
   了，读不到那次会话里实际发生了什么。
5. **`AdminTeamNoteDetailPage` 删除**（并入双栏右侧）。设计稿画的是单屏双栏，
   保留一个独立详情页会让同一内容有两个入口、两套布局。
6. **Findings 从表格改成行卡，工具调用保持表格。** 设计稿如此：Finding 有长摘要，
   表格塞不下；工具调用是等宽的结构化字段，表格更好扫。
7. **不做「区块级隔离」的重新实现。** `pages/operations/hooks.ts` 的三个 region hook
   是既有的、已被测试覆盖的机制，本阶段只换呈现层。重写它们只会让一个已验收的
   属性重新变成未验收。

### 8.1 本阶段丢掉的旧信息

（终审修复时补记）以下信息在旧版（重写前的）页面上存在，本阶段重画后不再出现在
任何屏幕上，也没有下钻或其它入口能找回来。这些取舍此前只活在
`.superpowers/sdd/` 下的 SDD ledger 里——那个目录是 gitignored 的，不写进本文档，
下一个人翻 spec 就会以为它们从未存在过：

- **Pipeline `duplicate_events`。** 旧 `OperationsSummary.observations` 里的
  `duplicate_events`（去重计数）在新的六格指标条上没有格位，也没有进任何副标——
  唯一的例外是「失败」格副标里的 `errors`（终审 I2 加的，见 §8 裁定 3）。
  `duplicate_events` 目前无处可见。
- **Session audit 按天视图的 ToolBreakdown chips。** 旧的按天表格会展开
  `tool_breakdown`（每种工具的调用次数）；新的柱状图（`SessionDaysChart`）只画
  `event_count`/`session_count`/`tool_call_count`/`high_risk_count`，
  `tool_breakdown` 这个字段仍在 API 响应里，但页面上没有任何地方渲染它。
- **Explorer 左栏笔记卡片丢掉的字段。** 旧版 `AdminExplorerPage.tsx` 的列表列是
  「Team Note / Kind / **Agent** / State / **Updated**」，并且在 `task_ref` /
  `thread_ref` 任一存在时额外渲染一行。新的 `NoteList.tsx` 左栏卡片只有 subject、
  `note_id · revision`、Kind tag、状态 Badge——**写它的 Agent、更新时间、
  task_ref、thread_ref 全部不见了**。这些字段目前只能在点进某条笔记的右栏详情
  （笔记头 / 溯源链）里间接拼凑出一部分（origin_agent_id 在笔记头，没有
  task_ref/thread_ref），左栏本身不再显示。
- **Explorer 右栏相对旧详情页压缩掉的信息。** 见 §8 裁定 4 的清单：每条源事件的
  正文、candidate 的 subject/body、投递的逐条明细表、到抽取运行诊断的链接、
  召回表的 `disposition` 列。

---

## 9. 全局约束

- 纯前端，零后端改动
- 不引入任何前端运行时依赖
- 按钮一律走 `web/src/components/Button.tsx`，禁止 `.btn.ghost` 点分写法
  （`<Link className="btn btn-ghost">` 是仓库既有约定，允许）
- 样式只引用 `--color-*` / `--space-*` / `--font-*`，以及阶段 4 新增的 `--ceremony-*`；
  **禁止** `tokens.css` 第二个 `:root` 块里的兼容别名
- 间距刻度只有 `--space-1/2/3/4/6/8`，**没有 `--space-5` 和 `--space-7`**
- 新特性样式写进**新建**的 `web/src/styles/features/governance.css`
- **三主题（beige / dark / arcade）必须实测**——阶段 4 的验证页
  `.superpowers/sdd/2026-08-06-portal-modernist-phase4-agent-detail/theme-check.html`
  是可复用的方法；arcade 把红色用作页面底色、accent 阶被重映射成墨色系，
  任何「accent 恒为红」的假设都会在 arcade 下崩掉
- 低透明度次要文字在饱和底色上容易不过 AA——新写的 `opacity` 淡化文字要实测
- 提交信息用中文

---

## 10. 风险

| 风险 | 缓解 |
|---|---|
| Explorer 双栏在窄屏上塞不下 | 断点折叠为单栏：无 `:noteId` 时只显示列表，有则只显示详情 + 返回链接 |
| 重画 Pipeline 时破坏区块级隔离 | 三个 region hook 一个不改；测试逐块断言「失败一块其余仍渲染」 |
| 溯源链在老数据上大面积缺段 | 每段独立降级，缺段渲染说明而非报错；`revisions` 为空走正向空态 |
| 四屏一起重画，改动面大 | 拆成四个独立任务，每屏各自可验收；`pages/operations/` 九个既有组件不动 |
| arcade 主题下新样式对比度不足 | 复用阶段 4 的验证页，三主题实测后才算完成 |
