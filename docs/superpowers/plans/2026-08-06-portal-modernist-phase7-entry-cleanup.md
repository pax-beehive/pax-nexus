# Modernist Portal 阶段 7 · 入场动线 + 清理 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 七个入场屏按 Modernist 重画，SaaS 与 on-prem 两条入场统一为 `/welcome`，删死代码，文档与实际结构对齐。

**Architecture:** 入场屏挂在 `App.tsx` 里、在 `PortalShell` 之外，按 `AuthState.kind` 分派。本阶段把 `EntryPage` 与 `OnboardingPage` 合并成一个按 `profile` 分叉的 `WelcomePage`；`no-membership` 的 `path="*"` 兜底改为重定向到 `/welcome`。跨屏验收单列为最后一个任务。

**Tech Stack:** React 18 + TypeScript + react-router-dom；vitest + @testing-library/react；纯 CSS。

## Global Constraints

- **纯前端，零后端改动。** diff 只允许出现在 `web/` 与 `docs/` 下。
- 不引入任何新 npm 依赖。
- 按钮一律走 `web/src/components/Button.tsx`，禁止 `.btn.ghost` 点分写法。
- 样式只引用 `--color-*` / `--space-*` / `--font-*` 与前几阶段新增的 token；**禁止** `tokens.css` 第二个 `:root` 块里的兼容别名。
- 间距刻度只有 `--space-1/2/3/4/6/8`，**没有 `--space-5` 和 `--space-7`**。
- **`opacity < 1` 淡化正文在 arcade 下不安全**——arcade 的正文/底色对比度本身只有 ~4.74:1，任何 `opacity` 都会把它压到 AA 线以下。次要文字用显式的主题稳定颜色。
- **全站语言规则**（阶段 6 §8.1 定下）：可见文案中文；`aria-label` 与 landmark 名英文；kicker 用 `分区 · 子页` 的英文原文。
- 一次性凭据（邀请令牌、bootstrap secret）永不进 `localStorage` / `sessionStorage` / URL query / 日志。
- 提交用 `git commit -- <明确路径>` 的 **pathspec 形式，尾部 `--` 不能省**。
- 提交信息用中文。

---

## 文件结构

**新建**：`web/src/pages/WelcomePage.tsx`、`web/src/styles/features/entry.css`
**重写**：`LoginPage.tsx`、`JoinPage.tsx`、`BootstrapPage.tsx`、`SuspendedPage.tsx`、`NotConfiguredPage.tsx`
**删除**：`EntryPage.tsx`、`OnboardingPage.tsx`（合并进 `WelcomePage`）
**修改**：`web/src/App.tsx`（路由分派）、`web/src/app/routes.tsx`（`/onboarding` 重定向）、`web/src/styles/index.css`（追加 `@import`）
**文档**：`AGENTS.md`、`docs/on-prem-identity-frontend-integration.md`、`docs/on-prem-operations-frontend-integration.md`、`docs/frontend-redesign-brief.md`

---

## Task 1: `/welcome` 统一入场

**Files:** Create `web/src/pages/WelcomePage.tsx`、`web/src/styles/features/entry.css`；Modify `App.tsx`、`app/routes.tsx`、`styles/index.css`；Delete `EntryPage.tsx`、`OnboardingPage.tsx`

**这是本阶段唯一的结构性改动。**

- [ ] **Step 1: 摸清现状**

通读 `web/src/App.tsx`（看 `AuthState.kind` 怎么分派）、`EntryPage.tsx`、`OnboardingPage.tsx`、`auth/AuthContext.tsx`（`DeploymentProfile` 的探测与缓存）。
`ls web/tests | grep -iE "entry|onboarding|auth|app"` 找到既有测试，**通读并列出它们保护的行为清单**（写进报告）。

- [ ] **Step 2: 写失败测试（红）**

