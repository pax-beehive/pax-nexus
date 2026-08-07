# Modernist Portal 阶段 7 · 入场动线 + 清理

Date: 2026-08-06
Status: Accepted

七阶段重构的最后一阶段。七个入场屏按 Modernist 重画，SaaS 与 on-prem 两条入场
统一为 `/welcome` 并按 profile 分叉，删死代码，把文档与实际结构对齐。

上位设计：`docs/superpowers/specs/2026-08-04-portal-modernist-redesign-design.md` §阶段 7。
前一阶段：`2026-08-06-portal-modernist-phase6-apps-settings-design.md`。

**本文的裁定由作者独立做出**——用户在 2026-08-06 出门前授权独立闭环完成剩余阶段。
可能引起异议的取舍在 §6 逐条记账。

---

## 1. 现状

七个入场屏挂在 `web/src/App.tsx` 里、**在 `PortalShell` 之外**，按 `AuthState.kind` 分派：

| 状态 | 渲染 |
|---|---|
| `not-configured` | `NotConfiguredPage` |
| `unauthenticated` | `LoginPage`（`path="*"`） |
| `no-membership` + saas | `EntryPage`（`path="*"`） |
| `no-membership` + onprem | `BootstrapPage`（`/bootstrap`）+ `EntryPage`（`path="*"`） |
| 无 membership 但需引导 | `OnboardingPage`（`path="*"`，另有 `/onboarding` 在壳内） |
| `suspended` | `SuspendedPage` |
| 任意状态 | `JoinPage`（`/join`，令牌在 URL 片段里） |

profile 分叉机制**已存在**：`auth/AuthContext.tsx:20` 的 `DeploymentProfile = "saas" | "onprem"`，
通过 `GET /v1/teams` 探测（saas 200 / on-prem 501），结果缓存在
`sessionStorage` 的 `portal.deployment-profile`。

---

## 2. 要做的三件事

### 2.1 七屏按 Modernist 重画

统一的入场版式：屏幕居中的窄栏（`max-width` 约 420–520px），顶部品牌，
kicker + 大标题 + 一句人话说明 + 主操作。不使用顶栏/subnav（它们属于壳内）。

**文案一律中文**（见 §5 的全站语言规则）。

### 2.2 SaaS 与 on-prem 两条入场统一为 `/welcome`

现状是两条 `path="*"` 的兜底各渲染一个组件（`EntryPage` / `OnboardingPage`），
用户在地址栏看到的是自己原本要去的任意路径，而不是一个可辨认、可分享、可回退的入口。

改为：**一条命名路由 `/welcome`**，页面内部按 `profile` 分叉：

- **saas**：「你还不属于任何团队」——等待邀请 / 用邀请链接加入
- **onprem**：首次安装则引导认领 Owner（`/bootstrap` 的内容并入），
  否则同 saas 的等待态

`no-membership` 状态下的 `path="*"` 兜底**重定向到 `/welcome`**（`replace`），
而不是就地渲染。这样地址栏与实际内容一致。

**`/bootstrap` 保留为独立路由**——它是一次性的高风险操作（认领第一个 Owner），
有自己的 URL 便于文档引用与直达。`/welcome` 在 onprem + 可 bootstrap 时给出入口。

### 2.3 删死代码 + 文档对齐

- 重画后不再被引用的样式与组件
- `AGENTS.md` §Web frontend：`styles/` 文件清单已变（七个阶段新增了
  `access-tree.css` / `agent-detail.css` / `governance*.css` / `apps-*.css` /
  `settings-pages.css` 等）；**「全屏应用渲染在 PortalShell 之外」这条规则要删**
  ——阶段 6 已经把最后一个全屏逃逸模式删掉了；组件清单补齐
- `docs/on-prem-identity-frontend-integration.md` 与
  `docs/on-prem-operations-frontend-integration.md` 里引用的 Portal 路由路径，
  按七个阶段之后的实际路由表更新
- `docs/frontend-redesign-brief.md` 标注为历史文档

---

## 3. 取数与降级

入场屏基本无取数（`AuthContext` 已经把状态解析好）。例外：

