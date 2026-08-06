# Modernist Portal 阶段 6 · Apps + Settings

Date: 2026-08-06
Status: Accepted

七阶段重构的第 6 阶段。Apps 两屏（Wiki / Todos）与 Settings 四屏
（Team / Memory rules / Model usage / Appearance）按 Modernist 重画，
并把「全屏逃逸模式」彻底删除。

上位设计：`docs/superpowers/specs/2026-08-04-portal-modernist-redesign-design.md` §阶段 6。
前一阶段：`2026-08-06-portal-modernist-phase5-governance-design.md`。
设计稿：`docs/w.html`（`isWiki` / `isTodos` / `isTeamSet` / `isRules` / `isUsage` / `isAppearance`）。

**本文的裁定由作者独立做出**——用户在 2026-08-06 出门前授权独立闭环完成剩余阶段。
所有可能引起异议的取舍在 §7 逐条记账。

---

## 1. 现状与实际工作量

上位设计给阶段 6 的描述是「Wiki 回壳内、slug 进 path（legacy `?page=` 重定向）、
Todos；`/wiki` 策略页拆成 Settings › Memory rules 与 Settings › Model usage；
Team / Appearance」。**其中两项早已完成**：

- **Wiki 已在壳内、slug 已在 path**：`WikiBrowsePage` 挂在 `/apps/wiki` 与
  `/apps/wiki/:slug` 两条路由上，`useParams` 读 slug（`WikiBrowsePage.tsx:40`）。
- **legacy `?page=` 重定向已存在**：`app/legacyRoutes.ts:58-82` 把 `/wiki?page=<slug>`
  与 `/wiki/browse?page=<slug>` 转成 `/apps/wiki/<slug>`，`?revision=` 保留为 query。

**所以本阶段的实际工作是三件：**

1. 六屏按 Modernist 重画
2. **把 `/settings/memory` 与 `/settings/usage` 真正拆开**——它们现在是**同一个组件**
   `WikiStatusPage`（`routes.tsx:192,194`），两条路由渲染一模一样的内容
3. **删除全屏逃逸模式**：`TodoPage.tsx:126-127` 是最后一个 `.app-fullscreen` 调用点；
   `styles/features/apps.css:15-18` 的四条规则随之成为死代码，而且它们还引用了
   被禁的兼容别名（`--muted` / `--text`）

---

## 2. 六屏各自要做什么

### 2.1 Apps › Wiki（`/apps/wiki[/:slug]`）

页头 kicker `Apps · wiki` + 标题「团队百科」+ 一行说明（页数 · 由会话写成，不是手写 · 新页面自己出现）+ 搜索框。

三栏：左 280px 主题树（`components/wiki/TopicTree.tsx`）／中 正文（`WikiMarkdown`）／右 260px 关系（`RelationList`）。
窄屏折叠为单栏，树与关系收进可展开的段落。

**取数、搜索、修订切换、树展开状态一律不动**——本屏只换外壳与样式。

### 2.2 Apps › Todos（`/apps/todos`）

页头 kicker `Apps · todos` + 标题「Agent 替你发现的活儿」+ 说明
「团队写下的阻塞与交接会被转成建议。接受一条它就归你；忽略它就不会再回来。」
+ 右侧扫描按钮。

两栏：左「建议」（每条是 kind tag + 标题 + 正文 + 右侧「接下来我做」/「忽略」两个按钮），
右「你的清单」（输入框 + 未完成项 + 已完成项分组）。

**`.app-fullscreen` / `.app-fullscreen-inner` 必须删除**——这是本阶段验收项
「全屏逃逸模式彻底消失」的落点。

### 2.3 Settings › Team（`/settings/team`）

页头 kicker `Settings · team` + 团队名作标题 + 一行部署形态说明。
下方四格信息条（地址 / 部署形态 / 成员数 / 创建时间，以 `TeamSettingsPage` 现有数据为准）。

on-prem 无团队切换器时该路由本就不挂载（`routes.tsx:191` 的 `hasTeams(me)` 门控），不动。

### 2.4 Settings › Memory rules（`/settings/memory`）

页头 kicker `Settings · memory rules` + 标题「记忆是怎么写出来的」+ 说明
「这些规则只对以后的运行生效。要让它们作用于已有内容，就重建——它会重读证据、重写每一页。」

内容 = 现有 `WikiStatusPage` 的**写入规则那一半**：
`WikiProgressCard` + `WikiIngestionCard` + `WikiGenerationCard` + `WikiRebuildDialog`。

### 2.5 Settings › Model usage（`/settings/usage`）

页头 kicker `Settings · model usage` + 标题「记忆跑起来要花多少」。