```
1. saas + no-membership → 落在 /welcome，看到「你还不属于任何团队」的等待态
2. onprem + no-membership + 可 bootstrap → 落在 /welcome，看到认领 Owner 的入口
3. onprem + no-membership + 不可 bootstrap → 落在 /welcome，看到等待态
4. no-membership 下访问任意路径（如 /management）→ 重定向到 /welcome（地址栏是 /welcome）
5. /onboarding 重定向到 /welcome
6. /welcome 自身不再重定向（防循环）
```
第 4、6 条一起防重定向循环。

- [ ] **Step 3: 实现**

- `WelcomePage` 按 `profile` 分叉，内部不再分成两个组件
- `no-membership` 的 `path="*"` 改成 `<Navigate to="/welcome" replace />`
- **`/bootstrap` 保留为独立路由**（一次性高风险操作需要可直达的 URL），`/welcome` 在 onprem + 可 bootstrap 时给出入口
- 壳内的 `/onboarding` 保留并重定向到 `/welcome`
- `git rm` 两个旧页面

- [ ] **Step 4: 变异验证**

把 `path="*"` 的重定向改回就地渲染 → 确认第 4 条会红（**断言的是地址栏，不是内容**——只断言内容的话就地渲染也会绿）。改回。

- [ ] **Step 5: 跑 scoped 测试 + 提交**

---

## Task 2: 五屏重画

**Files:** Rewrite `LoginPage.tsx`、`JoinPage.tsx`、`BootstrapPage.tsx`、`SuspendedPage.tsx`、`NotConfiguredPage.tsx`；Modify `styles/features/entry.css`

**统一版式：** 屏幕居中的窄栏（`max-width` 约 420–520px），顶部品牌，kicker + 大标题 + 一句人话说明 + 主操作。不用顶栏/subnav（那是壳内的东西）。文案中文。

**⚠️ 两条一次性凭据纪律是硬不变量，重画不得削弱：**

- **Join**：邀请令牌来自 **URL 片段**（`#invite=`），不是 query。片段不进服务端访问日志、不进 Referer。重画后仍必须如此，且流程结束后 `localStorage` / `sessionStorage` / `location.href` 三处均无令牌
- **Bootstrap**：secret 只在请求 header 里（`X-PAX-Bootstrap-Secret`），不持久化，**请求落定后立刻清空输入框**，不自动重试

**先通读这两页的现有测试**，把它们保护的行为列进报告，重画后逐条核对。

- [ ] **Step 1: 通读五页与其测试，列行为清单**
- [ ] **Step 2: 重画**
- [ ] **Step 3: 变异验证**：把 Bootstrap 的「请求落定后清空输入框」去掉 → 确认有测试会红。**如果不红，补一条**
- [ ] **Step 4: 逐条核对行为清单 + 提交**

---

## Task 3: 三主题实测

**Files:** `web/src/styles/features/entry.css`，必要时 `tokens.css` / `themes.css`

复用阶段 5/6 的验证工装：`.superpowers/sdd/2026-08-06-portal-modernist-phase6-apps-settings/theme-check-phase6.html`（或阶段 5 的同类文件）。它已修掉三个方法论 bug——背景 alpha 合成、祖先 opacity 累乘、**Chrome 把 `color-mix()` 序列化成 `color(srgb r g b / a)` 而非 `rgb()`**。

**判定标准：** 本阶段新增的类三主题全部 ≥ 4.5。既有类不达标只记录不修。

**特别注意：** 入场屏是**全屏铺底**的，不像壳内页面嵌在 `.card` 里。阶段 6 踩过这个坑——`MetricTile` 复用到页面底色上反而退化，因为它此前的调用点都嵌在 `.card` 里。**入场屏的每个类都要按「直接坐在页面底色上」来测。**

- [ ] **Step 1: 搭验证页并实测三主题**
- [ ] **Step 2: 修到本阶段新增类全部 ≥ 4.5**
- [ ] **Step 3: 报告给出修复前后完整数字表 + 「未解析变量」检查结果**

---

## Task 4: 文档对齐

**Files:** `AGENTS.md`、`docs/on-prem-identity-frontend-integration.md`、`docs/on-prem-operations-frontend-integration.md`、`docs/frontend-redesign-brief.md`

**这个任务要求逐条核对，不是「看起来更新了」。**

