# Modernist Portal 阶段 6 · Apps + Settings 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Apps 两屏与 Settings 四屏按 Modernist 重画，真正拆开 `/settings/memory` 与 `/settings/usage`，并删除全屏逃逸模式。

**Architecture:** 六屏彼此独立，只换外壳与样式；唯一的结构性改动是把 `WikiStatusPage` 拆成 `MemoryRulesPage` 与 `ModelUsagePage`（`pages/wiki-status/` 下的五张卡片组件原样复用）。**跨屏验收项单列为 Task 7**——不指派给具体任务的验收项在前两个阶段各失守过一次。

**Tech Stack:** React 18 + TypeScript + react-router-dom；vitest + @testing-library/react；纯 CSS。

## Global Constraints

- **纯前端，零后端改动。** diff 只允许出现在 `web/` 与 `docs/` 下。
- 不引入任何新 npm 依赖。
- 按钮一律走 `web/src/components/Button.tsx`，禁止 `.btn.ghost` 这类**点分**写法（`<Link className="btn btn-ghost">` 是仓库既有约定，允许）。
- 样式只引用 `--color-*` / `--space-*` / `--font-*`，以及前几阶段新增的 `--ceremony-*` / `--color-chart-bar` / `--color-label-accent`；**禁止** `web/src/styles/tokens.css` 第二个 `:root` 块里的兼容别名（`--bg` `--muted` `--accent` `--text` `--border` `--surface` `--mono` 等）。
- 间距刻度只有 `--space-1/2/3/4/6/8`，**没有 `--space-5` 和 `--space-7`**。用了不存在的变量不报错、只静默失效。
- 新特性样式写进**新建**的 `styles/features/` 文件并在 `index.css` 追加 `@import`；`apps.css` 只做删除，不追加。
- 组件级测试若间接依赖 `AuthContext`（用了 `useErrorHandler` 就会），测试要包一层真的 `AuthProvider` 并桩掉它挂载时的 `GET /v1/me`——照 `web/tests/agent-identity.dom.test.tsx` 开头的写法。
- 提交用 `git commit -- <明确路径>` 的 **pathspec 形式，尾部 `--` 不能省**；`git add` 也只加明确路径。
- 提交信息用中文。
- **每个任务都要为自己那一屏写「失败态」测试**（见各任务的验收），Task 7 会统一复核。

---

## 文件结构

**新建**

| 文件 | 职责 |
|---|---|
| `web/src/pages/settings/MemoryRulesPage.tsx` | `/settings/memory`，写入规则那一半 |
| `web/src/pages/settings/ModelUsagePage.tsx` | `/settings/usage`，用量那一半 |
| `web/src/styles/features/apps-settings.css` | 本阶段特性样式 |

**重写**：`WikiBrowsePage.tsx`、`TodoPage.tsx`、`TeamSettingsPage.tsx`、`pages/settings/AppearancePage.tsx`
**删除**：`WikiStatusPage.tsx`；`styles/features/apps.css` 里的 `.app-fullscreen` / `.app-fullscreen-inner` / `.app-back` 四条规则
**不动**：`pages/wiki-status/` 下的五张卡片、`components/wiki/` 下的四个组件、`app/legacyRoutes.ts`

**测试改动面**：实现者各自 `ls web/tests | grep -iE "wiki|todo|team|appearance|settings"` 摸清实际文件名，**不要相信这里的猜测**。

---

## Task 1: 拆开 Memory rules 与 Model usage

**Files:**
- Create: `web/src/pages/settings/MemoryRulesPage.tsx`、`web/src/pages/settings/ModelUsagePage.tsx`
- Create: `web/src/styles/features/apps-settings.css`（本任务建立，后续任务追加）
- Modify: `web/src/styles/index.css`（末尾追加 `@import "./features/apps-settings.css";`）
- Modify: `web/src/app/routes.tsx`（两条路由各指向新组件）
- Delete: `web/src/pages/WikiStatusPage.tsx`