| 屏 | 取数 | 失败表现 |
|---|---|---|
| Join | `POST /v1/invitations/accept`（令牌来自 URL 片段） | 就地错误 + 可重试；**令牌绝不进 URL query 或存储** |
| Bootstrap | `POST /v1/bootstrap/claim`（secret 来自输入框） | 就地错误；**secret 只在 header 里，不持久化，请求落定后立刻清输入框** |
| Welcome | 无（profile 已在 `AuthContext` 里） | — |

**Join 与 Bootstrap 的一次性凭据纪律是本阶段的硬不变量**，重画不得削弱。

---

## 4. 跨屏验收项（显式指派，见计划的最后一个任务）

阶段 4、5、6 连续三次栽在「给整个阶段的验收项没被写进任何一个任务的 brief」上。
本阶段继续把它们单列：

1. **两种部署形态各走一遍首次进入** —— saas 与 onprem 各一条端到端测试：
   未认证 → 登录 → 无 membership → `/welcome` → 看到正确的分叉内容
2. **旧入场路径仍可达** —— `/onboarding`、`/bootstrap`、`/join#invite=...`
   各有测试；`no-membership` 下任意路径重定向到 `/welcome`
3. **一次性凭据不泄漏** —— Join 的令牌与 Bootstrap 的 secret 在流程结束后，
   `localStorage` / `sessionStorage` / `location.href` 三处均无
4. **`AGENTS.md` 与实际结构一致** —— `styles/` 清单、组件清单、
   「全屏逃逸」那条规则的删除，逐条核对

---

## 5. 全站语言规则（阶段 6 §8.1 定下，本阶段继续适用）

- **可见文案中文**
- **`aria-label` 与 landmark 名英文**（仓库现状 38 英 : 2 中）
- **kicker 用 `分区 · 子页` 的英文原文**

阶段 4、5、6 的中英文分界线三次与任务边界重合，根因是没有任何任务拥有这条规则。
本阶段把它写进全局约束，并由跨屏验收任务复核。

---

## 6. 记账偏离与作者裁定

1. **`/bootstrap` 保留为独立路由**，不并进 `/welcome`。它是一次性高风险操作，
   需要可直达、可在文档里引用的 URL。`/welcome` 提供入口。
2. **`no-membership` 的 `path="*"` 改为重定向而非就地渲染**。就地渲染会让地址栏
   停在用户原本要去的任意路径上，既不可分享也不可回退。
3. **`OnboardingPage` 与 `EntryPage` 合并进 `/welcome`**，两个组件删除。
   它们的差异本质上就是 profile 分叉，用两个组件表达是历史包袱。
   壳内的 `/onboarding` 路由**保留并重定向到 `/welcome`**（可能有外部链接）。
4. **`docs/frontend-redesign-brief.md` 只加一行历史标注，不删**——它记录了重构前的
   现状与痛点，是这七个阶段的动机来源，有档案价值。

---

## 7. 全局约束

- 纯前端，零后端改动
- 不引入任何前端运行时依赖
- 按钮一律走 `web/src/components/Button.tsx`，禁止 `.btn.ghost` 点分写法
- 样式只引用 `--color-*` / `--space-*` / `--font-*` 与前几阶段新增的 token；
  **禁止** `tokens.css` 第二个 `:root` 块里的兼容别名
- 间距刻度只有 `--space-1/2/3/4/6/8`，**没有 `--space-5` 和 `--space-7`**
- **`opacity < 1` 淡化正文在 arcade 下不安全**——arcade 的正文/底色对比度本身只有
  ~4.74:1，任何 `opacity` 都会把它压到 AA 线以下。次要文字用显式的
  主题稳定颜色，不要用 opacity
- 一次性凭据（邀请令牌、bootstrap secret）永不进 `localStorage` / `sessionStorage` /
  URL query / 日志
- **三主题必须实测**，复用阶段 5/6 的验证工装
- 提交信息用中文

---

## 8. 风险

| 风险 | 缓解 |
|---|---|
| 合并 Entry/Onboarding 时弄丢某条分支 | 重画前先通读两个组件与现有测试，列出行为清单逐条核对 |
| `/welcome` 重定向造成循环 | `no-membership` 下 `/welcome` 自身不再重定向；测试覆盖两种 profile |
| 重画削弱一次性凭据纪律 | §4 第 3 条的三处存储断言 |
| 文档更新流于形式 | §4 第 4 条要求逐条核对，而不是「看起来更新了」 |