内容 = 现有 `WikiStatusPage` 的**用量那一半**：`WikiLLMUsageCard`。

**这两屏的拆分是本阶段唯一的结构性改动**（见 §3）。

### 2.6 Settings › Appearance（`/settings/appearance`）

页头 kicker `Settings · appearance` + 标题「它看起来什么样」。
三个主题的选择器按 Modernist 重画；现有的 `lib/theme.ts` 持久化机制不动。

---

## 3. `/settings/memory` 与 `/settings/usage` 的拆分

现状：两条路由渲染同一个 `WikiStatusPage`，用户在 subnav 上点两个不同的条目
看到的是同一屏。

拆法：

- `WikiStatusPage.tsx` **拆成两个页面组件**：`MemoryRulesPage` 与 `ModelUsagePage`，
  都放在 `pages/settings/` 下
- `pages/wiki-status/` 下的五个卡片组件**原样复用**，各自归到新的一屏里
- `WikiStatusPage.tsx` 删除

**共享状态的处理**：现有 `WikiStatusPage` 顶层持有 wiki 状态（`status` / `statusError`）
并往下传给多个卡片。拆开后：

- Memory rules 需要 `status`（进度、注入开关、生成设置都依赖它）
- Model usage 的 `WikiLLMUsageCard` 是否依赖同一份 `status`，**实现时按实际 props 判定**：
  若依赖，两屏各自取一次（各自独立降级，符合本项目一贯的区块契约）；
  若不依赖，Model usage 就不取 wiki 状态

不要为了「省一次请求」把两屏耦合成共享 context——两条路由本就不会同时挂载。

---

## 4. 取数与降级

六屏全部只读或只改设置，无新端点。降级契约沿用前几阶段：

| 屏 | 失败表现 |
|---|---|
| Wiki | 页面列表失败 → 整页可重试错误；单页正文失败 → 中栏可重试错误，树与关系仍可用 |
| Todos | 建议列表与个人清单**各自独立降级**，一边失败另一边照常 |
| Team | 无取数——数据来自启动时已解析的 `me.teams`，没有失败模式 |
| Memory rules | 每张卡各自降级（现状如此，保持） |
| Model usage | 整屏一张卡，失败即该卡可重试 |
| Appearance | 无取数 |

---

## 5. 路由

**不新增、不删除任何路由。** `/settings/memory` 与 `/settings/usage` 的元素从
同一个 `WikiStatusPage` 换成两个不同的页面组件。

legacy 重定向表（`app/legacyRoutes.ts`）**一行都不改**——`/wiki → /settings/memory`、
`/todo → /apps/todos`、`/team → /settings/team`、`?page=` 转 path 全部已在位。
本阶段只需**确认它们仍然生效**（见 §6 的跨屏验收）。

---

## 6. 跨屏验收项（本阶段的重点，必须显式指派给某个任务）

**阶段 4 与阶段 5 各栽过一次同样的跟头**：上位设计给整个阶段的验收项，
因为没有被写进任何一个任务的 brief，就只在碰巧点名它的那个任务上被证明了。
阶段 5 终审的原话是「每个任务把自己 brief 点名的不变量守得很死，
横跨多屏的那一面没有任何一个任务拥有」。

**所以本阶段把三条跨屏验收项单列为一个任务**（计划里的最后一个实现任务）：

1. **全屏逃逸模式彻底消失** —— `grep -rn "app-fullscreen\|app-back" web/src` 零命中，
   且 `styles/features/apps.css` 里那四条规则已删。
2. **旧 wiki 深链可用** —— `/wiki?page=<slug>`、`/wiki/browse?page=<slug>&revision=<id>`、
   `/wiki`、`/todo`、`/team` 五条各有一条测试，断言落到正确的新 URL。
3. **六屏的失败态各有覆盖** —— §4 表里每一行都要有对应测试。
   **这条是阶段 5 的 Critical 的直接对策**：那次把三屏的错误分支整段删掉，
   626 个测试全绿。

---

## 7. 记账偏离与作者裁定

1. **上位设计写的「Wiki 回壳内、slug 进 path、legacy 重定向」三项已完成**，
   本阶段不重做，只重画。见 §1。
2. **`WikiStatusPage` 删除、拆成两个页面**，而不是加一个 `?tab=` 参数或条件渲染。
   两条路由在 subnav 上是两个平级条目，用户预期是两屏。
3. **不为拆分引入共享 context**。两条路由不会同时挂载，各取各的更简单也更符合
   本项目「区块各自独立降级」的一贯契约。
4. **Todos 的「建议 / 你的清单」两栏各自独立降级**，而不是整页一个错误态——
   这两份数据来源不同，一边挂掉不该让另一边也不可用。