1. **`AGENTS.md` §Web frontend：**
   - `styles/` 文件清单——七个阶段新增了 `access-tree.css` / `agent-detail.css` / `governance*.css` / `apps-*.css` / `settings-pages.css` / `entry.css` 等，**去 `ls web/src/styles/features/` 拿实际清单**
   - **删掉「全屏应用渲染在 PortalShell 之外」这条规则**——阶段 6 已经把最后一个全屏逃逸模式删了
   - 组件清单补齐（`Kicker` / `Tag` / `Seg` / `Card` / `MetricTile` / `Crumbs` / `EmptyState` / `DataTable` / `CommandPalette` / `SecretCeremony` 等，**以 `ls web/src/components/` 为准**）
2. **两份 frontend-integration 文档**里引用的 Portal 路由路径，按七个阶段之后的**实际路由表**更新（`web/src/app/routes.tsx` + `App.tsx` 是真源）
3. **`docs/frontend-redesign-brief.md`** 顶部加一行历史标注（**不删**——它记录了重构动机，有档案价值）

**方法：** 对每份文档，先 `grep` 出所有路由路径与文件名引用，逐个对照实际代码确认，再改。**在报告里列出「改了哪些、每处的依据是什么」**。

- [ ] **Step 1–3: 逐份文档处理**
- [ ] **Step 4: 提交**

---

## Task 5: 跨屏验收（本阶段的重点任务）

**Files:** 只加测试，不改实现（除非发现真缺陷）

**为什么单列：** 阶段 4、5、6 **连续三次**栽在「给整个阶段的验收项没被写进任何任务的 brief」上。阶段 6 单列了这个任务，结果它抓出 4 处覆盖缺口 + 1 个真实实现缺陷（Model usage 的「可重试」从来没实现过）。

**四条验收，逐条写测试：**

**(1) 两种部署形态各走一遍首次进入** —— saas 与 onprem 各一条端到端：
未认证 → 登录 → 无 membership → `/welcome` → 看到正确的分叉内容。

**(2) 旧入场路径仍可达** —— `/onboarding`、`/bootstrap`、`/join#invite=...` 各一条；
`no-membership` 下任意路径重定向到 `/welcome`（**断言地址栏**）。

**(3) 一次性凭据不泄漏** —— Join 的令牌与 Bootstrap 的 secret，流程结束后
`localStorage` / `sessionStorage` / `location.href` 三处均无。
**变异自验**：故意把令牌写进 `sessionStorage` → 确认测试会红。

**(4) `AGENTS.md` 与实际结构一致** —— 逐条核对 Task 4 的三项：
`styles/` 清单对得上 `ls web/src/styles/features/`；组件清单对得上 `ls web/src/components/`；
「全屏逃逸」那条规则确实删了（`grep -n "fullscreen\|全屏" AGENTS.md` 应无残留规则）。
**这条可以写成一个读文件对比的测试**，也可以是人工核对后在报告里给出逐条结论——你判断哪种更能真正守住。

**方法**：对 (1)(2)(3)，把对应实现临时改坏，跑**全量**测试，确认有测试变红。**任何一处改坏后仍全绿的就是缺口，补测试。** 每次变异的实际输出记进报告。

- [ ] **Step 1–4: 四条验收逐条处理**
- [ ] **Step 5: 跑全量 `npm --prefix web test && npm --prefix web run build`**
- [ ] **Step 6: 提交**

---

## 收尾检查

- [ ] `npm --prefix web test` 全绿
- [ ] `npm --prefix web run build` 干净
- [ ] `git diff --stat main...HEAD -- . ':(exclude)web' ':(exclude)docs' ':(exclude)AGENTS.md'` 为空
- [ ] `grep -rn "EntryPage\|OnboardingPage" web/src web/tests` —— 零命中
- [ ] `grep -rnE "var\(--(bg|muted|accent|text|border|surface|mono)\)" web/src/styles/features/entry.css` —— 零命中
- [ ] 三主题实测记录已写进报告
- [ ] `AGENTS.md` 的三项逐条核对结论已写进报告