**这是本阶段唯一的结构性改动。** 现在 `/settings/memory` 与 `/settings/usage` 渲染的是**同一个组件**（`routes.tsx:192,194`），用户在 subnav 上点两个平级条目看到同一屏。

- [ ] **Step 1: 摸清现状**

通读 `web/src/pages/WikiStatusPage.tsx`：它顶层持有什么状态、往下传给哪几张卡、哪些卡依赖 `status`。
`ls web/tests | grep -iE "wiki-status|settings"` 找到既有测试，通读，记下每条断言保护什么。

- [ ] **Step 2: 写失败测试（红）**

两屏各至少三条：
```
/settings/memory：
  1. 渲染进度 / 注入 / 生成三张卡（各断言一个可辨识的文案）
  2. 不渲染 LLM 用量卡
  3. wiki 状态取数失败时页面塌成可重试错误（或各卡各自降级——按现有实现的契约写）

/settings/usage：
  4. 渲染 LLM 用量卡
  5. 不渲染进度 / 注入 / 生成三张卡
  6. 取数失败时的降级
```
第 2、5 条是这次拆分的核心断言——**没有它们，把两个页面写成同一个也会全绿**。

- [ ] **Step 3: 实现**

- 两个新页面都放 `pages/settings/` 下
- 五张卡片组件（`pages/wiki-status/` 下）**原样复用，一个字不改**
- `WikiLLMUsageCard` 是否依赖 `status`，**读它的 props 后按实际情况决定**：依赖就在 `ModelUsagePage` 里各自取一次，不依赖就不取
- **不要引入共享 context**——两条路由不会同时挂载
- 页头：Memory rules 用 kicker `Settings · memory rules` + 「记忆是怎么写出来的」+ spec §2.4 的说明；Model usage 用 `Settings · model usage` + 「记忆跑起来要花多少」

- [ ] **Step 4: 删旧页面 + 跑构建**

```bash
git rm web/src/pages/WikiStatusPage.tsx
npm --prefix web run build
```
把残留引用改干净。

- [ ] **Step 5: 变异验证**

把 `ModelUsagePage` 改成也渲染三张写入规则卡 → 确认「不渲染」那两条会红。改回。

- [ ] **Step 6: 提交**

---

## Task 2: Todos 重画 + 删除全屏逃逸模式

**Files:**
- Rewrite: `web/src/pages/TodoPage.tsx`
- Modify: `web/src/styles/features/apps-settings.css`（追加）
- Modify: `web/src/styles/features/apps.css`（**只删不加**：`.app-fullscreen` / `.app-fullscreen-inner` / `.app-back` 及其 `:hover`）

**`TodoPage.tsx:126-127` 是全站最后一个 `.app-fullscreen` 调用点。** 那四条规则删掉后，它们引用的兼容别名（`--muted` / `--text`）也随之少两个调用点。

**要做的：**
1. 页头 kicker `Apps · todos` + h1「Agent 替你发现的活儿」+ spec §2.2 的说明 + 右侧扫描按钮
2. 两栏：左「建议」（kind tag + 标题 + 正文 + 右侧两个按钮），右「你的清单」（输入框 + 未完成 + 已完成分组）
3. 删掉 `.app-fullscreen` 包裹

**验收（本屏的失败态，Task 7 会复核）：** 建议列表与个人清单**各自独立降级**——一边失败另一边照常渲染。写两条测试，各失败一次。

- [ ] **Step 1: 摸清现状 + 写失败测试（红）**
- [ ] **Step 2: 实现重画 + 删 `.app-fullscreen`**
- [ ] **Step 3: 变异验证**：把「一边失败整页塌掉」，确认独立降级那两条会红
- [ ] **Step 4: `grep -rn "app-fullscreen\|app-back" web/src` 确认零命中**
- [ ] **Step 5: 提交**

---

## Task 3: Wiki 重画