5. **`.app-fullscreen` 四条规则连同它们引用的兼容别名一起删**（`--muted` / `--text`）。
   这是顺带的收益：兼容别名的退场清单又短一截。
6. **规格更正（评审阶段发现，非实现遗漏）：§4 表里 Team 那一行原写「整页可重试
   错误」是写错了。`TeamSettingsPage` 的数据来自 `me.teams`——启动时
   `AuthContext` 已经解析好、作为 prop 传入，页面本身不发请求，没有失败模式。
   Task 4 实现时为了让这行规格可测，给页面新加了一次 `GET /v1/teams` 调用
   （复用既有端点，非新后端），制造出一个原本不存在的失败态——这是「产品代码
   迁就错误的规格」，方向反了。评审时改回：页面撤销新增请求，§4 表该行改成
   「无取数——数据来自启动时已解析的 `me.teams`，没有失败模式」，对应的
   retry 测试一并删除。

---

## 8. 全局约束

- 纯前端，零后端改动
- 不引入任何前端运行时依赖
- 按钮一律走 `web/src/components/Button.tsx`，禁止 `.btn.ghost` 点分写法
  （`<Link className="btn btn-ghost">` 是仓库既有约定，允许）
- 样式只引用 `--color-*` / `--space-*` / `--font-*` 以及前几阶段新增的
  `--ceremony-*` / `--color-chart-bar` / `--color-label-accent`；
  **禁止** `tokens.css` 第二个 `:root` 块里的兼容别名
- 间距刻度只有 `--space-1/2/3/4/6/8`，**没有 `--space-5` 和 `--space-7`**
- 新特性样式写进新建的 `styles/features/` 文件；`apps.css` 只做删除，不追加
- **三主题（beige / dark / arcade）必须实测**。复用阶段 5 修正后的验证页
  `.superpowers/sdd/2026-08-06-portal-modernist-phase5-governance/theme-check-governance.html`
  ——它已修掉三个方法论 bug（背景 alpha 合成、祖先 opacity 累乘、
  **Chrome 把 `color-mix()` 序列化成 `color(srgb r g b / a)` 而非 `rgb()`**）。
  arcade 把红色用作页面底色、accent 阶被重映射成墨色系，
  **任何「accent 恒为红」的假设都会崩**
- 提交信息用中文

### 8.1 全站语言规则

合并评审第二轮的 I4：中英文分界线连续三个阶段与任务边界重合——阶段 5 是
「Audit 页头中文其余全英、Explorer 几乎全中文」，阶段 6 初稿是「Wiki 页头
中文其余全英、Todos 几乎全中文」。根因是没有任何一个任务的验收清单里显式
写着「全站语言规则」这一条，写这条规则本身，而不是逐屏各自判断。

- **可见文案（用户会读到的正文、标题、按钮、占位符、空态/错误态提示）一律
  中文。** 不区分屏，不留「这个组件暂时保持英文」的例外。
- **`aria-label`、`aria-labelledby` 指向的文本、以及仅供屏幕阅读器使用的
  `sr-only` 文本，保持英文。** 这是仓库既有约定（现状 38 英 : 2 中，阶段 6
  Wiki 屏的实现者也自发这样写），不是本条新定的规则，这里只是把它写清楚、
  别再被下一个任务当成「随便选」。
- **`<Kicker>` 组件渲染的「分区 · 子页」字样保持英文原文**（如
  `Apps · wiki`、`Settings · memory rules`），不翻译。这是跨屏统一的导航
  面包屑记号，翻译后反而在中英文案之间制造新的不一致。
- 数据本身（slug、ID、后端返回的枚举值如 `entity_type`）不算「可见文案」，
  不需要翻译，也不应该被翻译（翻译了就对不上后端）。

**这条规则适用于阶段 7 及以后的每一个任务**，不需要每次重新判断中英文比例
该怎么分配——按上面三条执行，任务收尾前用它自查一遍即可。

---

## 9. 风险

| 风险 | 缓解 |
|---|---|
| 拆 `WikiStatusPage` 时把某张卡的 props 接错，导致一屏空白 | 五张卡组件原样复用不改；拆分后两屏各写一条「卡片渲染出来了」的测试 |
| Wiki 三栏在窄屏塞不下 | 断点折叠单栏，树与关系收进可展开段落；jsdom 测不了 media query，但可断言折叠用的属性/类 |
| 删 `.app-fullscreen` 时漏掉调用点 | §6 第 1 条的 grep 验收 |
| 重画弄丢 wiki 的修订切换或树展开状态 | 本屏只换外壳；重画前先通读现有测试，逐条确认行为未丢 |
| arcade 下新样式对比度不足 | 复用阶段 5 的修正版验证页，三主题实测后才算完成 |