**Files:**
- Rewrite: `web/src/pages/WikiBrowsePage.tsx`（**只换外壳与样式**）
- Modify: `web/src/styles/features/apps-settings.css`（追加）

**这一屏取数逻辑复杂（页面列表、正文、修订、树展开状态、搜索），本任务一律不动。**
先通读现有测试，逐条记下它们保护什么行为；重画后每一条都要还在。

**要做的：**
1. 页头 kicker `Apps · wiki` + 标题「团队百科」+ 一行说明 + 搜索框
2. 三栏：左 280px 主题树（`TopicTree`）／中 正文（`WikiMarkdown`）／右 260px 关系（`RelationList`）
3. 窄屏折叠单栏，树与关系收进可展开段落

**验收（本屏的失败态）：** 页面列表失败 → 整页可重试错误；单页正文失败 → 中栏可重试错误、**树与关系仍可用**。两条测试。

- [ ] **Step 1: 通读现有测试并列出它保护的行为清单**（写进报告）
- [ ] **Step 2: 写失败态的两条新测试（红）**
- [ ] **Step 3: 重画**
- [ ] **Step 4: 变异验证**：把正文失败改成整页 error，确认「树与关系仍可用」那条会红
- [ ] **Step 5: 跑既有 wiki 测试全绿，逐条对照 Step 1 的清单确认无遗漏**
- [ ] **Step 6: 提交**

---

## Task 4: Team 与 Appearance 重画

**Files:**
- Rewrite: `web/src/pages/TeamSettingsPage.tsx`、`web/src/pages/settings/AppearancePage.tsx`
- Modify: `web/src/styles/features/apps-settings.css`（追加）

**Team：** 页头 kicker `Settings · team` + 团队名作 h1 + 一行部署形态说明；下方四格信息条。
**四格的内容以 `TeamSettingsPage` 现有能拿到的数据为准**——设计稿画的是「地址 / 部署形态 / 成员数 / 创建时间」，
但如果某一格没有数据源，**砍掉那一格并在报告里记账**，不要编。

**Appearance：** 页头 kicker `Settings · appearance` + 标题「它看起来什么样」；三主题选择器按 Modernist 重画。
`lib/theme.ts` 的持久化机制不动。

**验收（失败态）：** Team 整页可重试错误一条测试。Appearance 无取数，不需要。

- [ ] **Step 1: 摸清两页现状与可用数据 + 写测试（红）**
- [ ] **Step 2: 实现**
- [ ] **Step 3: 变异验证**：主题切换——把持久化去掉，确认既有测试会红（若既有测试不覆盖，补一条）
- [ ] **Step 4: 提交**

---

## Task 5: 三主题实测与修复

**Files:** `web/src/styles/features/apps-settings.css`，必要时 `tokens.css` / `themes.css`

**直接复用阶段 5 修正后的验证页**：`.superpowers/sdd/2026-08-06-portal-modernist-phase5-governance/theme-check-governance.html`
（主仓库同名目录下也有一份）。它已修掉三个方法论 bug：

1. 半透明背景没做 alpha 合成
2. 子元素 opacity 没沿变暗的祖先容器累乘
3. **Chrome 把 `color-mix()` 序列化成 `color(srgb r g b / a)`（0–1 区间）而非 `rgb()`** ——朴素解析会把三主题下所有 color-mix 颜色读错两个数量级

照它的结构做一份本阶段版本，用本阶段的真实类名摆出六屏的代表性区块。

```bash
npm --prefix web run build
cp <验证页> web/dist/   # 注意 <link href> 的 CSS 文件名带 hash，每次 build 都变
cd web/dist && python3 -m http.server 8899
```

**判定标准：**
- **本阶段新增的类必须三主题全部 ≥ 4.5**（WCAG AA）
- 既有类不达标：记录数字，**不要在本任务里改**（那是设计系统层面的问题），如实写进报告
- beige/dark 现在大体是对的，修 arcade 时不能把它们弄差——每次改动后三主题都要重测

**重点嫌疑：** 任何用了 `--color-accent*` 的地方（arcade 下那是墨色不是红）；
任何 `opacity: 0.4~0.7` 的次要文字压在 arcade 饱和红底上。

- [ ] **Step 1: 搭验证页并实测三主题**
- [ ] **Step 2: 修到本阶段新增类全部 ≥ 4.5**
- [ ] **Step 3: 删掉塞进 `web/dist/` 的验证页；副本存进本阶段的 SDD 目录**
- [ ] **Step 4: 报告里给出修复前后三主题的完整数字表 + 「未解析变量」检查结果**
- [ ] **Step 5: 提交**

---

## Task 6: 跨屏验收（本阶段的重点任务）

**Files:** 只加测试，不改实现（除非发现真缺陷）

**为什么单列这个任务：** 上位设计给整个阶段的验收项，如果不写进任何一个任务的 brief，
就只会在碰巧点名它的那个任务上被证明。**阶段 4 与阶段 5 各栽过一次。**
阶段 5 终审把三屏的错误分支整段删掉，626 个测试**全绿**。

**三条验收，逐条写测试：**

**(1) 全屏逃逸模式彻底消失**
```bash
grep -rn "app-fullscreen\|app-back" web/src
```
应零命中。并确认 `styles/features/apps.css` 里那四条规则已删。
**写成一条测试**（可以是读文件内容的单元测试，或渲染 Todos 后断言 DOM 里没有那个类名）。

**(2) 旧 wiki 深链可用** —— 五条各一条测试，断言落到正确的新 URL：
```
/wiki?page=<slug>                        → /apps/wiki/<slug>
/wiki/browse?page=<slug>&revision=<id>   → /apps/wiki/<slug>?revision=<id>
/wiki                                    → /settings/memory
/todo                                    → /apps/todos
/team                                    → /settings/team
```
先读 `web/src/app/legacyRoutes.ts` 与既有的 `web/tests/legacy-routes.test.ts`——
**有些可能已经被覆盖了，只补缺的那些**，并在报告里说明哪些是新增、哪些本就有。

**(3) 六屏的失败态各有覆盖** —— 逐屏核对 spec §4 的表：

| 屏 | 要求 | 谁的任务 |
|---|---|---|
| Wiki | 列表失败整页错误；正文失败只塌中栏 | Task 3 |
| Todos | 建议与清单各自独立降级 | Task 2 |
| Team | 整页可重试错误 | Task 4 |
| Memory rules | 各卡各自降级 | Task 1 |
| Model usage | 该卡可重试 | Task 1 |
| Appearance | 无取数 | — |

**你的工作是复核，不是重写**：逐条把对应的错误分支**临时删掉**，跑**全量** `npx vitest run`，
确认有测试变红。**任何一处删掉后全绿的，就是缺口，补一条测试。**
把每一次变异的实际输出记进报告。

- [ ] **Step 1: (1) 的 grep 与测试**
- [ ] **Step 2: (2) 的五条，先查既有覆盖再补缺**
- [ ] **Step 3: (3) 的逐屏变异复核，补缺口**
- [ ] **Step 4: 跑全量 `npm --prefix web test && npm --prefix web run build`**
- [ ] **Step 5: 提交**

---

## 收尾检查

- [ ] `npm --prefix web test` 全绿
- [ ] `npm --prefix web run build` 干净（只剩既有 chunk-size 警告）
- [ ] `git diff --stat main...HEAD -- . ':(exclude)web' ':(exclude)docs'` 为空
- [ ] `grep -rn "app-fullscreen\|app-back" web/src` —— 零命中
- [ ] `grep -rn "WikiStatusPage" web/src web/tests` —— 零命中
- [ ] `grep -rnE "var\(--(bg|muted|accent|text|border|surface|mono)\)" web/src/styles/features/apps-settings.css` —— 零命中
- [ ] 三主题实测记录已写进报告，**不允许「应该没问题」式的推断**
