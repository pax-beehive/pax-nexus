# Modernist Portal 阶段 1：设计系统 + 外壳 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `web/` 的样式基建换成 Modernist 设计系统，用顶栏 + 二级 subnav 的 `AppShell` 替换侧边栏，并让全部旧路由重定向到新路由——期间现有页面原样可用。

**Architecture:** 样式层原地换血（`styles/` 按层重组，组件外部 API 不变、仅重绑类名），外壳层新建 `src/app/`（导航模型、顶栏、subnav、路由表、旧路由重定向表、⌘K 面板）。导航可见性由纯函数 `navModel.ts` 计算，页面组件在本阶段不改内部结构。

**Tech Stack:** React 18 + TypeScript + Vite + react-router-dom v6 + vitest/testing-library。**不新增任何运行时依赖。**

**上游文档：** `docs/superpowers/specs/2026-08-04-portal-modernist-redesign-design.md`（本计划只覆盖其中的「阶段 1」）。设计稿本体 `docs/w.html`。

**明确不在本阶段范围内**（spec §3.3 提到，但归属后续阶段）：`ConfirmDialog` 的
`preview` 槽随阶段 3 的级联吊销弹窗落地；`SecretCard` → `SecretCeremony` 的全屏化
随阶段 4 的令牌仪式落地。本阶段这两个组件只重绑类名，行为不变。

## Global Constraints

- **不新增运行时依赖。** 生产依赖仍限于 `react` `react-dom` `react-router-dom` `react-markdown` `remark-gfm`。
- **`AGENTS.md` §Web frontend 三条硬约定继续成立：** 按钮一律通过 `components/Button.tsx` 渲染，不在 `<button>` 上手写 `className="btn …"`；组件只引用 CSS 变量，不硬编码颜色；用布局工具类而非 inline 间距样式。设计稿自身是 inline-style 原型产物，**不照搬**。
- **两色制。** Modernist 调色板不含绿/琥珀/蓝。朱红（accent）表示「需要注意 / 主行动 / 危险」，中性（neutral）表示「常态」。不得为状态徽标或 toast 引入新色相。危险确认按钮用 `.btn-primary`，安全选项用 `.btn-secondary`（与设计稿一致）。
- **token 值逐字照抄设计稿**，不自行发挥：`--color-bg #f3f2f2`、`--color-surface #eae9e9`、`--color-text #201e1d`、`--color-accent #ec3013`、`--color-accent-2 #e15b47`、`--radius-*` 全部为 `0`。
- **组件外部 API 不变。** `Button` `Badge` `RoleBadge` `Modal` `ConfirmDialog` `Toasts` `SecretCard` `PagedListCard` `Countdown` `ErrorBoundary` `RegionError` `TeamSwitcher` 的 props 签名在本阶段一律不动，只改内部输出的 class。
- **红线不得倒退**（本阶段不新增这些能力，但改动不得破坏）：能力门控、一次性密钥的一次性、四个不支持 `Idempotency-Key` 的创建操作不自动重试、乐观锁冲突重取不覆盖、级联后果前置披露、终态不可逆、区块级错误隔离、审计页原始 ID 永远可见。
- **测试命令：** 全量 `cd web && npm test`；单文件 `cd web && npx vitest run <path>`；类型与构建 `cd web && npm run build`。
- **提交粒度：** 每个 Task 末尾提交一次，commit message 用 `feat(web):` / `refactor(web):` / `test(web):` 前缀。

---

## File Structure

**新建**

| 文件 | 职责 |
|---|---|
| `web/src/styles/fonts/archivo-latin.woff2` 等 3 个 | 自托管 Archivo 可变字体子集 |
| `web/src/styles/tokens.css` | Modernist 变量单一真源 + `@font-face` |
| `web/src/styles/layout.css` | `.app-shell` `.topbar` `.subnav` `.page` 等外壳与布局工具类 |
| `web/src/app/navModel.ts` | 纯函数：`HumanMe` → 顶栏项 + subnav 项 |
| `web/src/app/TopBar.tsx` | 品牌 / 团队切换器 / 顶栏项 / ⌘K 触发 / 用户菜单 |
| `web/src/app/SubNav.tsx` | 二级导航条 |
| `web/src/app/UserMenu.tsx` | 邮箱 + 角色 + Appearance 链接 + Sign out |
| `web/src/app/AppShell.tsx` | 外壳组合，替代 `pages/PortalShell.tsx` 的壳部分 |
| `web/src/app/routes.tsx` | 新路由 → 页面组件 |
| `web/src/app/legacyRoutes.ts` | 旧路由 → 新路由的数据表 |
| `web/src/app/LegacyRedirect.tsx` | 按 `legacyRoutes` 渲染重定向路由 |
| `web/src/components/CommandPalette.tsx` | ⌘K 面板 |
| `web/src/components/Card.tsx` `Tag.tsx` `Seg.tsx` `Field.tsx` `DataTable.tsx` `MetricTile.tsx` `Kicker.tsx` `Crumbs.tsx` `EmptyState.tsx` | 设计稿要求、现在缺失的基础组件 |
| `web/src/pages/settings/AppearancePage.tsx` | 最小主题选择页（主题控件从侧边栏迁出的落点） |
| `web/tests/design-tokens.test.ts` | token 完整性 + 主题完整性 + 对比度 |
| `web/tests/contrast.ts` | 测试用对比度计算助手 |
| `web/tests/navModel.test.ts` | 导航模型纯函数测试 |
| `web/tests/legacy-routes.test.ts` | 重定向表遍历断言 |
| `web/tests/app-shell.dom.test.tsx` | 顶栏 / subnav / 用户菜单 DOM 测试 |
| `web/tests/command-palette.dom.test.tsx` | ⌘K 键盘路径 |
| `web/tests/ui-components.dom.test.tsx` | 新增基础组件 |

**重写**

| 文件 | 变化 |
|---|---|
| `web/src/styles/base.css` | 只留 reset + 排版刻度 + 链接/焦点/选区；布局搬去 `layout.css`，token 搬去 `tokens.css` |
| `web/src/styles/themes.css` | 三主题重锚到 Modernist |
| `web/src/styles/components.css` | 按 Modernist 类系统重写 |
| `web/src/styles/index.css` | 导入顺序改为 tokens → themes → base → components → layout → features/* |

**修改**

| 文件 | 变化 |
|---|---|
| `web/src/components/Button.tsx` | class 从 `btn primary sm` 改为 `btn btn-primary btn-sm` |
| `web/src/components/Badge.tsx` | class 从 `badge b-<status>` 改为 `tag tag-attention` / `tag tag-neutral` |
| `web/src/App.tsx` | `PortalShell` → `AppShell`；全屏应用路由并入壳内路由 |
| `web/src/pages/PortalShell.tsx` | **删除**（壳职责移交 `app/AppShell.tsx`，路由移交 `app/routes.tsx`） |
| `web/src/pages/AppsPage.tsx` | **删除** |
| `web/src/styles/apps.css` `pulse.css` `operations.css` `session-audit.css` `teams.css` `wiki.css` | 移入 `styles/features/` 子目录，内容本阶段不动 |
| `web/tests/portal-nav-groups.dom.test.tsx` `portal-side-collapse.dom.test.tsx` `apps.dom.test.tsx` | **删除**（被测能力已移除，替代测试见 Task 5/9） |
| `web/tests/theme.dom.test.tsx` | 重写：主题控件从侧边栏迁到 `/settings/appearance` |

---

## Task 1: Archivo 字体与 tokens.css

**Files:**
- Create: `web/src/styles/fonts/archivo-latin.woff2`、`archivo-latin-ext.woff2`、`archivo-vietnamese.woff2`
- Create: `web/src/styles/tokens.css`
- Create: `web/tests/design-tokens.test.ts`
- Modify: `web/src/styles/index.css`

**Interfaces:**
- Consumes: 无
- Produces: `tokens.css` 中 `:root` 上的全部自定义属性，后续所有样式与组件只允许引用这些名字。导出的 token 名清单即 Task 2 的主题完整性断言基准。

- [ ] **Step 1: 从设计稿 bundle 中解出字体文件**

设计稿把 3 个 woff2 子集以 base64 存在 `docs/w.html` 的 manifest 里。运行：

```bash
cd /Users/toddzheng/Workspace/golang/team-memory
mkdir -p web/src/styles/fonts
python3 - <<'PY'
import re, json, base64
src = open('docs/w.html', encoding='utf-8').read()
man = json.loads(re.search(r'<script type="__bundler/manifest"[^>]*>(.*?)</script>', src, re.S).group(1))
# uuid → 目标文件名，取自 w.html 内 @font-face 的 unicode-range 归属
names = {
    '3ff247ce-d2ab-4581-99e7-4d81d477a6fc': 'archivo-latin.woff2',
    'a5fef365-6c4f-4b71-9a07-53a67adea43d': 'archivo-latin-ext.woff2',
    '6738cbd8-8672-43fc-b2cf-097c17364866': 'archivo-vietnamese.woff2',
}
for uuid, name in names.items():
    data = base64.b64decode(man[uuid]['data'])
    assert data[:4] == b'wOF2', name
    open('web/src/styles/fonts/' + name, 'wb').write(data)
    print(name, len(data))
PY
```

预期输出三行，分别约 34940 / 32672 / 13216 字节。

- [ ] **Step 2: 写失败的测试**

创建 `web/tests/design-tokens.test.ts`：

```ts
// tokens.css 是 Modernist 设计系统的单一真源。这些断言防止重构中意外删掉
// 某个 token —— 删掉后引用它的组件会静默退化成浏览器默认值，不会报错。
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const tokens = readFileSync(new URL("../src/styles/tokens.css", import.meta.url), "utf8");

/**
 * 抓取所有 :root 块里声明的自定义属性名。tokens.css 有两个 :root：
 * 正式 token 一个，过渡期的兼容别名一个 —— 两个都要算进来。
 */
export function declaredTokens(css: string): string[] {
  const names: string[] = [];
  for (const block of css.matchAll(/:root\s*\{([\s\S]*?)\n\}/g)) {
    names.push(...[...block[1].matchAll(/^\s*(--[a-z0-9-]+)\s*:/gm)].map((m) => m[1]));
  }
  return names;
}

const REQUIRED = [
  "--color-bg", "--color-surface", "--color-text",
  "--color-accent", "--color-accent-2", "--color-divider",
  "--font-heading", "--font-heading-weight", "--font-body", "--font-mono",
  "--space-1", "--space-2", "--space-3", "--space-4", "--space-6", "--space-8",
  "--radius-sm", "--radius-md", "--radius-lg",
  "--shadow-sm", "--shadow-md", "--shadow-lg",
];

describe("design tokens", () => {
  it("declares every required token", () => {
    const declared = new Set(declaredTokens(tokens));
    expect(REQUIRED.filter((t) => !declared.has(t))).toEqual([]);
  });

  it("declares full neutral / accent / accent-2 ramps", () => {
    const declared = new Set(declaredTokens(tokens));
    for (const role of ["neutral", "accent", "accent-2"]) {
      for (let step = 100; step <= 900; step += 100) {
        expect(declared.has(`--color-${role}-${step}`)).toBe(true);
      }
    }
  });

  it("pins the Modernist base values verbatim", () => {
    expect(tokens).toContain("--color-bg: #f3f2f2");
    expect(tokens).toContain("--color-text: #201e1d");
    expect(tokens).toContain("--color-accent: #ec3013");
  });

  it("keeps every radius at zero", () => {
    for (const name of ["--radius-sm", "--radius-md", "--radius-lg"]) {
      expect(tokens).toMatch(new RegExp(`${name}:\\s*0(px)?;`));
    }
  });

  it("self-hosts Archivo and references no external font host", () => {
    expect(tokens).toContain("@font-face");
    expect(tokens).toContain("./fonts/archivo-latin.woff2");
    expect(tokens).not.toMatch(/https?:\/\//);
  });

  // styles/features/ 的六个特性样式表在阶段 1 不重写，仍引用旧 token 名。
  // 删掉任何一个别名都会让对应页面掉进浏览器默认样式且不报错，所以在这里钉住。
  // 这些别名随各特性样式表的重写逐个删除，阶段 6 结束时本用例一并删除。
  it("keeps the deprecated token aliases the un-migrated feature sheets need", () => {
    const declared = new Set(declaredTokens(tokens));
    const legacy = [
      "--bg", "--surface", "--surface-2", "--border", "--border-strong",
      "--text", "--muted", "--faint", "--accent", "--accent-hover", "--accent-soft",
      "--ok", "--ok-bg", "--warn", "--warn-bg", "--bad", "--bad-bg", "--info", "--info-bg",
      "--input-bg", "--on-accent", "--secret-bg", "--danger-border", "--warn-border",
      "--bad-border", "--accent-border", "--shadow", "--mono", "--serif", "--sans",
    ];
    expect(legacy.filter((t) => !declared.has(t))).toEqual([]);
  });
});
```

- [ ] **Step 3: 运行测试确认失败**

Run: `cd web && npx vitest run tests/design-tokens.test.ts`
Expected: FAIL —— `ENOENT` 读不到 `src/styles/tokens.css`。

- [ ] **Step 4: 写 tokens.css**

创建 `web/src/styles/tokens.css`：

```css
/* Modernist — 设计系统 token 单一真源。取自 docs/w.html，数值不得自行改动；
   要调整观感请改这里，不要在组件里覆盖。 */

/* Archivo 是可变字体，三个文件是按 unicode-range 切的子集，同一个文件覆盖
   100–900 全字重区间。自托管，不引外部 CDN。 */
@font-face {
  font-family: "Archivo";
  font-style: normal;
  font-weight: 100 900;
  font-stretch: 100%;
  font-display: swap;
  src: url("./fonts/archivo-latin.woff2") format("woff2");
  unicode-range: U+0000-00FF, U+0131, U+0152-0153, U+02BB-02BC, U+02C6, U+02DA,
    U+02DC, U+0304, U+0308, U+0329, U+2000-206F, U+20AC, U+2122, U+2191, U+2193,
    U+2212, U+2215, U+FEFF, U+FFFD;
}
@font-face {
  font-family: "Archivo";
  font-style: normal;
  font-weight: 100 900;
  font-stretch: 100%;
  font-display: swap;
  src: url("./fonts/archivo-latin-ext.woff2") format("woff2");
  unicode-range: U+0100-02BA, U+02BD-02C5, U+02C7-02CC, U+02CE-02D7, U+02DD-02FF,
    U+0304, U+0308, U+0329, U+1D00-1DBF, U+1E00-1E9F, U+1EF2-1EFF, U+2020,
    U+20A0-20AB, U+20AD-20C0, U+2113, U+2C60-2C7F, U+A720-A7FF;
}
@font-face {
  font-family: "Archivo";
  font-style: normal;
  font-weight: 100 900;
  font-stretch: 100%;
  font-display: swap;
  src: url("./fonts/archivo-vietnamese.woff2") format("woff2");
  unicode-range: U+0102-0103, U+0110-0111, U+0128-0129, U+0168-0169, U+01A0-01A1,
    U+01AF-01B0, U+0300-0301, U+0303-0304, U+0308-0309, U+0323, U+0329,
    U+1EA0-1EF9, U+20AB;
}

:root {
  --color-bg: #f3f2f2;
  --color-surface: #eae9e9;
  --color-text: #201e1d;
  --color-accent: #ec3013;
  --color-accent-2: #e15b47;
  --color-divider: color-mix(in srgb, #201e1d 40%, transparent);

  /* 三条色阶在 OKLCH 上共用同一套亮度刻度，所以任意角色的同一档视觉明度相同。 */
  --color-neutral-100: #f8f4f4;
  --color-neutral-200: #eae7e7;
  --color-neutral-300: #d7d3d3;
  --color-neutral-400: #bab6b6;
  --color-neutral-500: #9b9797;
  --color-neutral-600: #7d7979;
  --color-neutral-700: #605d5d;
  --color-neutral-800: #444141;
  --color-neutral-900: #2d2b2b;

  --color-accent-100: #fff2ef;
  --color-accent-200: #ffe0d9;
  --color-accent-300: #ffc4b8;
  --color-accent-400: #ff9783;
  --color-accent-500: #ff563c;
  --color-accent-600: #dd2b0f;
  --color-accent-700: #ae1800;
  --color-accent-800: #7c1405;
  --color-accent-900: #4d170e;

  --color-accent-2-100: #fff2ef;
  --color-accent-2-200: #ffe0da;
  --color-accent-2-300: #ffc4b9;
  --color-accent-2-400: #ff9784;
  --color-accent-2-500: #ef6853;
  --color-accent-2-600: #c94b39;
  --color-accent-2-700: #9e3526;
  --color-accent-2-800: #71261b;
  --color-accent-2-900: #471d16;

  --font-heading: "Archivo", system-ui, sans-serif;
  --font-heading-weight: 800;
  --font-body: "Archivo", system-ui, sans-serif;
  --font-mono: ui-monospace, "SF Mono", Menlo, Consolas, monospace;

  --space-1: 4px;
  --space-2: 8px;
  --space-3: 12px;
  --space-4: 16px;
  --space-6: 24px;
  --space-8: 32px;

  --radius-sm: 0;
  --radius-md: 0;
  --radius-lg: 0;

  --shadow-sm: 0 1px 2px color-mix(in srgb, #2d2b2b 14%, transparent);
  --shadow-md: 0 3px 10px color-mix(in srgb, #2d2b2b 16%, transparent);
  --shadow-lg: 0 12px 32px color-mix(in srgb, #2d2b2b 22%, transparent);

  --backdrop: color-mix(in srgb, var(--color-neutral-900) 50%, transparent);
}

/* — 兼容别名（临时）—
   styles/features/ 下的六个特性样式表（wiki / pulse / operations / session-audit /
   teams / apps）在阶段 1 不重写，它们仍引用改造前的 token 名。这里把旧名映射到
   Modernist 值，让那些页面在过渡期保持可读。
   两色制下 ok/info 归中性、warn/bad 归 accent —— 绿色和琥珀色不再存在。
   每重写一个特性样式表就删掉它用到的别名；阶段 6 结束时本块整体移除。
   **禁止在新代码里引用这些名字。** */
:root {
  --bg: var(--color-bg);
  --surface: var(--color-surface);
  --surface-2: var(--color-neutral-200);
  --border: var(--color-divider);
  --border-strong: var(--color-neutral-500);
  --text: var(--color-text);
  --muted: color-mix(in srgb, var(--color-text) 55%, transparent);
  --faint: color-mix(in srgb, var(--color-text) 40%, transparent);
  --accent: var(--color-accent);
  --accent-hover: var(--color-accent-600);
  --accent-soft: var(--color-accent-100);
  --ok: var(--color-neutral-800);
  --ok-bg: var(--color-neutral-100);
  --warn: var(--color-accent-800);
  --warn-bg: var(--color-accent-100);
  --bad: var(--color-accent-800);
  --bad-bg: var(--color-accent-100);
  --info: var(--color-neutral-800);
  --info-bg: var(--color-neutral-100);
  --input-bg: var(--color-surface);
  --on-accent: var(--color-bg);
  --secret-bg: var(--color-accent-100);
  --danger-border: var(--color-accent);
  --warn-border: var(--color-accent);
  --bad-border: var(--color-accent);
  --accent-border: var(--color-accent);
  --shadow: color-mix(in srgb, var(--color-neutral-900) 22%, transparent);
  --mono: var(--font-mono);
  --serif: var(--font-heading);
  --sans: var(--font-body);
}
```

> 特性样式表里写死的 `border-radius: 8px` 之类的字面量不在别名覆盖范围内，那几个区域
> 在过渡期仍是圆角。这是已知且可接受的过渡态，随各自阶段的重写消失。

- [ ] **Step 5: 把 tokens.css 接进 index.css**

修改 `web/src/styles/index.css`，把 `tokens.css` 放在最前（其余行暂不动）：

```css
/* Global stylesheet entry. Keep each concern in its own file; add new
   feature styles as a new file here rather than appending to an existing one. */
@import "./tokens.css";
@import "./base.css";
@import "./themes.css";
@import "./components.css";
@import "./apps.css";
@import "./operations.css";
@import "./wiki.css";
@import "./pulse.css";
@import "./session-audit.css";
@import "./teams.css";
```

- [ ] **Step 6: 运行测试确认通过**

Run: `cd web && npx vitest run tests/design-tokens.test.ts`
Expected: PASS，5 个断言全绿。

- [ ] **Step 7: 确认构建能处理字体**

Run: `cd web && npm run build`
Expected: 构建成功，`dist/assets/` 下出现 3 个带 hash 的 `.woff2`。

- [ ] **Step 8: 提交**

```bash
git add web/src/styles/fonts web/src/styles/tokens.css web/src/styles/index.css web/tests/design-tokens.test.ts
git commit -m "feat(web): Modernist design tokens and self-hosted Archivo"
```

---

## Task 2: 三主题重锚与对比度校验

**Files:**
- Create: `web/tests/contrast.ts`
- Modify: `web/src/styles/themes.css`（整体重写）
- Modify: `web/tests/design-tokens.test.ts`（追加主题断言）

**Interfaces:**
- Consumes: Task 1 的 `declaredTokens(css)` 与 `tokens.css` 的 token 名清单
- Produces: `contrastRatio(hex, hex): number`（`web/tests/contrast.ts` 导出），Task 后续无人依赖，仅供本文件与将来的主题改动复用

- [ ] **Step 1: 写对比度助手**

创建 `web/tests/contrast.ts`：

```ts
// WCAG 2.1 相对亮度与对比度。只支持 #rrggbb —— 主题文件里的颜色一律是这个
// 形式，color-mix() 之类的动态值不参与校验。
export function relativeLuminance(hex: string): number {
  const value = hex.replace("#", "");
  const channels = [0, 2, 4].map((i) => parseInt(value.slice(i, i + 2), 16) / 255);
  const linear = channels.map((c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
  return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
}

export function contrastRatio(a: string, b: string): number {
  const la = relativeLuminance(a);
  const lb = relativeLuminance(b);
  const [hi, lo] = la > lb ? [la, lb] : [lb, la];
  return (hi + 0.05) / (lo + 0.05);
}
```

- [ ] **Step 2: 写失败的测试**

在 `web/tests/design-tokens.test.ts` 末尾追加。注意顶部 import 需要补 `contrastRatio`：

```ts
import { contrastRatio } from "./contrast";

const themes = readFileSync(new URL("../src/styles/themes.css", import.meta.url), "utf8");

/** 抓取 [data-theme="x"] 块里声明的自定义属性，返回 名→值。 */
function themeBlock(css: string, name: string): Record<string, string> {
  const block = css.match(new RegExp(`\\[data-theme="${name}"\\]\\s*\\{([\\s\\S]*?)\\n\\}`));
  if (!block) return {};
  const out: Record<string, string> = {};
  for (const m of block[1].matchAll(/^\s*(--[a-z0-9-]+)\s*:\s*([^;]+);/gm)) {
    out[m[1]] = m[2].trim();
  }
  return out;
}

describe("themes", () => {
  // 主题只允许覆盖颜色类 token；字体、间距、圆角、阴影是全局不变量。
  const colorTokens = () =>
    declaredTokens(tokens).filter((t) => t.startsWith("--color-") || t === "--backdrop");

  it.each(["dark", "arcade"])("theme %s overrides every color token", (name) => {
    const overridden = new Set(Object.keys(themeBlock(themes, name)));
    expect(colorTokens().filter((t) => !overridden.has(t))).toEqual([]);
  });

  it.each([
    ["beige", "#f3f2f2", "#201e1d"],
    ["dark", "#201e1d", "#f3f2f2"],
    ["arcade", "#ec3013", "#ffffff"],
  ])("theme %s meets WCAG AA for body text", (_name, bg, text) => {
    expect(contrastRatio(bg, text)).toBeGreaterThanOrEqual(4.5);
  });
});
```

- [ ] **Step 3: 运行测试确认失败**

Run: `cd web && npx vitest run tests/design-tokens.test.ts`
Expected: FAIL —— 现有 `themes.css` 用的是旧 token 名（`--bg` `--surface` 等），`dark` / `arcade` 都覆盖不到 `--color-*`。

- [ ] **Step 4: 重写 themes.css**

用下面内容**整体替换** `web/src/styles/themes.css`：

```css
/* 主题只覆盖颜色类 token（含 --backdrop），字体/间距/圆角/阴影是全局不变量。
   默认 beige 主题就是 tokens.css 的 :root，不在这里重复声明。
   两色制：accent 表示需要注意/主行动/危险，neutral 表示常态。 */

[data-theme="dark"] {
  --color-bg: #201e1d;
  --color-surface: #2d2b2b;
  --color-text: #f3f2f2;
  --color-accent: #ff563c;
  --color-accent-2: #ef6853;
  --color-divider: color-mix(in srgb, #f3f2f2 35%, transparent);

  --color-neutral-100: #2d2b2b;
  --color-neutral-200: #444141;
  --color-neutral-300: #605d5d;
  --color-neutral-400: #7d7979;
  --color-neutral-500: #9b9797;
  --color-neutral-600: #bab6b6;
  --color-neutral-700: #d7d3d3;
  --color-neutral-800: #eae7e7;
  --color-neutral-900: #f8f4f4;

  --color-accent-100: #4d170e;
  --color-accent-200: #7c1405;
  --color-accent-300: #ae1800;
  --color-accent-400: #dd2b0f;
  --color-accent-500: #ff563c;
  --color-accent-600: #ff9783;
  --color-accent-700: #ffc4b8;
  --color-accent-800: #ffe0d9;
  --color-accent-900: #fff2ef;

  --color-accent-2-100: #471d16;
  --color-accent-2-200: #71261b;
  --color-accent-2-300: #9e3526;
  --color-accent-2-400: #c94b39;
  --color-accent-2-500: #ef6853;
  --color-accent-2-600: #ff9784;
  --color-accent-2-700: #ffc4b9;
  --color-accent-2-800: #ffe0da;
  --color-accent-2-900: #fff2ef;

  --backdrop: color-mix(in srgb, #000 62%, transparent);
}

/* Arcade：朱红铺底。正文压在 accent-900 之上以满足 AA；此主题下
   .btn-primary 反转为墨色底，否则按钮会消失在背景里。 */
[data-theme="arcade"] {
  --color-bg: #ec3013;
  --color-surface: #d4290f;
  --color-text: #ffffff;
  --color-accent: #201e1d;
  --color-accent-2: #4d170e;
  --color-divider: color-mix(in srgb, #ffffff 45%, transparent);

  --color-neutral-100: #d4290f;
  --color-neutral-200: #c2250d;
  --color-neutral-300: #a81f0b;
  --color-neutral-400: #8d1a09;
  --color-neutral-500: #ffb3a5;
  --color-neutral-600: #ffc9bf;
  --color-neutral-700: #ffdad3;
  --color-neutral-800: #ffece8;
  --color-neutral-900: #fff6f4;

  --color-accent-100: #4d170e;
  --color-accent-200: #3d1410;
  --color-accent-300: #2d1110;
  --color-accent-400: #251010;
  --color-accent-500: #201e1d;
  --color-accent-600: #2d2b2b;
  --color-accent-700: #444141;
  --color-accent-800: #ffece8;
  --color-accent-900: #fff6f4;

  --color-accent-2-100: #471d16;
  --color-accent-2-200: #3a1712;
  --color-accent-2-300: #2e120f;
  --color-accent-2-400: #260f0d;
  --color-accent-2-500: #201e1d;
  --color-accent-2-600: #2d2b2b;
  --color-accent-2-700: #444141;
  --color-accent-2-800: #ffece8;
  --color-accent-2-900: #fff6f4;

  --backdrop: color-mix(in srgb, #201e1d 60%, transparent);
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `cd web && npx vitest run tests/design-tokens.test.ts`
Expected: PASS。`#ec3013` 对 `#ffffff` 的对比度约 4.7，刚过 AA；若实现中改动了 arcade 背景，这条会立刻挡住。

- [ ] **Step 6: 提交**

```bash
git add web/src/styles/themes.css web/tests/contrast.ts web/tests/design-tokens.test.ts
git commit -m "feat(web): re-anchor beige/dark/arcade themes to Modernist tokens"
```

---

## Task 3: base.css 与 components.css 重写

**Files:**
- Modify: `web/src/styles/base.css`（整体重写）
- Modify: `web/src/styles/components.css`（整体重写）
- Modify: `web/src/components/Button.tsx`
- Modify: `web/src/components/Badge.tsx`
- Create: `web/tests/ui-classes.dom.test.tsx`

**Interfaces:**
- Consumes: Task 1 的 token 名
- Produces: 组件类系统 `.btn/.btn-primary/.btn-secondary/.btn-ghost/.btn-icon/.btn-sm`、`.card/.card-kicker/.card-title/.card-body/.card-meta`、`.tag/.tag-attention/.tag-neutral/.tag-outline`、`.table`、`.seg/.seg-opt`、`.input/.field`、`.dialog/.dialog-backdrop/.dialog-title/.dialog-body/.dialog-actions`、`.hr`、`.elev-sm/md/lg`。Task 5、7、8 只允许使用这些类名。

- [ ] **Step 1: 写失败的测试**

创建 `web/tests/ui-classes.dom.test.tsx`。锁住组件输出的类名契约——后续任何人改 `Button`/`Badge` 内部映射都会被这里挡住：

```tsx
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Badge, RoleBadge } from "../src/components/Badge";
import { Button } from "../src/components/Button";

afterEach(cleanup);

describe("Button classes", () => {
  it("emits the Modernist btn class system", () => {
    render(<Button>Default</Button>);
    expect(screen.getByRole("button").className).toBe("btn btn-secondary");
  });

  it("maps variant and size to modifier classes", () => {
    render(
      <Button variant="primary" size="sm">
        Go
      </Button>,
    );
    expect(screen.getByRole("button").className).toBe("btn btn-primary btn-sm");
  });

  // 两色制：危险动作与主行动共用 accent，但 danger 仍是独立 variant，
  // 保证代码里的意图可读，也留出将来单独调整的接缝。
  it("renders danger with the primary appearance", () => {
    render(<Button variant="danger">Revoke</Button>);
    expect(screen.getByRole("button").className).toBe("btn btn-primary btn-danger");
  });

  it("appends caller className last", () => {
    render(<Button className="btn-block">Wide</Button>);
    expect(screen.getByRole("button").className).toBe("btn btn-secondary btn-block");
  });
});

describe("Badge classes", () => {
  // 需要人处理的状态用 attention（朱红），其余一律 neutral。
  it.each(["suspended", "pending"])("marks %s as attention", (status) => {
    render(<Badge status={status} />);
    expect(screen.getByText(status).className).toBe("tag tag-attention");
  });

  it.each(["active", "retired", "revoked", "expired", "accepted", "consumed", "removed"])(
    "marks %s as neutral",
    (status) => {
      render(<Badge status={status} />);
      expect(screen.getByText(status).className).toBe("tag tag-neutral");
    },
  );

  it("outlines elevated roles and keeps member neutral", () => {
    render(
      <>
        <RoleBadge role="owner" />
        <RoleBadge role="member" />
      </>,
    );
    expect(screen.getByText("owner").className).toBe("tag tag-outline");
    expect(screen.getByText("member").className).toBe("tag tag-neutral");
  });
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npx vitest run tests/ui-classes.dom.test.tsx`
Expected: FAIL —— 当前输出是 `btn`、`badge b-suspended`、`badge b-role`。

- [ ] **Step 3: 改 Button.tsx**

用下面内容替换 `web/src/components/Button.tsx` 的组件体：

```tsx
import { forwardRef } from "react";
import type { ButtonHTMLAttributes } from "react";

export type ButtonVariant = "default" | "primary" | "danger" | "ghost";
export type ButtonSize = "md" | "sm";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
}

/**
 * 共享按钮，渲染 styles/components.css 的 .btn 类系统。
 *
 * 两色制（见设计系统约定）：Modernist 调色板没有独立的危险色，accent 本身
 * 就是朱红，所以 danger 复用 .btn-primary 的外观，另加一个语义化的
 * .btn-danger 标记 —— 代码里的意图保持可读，将来要区分也有接缝。
 */
const VARIANT_CLASSES: Record<ButtonVariant, string[]> = {
  default: ["btn-secondary"],
  primary: ["btn-primary"],
  danger: ["btn-primary", "btn-danger"],
  ghost: ["btn-ghost"],
};

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = "default", size = "md", className, ...rest },
  ref,
) {
  const classes = ["btn", ...VARIANT_CLASSES[variant]];
  if (size !== "md") classes.push(`btn-${size}`);
  if (className) classes.push(className);
  return <button ref={ref} className={classes.join(" ")} {...rest} />;
});
```

- [ ] **Step 4: 改 Badge.tsx**

用下面内容替换 `web/src/components/Badge.tsx`：

```tsx
import type { AgentProfile } from "../api/types";

/**
 * 两色制：需要人处理的状态用 attention（朱红），其余一律 neutral。
 * 终态（retired / removed / revoked / expired）不需要人处理，因此是 neutral。
 */
const ATTENTION_STATUSES = new Set(["suspended", "pending"]);

function toneFor(status: string): string {
  return ATTENTION_STATUSES.has(status) ? "tag-attention" : "tag-neutral";
}

export function Badge({ status }: { status: string }) {
  return <span className={`tag ${toneFor(status)}`}>{status}</span>;
}

/** owner / admin 用描边强调，member 是常态。 */
export function RoleBadge({ role }: { role: string }) {
  const tone = role === "member" ? "tag-neutral" : "tag-outline";
  return <span className={`tag ${tone}`}>{role}</span>;
}

/**
 * Doc section 8.1: `provisioned_by` 只有在 Device 自助注册该 Agent 时才存在。
 * 徽标由字段是否存在决定（缺失即 undefined），绝不由值的真假决定。
 */
export function ProvisionedByBadge({
  agent,
}: {
  agent: Pick<AgentProfile, "provisioned_by">;
}) {
  if (agent.provisioned_by === undefined) {
    return <span className="tag tag-neutral">human-registered</span>;
  }
  return (
    <span className="tag tag-neutral" title={`provisioned by device ${agent.provisioned_by}`}>
      device-provisioned
    </span>
  );
}
```

- [ ] **Step 5: 重写 base.css**

用下面内容**整体替换** `web/src/styles/base.css`。原文件里的布局规则（`.shell` `.side` `.nav` `.main` `.page-head` 与那条 820px 媒体查询）**不要搬过来**——它们属于即将删除的侧边栏，`layout.css` 会在 Task 5 提供替代品：

```css
/* Reset、排版刻度与全局元素样式。token 见 tokens.css，组件类见 components.css，
   布局与外壳见 layout.css。 */

*, *::before, *::after { box-sizing: border-box; }

body {
  margin: 0;
  background: var(--color-bg);
  color: var(--color-text);
  font-family: var(--font-body);
  font-size: 15px;
  line-height: 1.55;
  font-weight: 400;
}

h1, h2, h3, h4, h5, h6 {
  font-family: var(--font-heading);
  font-weight: var(--font-heading-weight);
  line-height: 1.12;
  letter-spacing: -0.015em;
  margin: 0 0 var(--space-2);
}
h1 { font-size: 42px; }
h2 { font-size: 32px; }
h3 { font-size: 25px; }
h4 { font-size: 20px; }
h5 { font-size: 16px; }
h6 { font-size: 13px; letter-spacing: 0.08em; text-transform: uppercase; }

p { margin: 0 0 var(--space-3); }
a { color: var(--color-accent); text-underline-offset: 3px; }
img { display: block; max-width: 100%; }
figure { margin: 0; }

code, .mono {
  font-family: var(--font-mono);
  font-size: 12.5px;
}
code {
  background: var(--color-surface);
  border: 1px solid var(--color-divider);
  padding: 1px 5px;
}

.text-muted, .muted { color: color-mix(in srgb, var(--color-text) 55%, transparent); }
.faint { color: color-mix(in srgb, var(--color-text) 40%, transparent); }
.small { font-size: 12.5px; }

:focus { outline: none; }
:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
::selection { background: color-mix(in srgb, var(--color-accent) 30%, transparent); }

.sr-only {
  position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px;
  overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; border: 0;
}
```

- [ ] **Step 6: 重写 components.css**

用下面内容**整体替换** `web/src/styles/components.css`。类系统照抄设计稿，末尾三块是本项目必需、设计稿未覆盖的扩展（`.btn-danger` 语义别名、`.tag-attention`、`.note`/`.toast`/`.secret-*` 的两色化）：

```css
/* Modernist 组件类。纯 CSS，无 JS。改观感请先改 tokens.css。 */

/* — rules — */
.hr, .divider { height: 2px; border: 0; margin: var(--space-4) 0; background: var(--color-divider); }

/* — buttons — */
.btn {
  display: inline-flex; align-items: center; justify-content: center; gap: 6px;
  cursor: pointer; text-decoration: none;
  font-family: var(--font-heading); font-weight: var(--font-heading-weight);
  font-size: 13px; line-height: 1.2; color: var(--color-text);
  background: transparent; border: 1px solid transparent;
  padding: calc(var(--space-2) * 0.85) var(--space-3);
  border-radius: var(--radius-md);
}
.btn svg { display: block; }
.btn:disabled { opacity: 0.45; cursor: not-allowed; }
.btn-primary { background: var(--color-accent); color: var(--color-bg); }
.btn-primary:hover:not(:disabled) { background: var(--color-accent-600); }
.btn-primary:active:not(:disabled) { background: var(--color-accent-700); }
.btn-secondary { border-color: var(--color-divider); }
.btn-secondary:hover:not(:disabled) { background: color-mix(in srgb, var(--color-text) 7%, transparent); }
.btn-secondary:active:not(:disabled) { background: color-mix(in srgb, var(--color-text) 14%, transparent); }
.btn-ghost { color: var(--color-accent); padding-inline: var(--space-1); }
.btn-ghost:hover:not(:disabled) { background: color-mix(in srgb, var(--color-accent) 10%, transparent); }
.btn-ghost:active:not(:disabled) { background: color-mix(in srgb, var(--color-accent) 18%, transparent); }
.btn-icon { width: 34px; height: 34px; padding: 0; }
.btn-block { width: 100%; justify-content: flex-start; text-align: left; }
.btn-sm { font-size: 12px; padding: 4px var(--space-2); }
/* 语义别名：外观与 .btn-primary 相同（两色制），仅标记意图。 */
.btn-danger { }

/* — forms — */
.field > label {
  display: block; font-size: 12px; margin-bottom: 5px;
  color: color-mix(in srgb, var(--color-text) 70%, transparent);
}
.input, input[type="text"], input[type="email"], input[type="password"],
input[type="search"], input[type="date"], select, textarea {
  width: 100%; min-height: 36px; padding: 6px 10px; font: inherit;
  font-size: 14px; color: var(--color-text); caret-color: var(--color-accent);
  background: var(--color-surface);
  border: 1px solid var(--color-divider); border-radius: var(--radius-md);
}
.input:hover, input:hover, select:hover, textarea:hover {
  border-color: color-mix(in srgb, var(--color-text) 45%, transparent);
}
.input:focus-visible, input:focus-visible, select:focus-visible, textarea:focus-visible {
  border-color: var(--color-accent); outline-offset: 0;
}
textarea { min-height: 90px; resize: vertical; }
.ck { display: inline-flex; align-items: center; gap: 8px; cursor: pointer; font-size: 14px; }
.ck input { width: auto; min-height: 0; }

/* — segmented control — */
.seg { display: inline-flex; overflow: hidden; border: 1px solid var(--color-divider); border-radius: var(--radius-md); }
.seg-opt, .seg button {
  display: inline-flex; align-items: center; gap: 6px;
  padding: 7px 12px; font-size: 13px; cursor: pointer;
  background: transparent; border: 0; color: inherit; font-family: inherit;
}
.seg-opt + .seg-opt, .seg button + button { border-left: 1px solid var(--color-divider); }
.seg-opt:has(input:checked), .seg button.on { background: var(--color-accent); color: var(--color-bg); }
.seg-opt:not(:has(input:checked)):hover, .seg button:not(.on):hover {
  background: color-mix(in srgb, var(--color-text) 7%, transparent);
}

/* — cards — */
.card {
  display: flex; flex-direction: column; gap: var(--space-2);
  padding: var(--space-3); border-radius: var(--radius-md); background: var(--color-surface);
}
.card.flat { background: transparent; border: 1px solid var(--color-divider); }
.card-kicker, .kicker {
  font-size: 10px; letter-spacing: 0.1em; text-transform: uppercase; color: var(--color-accent);
}
.card-title { font-family: var(--font-heading); font-weight: var(--font-heading-weight); font-size: 17px; line-height: 1.2; }
.card-body { margin: 0; font-size: 13px; opacity: 0.8; flex: 1; }
.card-meta { display: flex; align-items: center; gap: 6px; font-size: 11px; color: color-mix(in srgb, var(--color-text) 50%, transparent); }
.elev-sm { box-shadow: var(--shadow-sm); }
.elev-md { box-shadow: var(--shadow-md); }
.elev-lg { box-shadow: var(--shadow-lg); }

/* — tags — */
.tag {
  display: inline-flex; align-items: center; font-size: 11px;
  letter-spacing: 0.02em; padding: 3px 10px; border-radius: var(--radius-sm);
}
.tag-attention { background: var(--color-accent-100); color: var(--color-accent-800); }
.tag-neutral { background: var(--color-neutral-100); color: var(--color-neutral-800); }
.tag-outline { border: 1px solid var(--color-accent); color: var(--color-accent); }

/* — tables — */
.table, table { width: 100%; border-collapse: collapse; font-size: 14px; }
.table th, table th {
  text-align: left; font-size: 11px; letter-spacing: 0.08em; text-transform: uppercase;
  color: color-mix(in srgb, var(--color-text) 60%, transparent);
  padding: var(--space-2); border-bottom: 2px solid var(--color-divider);
}
.table td, table td { padding: var(--space-2); border-bottom: 1px solid var(--color-divider); }
.table tbody tr:hover, table tbody tr:hover { background: color-mix(in srgb, var(--color-text) 4%, transparent); }

/* — dialog — */
.dialog-backdrop, .modal-backdrop {
  position: fixed; inset: 0; display: grid; place-items: center;
  padding: var(--space-4); background: var(--backdrop); z-index: 50;
}
.dialog, .modal {
  width: min(440px, 100%); display: flex; flex-direction: column; gap: var(--space-3);
  padding: var(--space-4); border-radius: var(--radius-lg);
  background: var(--color-surface); box-shadow: var(--shadow-lg);
  max-height: 85vh; overflow-y: auto;
}
.dialog-title { font-family: var(--font-heading); font-weight: var(--font-heading-weight); font-size: 20px; }
.dialog-body { font-size: 14px; opacity: 0.85; }
.dialog-actions { display: flex; justify-content: flex-end; gap: var(--space-2); margin-top: var(--space-2); }

/* — notices（两色制：需要注意 = accent，常态 = neutral） — */
.note {
  font-size: 13px; padding: var(--space-2) var(--space-3);
  border-left: 2px solid var(--color-divider); background: var(--color-neutral-100);
  margin: var(--space-2) 0;
}
.note.warn, .note.bad { border-left-color: var(--color-accent); background: var(--color-accent-100); }

/* — toasts — */
.toasts { position: fixed; right: var(--space-4); bottom: var(--space-4); display: grid; gap: var(--space-2); z-index: 60; }
.toast {
  font-size: 13px; padding: var(--space-2) var(--space-3);
  background: var(--color-neutral-900); color: var(--color-neutral-100);
  box-shadow: var(--shadow-md); border-left: 2px solid transparent;
}
.toast.bad, .toast.warn { border-left-color: var(--color-accent); }

/* — one-time secrets — */
.secret-card { border: 2px solid var(--color-accent); background: var(--color-accent-100); padding: var(--space-3); }
.secret-val {
  font-family: var(--font-mono); font-size: 13px; word-break: break-all;
  background: var(--color-bg); border: 1px solid var(--color-divider); padding: var(--space-2);
}
.countdown { font-family: var(--font-mono); font-size: 12px; color: var(--color-accent); }

/* — misc — */
.center-page { min-height: 100vh; display: grid; place-items: center; padding: var(--space-6); }
.center-box { width: min(460px, 100%); display: flex; flex-direction: column; gap: var(--space-3); }
.grid { display: grid; gap: var(--space-3); grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); }
.agent-card { cursor: pointer; }
.agent-card:hover { background: color-mix(in srgb, var(--color-text) 5%, transparent); }
.field-row { display: grid; gap: var(--space-3); grid-template-columns: 1fr 1fr; }
.filter-label { font-size: 12px; color: color-mix(in srgb, var(--color-text) 70%, transparent); }

/* — 兼容别名（临时）—
   阶段 1 不改页面内部结构，而 18 处页面代码仍手写 className="badge b-…"
   （AdminOperationsPage 的 TONE_BADGE、AdminTeamNoteDetailPage、TodoPage、
   WikiBrowsePage、AdminSessionAuditPage 的 b-risk-*/b-approval-* 等）。
   这里把旧 badge 类映射到两色制外观，页面代码与其断言均无需改动。
   每迁移一个页面到 <Tag> 就删掉对应行；阶段 6 结束时本块整体移除。
   **禁止在新代码里使用 .badge / .b-*，新代码一律用 .tag / <Tag>。** */
.badge {
  display: inline-flex; align-items: center; font-size: 11px;
  letter-spacing: 0.02em; padding: 3px 10px; border-radius: var(--radius-sm);
  background: var(--color-neutral-100); color: var(--color-neutral-800);
}
/* 需要人处理 → accent；其余（含全部终态与正常态）→ 保持中性默认值。 */
.b-suspended, .b-pending, .b-risk-high, .b-risk-critical,
.b-approval-denied, .b-approval-unknown {
  background: var(--color-accent-100); color: var(--color-accent-800);
}
.b-role { border: 1px solid var(--color-accent); background: transparent; color: var(--color-accent); }

/* .tabs 与 .seg 的差别在语义（多视图 vs 同一数据的取值），过渡期外观一致。 */
.tabs { display: inline-flex; border-bottom: 2px solid var(--color-divider); }
.tabs button {
  padding: 8px 14px; font-size: 13px; cursor: pointer;
  background: transparent; border: 0; color: inherit; font-family: inherit;
  border-bottom: 2px solid transparent; margin-bottom: -2px;
}
.tabs button.on { border-bottom-color: var(--color-accent); color: var(--color-accent); }
```

> `.badge` 与 `.b-*` 只在这里获得外观，`components/Badge.tsx` 已经改用 `.tag`。
> 两者短期并存是有意为之：组件化的徽标立刻进入新系统，页面里手写的那批
> 随各自阶段迁移。`tests/admin-operations-events.dom.test.tsx` 里那三条
> `className` 断言因此**保持不变**。

- [ ] **Step 7: 运行新测试确认通过**

Run: `cd web && npx vitest run tests/ui-classes.dom.test.tsx`
Expected: PASS。

- [ ] **Step 8: 运行全量测试，确认没有测试依赖旧类名**

Run: `cd web && npm test`

Expected: **全绿**。兼容别名块的作用就是让这一步不产生连带损伤——页面代码、
feature 样式表、以及 `admin-operations-events.dom.test.tsx` 里的 `className` 断言
全都无需改动。

若 `theme.dom.test.tsx` 失败（它断言侧边栏里的主题下拉），说明你提前动了侧边栏——
本 Task 不应触碰 `PortalShell.tsx`，回退那部分改动，该文件在 Task 9 处理。
若其它文件失败，逐个查看：断言旧类名的就地改成按 role/name 查询；断言旧
token 名的说明兼容别名缺了一项，补进 `tokens.css` 的别名块并同步
`design-tokens.test.ts` 的清单。

- [ ] **Step 9: 提交**

```bash
git add web/src/styles/base.css web/src/styles/components.css \
        web/src/components/Button.tsx web/src/components/Badge.tsx \
        web/tests/ui-classes.dom.test.tsx
git commit -m "refactor(web): rebind Button/Badge to the Modernist class system"
```

---

## Task 4: 导航模型

**Files:**
- Create: `web/src/app/navModel.ts`
- Create: `web/tests/navModel.test.ts`

**Interfaces:**
- Consumes: `src/api/types.ts` 的 `HumanMe`；`src/lib/capabilities.ts` 的 `can` 与 `hasServerCapability`
- Produces:
  - `export interface NavItem { to: string; label: string }`
  - `export interface NavSection { id: string; label: string; to: string; items: NavItem[] }`
  - `export function navSections(me: HumanMe): NavSection[]`
  - `export function landingPath(me: HumanMe): string`
  - `export function sectionForPath(sections: NavSection[], pathname: string): NavSection | undefined`

- [ ] **Step 1: 写失败的测试**

创建 `web/tests/navModel.test.ts`：

```ts
// 导航可见性是纯函数，单独测掉它，DOM 测试就只需要验证渲染而不必穷举角色。
import { describe, expect, it } from "vitest";
import { landingPath, navSections, sectionForPath } from "../src/app/navModel";
import { makeMe, makeSaasMe } from "./helpers";

const sectionIds = (me = makeMe()) => navSections(me).map((s) => s.id);
const itemsOf = (id: string, me = makeMe()) =>
  navSections(me).find((s) => s.id === id)?.items.map((i) => i.to) ?? [];

describe("navSections", () => {
  it("shows member only management / apps / settings", () => {
    const me = makeMe({ role: "member", capabilities: [] });
    expect(sectionIds(me)).toEqual(["management", "apps", "settings"]);
  });

  it("hides overview without the server-issued view.operations capability", () => {
    expect(sectionIds(makeMe({ role: "owner", capabilities: [] }))).not.toContain("overview");
  });

  it("shows overview once view.operations is granted", () => {
    expect(sectionIds(makeMe({ capabilities: ["view.operations"] }))).toContain("overview");
  });

  it("gives member a management section with no admin sub-items", () => {
    expect(itemsOf("management", makeMe({ role: "member" }))).toEqual(["/management"]);
  });

  it("gives admin the full management sub-navigation", () => {
    expect(itemsOf("management", makeMe({ role: "admin" }))).toEqual([
      "/management",
      "/management/members",
      "/management/devices",
      "/management/agents",
      "/management/invitations",
    ]);
  });

  it("builds governance from audit role plus server capabilities", () => {
    const me = makeMe({ role: "admin", capabilities: ["view.operations", "view.team-memory"] });
    expect(itemsOf("governance", me)).toEqual([
      "/governance/audit",
      "/governance/sessions",
      "/governance/pipeline",
      "/governance/memory",
    ]);
  });

  it("drops the governance section entirely when nothing inside is visible", () => {
    expect(sectionIds(makeMe({ role: "member" }))).not.toContain("governance");
  });

  it("shows Team settings only in the saas profile", () => {
    expect(itemsOf("settings", makeMe({ role: "owner" }))).toEqual([
      "/settings/memory",
      "/settings/usage",
      "/settings/appearance",
    ]);
    expect(itemsOf("settings", makeSaasMe({ role: "owner" }))[0]).toBe("/settings/team");
  });
});

describe("landingPath", () => {
  it("lands admins on overview when they can see it", () => {
    expect(landingPath(makeMe({ capabilities: ["view.operations"] }))).toBe("/overview");
  });

  it("lands everyone else on management", () => {
    expect(landingPath(makeMe({ role: "member" }))).toBe("/management");
  });
});

describe("sectionForPath", () => {
  it("matches the section owning the deepest path", () => {
    const sections = navSections(makeMe({ role: "admin" }));
    expect(sectionForPath(sections, "/management/devices/dev_01")?.id).toBe("management");
    expect(sectionForPath(sections, "/governance/audit")?.id).toBe("governance");
  });

  it("returns undefined for a path outside every section", () => {
    expect(sectionForPath(navSections(makeMe()), "/join")).toBeUndefined();
  });
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npx vitest run tests/navModel.test.ts`
Expected: FAIL —— 找不到模块 `../src/app/navModel`。

- [ ] **Step 3: 实现 navModel.ts**

创建 `web/src/app/navModel.ts`：

```ts
// 顶栏与二级导航的可见性规则。纯函数，不碰 React、不碰路由。
//
// 两类门控并存：`can()` 是客户端角色矩阵（只用于隐藏入口，后端逐请求鉴权），
// `hasServerCapability()` 是服务端下发的能力位。Overview 与 Pipeline health
// 走后者，因为它们的数据源本身就由服务端能力决定。

import type { HumanMe } from "../api/types";
import { can, hasServerCapability } from "../lib/capabilities";
import { hasTeams } from "../lib/teams";

export interface NavItem {
  to: string;
  label: string;
}

export interface NavSection {
  id: string;
  label: string;
  /** 点击顶栏项时前往的路径，等于第一个可见子项。 */
  to: string;
  items: NavItem[];
}

export function navSections(me: HumanMe): NavSection[] {
  const sections: NavSection[] = [];
  const adminLike = can(me.role, "view.members");

  if (hasServerCapability(me, "view.operations")) {
    sections.push({ id: "overview", label: "Overview", to: "/overview", items: [] });
  }

  // Management 对所有人可见。member 只看到访问树本身，根节点是他自己。
  const management: NavItem[] = [{ to: "/management", label: "Access tree" }];
  if (adminLike) {
    management.push(
      { to: "/management/members", label: "Members" },
      { to: "/management/devices", label: "Devices" },
      { to: "/management/agents", label: "Agents" },
    );
  }
  if (can(me.role, "invite.member")) {
    management.push({ to: "/management/invitations", label: "Invitations" });
  }
  sections.push({ id: "management", label: "Management", to: "/management", items: management });

  const governance: NavItem[] = [];
  if (can(me.role, "view.audit")) {
    governance.push(
      { to: "/governance/audit", label: "Audit trail" },
      { to: "/governance/sessions", label: "Session audit" },
    );
  }
  if (hasServerCapability(me, "view.operations")) {
    governance.push({ to: "/governance/pipeline", label: "Pipeline health" });
  }
  if (hasServerCapability(me, "view.team-memory")) {
    governance.push({ to: "/governance/memory", label: "Memory explorer" });
  }
  if (governance.length > 0) {
    sections.push({
      id: "governance",
      label: "Governance",
      to: governance[0].to,
      items: governance,
    });
  }

  sections.push({
    id: "apps",
    label: "Apps",
    to: "/apps/wiki",
    items: [
      { to: "/apps/wiki", label: "Wiki" },
      { to: "/apps/todos", label: "Todos" },
    ],
  });

  const settings: NavItem[] = [];
  // on-prem 没有团队概念，Team 面板整条不存在。
  if (hasTeams(me)) settings.push({ to: "/settings/team", label: "Team" });
  settings.push(
    { to: "/settings/memory", label: "Memory rules" },
    { to: "/settings/usage", label: "Model usage" },
    { to: "/settings/appearance", label: "Appearance" },
  );
  sections.push({ id: "settings", label: "Settings", to: settings[0].to, items: settings });

  return sections;
}

/** 登录后的默认落地路径：能看 Overview 就落 Overview，否则落 Management。 */
export function landingPath(me: HumanMe): string {
  return hasServerCapability(me, "view.operations") ? "/overview" : "/management";
}

/**
 * 当前路径归属哪个顶栏分区。用最长前缀匹配，这样 /management/devices/dev_01
 * 这类深层路径也能正确点亮顶栏。
 */
export function sectionForPath(
  sections: NavSection[],
  pathname: string,
): NavSection | undefined {
  let best: NavSection | undefined;
  let bestLength = 0;
  for (const section of sections) {
    const prefixes = section.items.length > 0 ? section.items.map((i) => i.to) : [section.to];
    for (const prefix of prefixes) {
      if ((pathname === prefix || pathname.startsWith(`${prefix}/`)) && prefix.length > bestLength) {
        best = section;
        bestLength = prefix.length;
      }
    }
  }
  return best;
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd web && npx vitest run tests/navModel.test.ts`
Expected: PASS，12 个用例全绿。

- [ ] **Step 5: 提交**

```bash
git add web/src/app/navModel.ts web/tests/navModel.test.ts
git commit -m "feat(web): navigation model for the top-bar IA"
```

---

## Task 5: AppShell（顶栏 + subnav + 用户菜单）与 layout.css

**Files:**
- Create: `web/src/app/TopBar.tsx`、`web/src/app/SubNav.tsx`、`web/src/app/UserMenu.tsx`、`web/src/app/AppShell.tsx`
- Create: `web/src/styles/layout.css`
- Create: `web/tests/app-shell.dom.test.tsx`
- Modify: `web/src/styles/index.css`

**Interfaces:**
- Consumes: Task 4 的 `navSections` `sectionForPath`；现有 `components/TeamSwitcher.tsx`（props `{ me, collapsed }`，本阶段传 `collapsed={false}`）；`auth/AuthContext` 的 `useAuth().logout`
- Produces:
  - `export function AppShell({ me, children }: { me: HumanMe; children: ReactNode })`
  - `export function TopBar({ me, onOpenPalette }: { me: HumanMe; onOpenPalette: () => void })`
  - CSS 类：`.app-shell` `.topbar` `.topbar-cell` `.topbar-brand` `.subnav` `.page` `.page-head` `.toolbar` `.row` `.between` `.wrap` `.stack` `.section` `.flush`

- [ ] **Step 1: 写失败的测试**

创建 `web/tests/app-shell.dom.test.tsx`：

```tsx
// 外壳的 DOM 契约：顶栏项按角色渲染、subnav 跟随当前分区、用户菜单能登出。
// 可见性的穷举在 tests/navModel.test.ts，这里只验证渲染与交互。
import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

function shellFetch(path: string, init: RequestInit): Response {
  if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
  if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [] });
  if (path.startsWith("/v1/teams")) return jsonResponse({ teams: [] });
  throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
}

function topbar(): HTMLElement {
  return screen.getByRole("navigation", { name: "Sections" });
}

describe("AppShell top bar", () => {
  it("renders only the sections a member may see", async () => {
    await renderApp({
      route: "/management",
      me: makeMe({ role: "member" }),
      fetch: shellFetch,
    });

    const nav = topbar();
    within(nav).getByRole("link", { name: "Management" });
    within(nav).getByRole("link", { name: "Apps" });
    within(nav).getByRole("link", { name: "Settings" });
    expect(within(nav).queryByRole("link", { name: "Overview" })).toBeNull();
    expect(within(nav).queryByRole("link", { name: "Governance" })).toBeNull();
  });

  it("marks the active section and renders its sub-navigation", async () => {
    await renderApp({
      route: "/management/members",
      me: makeMe({ role: "admin" }),
      fetch: (path, init) => {
        if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [] });
        return shellFetch(path, init);
      },
    });

    expect(
      within(topbar()).getByRole("link", { name: "Management" }).getAttribute("aria-current"),
    ).toBe("page");
    const sub = screen.getByRole("navigation", { name: "Section pages" });
    within(sub).getByRole("link", { name: "Access tree" });
    within(sub).getByRole("link", { name: "Members" });
    within(sub).getByRole("link", { name: "Devices" });
  });

  it("hides the sub-navigation for a section with no sub-pages", async () => {
    // /overview 在阶段 1 由 AdminPulsePage 顶替，它会请求 operations 的
    // agents 与 events；两个响应都要带上各自的时间字段，否则页面在渲染
    // "generated at" 时抛错，被 ErrorBoundary 接住，断言就测不到本意。
    await renderApp({
      route: "/overview",
      me: makeMe({ capabilities: ["view.operations"] }),
      fetch: (path, init) => {
        if (path.startsWith("/v1/admin/operations/agents")) {
          return jsonResponse({
            agents: [],
            from_time: "2026-08-04T00:00:00Z",
            to_time: "2026-08-04T01:00:00Z",
            generated_at: "2026-08-04T01:00:00Z",
          });
        }
        if (path.startsWith("/v1/admin/operations/events")) {
          return jsonResponse({ events: [], generated_at: "2026-08-04T01:00:00Z" });
        }
        return shellFetch(path, init);
      },
    });

    await screen.findByRole("heading", { name: "Team Pulse" });
    expect(screen.queryByRole("navigation", { name: "Section pages" })).toBeNull();
  });

  it("signs out from the user menu", async () => {
    const { user, fetchMock } = await renderApp({
      route: "/management",
      me: makeMe({ role: "member" }),
      fetch: (path, init) => {
        if (path === "/v1/auth/logout") return jsonResponse({});
        return shellFetch(path, init);
      },
    });

    await user.click(screen.getByRole("button", { name: /alice@example\.com/ }));
    await user.click(screen.getByRole("menuitem", { name: "Sign out" }));

    expect(
      fetchMock.mock.calls.some(([input, init]) =>
        String(input) === "/v1/auth/logout" && (init as RequestInit)?.method === "POST",
      ),
    ).toBe(true);
  });

  it("closes the user menu on Escape", async () => {
    const { user } = await renderApp({
      route: "/management",
      me: makeMe({ role: "member" }),
      fetch: shellFetch,
    });

    await user.click(screen.getByRole("button", { name: /alice@example\.com/ }));
    screen.getByRole("menu");
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("menu")).toBeNull();
  });
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npx vitest run tests/app-shell.dom.test.tsx`
Expected: FAIL —— 找不到 `role="navigation"` 且 name 为 `Sections` 的元素（现在渲染的是侧边栏 `Portal navigation`）。

- [ ] **Step 3: 写 layout.css**

创建 `web/src/styles/layout.css`：

```css
/* 外壳与布局工具类。2px 主分隔线 / 1px 次分隔线是 Modernist 的骨架特征，
   在这里统一定义，页面不要自行绘制分隔线。 */

.app-shell { min-height: 100vh; background: var(--color-bg); color: var(--color-text); }

/* — 顶栏 — */
.topbar {
  display: flex; align-items: stretch;
  border-bottom: 2px solid var(--color-divider);
  background: var(--color-bg);
  position: sticky; top: 0; z-index: 30;
}
.topbar-cell {
  display: flex; align-items: center; gap: 10px;
  padding: 0 18px; border-right: 1px solid var(--color-divider);
}
.topbar-brand {
  font-family: var(--font-heading); font-weight: var(--font-heading-weight);
  font-size: 21px; letter-spacing: -0.025em; line-height: 1.1;
  padding: 10px 20px; min-width: 210px;
  border-right: 1px solid var(--color-divider);
  display: flex; align-items: center;
}
.topbar-nav { display: flex; align-items: stretch; }
.topbar-nav a {
  display: flex; align-items: center; padding: 0 20px;
  font-size: 13px; letter-spacing: 0.02em; color: inherit; text-decoration: none;
  border-right: 1px solid var(--color-divider);
}
.topbar-nav a:hover { background: color-mix(in srgb, var(--color-text) 6%, transparent); }
.topbar-nav a[aria-current="page"] { background: var(--color-accent); color: var(--color-bg); }
.topbar-search {
  margin-left: auto; min-width: 260px; justify-content: flex-start;
  border-left: 1px solid var(--color-divider); border-right: 0;
  font-family: var(--font-body); font-weight: 400;
  color: color-mix(in srgb, var(--color-text) 60%, transparent);
}
.topbar-kbd {
  margin-left: auto; border: 1px solid var(--color-divider);
  padding: 1px 6px; font-size: 10px; letter-spacing: 0.06em; font-family: var(--font-mono);
}
.topbar-user { border-left: 1px solid var(--color-divider); border-right: 0; }

/* — 用户菜单 — */
.menu-pop {
  position: absolute; right: 0; top: 100%; min-width: 240px; z-index: 40;
  background: var(--color-bg); border: 2px solid var(--color-divider); box-shadow: var(--shadow-lg);
}
.menu-pop [role="menuitem"] {
  display: flex; width: 100%; align-items: center; gap: 10px;
  padding: 10px 14px; border: 0; border-bottom: 1px solid var(--color-divider);
  background: transparent; font-family: var(--font-body); font-size: 13px;
  color: inherit; text-align: left; cursor: pointer; text-decoration: none;
}
.menu-pop [role="menuitem"]:last-child { border-bottom: 0; }
.menu-pop [role="menuitem"]:hover { background: color-mix(in srgb, var(--color-text) 6%, transparent); }
.menu-head { padding: 8px 14px; font-size: 10px; letter-spacing: 0.12em; text-transform: uppercase; opacity: 0.5; border-bottom: 1px solid var(--color-divider); }

/* — 二级导航 — */
.subnav {
  display: flex; align-items: stretch; gap: 2px; padding: 0 20px;
  border-bottom: 2px solid var(--color-divider); background: var(--color-bg);
  position: sticky; top: 57px; z-index: 25; overflow-x: auto;
}
.subnav a {
  padding: 10px 14px; font-size: 13px; color: inherit; text-decoration: none;
  white-space: nowrap; border-bottom: 2px solid transparent; margin-bottom: -2px;
}
.subnav a:hover { color: var(--color-accent); }
.subnav a[aria-current="page"] { border-bottom-color: var(--color-accent); color: var(--color-accent); }

/* — 内容区 — */
.page { padding: 26px 28px 96px; max-width: 1280px; }
.page-head {
  display: flex; justify-content: space-between; align-items: flex-end;
  gap: var(--space-4); margin-bottom: var(--space-6);
}

/* — 工具类 — */
.row { display: flex; align-items: center; gap: var(--space-2); }
.row.between { justify-content: space-between; }
.row.wrap { flex-wrap: wrap; }
.stack { display: flex; flex-direction: column; gap: var(--space-3); }
.toolbar { display: flex; align-items: center; gap: var(--space-2); flex-wrap: wrap; }
.toolbar input, .toolbar select { width: auto; }
.section { margin-top: var(--space-6); }
.flush { margin: 0; }
```

- [ ] **Step 4: 把 layout.css 接进 index.css**

修改 `web/src/styles/index.css`，在 `components.css` 之后插入一行：

```css
@import "./components.css";
@import "./layout.css";
```

- [ ] **Step 5: 实现 UserMenu.tsx**

创建 `web/src/app/UserMenu.tsx`：

```tsx
// 顶栏右侧的用户菜单。设计稿的顶栏只画了邮箱 + 角色徽标，没有给登出留位置；
// 登出必须存在，所以这里把它做成一个菜单：身份信息 + Appearance 快捷 + Sign out。

import { useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useAuth } from "../auth/AuthContext";
import { RoleBadge } from "../components/Badge";
import { Button } from "../components/Button";
import { useToast } from "../components/Toasts";
import type { HumanMe } from "../api/types";

export function UserMenu({ me }: { me: HumanMe }) {
  const { logout } = useAuth();
  const toast = useToast();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    const onPointerDown = (event: MouseEvent) => {
      if (!wrapRef.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("keydown", onKeyDown);
    document.addEventListener("mousedown", onPointerDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      document.removeEventListener("mousedown", onPointerDown);
    };
  }, [open]);

  const signOut = async () => {
    setOpen(false);
    await logout();
    toast("ok", "Signed out");
    navigate("/", { replace: true });
  };

  const label = me.email ?? me.user_id;

  return (
    <div className="topbar-cell topbar-user" ref={wrapRef} style={{ position: "relative" }}>
      <Button
        variant="ghost"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((current) => !current)}
      >
        <span className="small">{label}</span>
        <RoleBadge role={me.role ?? "member"} />
      </Button>
      {open && (
        <div className="menu-pop" role="menu" aria-label="Account">
          <div className="menu-head">Signed in as {label}</div>
          <Link role="menuitem" to="/settings/appearance" onClick={() => setOpen(false)}>
            Appearance
          </Link>
          <button role="menuitem" type="button" onClick={() => void signOut()}>
            Sign out
          </button>
        </div>
      )}
    </div>
  );
}
```

- [ ] **Step 6: 实现 TopBar.tsx 与 SubNav.tsx**

创建 `web/src/app/TopBar.tsx`：

```tsx
import { NavLink, useLocation } from "react-router-dom";
import type { HumanMe } from "../api/types";
import { hasTeams } from "../lib/teams";
import { TeamSwitcher } from "../components/TeamSwitcher";
import { Button } from "../components/Button";
import { navSections, sectionForPath } from "./navModel";
import { UserMenu } from "./UserMenu";

export function TopBar({ me, onOpenPalette }: { me: HumanMe; onOpenPalette: () => void }) {
  const location = useLocation();
  const sections = navSections(me);
  const active = sectionForPath(sections, location.pathname);

  return (
    <div className="topbar">
      <div className="topbar-brand">PAX Nexus</div>
      {hasTeams(me) && (
        <div className="topbar-cell">
          <TeamSwitcher me={me} collapsed={false} />
        </div>
      )}
      <nav className="topbar-nav" aria-label="Sections">
        {sections.map((section) => (
          <NavLink
            key={section.id}
            to={section.to}
            aria-current={section.id === active?.id ? "page" : undefined}
          >
            {section.label}
          </NavLink>
        ))}
      </nav>
      <Button
        className="topbar-cell topbar-search"
        variant="ghost"
        onClick={onOpenPalette}
      >
        <span>Search agents, notes, actions…</span>
        <span className="topbar-kbd">⌘K</span>
      </Button>
      <UserMenu me={me} />
    </div>
  );
}
```

创建 `web/src/app/SubNav.tsx`：

```tsx
import { NavLink } from "react-router-dom";
import type { NavItem } from "./navModel";

/** 二级导航条。分区没有子页面时（Overview）整条不渲染。 */
export function SubNav({ items }: { items: NavItem[] }) {
  if (items.length === 0) return null;
  return (
    <nav className="subnav" aria-label="Section pages">
      {items.map((item) => (
        <NavLink
          key={item.to}
          to={item.to}
          end={item.to.split("/").length <= 2}
          className={({ isActive }) => (isActive ? "active" : "")}
          aria-current={undefined}
        >
          {item.label}
        </NavLink>
      ))}
    </nav>
  );
}
```

> 注意：`NavLink` 在匹配时会自动写 `aria-current="page"`，上面显式传 `undefined` 是为了不覆盖它——保留这一行以免有人误以为漏了。

- [ ] **Step 7: 实现 AppShell.tsx**

创建 `web/src/app/AppShell.tsx`：

```tsx
import { useState, type ReactNode } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import type { HumanMe } from "../api/types";
import { ErrorBoundary } from "../components/ErrorBoundary";
import { CommandPalette } from "../components/CommandPalette";
import { navSections, sectionForPath } from "./navModel";
import { SubNav } from "./SubNav";
import { TopBar } from "./TopBar";

/**
 * 外壳：顶栏 + 二级导航 + 内容区。路由本身在 app/routes.tsx，这里只负责框。
 *
 * 内容区的 ErrorBoundary 用 pathname + 当前 team 作 key：一个路由崩了不会带走
 * 顶栏，换路由自动恢复；切换团队时重挂载，让每个视图对新团队重新取数。
 */
export function AppShell({ me, children }: { me: HumanMe; children: ReactNode }) {
  const location = useLocation();
  const navigate = useNavigate();
  const [paletteOpen, setPaletteOpen] = useState(false);
  const sections = navSections(me);
  const active = sectionForPath(sections, location.pathname);

  return (
    <div className="app-shell">
      <TopBar me={me} onOpenPalette={() => setPaletteOpen(true)} />
      <SubNav items={active?.items ?? []} />
      <main className="page">
        <ErrorBoundary
          key={`${location.pathname}:${me.current_team_id ?? ""}`}
          region="route"
          escapeLabel="Back to Management"
          onEscape={() => navigate("/management")}
        >
          {children}
        </ErrorBoundary>
      </main>
      <CommandPalette
        me={me}
        open={paletteOpen}
        onOpenChange={setPaletteOpen}
        sections={sections}
      />
    </div>
  );
}
```

`CommandPalette` 的完整实现在 Task 8。本 Task 先创建占位实现让类型通过——
创建 `web/src/components/CommandPalette.tsx`：

```tsx
// 占位实现：只固定 props 契约，让 AppShell 在 Task 8 之前就能通过类型检查。
// Task 8 用完整实现整体替换本文件。

import type { HumanMe } from "../api/types";
import type { NavSection } from "../app/navModel";

export function CommandPalette({
  open,
  onOpenChange,
}: {
  me: HumanMe;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  sections: NavSection[];
}) {
  if (!open) return null;
  return (
    <div
      className="dialog-backdrop"
      onClick={() => onOpenChange(false)}
      role="dialog"
      aria-modal="true"
      aria-label="Command palette"
    />
  );
}
```

`me` 与 `sections` 在占位实现里未使用。`tsconfig` 开了 `noUnusedParameters`，
但解构里未使用的属性不受该规则约束（它只管函数形参），所以上面这样写能通过；
不要为了消除告警而删掉这两个 props，Task 8 需要它们。

- [ ] **Step 8: 运行测试确认通过**

Run: `cd web && npx vitest run tests/app-shell.dom.test.tsx`
Expected: 失败——因为 `App.tsx` 还在渲染 `PortalShell`。**这是预期的**：本 Task 只交付外壳组件，接线在 Task 9。先把测试文件标记为跳过以保持 CI 绿：把 `describe(` 改为 `describe.skip(`，并在文件顶部加注释 `// Task 9 接线后移除 .skip`。

- [ ] **Step 9: 验证类型与构建**

Run: `cd web && npm run build`
Expected: 构建成功。

- [ ] **Step 10: 提交**

```bash
git add web/src/app web/src/components/CommandPalette.tsx web/src/styles/layout.css \
        web/src/styles/index.css web/tests/app-shell.dom.test.tsx
git commit -m "feat(web): AppShell with top-bar navigation and user menu"
```

---

## Task 6: 路由表与旧路由重定向

**Files:**
- Create: `web/src/app/legacyRoutes.ts`
- Create: `web/src/app/LegacyRedirect.tsx`
- Create: `web/tests/legacy-routes.test.ts`

**Interfaces:**
- Consumes: 无（纯数据 + 一个路由组件）
- Produces:
  - `export interface LegacyRoute { from: string; to: string }`
  - `export const LEGACY_ROUTES: LegacyRoute[]`
  - `export function resolveLegacy(pathname: string, search: string): string | undefined`
  - `export function LegacyRedirect()` —— 一个读取当前 location 并 `<Navigate replace>` 的组件

- [ ] **Step 1: 写失败的测试**

创建 `web/tests/legacy-routes.test.ts`：

```ts
// 旧书签必须继续可用。重定向表是数据，这里逐条遍历断言 —— 漏一条就红。
import { describe, expect, it } from "vitest";
import { LEGACY_ROUTES, resolveLegacy } from "../src/app/legacyRoutes";

describe("legacy route table", () => {
  it("covers every pre-redesign route", () => {
    const froms = LEGACY_ROUTES.map((r) => r.from).sort();
    expect(froms).toEqual(
      [
        "/agents",
        "/agents/:agentId",
        "/admin/agents",
        "/admin/agents/:agentId",
        "/admin/audit",
        "/admin/devices",
        "/admin/devices/:credentialId",
        "/admin/explorer",
        "/admin/explorer/notes/:noteId",
        "/admin/invitations",
        "/admin/members",
        "/admin/operations",
        "/admin/pulse",
        "/admin/session-audit",
        "/apps",
        "/team",
        "/todo",
        "/wiki",
        "/wiki/browse",
      ].sort(),
    );
  });

  it("never redirects to another legacy path", () => {
    const froms = new Set(LEGACY_ROUTES.map((r) => r.from));
    for (const route of LEGACY_ROUTES) {
      expect(froms.has(route.to)).toBe(false);
    }
  });

  it.each([
    ["/agents", "", "/management"],
    ["/admin/members", "", "/management/members"],
    ["/admin/pulse", "", "/overview"],
    ["/admin/session-audit", "", "/governance/sessions"],
    ["/admin/operations", "", "/governance/pipeline"],
    ["/admin/explorer", "", "/governance/memory"],
    ["/apps", "", "/apps/wiki"],
    ["/todo", "", "/apps/todos"],
    ["/wiki", "", "/settings/memory"],
  ])("resolves %s%s to %s", (pathname, search, expected) => {
    expect(resolveLegacy(pathname, search)).toBe(expected);
  });

  it("carries path parameters through", () => {
    expect(resolveLegacy("/agents/agent-1", "")).toBe("/management/agents/agent-1");
    expect(resolveLegacy("/admin/devices/dev_01", "")).toBe("/management/devices/dev_01");
    expect(resolveLegacy("/admin/explorer/notes/note_01", "")).toBe(
      "/governance/memory/note_01",
    );
  });

  // 旧 wiki 深链把 slug 放在 query 里，新路由把它放进 path；revision 仍留在 query。
  it("moves the wiki page slug from query into the path", () => {
    expect(resolveLegacy("/wiki/browse", "?page=lake-retention")).toBe(
      "/apps/wiki/lake-retention",
    );
    expect(resolveLegacy("/wiki/browse", "?page=lake-retention&revision=r2")).toBe(
      "/apps/wiki/lake-retention?revision=r2",
    );
    expect(resolveLegacy("/wiki/browse", "")).toBe("/apps/wiki");
  });

  // /wiki?page=… 曾是更早的 wiki 深链形态，也要落到阅读器而不是设置页。
  it("routes the oldest wiki deep link to the reader, not to settings", () => {
    expect(resolveLegacy("/wiki", "?page=lake-retention")).toBe("/apps/wiki/lake-retention");
    expect(resolveLegacy("/wiki", "")).toBe("/settings/memory");
  });

  it("returns undefined for a path that was never legacy", () => {
    expect(resolveLegacy("/management", "")).toBeUndefined();
    expect(resolveLegacy("/join", "")).toBeUndefined();
  });
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npx vitest run tests/legacy-routes.test.ts`
Expected: FAIL —— 找不到模块 `../src/app/legacyRoutes`。

- [ ] **Step 3: 实现 legacyRoutes.ts**

创建 `web/src/app/legacyRoutes.ts`：

```ts
// 重定向前 IA 的全部路由。表是数据，由 tests/legacy-routes.test.ts 逐条遍历，
// 新增或改动路由时必须同步这里，否则旧书签会 404。

export interface LegacyRoute {
  /** react-router 形式的旧路径，:param 会被原样搬到新路径的同名占位符上。 */
  from: string;
  to: string;
}

export const LEGACY_ROUTES: LegacyRoute[] = [
  { from: "/agents", to: "/management" },
  { from: "/agents/:agentId", to: "/management/agents/:agentId" },
  { from: "/admin/members", to: "/management/members" },
  { from: "/admin/invitations", to: "/management/invitations" },
  { from: "/admin/agents", to: "/management/agents" },
  { from: "/admin/agents/:agentId", to: "/management/agents/:agentId" },
  { from: "/admin/devices", to: "/management/devices" },
  { from: "/admin/devices/:credentialId", to: "/management/devices/:credentialId" },
  { from: "/admin/audit", to: "/governance/audit" },
  { from: "/admin/session-audit", to: "/governance/sessions" },
  { from: "/admin/operations", to: "/governance/pipeline" },
  { from: "/admin/pulse", to: "/overview" },
  { from: "/admin/explorer", to: "/governance/memory" },
  { from: "/admin/explorer/notes/:noteId", to: "/governance/memory/:noteId" },
  { from: "/apps", to: "/apps/wiki" },
  { from: "/wiki/browse", to: "/apps/wiki" },
  { from: "/wiki", to: "/settings/memory" },
  { from: "/todo", to: "/apps/todos" },
  { from: "/team", to: "/settings/team" },
];

/** 把 "/agents/:agentId" 这类模式编译成一个匹配器。 */
function matchPattern(pattern: string, pathname: string): Record<string, string> | undefined {
  const patternParts = pattern.split("/");
  const pathParts = pathname.split("/");
  if (patternParts.length !== pathParts.length) return undefined;
  const params: Record<string, string> = {};
  for (let i = 0; i < patternParts.length; i += 1) {
    const expected = patternParts[i];
    if (expected.startsWith(":")) {
      if (pathParts[i] === "") return undefined;
      params[expected.slice(1)] = decodeURIComponent(pathParts[i]);
      continue;
    }
    if (expected !== pathParts[i]) return undefined;
  }
  return params;
}

function fill(template: string, params: Record<string, string>): string {
  return template
    .split("/")
    .map((part) => (part.startsWith(":") ? encodeURIComponent(params[part.slice(1)]) : part))
    .join("/");
}

/**
 * 旧 wiki 深链把页面 slug 放在 `?page=`，新阅读器把它放进 path。
 * `?revision=` 仍是 query，原样保留。`/wiki?page=…` 是更早的形态，
 * 也要落到阅读器而不是设置页。
 */
function resolveWikiDeepLink(search: string): string | undefined {
  const slug = new URLSearchParams(search).get("page");
  if (!slug) return undefined;
  const revision = new URLSearchParams(search).get("revision");
  const base = `/apps/wiki/${encodeURIComponent(slug)}`;
  return revision ? `${base}?revision=${encodeURIComponent(revision)}` : base;
}

/** 旧路径 → 新路径；不是旧路径则返回 undefined。 */
export function resolveLegacy(pathname: string, search: string): string | undefined {
  if (pathname === "/wiki" || pathname === "/wiki/browse") {
    const deepLink = resolveWikiDeepLink(search);
    if (deepLink) return deepLink;
  }
  for (const route of LEGACY_ROUTES) {
    const params = matchPattern(route.from, pathname);
    if (params) return fill(route.to, params);
  }
  return undefined;
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd web && npx vitest run tests/legacy-routes.test.ts`
Expected: PASS。

- [ ] **Step 5: 实现 LegacyRedirect.tsx**

创建 `web/src/app/LegacyRedirect.tsx`：

```tsx
import { Navigate, useLocation } from "react-router-dom";
import { resolveLegacy } from "./legacyRoutes";

/**
 * 挂在每条旧路径上：把当前 location 翻译成新路径并 replace 跳转。
 * 表里查不到（理论上不会发生）就落到 /management，不留白屏。
 */
export function LegacyRedirect() {
  const location = useLocation();
  const target = resolveLegacy(location.pathname, location.search) ?? "/management";
  return <Navigate to={target} replace />;
}
```

- [ ] **Step 6: 提交**

```bash
git add web/src/app/legacyRoutes.ts web/src/app/LegacyRedirect.tsx web/tests/legacy-routes.test.ts
git commit -m "feat(web): legacy route redirect table"
```

---

## Task 7: 新增基础组件

**Files:**
- Create: `web/src/components/Card.tsx`、`Tag.tsx`、`Seg.tsx`、`Field.tsx`、`DataTable.tsx`、`MetricTile.tsx`、`Kicker.tsx`、`Crumbs.tsx`、`EmptyState.tsx`
- Create: `web/tests/ui-components.dom.test.tsx`

**Interfaces:**
- Consumes: Task 3 的组件类系统
- Produces（后续阶段全部依赖这些签名）：
  - `Card({ kicker?, title?, meta?, className?, children })`
  - `Tag({ tone?: "attention" | "neutral" | "outline", children })`
  - `Seg<T extends string>({ label, options, value, onChange })`
  - `Field({ label, htmlFor, hint?, error?, children })`
  - `DataTable<T>({ caption, columns, rows, rowKey, empty })`，`columns: { key: string; header: string; render: (row: T) => ReactNode }[]`
  - `MetricTile({ label, value, unit?, note? })`
  - `Kicker({ children })`
  - `Crumbs({ items: { label: string; to?: string }[] })`
  - `EmptyState({ mark?, title, body?, action? })`

- [ ] **Step 1: 写失败的测试**

创建 `web/tests/ui-components.dom.test.tsx`：

```tsx
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { Card } from "../src/components/Card";
import { Crumbs } from "../src/components/Crumbs";
import { DataTable } from "../src/components/DataTable";
import { EmptyState } from "../src/components/EmptyState";
import { Field } from "../src/components/Field";
import { MetricTile } from "../src/components/MetricTile";
import { Seg } from "../src/components/Seg";
import { Tag } from "../src/components/Tag";

afterEach(cleanup);

describe("Card", () => {
  it("renders kicker, title, meta and children", () => {
    render(
      <Card kicker="Governance" title="Pipeline health" meta={<span>updated 2m ago</span>}>
        <p>body</p>
      </Card>,
    );
    screen.getByText("Governance");
    screen.getByText("Pipeline health");
    screen.getByText("updated 2m ago");
    screen.getByText("body");
  });

  it("omits optional slots entirely", () => {
    const { container } = render(<Card>only body</Card>);
    expect(container.querySelector(".card-kicker")).toBeNull();
    expect(container.querySelector(".card-title")).toBeNull();
  });
});

describe("Tag", () => {
  it("defaults to the neutral tone", () => {
    render(<Tag>active</Tag>);
    expect(screen.getByText("active").className).toBe("tag tag-neutral");
  });

  it("renders the attention tone", () => {
    render(<Tag tone="attention">suspended</Tag>);
    expect(screen.getByText("suspended").className).toBe("tag tag-attention");
  });
});

describe("Seg", () => {
  it("marks the selected option and reports changes", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <Seg
        label="Time window"
        options={[
          { value: "1h", label: "1h" },
          { value: "24h", label: "24h" },
        ]}
        value="1h"
        onChange={onChange}
      />,
    );
    const group = screen.getByRole("group", { name: "Time window" });
    expect(within(group).getByRole("button", { name: "1h" }).getAttribute("aria-pressed")).toBe(
      "true",
    );
    await user.click(within(group).getByRole("button", { name: "24h" }));
    expect(onChange).toHaveBeenCalledWith("24h");
  });
});

describe("Field", () => {
  it("associates the label with the control and exposes hint and error", () => {
    render(
      <Field label="Device name" htmlFor="dev-name" hint="Shown in the access tree" error="Required">
        <input id="dev-name" />
      </Field>,
    );
    expect(screen.getByLabelText("Device name")).toBeTruthy();
    screen.getByText("Shown in the access tree");
    screen.getByText("Required");
  });
});

describe("DataTable", () => {
  const columns = [
    { key: "name", header: "Name", render: (row: { name: string }) => row.name },
  ];

  it("renders headers and rows", () => {
    render(
      <DataTable
        caption="Agents"
        columns={columns}
        rows={[{ name: "codex-planner" }]}
        rowKey={(row) => row.name}
        empty="No agents"
      />,
    );
    screen.getByRole("columnheader", { name: "Name" });
    screen.getByRole("cell", { name: "codex-planner" });
  });

  it("renders the empty message instead of an empty table body", () => {
    render(
      <DataTable
        caption="Agents"
        columns={columns}
        rows={[]}
        rowKey={(row) => row.name}
        empty="No agents"
      />,
    );
    screen.getByText("No agents");
    expect(screen.queryByRole("cell")).toBeNull();
  });
});

describe("MetricTile", () => {
  it("renders label, value, unit and note", () => {
    render(<MetricTile label="Time to remember" value="1.9" unit="s" note="p95" />);
    screen.getByText("Time to remember");
    screen.getByText("1.9");
    screen.getByText("s");
    screen.getByText("p95");
  });
});

describe("Crumbs", () => {
  it("links every item except the last", () => {
    render(
      <MemoryRouter>
        <Crumbs
          items={[
            { label: "Access tree", to: "/management" },
            { label: "mac-studio-01" },
          ]}
        />
      </MemoryRouter>,
    );
    screen.getByRole("link", { name: "Access tree" });
    expect(screen.queryByRole("link", { name: "mac-studio-01" })).toBeNull();
    expect(screen.getByText("mac-studio-01").getAttribute("aria-current")).toBe("page");
  });
});

describe("EmptyState", () => {
  it("renders title, body and action", () => {
    render(
      <EmptyState mark="W" title="No pages yet" body="Pages appear after ingestion." action={<button>Refresh</button>} />,
    );
    screen.getByRole("heading", { name: "No pages yet" });
    screen.getByText("Pages appear after ingestion.");
    screen.getByRole("button", { name: "Refresh" });
  });
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npx vitest run tests/ui-components.dom.test.tsx`
Expected: FAIL —— 全部模块不存在。

- [ ] **Step 3: 实现九个组件**

创建 `web/src/components/Card.tsx`：

```tsx
import type { ReactNode } from "react";

/** Modernist 卡片：kicker / title / meta 三个可选槽 + 主体。 */
export function Card({
  kicker,
  title,
  meta,
  className,
  children,
}: {
  kicker?: ReactNode;
  title?: ReactNode;
  meta?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={className ? `card ${className}` : "card"}>
      {kicker !== undefined && <span className="card-kicker">{kicker}</span>}
      {title !== undefined && <span className="card-title">{title}</span>}
      <div className="card-body">{children}</div>
      {meta !== undefined && <div className="card-meta">{meta}</div>}
    </div>
  );
}
```

创建 `web/src/components/Tag.tsx`：

```tsx
import type { ReactNode } from "react";

export type TagTone = "attention" | "neutral" | "outline";

/** 两色制标签：attention = 需要注意，neutral = 常态，outline = 强调但非告警。 */
export function Tag({
  tone = "neutral",
  title,
  children,
}: {
  tone?: TagTone;
  title?: string;
  children: ReactNode;
}) {
  return (
    <span className={`tag tag-${tone}`} title={title}>
      {children}
    </span>
  );
}
```

创建 `web/src/components/Seg.tsx`：

```tsx
/** 单选预设切换。与 .tabs 不同：.seg 用于「同一数据的不同取值」。 */
export function Seg<T extends string>({
  label,
  options,
  value,
  onChange,
}: {
  label: string;
  options: { value: T; label: string }[];
  value: T;
  onChange: (value: T) => void;
}) {
  return (
    <div className="seg" role="group" aria-label={label}>
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          className={option.value === value ? "on" : ""}
          aria-pressed={option.value === value}
          onClick={() => onChange(option.value)}
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}
```

创建 `web/src/components/Field.tsx`：

```tsx
import type { ReactNode } from "react";

/** 表单字段包装：标签、可选提示、可选错误。控件由调用方传入并自带 id。 */
export function Field({
  label,
  htmlFor,
  hint,
  error,
  children,
}: {
  label: string;
  htmlFor: string;
  hint?: string;
  error?: string;
  children: ReactNode;
}) {
  return (
    <div className="field">
      <label htmlFor={htmlFor}>{label}</label>
      {children}
      {hint !== undefined && <p className="small muted flush">{hint}</p>}
      {error !== undefined && <p className="note bad flush">{error}</p>}
    </div>
  );
}
```

创建 `web/src/components/DataTable.tsx`：

```tsx
import type { ReactNode } from "react";

export interface Column<T> {
  key: string;
  header: string;
  render: (row: T) => ReactNode;
}

/**
 * 表格：空态渲染成一句话而不是空的 tbody —— 空表格在 Modernist 的
 * 2px 表头下看起来像加载失败。
 */
export function DataTable<T>({
  caption,
  columns,
  rows,
  rowKey,
  empty,
}: {
  caption: string;
  columns: Column<T>[];
  rows: T[];
  rowKey: (row: T) => string;
  empty: string;
}) {
  if (rows.length === 0) return <p className="muted small">{empty}</p>;
  return (
    <table className="table">
      <caption className="sr-only">{caption}</caption>
      <thead>
        <tr>
          {columns.map((column) => (
            <th key={column.key} scope="col">
              {column.header}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {rows.map((row) => (
          <tr key={rowKey(row)}>
            {columns.map((column) => (
              <td key={column.key}>{column.render(row)}</td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  );
}
```

创建 `web/src/components/MetricTile.tsx`：

```tsx
import type { ReactNode } from "react";

/** Overview 指标格：大数字 + 小单位 + 注解。 */
export function MetricTile({
  label,
  value,
  unit,
  note,
}: {
  label: string;
  value: ReactNode;
  unit?: string;
  note?: ReactNode;
}) {
  return (
    <div className="metric-tile">
      <div className="card-kicker">{label}</div>
      <div className="metric-value">
        <span className="metric-number">{value}</span>
        {unit !== undefined && <span className="metric-unit">{unit}</span>}
      </div>
      {note !== undefined && <div className="small muted">{note}</div>}
    </div>
  );
}
```

创建 `web/src/components/Kicker.tsx`：

```tsx
import type { ReactNode } from "react";

/** 小号大写导语，用在页面标题上方。 */
export function Kicker({ children }: { children: ReactNode }) {
  return <span className="kicker">{children}</span>;
}
```

创建 `web/src/components/Crumbs.tsx`：

```tsx
import { Link } from "react-router-dom";

/** 面包屑：除最后一项外都是链接，最后一项带 aria-current。 */
export function Crumbs({ items }: { items: { label: string; to?: string }[] }) {
  return (
    <nav className="crumbs" aria-label="Breadcrumb">
      {items.map((item, index) => {
        const last = index === items.length - 1;
        return (
          <span key={item.label}>
            {index > 0 && <span className="crumb-sep"> / </span>}
            {last || item.to === undefined ? (
              <span aria-current="page">{item.label}</span>
            ) : (
              <Link to={item.to}>{item.label}</Link>
            )}
          </span>
        );
      })}
    </nav>
  );
}
```

创建 `web/src/components/EmptyState.tsx`：

```tsx
import type { ReactNode } from "react";

/** 正向空态：不是错误、不是加载中，而是「这里目前就是空的」。 */
export function EmptyState({
  mark,
  title,
  body,
  action,
}: {
  mark?: string;
  title: string;
  body?: string;
  action?: ReactNode;
}) {
  return (
    <section className="empty-state">
      {mark !== undefined && (
        <span className="empty-mark" aria-hidden="true">
          {mark}
        </span>
      )}
      <h2>{title}</h2>
      {body !== undefined && <p className="muted">{body}</p>}
      {action}
    </section>
  );
}
```

- [ ] **Step 4: 补上这些组件用到的样式**

在 `web/src/styles/components.css` 末尾追加：

```css
/* — 新增组件 — */
.metric-tile { display: flex; flex-direction: column; gap: 4px; padding: var(--space-3); border-right: 1px solid var(--color-divider); }
.metric-tile:last-child { border-right: 0; }
.metric-value { display: flex; align-items: baseline; gap: 4px; }
.metric-number { font-family: var(--font-heading); font-weight: var(--font-heading-weight); font-size: 34px; line-height: 1; }
.metric-unit { font-size: 14px; opacity: 0.6; }

.crumbs { font-size: 13px; margin-bottom: var(--space-2); }
.crumbs a { color: inherit; }
.crumb-sep { opacity: 0.4; }

.empty-state { display: flex; flex-direction: column; align-items: center; gap: var(--space-2); padding: var(--space-8) var(--space-4); text-align: center; }
.empty-mark {
  font-family: var(--font-heading); font-weight: var(--font-heading-weight); font-size: 42px;
  width: 72px; height: 72px; display: grid; place-items: center;
  background: var(--color-accent); color: var(--color-bg);
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `cd web && npx vitest run tests/ui-components.dom.test.tsx`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add web/src/components/Card.tsx web/src/components/Tag.tsx web/src/components/Seg.tsx \
        web/src/components/Field.tsx web/src/components/DataTable.tsx \
        web/src/components/MetricTile.tsx web/src/components/Kicker.tsx \
        web/src/components/Crumbs.tsx web/src/components/EmptyState.tsx \
        web/src/styles/components.css web/tests/ui-components.dom.test.tsx
git commit -m "feat(web): Modernist base components"
```

---

## Task 8: ⌘K 命令面板

**Files:**
- Modify: `web/src/components/CommandPalette.tsx`（替换 Task 5 的占位实现）
- Create: `web/tests/command-palette.dom.test.tsx`
- Modify: `web/src/styles/components.css`

**Interfaces:**
- Consumes: Task 4 的 `NavSection`；`api/queries.ts` 的 `listAdminAgents` / `listMyAgents`；`api/wiki.ts` 的 `searchWiki`；`lib/capabilities.ts` 的 `can`
- Produces: `CommandPalette({ me, open, onOpenChange, sections })`

- [ ] **Step 1: 写失败的测试**

创建 `web/tests/command-palette.dom.test.tsx`：

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { cleanup } from "@testing-library/react";
import { CommandPalette } from "../src/components/CommandPalette";
import { navSections } from "../src/app/navModel";
import { jsonResponse, makeMe, stubFetch } from "./helpers";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function renderPalette(onOpenChange = vi.fn()) {
  const me = makeMe({ role: "admin" });
  render(
    <MemoryRouter>
      <CommandPalette me={me} open onOpenChange={onOpenChange} sections={navSections(me)} />
    </MemoryRouter>,
  );
  return { user: userEvent.setup(), onOpenChange };
}

describe("CommandPalette", () => {
  it("lists navigation actions filtered by the query", async () => {
    stubFetch(() => jsonResponse({ agents: [] }));
    const { user } = renderPalette();

    await user.type(screen.getByRole("combobox", { name: "Search" }), "devices");
    await waitFor(() => screen.getByRole("option", { name: /Devices/ }));
    expect(screen.queryByRole("option", { name: /Audit trail/ })).toBeNull();
  });

  it("closes on Escape", async () => {
    stubFetch(() => jsonResponse({ agents: [] }));
    const { user, onOpenChange } = renderPalette();

    await user.keyboard("{Escape}");
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("moves the active option with the arrow keys", async () => {
    stubFetch(() => jsonResponse({ agents: [] }));
    const { user } = renderPalette();

    const input = screen.getByRole("combobox", { name: "Search" });
    await user.type(input, "a");
    await waitFor(() => expect(screen.getAllByRole("option").length).toBeGreaterThan(1));

    const first = screen.getAllByRole("option")[0];
    expect(first.getAttribute("aria-selected")).toBe("true");
    await user.keyboard("{ArrowDown}");
    expect(screen.getAllByRole("option")[1].getAttribute("aria-selected")).toBe("true");
  });

  it("surfaces agents returned by the search endpoint", async () => {
    stubFetch((path) => {
      if (path.startsWith("/v1/admin/agents")) {
        return jsonResponse({ agents: [{ agent_id: "codex-planner", display_name: "Codex Planner" }] });
      }
      if (path.startsWith("/v1/wiki/search")) return jsonResponse({ results: [] });
      throw new Error(`unexpected fetch: ${path}`);
    });
    const { user } = renderPalette();

    await user.type(screen.getByRole("combobox", { name: "Search" }), "codex");
    await waitFor(() => screen.getByRole("option", { name: /Codex Planner/ }));
  });

  // 搜索失败不能打断使用：静默降级成只剩导航动作，不弹错误。
  it("falls back to navigation actions when remote search fails", async () => {
    stubFetch((path) => {
      if (path.startsWith("/v1/admin/agents")) return jsonResponse({ code: "boom", message: "x" }, 500);
      if (path.startsWith("/v1/wiki/search")) return jsonResponse({ code: "boom", message: "x" }, 500);
      throw new Error(`unexpected fetch: ${path}`);
    });
    const { user } = renderPalette();

    await user.type(screen.getByRole("combobox", { name: "Search" }), "members");
    await waitFor(() => screen.getByRole("option", { name: /Members/ }));
    expect(screen.queryByText(/failed/i)).toBeNull();
  });
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npx vitest run tests/command-palette.dom.test.tsx`
Expected: FAIL —— 占位实现渲染的是空 dialog，没有 combobox。

- [ ] **Step 3: 实现 CommandPalette.tsx**

用下面内容替换 `web/src/components/CommandPalette.tsx`：

```tsx
// ⌘K 命令面板。三路来源：本地导航动作（永远可用）、Agent、Wiki 页面。
// 远端两路失败时静默降级为只剩导航动作 —— 搜索框里冒错误提示比搜不到更打断人。

import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import type { HumanMe } from "../api/types";
import type { NavSection } from "../app/navModel";
import { can } from "../lib/capabilities";
import { listAdminAgents, listMyAgents } from "../api/queries";
import { searchWiki } from "../api/wiki";

interface Entry {
  id: string;
  group: string;
  label: string;
  hint?: string;
  to: string;
}

const DEBOUNCE_MS = 200;

function navigationEntries(sections: NavSection[]): Entry[] {
  const entries: Entry[] = [];
  for (const section of sections) {
    if (section.items.length === 0) {
      entries.push({
        id: `nav:${section.to}`,
        group: "Go to",
        label: section.label,
        to: section.to,
      });
      continue;
    }
    for (const item of section.items) {
      entries.push({
        id: `nav:${item.to}`,
        group: "Go to",
        label: item.label,
        hint: section.label,
        to: item.to,
      });
    }
  }
  return entries;
}

export function CommandPalette({
  me,
  open,
  onOpenChange,
  sections,
}: {
  me: HumanMe;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  sections: NavSection[];
}) {
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const [remote, setRemote] = useState<Entry[]>([]);
  const [active, setActive] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  const navEntries = useMemo(() => navigationEntries(sections), [sections]);

  // 打开时清空上一轮状态并聚焦输入框。
  useEffect(() => {
    if (!open) return;
    setQuery("");
    setRemote([]);
    setActive(0);
    inputRef.current?.focus();
  }, [open]);

  // 远端检索：debounce + AbortController，两路各自独立降级。
  useEffect(() => {
    if (!open) return;
    const trimmed = query.trim();
    if (trimmed.length < 2) {
      setRemote([]);
      return;
    }
    const controller = new AbortController();
    const timer = setTimeout(() => {
      const agents = can(me.role, "view.all-agents")
        ? listAdminAgents({ q: trimmed, limit: 5 })
        : listMyAgents({ limit: 50 });
      void Promise.allSettled([agents, searchWiki(trimmed, controller.signal)]).then(
        ([agentResult, wikiResult]) => {
          if (controller.signal.aborted) return;
          const entries: Entry[] = [];
          if (agentResult.status === "fulfilled") {
            for (const agent of agentResult.value.items.slice(0, 5)) {
              entries.push({
                id: `agent:${agent.agent_id}`,
                group: "Agents",
                label: agent.display_name,
                hint: agent.agent_id,
                to: `/management/agents/${encodeURIComponent(agent.agent_id)}`,
              });
            }
          }
          if (wikiResult.status === "fulfilled") {
            for (const result of wikiResult.value.slice(0, 5)) {
              entries.push({
                id: `wiki:${result.page.id}:${result.section_key}`,
                group: "Wiki",
                label: result.page.title,
                hint: result.section_key,
                to: `/apps/wiki/${encodeURIComponent(result.page.slug)}`,
              });
            }
          }
          setRemote(entries);
        },
      );
    }, DEBOUNCE_MS);
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [open, query, me.role]);

  const results = useMemo(() => {
    const trimmed = query.trim().toLowerCase();
    const filteredNav = trimmed
      ? navEntries.filter((entry) => entry.label.toLowerCase().includes(trimmed))
      : navEntries;
    return [...filteredNav, ...remote];
  }, [navEntries, remote, query]);

  // 结果集变化后把选中项夹回有效范围。
  useEffect(() => {
    setActive((current) => (current >= results.length ? 0 : current));
  }, [results.length]);

  if (!open) return null;

  const go = (entry: Entry) => {
    onOpenChange(false);
    navigate(entry.to);
  };

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === "Escape") {
      event.preventDefault();
      onOpenChange(false);
      return;
    }
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setActive((current) => Math.min(current + 1, results.length - 1));
      return;
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      setActive((current) => Math.max(current - 1, 0));
      return;
    }
    if (event.key === "Enter" && results[active]) {
      event.preventDefault();
      go(results[active]);
    }
  };

  return (
    <div
      className="dialog-backdrop"
      onClick={(event) => {
        if (event.target === event.currentTarget) onOpenChange(false);
      }}
    >
      <div className="palette" role="dialog" aria-modal="true" aria-label="Command palette">
        <input
          ref={inputRef}
          className="palette-input"
          role="combobox"
          aria-label="Search"
          aria-expanded="true"
          aria-controls="palette-results"
          aria-autocomplete="list"
          placeholder="Search agents, notes, actions…"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={onKeyDown}
        />
        <ul className="palette-results" id="palette-results" role="listbox" aria-label="Results">
          {results.map((entry, index) => (
            <li
              key={entry.id}
              role="option"
              aria-selected={index === active}
              className={index === active ? "on" : ""}
              onMouseEnter={() => setActive(index)}
              onClick={() => go(entry)}
            >
              <span className="palette-group">{entry.group}</span>
              <span className="palette-label">{entry.label}</span>
              {entry.hint !== undefined && <span className="palette-hint">{entry.hint}</span>}
            </li>
          ))}
        </ul>
        <div className="palette-foot small muted">↑↓ move · ↵ open · esc close</div>
      </div>
    </div>
  );
}
```

- [ ] **Step 4: 加面板样式**

在 `web/src/styles/components.css` 末尾追加：

```css
/* — command palette — */
.palette {
  width: min(560px, 100%); background: var(--color-bg);
  border: 2px solid var(--color-divider); box-shadow: var(--shadow-lg);
  align-self: start; margin-top: 12vh;
}
.palette-input {
  border: 0; border-bottom: 2px solid var(--color-divider); background: transparent;
  min-height: 48px; font-size: 16px; border-radius: 0;
}
.palette-results { list-style: none; margin: 0; padding: 0; max-height: 50vh; overflow-y: auto; }
.palette-results li {
  display: flex; align-items: baseline; gap: 10px; padding: 9px 14px; cursor: pointer;
  border-bottom: 1px solid var(--color-divider);
}
.palette-results li.on { background: var(--color-accent); color: var(--color-bg); }
.palette-group { font-size: 10px; letter-spacing: 0.1em; text-transform: uppercase; opacity: 0.55; min-width: 56px; }
.palette-label { flex: 1; font-size: 14px; }
.palette-hint { font-family: var(--font-mono); font-size: 11px; opacity: 0.6; }
.palette-foot { padding: 8px 14px; border-top: 1px solid var(--color-divider); }
```

- [ ] **Step 5: 在 AppShell 接上 ⌘K 快捷键**

修改 `web/src/app/AppShell.tsx`，在 `const [paletteOpen, setPaletteOpen] = useState(false);` 之后插入：

```tsx
  // ⌘K / Ctrl-K 全局开关；Escape 由面板自己处理。
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setPaletteOpen((current) => !current);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);
```

并把顶部 import 改为 `import { useEffect, useState, type ReactNode } from "react";`。

- [ ] **Step 6: 运行测试确认通过**

Run: `cd web && npx vitest run tests/command-palette.dom.test.tsx`
Expected: PASS，5 个用例全绿。

- [ ] **Step 7: 提交**

```bash
git add web/src/components/CommandPalette.tsx web/src/app/AppShell.tsx \
        web/src/styles/components.css web/tests/command-palette.dom.test.tsx
git commit -m "feat(web): command palette with navigation, agent and wiki search"
```

---

## Task 9: 接线——新路由挂载现有页面，删除侧边栏

**Files:**
- Create: `web/src/app/routes.tsx`
- Create: `web/src/pages/settings/AppearancePage.tsx`
- Modify: `web/src/App.tsx`
- Delete: `web/src/pages/PortalShell.tsx`、`web/src/pages/AppsPage.tsx`
- Delete: `web/tests/portal-nav-groups.dom.test.tsx`、`web/tests/portal-side-collapse.dom.test.tsx`、`web/tests/apps.dom.test.tsx`
- Modify: `web/tests/theme.dom.test.tsx`、`web/tests/app-shell.dom.test.tsx`
- Modify: `web/src/styles/index.css`，移动 feature CSS 到 `styles/features/`

**Interfaces:**
- Consumes: Task 4 `navModel`、Task 5 `AppShell`、Task 6 `LegacyRedirect`
- Produces: `export function PortalRoutes({ me }: { me: HumanMe })` —— 渲染新路由树，内部页面组件本阶段沿用现有实现

- [ ] **Step 1: 建最小 Appearance 页**

主题控件原本挂在侧边栏底部，侧边栏即将删除，必须先有落点。
创建 `web/src/pages/settings/AppearancePage.tsx`：

```tsx
import { Kicker } from "../../components/Kicker";
import { THEMES, THEME_LABELS, useTheme, type Theme } from "../../lib/theme";

/**
 * 主题选择。阶段 6 会按设计稿做成带预览的卡片墙；本阶段只保证主题控件
 * 在侧边栏删除后仍有落点，行为与原来的下拉完全一致。
 */
export function AppearancePage() {
  const [theme, setTheme] = useTheme();

  return (
    <>
      <div className="page-head">
        <div>
          <Kicker>Settings · appearance</Kicker>
          <h1>Appearance</h1>
          <p className="muted flush">
            Applies to your account on this device only. Nobody else on the team sees your choice.
          </p>
        </div>
      </div>
      <div className="seg" role="group" aria-label="Theme">
        {THEMES.map((value) => (
          <button
            key={value}
            type="button"
            className={value === theme ? "on" : ""}
            aria-pressed={value === theme}
            onClick={() => setTheme(value as Theme)}
          >
            {THEME_LABELS[value]}
          </button>
        ))}
      </div>
    </>
  );
}
```

- [ ] **Step 2: 写 routes.tsx**

创建 `web/src/app/routes.tsx`。**本阶段页面组件全部沿用现有实现**，只是挂到新路径；
`/overview` 暂由 `AdminPulsePage` 顶替（阶段 2 换掉），`/management` 暂按角色顶替（阶段 3 换掉）：

```tsx
import { Navigate, Route, Routes } from "react-router-dom";
import type { HumanMe } from "../api/types";
import { can, hasServerCapability, type Capability } from "../lib/capabilities";
import { hasTeams } from "../lib/teams";
import { LegacyRedirect } from "./LegacyRedirect";
import { LEGACY_ROUTES } from "./legacyRoutes";
import { landingPath } from "./navModel";

import { MyAgentsPage } from "../pages/MyAgentsPage";
import { AgentDetailPage } from "../pages/AgentDetailPage";
import { AdminAgentsPage } from "../pages/AdminAgentsPage";
import { AdminAgentDetailPage } from "../pages/AdminAgentDetailPage";
import { AdminMembersPage } from "../pages/AdminMembersPage";
import { AdminInvitationsPage } from "../pages/AdminInvitationsPage";
import { AdminDevicesPage } from "../pages/AdminDevicesPage";
import { AdminDeviceDetailPage } from "../pages/AdminDeviceDetailPage";
import { AdminAuditPage } from "../pages/AdminAuditPage";
import { AdminSessionAuditPage } from "../pages/AdminSessionAuditPage";
import { AdminOperationsPage } from "../pages/AdminOperationsPage";
import { AdminPulsePage } from "../pages/AdminPulsePage";
import { AdminExplorerPage } from "../pages/AdminExplorerPage";
import { AdminTeamNoteDetailPage } from "../pages/AdminTeamNoteDetailPage";
import { WikiBrowsePage } from "../pages/WikiBrowsePage";
import { TodoPage } from "../pages/TodoPage";
import { WikiStatusPage } from "../pages/WikiStatusPage";
import { TeamSettingsPage } from "../pages/TeamSettingsPage";
import { AppearancePage } from "../pages/settings/AppearancePage";
import { OnboardingPage } from "../pages/OnboardingPage";

/** 角色矩阵门控；无权限一律回落地页，页面不挂载 = 不发请求。 */
function RequireRole({
  me,
  cap,
  children,
}: {
  me: HumanMe;
  cap: Capability;
  children: JSX.Element;
}) {
  if (!can(me.role, cap)) return <Navigate to={landingPath(me)} replace />;
  return children;
}

/** 服务端能力门控（Operations / Explorer）。 */
function RequireCapability({
  me,
  capability,
  children,
}: {
  me: HumanMe;
  capability: string;
  children: JSX.Element;
}) {
  if (!hasServerCapability(me, capability)) return <Navigate to={landingPath(me)} replace />;
  return children;
}

export function PortalRoutes({ me }: { me: HumanMe }) {
  const adminLike = can(me.role, "view.members");

  return (
    <Routes>
      {/* 旧路由：整表挂重定向，逐条由 tests/legacy-routes.test.ts 覆盖。 */}
      {LEGACY_ROUTES.map((route) => (
        <Route key={route.from} path={route.from} element={<LegacyRedirect />} />
      ))}

      <Route path="/onboarding" element={<OnboardingPage />} />

      {/* 阶段 2 用真正的 Overview 替换。 */}
      <Route
        path="/overview"
        element={
          <RequireCapability me={me} capability="view.operations">
            <AdminPulsePage />
          </RequireCapability>
        }
      />

      {/* 阶段 3 用 AccessTree 替换；现在按角色顶替。 */}
      <Route path="/management" element={adminLike ? <AdminAgentsPage me={me} /> : <MyAgentsPage />} />
      <Route
        path="/management/members"
        element={
          <RequireRole me={me} cap="view.members">
            <AdminMembersPage me={me} />
          </RequireRole>
        }
      />
      <Route
        path="/management/invitations"
        element={
          <RequireRole me={me} cap="invite.member">
            <AdminInvitationsPage me={me} />
          </RequireRole>
        }
      />
      <Route
        path="/management/agents"
        element={
          <RequireRole me={me} cap="view.all-agents">
            <AdminAgentsPage me={me} />
          </RequireRole>
        }
      />
      {/* 阶段 4 合并成一个 scope 自适应页面；现在按角色分派。 */}
      <Route
        path="/management/agents/:agentId"
        element={adminLike ? <AdminAgentDetailPage me={me} /> : <AgentDetailPage />}
      />
      <Route
        path="/management/devices"
        element={
          <RequireRole me={me} cap="view.devices">
            <AdminDevicesPage />
          </RequireRole>
        }
      />
      <Route
        path="/management/devices/:credentialId"
        element={
          <RequireRole me={me} cap="view.devices">
            <AdminDeviceDetailPage />
          </RequireRole>
        }
      />

      <Route
        path="/governance/audit"
        element={
          <RequireRole me={me} cap="view.audit">
            <AdminAuditPage />
          </RequireRole>
        }
      />
      <Route
        path="/governance/sessions"
        element={
          <RequireRole me={me} cap="view.audit">
            <AdminSessionAuditPage />
          </RequireRole>
        }
      />
      <Route
        path="/governance/pipeline"
        element={
          <RequireCapability me={me} capability="view.operations">
            <AdminOperationsPage
              canInspectTeamMemory={hasServerCapability(me, "view.team-memory")}
            />
          </RequireCapability>
        }
      />
      <Route
        path="/governance/memory"
        element={
          <RequireCapability me={me} capability="view.team-memory">
            <AdminExplorerPage />
          </RequireCapability>
        }
      />
      <Route
        path="/governance/memory/:noteId"
        element={
          <RequireCapability me={me} capability="view.team-memory">
            <AdminTeamNoteDetailPage />
          </RequireCapability>
        }
      />

      <Route path="/apps/wiki" element={<WikiBrowsePage />} />
      <Route path="/apps/wiki/:slug" element={<WikiBrowsePage />} />
      <Route path="/apps/todos" element={<TodoPage />} />

      {hasTeams(me) && <Route path="/settings/team" element={<TeamSettingsPage me={me} />} />}
      <Route path="/settings/memory" element={<WikiStatusPage me={me} />} />
      {/* 阶段 6 把 LLM 用量从 WikiStatusPage 拆出来独立成页。 */}
      <Route path="/settings/usage" element={<WikiStatusPage me={me} />} />
      <Route path="/settings/appearance" element={<AppearancePage />} />

      <Route path="*" element={<Navigate to={landingPath(me)} replace />} />
    </Routes>
  );
}
```

- [ ] **Step 3: 改 App.tsx**

用下面内容**整体替换** `web/src/App.tsx`。相对旧版的变化只有三处：`PortalShell` 换成
`AppShell` + `PortalRoutes`；`/wiki/browse` 与 `/todo` 两条全屏路由删除（它们现在是
壳内的 `/apps/wiki` 与 `/apps/todos`）；相应的 import 增删。**其余逻辑一字不改**——
continuation 重定向与 auth 分叉是身份文档第 4 节的契约。

```tsx
import { useEffect, useRef } from "react";
import { BrowserRouter, Route, Routes, useNavigate } from "react-router-dom";
import { AuthProvider, useAuth } from "./auth/AuthContext";
import { ErrorBoundary } from "./components/ErrorBoundary";
import { ToastProvider } from "./components/Toasts";
import { peekPendingInvitation, takeReturnUrl } from "./lib/continuations";
import { LoginPage } from "./pages/LoginPage";
import { NotConfiguredPage } from "./pages/NotConfiguredPage";
import { BootstrapPage } from "./pages/BootstrapPage";
import { JoinPage } from "./pages/JoinPage";
import { EntryPage } from "./pages/EntryPage";
import { OnboardingPage } from "./pages/OnboardingPage";
import { SuspendedPage } from "./pages/SuspendedPage";
import { AppShell } from "./app/AppShell";
import { PortalRoutes } from "./app/routes";

/**
 * After the OIDC round trip the backend always lands on the fixed
 * TEAM_MEMORY_PORTAL_URL. Restore the continuation exactly once: a pending
 * invitation wins over a plain return_url, and the two never mix (doc 4).
 */
function ContinuationRedirect() {
  const { state } = useAuth();
  const navigate = useNavigate();
  const done = useRef(false);

  useEffect(() => {
    if (done.current) return;
    if (state.kind !== "active" && state.kind !== "no-membership" && state.kind !== "suspended") {
      return;
    }
    done.current = true;
    if (peekPendingInvitation()) {
      navigate("/join", { replace: true });
      return;
    }
    const target = takeReturnUrl();
    const here = window.location.pathname + window.location.search;
    if (target && target !== here) navigate(target, { replace: true });
  }, [state.kind, navigate]);

  return null;
}

function AppRoutes() {
  const { state } = useAuth();

  switch (state.kind) {
    case "loading":
      return (
        <div className="center-page">
          <p className="muted">Loading…</p>
        </div>
      );
    case "not-configured":
      return <NotConfiguredPage />;
  }

  return (
    <Routes>
      {/* /join must stay reachable while unauthenticated and while the user
          has no membership yet; the page branches on auth state itself. */}
      <Route path="/join" element={<JoinPage />} />
      {state.kind === "unauthenticated" && <Route path="*" element={<LoginPage />} />}
      {state.kind === "no-membership" && state.profile === "onprem" && (
        <>
          <Route path="/bootstrap" element={<BootstrapPage />} />
          <Route path="*" element={<EntryPage />} />
        </>
      )}
      {state.kind === "no-membership" && state.profile === "saas" && (
        <Route path="*" element={<OnboardingPage />} />
      )}
      {state.kind === "suspended" && <Route path="*" element={<SuspendedPage />} />}
      {state.kind === "active" && (
        <Route
          path="*"
          element={
            <AppShell me={state.me}>
              <PortalRoutes me={state.me} />
            </AppShell>
          }
        />
      )}
    </Routes>
  );
}

export default function App() {
  // Outermost boundary: even a shell-level render failure leaves a safe
  // recovery page instead of a blank document (narrower boundaries live in
  // AppShell and Modal).
  return (
    <ErrorBoundary region="app" fullPage>
      <ToastProvider>
        <AuthProvider>
          <BrowserRouter>
            <ContinuationRedirect />
            <AppRoutes />
          </BrowserRouter>
        </AuthProvider>
      </ToastProvider>
    </ErrorBoundary>
  );
}
```

- [ ] **Step 4: 删除侧边栏与启动器**

```bash
git rm web/src/pages/PortalShell.tsx web/src/pages/AppsPage.tsx
git rm web/tests/portal-nav-groups.dom.test.tsx web/tests/portal-side-collapse.dom.test.tsx \
       web/tests/apps.dom.test.tsx
```

被删的三个测试文件覆盖的是已移除的能力（侧边栏分组、侧边栏折叠、`/apps` 启动器）；
其等价覆盖分别在 `tests/navModel.test.ts`（可见性）与 `tests/app-shell.dom.test.tsx`（渲染）。

- [ ] **Step 5: 移动 feature CSS 并更新 index.css**

```bash
mkdir -p web/src/styles/features
git mv web/src/styles/apps.css web/src/styles/features/apps.css
git mv web/src/styles/operations.css web/src/styles/features/operations.css
git mv web/src/styles/wiki.css web/src/styles/features/wiki.css
git mv web/src/styles/pulse.css web/src/styles/features/pulse.css
git mv web/src/styles/session-audit.css web/src/styles/features/session-audit.css
git mv web/src/styles/teams.css web/src/styles/features/teams.css
```

把 `web/src/styles/index.css` 整体替换为：

```css
/* 全局样式入口。按层导入：token → 主题 → 基础 → 组件 → 布局 → 特性。
   新的特性样式请在 features/ 下新建文件，不要往已有文件追加。 */
@import "./tokens.css";
@import "./themes.css";
@import "./base.css";
@import "./components.css";
@import "./layout.css";
@import "./features/apps.css";
@import "./features/operations.css";
@import "./features/wiki.css";
@import "./features/pulse.css";
@import "./features/session-audit.css";
@import "./features/teams.css";
```

- [ ] **Step 6: 启用 app-shell 测试**

把 `web/tests/app-shell.dom.test.tsx` 里的 `describe.skip(` 改回 `describe(`，并删除文件顶部
那行 `// Task 9 接线后移除 .skip` 注释。

- [ ] **Step 7: 重写 theme 测试**

用下面内容替换 `web/tests/theme.dom.test.tsx`：

```tsx
// 主题控件从侧边栏迁到 /settings/appearance；持久化与 data-theme 的行为不变。
import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

function appearanceFetch(path: string, init: RequestInit): Response {
  if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
  if (path.startsWith("/v1/teams")) return jsonResponse({ teams: [] });
  throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
}

describe("Appearance", () => {
  it("defaults to beige with no data-theme attribute", async () => {
    await renderApp({
      route: "/settings/appearance",
      me: makeMe({ role: "member" }),
      fetch: appearanceFetch,
    });

    await screen.findByRole("heading", { name: "Appearance" });
    expect(document.documentElement.dataset.theme).toBeUndefined();
  });

  it("applies and persists a non-default theme", async () => {
    const { user } = await renderApp({
      route: "/settings/appearance",
      me: makeMe({ role: "member" }),
      fetch: appearanceFetch,
    });

    await user.click(await screen.findByRole("button", { name: "Dark" }));

    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(localStorage.getItem("portal-theme")).toBe("dark");
  });

  it("is reachable from the user menu", async () => {
    const { user } = await renderApp({
      route: "/management",
      me: makeMe({ role: "member" }),
      fetch: appearanceFetch,
    });

    await user.click(screen.getByRole("button", { name: /alice@example\.com/ }));
    await user.click(screen.getByRole("menuitem", { name: "Appearance" }));

    await screen.findByRole("heading", { name: "Appearance" });
  });
});
```

- [ ] **Step 8: 跑全量测试并修复受影响的用例**

Run: `cd web && npm test`

Expected: 多个 `*.dom.test.tsx` 会因为 `route:` 用了旧路径而失败（例如 `route: "/admin/members"`）。
逐个把它们的 `route` 改成新路径——**改路径，不要改断言**。对照表：

| 旧 route | 新 route |
|---|---|
| `/agents` | `/management` |
| `/agents/:id` | `/management/agents/:id` |
| `/admin/members` | `/management/members` |
| `/admin/invitations` | `/management/invitations` |
| `/admin/agents` | `/management/agents` |
| `/admin/devices` | `/management/devices` |
| `/admin/audit` | `/governance/audit` |
| `/admin/session-audit` | `/governance/sessions` |
| `/admin/operations` | `/governance/pipeline` |
| `/admin/pulse` | `/overview` |
| `/admin/explorer` | `/governance/memory` |
| `/wiki/browse` | `/apps/wiki` |
| `/wiki` | `/settings/memory` |
| `/todo` | `/apps/todos` |
| `/team` | `/settings/team` |

`tests/a11y-controls.dom.test.tsx` 若断言了侧边栏元素，把那部分改为断言顶栏
（`role="navigation"` name `Sections`）。

反复运行直到全绿。

- [ ] **Step 9: 验证类型与构建**

Run: `cd web && npm run build`
Expected: 成功。若 `noUnusedLocals` 报 `AppsPage` 等未使用 import，删掉对应 import。

- [ ] **Step 10: 手工验收旧链接**

```bash
cd web && npm run dev
```

在浏览器依次访问并确认 URL 被 replace 成新路径：`/agents`、`/admin/members`、`/admin/pulse`、
`/wiki/browse?page=any-slug`、`/todo`。

- [ ] **Step 11: 提交**

```bash
git add -A web/src web/tests
git commit -m "refactor(web): mount pages on the new IA and remove the sidebar shell"
```

---

## Task 10: 响应式

**Files:**
- Modify: `web/src/styles/layout.css`
- Create: `web/src/app/TopBarMenu.tsx`
- Modify: `web/src/app/TopBar.tsx`
- Create: `web/tests/responsive.dom.test.tsx`

**Interfaces:**
- Consumes: Task 4 `navSections`、Task 5 的顶栏结构
- Produces: `TopBarMenu({ sections, activeId })` —— 窄屏下代替横向顶栏的汉堡菜单

- [ ] **Step 1: 写失败的测试**

创建 `web/tests/responsive.dom.test.tsx`：

```tsx
// jsdom 不做布局，所以断言的是「窄屏下渲染哪套 DOM」，而不是像素。
// matchMedia 在 jsdom 里不存在，这里按测试需要打桩。
import { screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

function stubMatchMedia(matches: boolean): void {
  vi.stubGlobal(
    "matchMedia",
    (query: string) =>
      ({
        matches,
        media: query,
        onchange: null,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
      }) as unknown as MediaQueryList,
  );
}

function shellFetch(path: string, init: RequestInit): Response {
  if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
  if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [] });
  if (path.startsWith("/v1/teams")) return jsonResponse({ teams: [] });
  throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
}

describe("responsive top bar", () => {
  it("renders the inline section nav on wide viewports", async () => {
    stubMatchMedia(false);
    await renderApp({ route: "/management", me: makeMe({ role: "admin" }), fetch: shellFetch });

    screen.getByRole("navigation", { name: "Sections" });
    expect(screen.queryByRole("button", { name: "Menu" })).toBeNull();
  });

  it("collapses the section nav into a menu on narrow viewports", async () => {
    stubMatchMedia(true);
    const { user } = await renderApp({
      route: "/management",
      me: makeMe({ role: "admin" }),
      fetch: shellFetch,
    });

    expect(screen.queryByRole("navigation", { name: "Sections" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Menu" }));
    const menu = screen.getByRole("menu", { name: "Sections" });
    expect(menu).toBeTruthy();
  });
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npx vitest run tests/responsive.dom.test.tsx`
Expected: FAIL —— 窄屏下仍渲染横向导航，找不到 `Menu` 按钮。

- [ ] **Step 3: 实现 TopBarMenu.tsx**

创建 `web/src/app/TopBarMenu.tsx`：

```tsx
import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "../components/Button";
import type { NavSection } from "./navModel";

/** 窄屏下的分区菜单。宽屏由 TopBar 直接渲染横向导航，不挂载本组件。 */
export function TopBarMenu({
  sections,
  activeId,
}: {
  sections: NavSection[];
  activeId?: string;
}) {
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    const onPointerDown = (event: MouseEvent) => {
      if (!wrapRef.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("keydown", onKeyDown);
    document.addEventListener("mousedown", onPointerDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      document.removeEventListener("mousedown", onPointerDown);
    };
  }, [open]);

  return (
    <div className="topbar-cell" ref={wrapRef} style={{ position: "relative" }}>
      <Button
        variant="ghost"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label="Menu"
        onClick={() => setOpen((current) => !current)}
      >
        ☰
      </Button>
      {open && (
        <div className="menu-pop" role="menu" aria-label="Sections">
          {sections.map((section) => (
            <Link
              key={section.id}
              role="menuitem"
              to={section.to}
              aria-current={section.id === activeId ? "page" : undefined}
              onClick={() => setOpen(false)}
            >
              {section.label}
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
```

- [ ] **Step 4: 在 TopBar 里按视口分叉**

修改 `web/src/app/TopBar.tsx`：顶部补一个 `useMediaQuery` 内联 hook 与 import，
把 `<nav className="topbar-nav">…</nav>` 替换为条件渲染。

在文件顶部 import 之后加入：

```tsx
import { useEffect, useState } from "react";
import { TopBarMenu } from "./TopBarMenu";

const NARROW = "(max-width: 900px)";

/** 视口查询。matchMedia 缺失（老环境或测试未打桩）时按宽屏处理。 */
function useNarrow(): boolean {
  const [narrow, setNarrow] = useState(
    () => typeof matchMedia === "function" && matchMedia(NARROW).matches,
  );
  useEffect(() => {
    if (typeof matchMedia !== "function") return;
    const query = matchMedia(NARROW);
    const onChange = () => setNarrow(query.matches);
    query.addEventListener("change", onChange);
    return () => query.removeEventListener("change", onChange);
  }, []);
  return narrow;
}
```

在组件体内取 `const narrow = useNarrow();`，并把导航部分改为：

```tsx
      {narrow ? (
        <TopBarMenu sections={sections} activeId={active?.id} />
      ) : (
        <nav className="topbar-nav" aria-label="Sections">
          {sections.map((section) => (
            <NavLink
              key={section.id}
              to={section.to}
              aria-current={section.id === active?.id ? "page" : undefined}
            >
              {section.label}
            </NavLink>
          ))}
        </nav>
      )}
```

- [ ] **Step 5: 加断点样式**

在 `web/src/styles/layout.css` 末尾追加：

```css
/* — 响应式 —
   1280 以上是设计基准；以下逐级收窄。宽内容（表格、图表）由各自的容器
   横向滚动，页面主体在任何断点都不允许出现横向滚动。 */
@media (max-width: 1280px) {
  .page { padding: 22px 20px 80px; }
}

@media (max-width: 900px) {
  .topbar-brand { min-width: 0; font-size: 18px; padding: 10px 14px; }
  .topbar-search { min-width: 0; }
  .topbar-search span:first-child { display: none; }
  .topbar-user .small { display: none; }
  .subnav { top: 53px; }
}

@media (max-width: 640px) {
  .page { padding: 16px 12px 64px; }
  .page-head { flex-direction: column; align-items: flex-start; gap: var(--space-2); }
  h1 { font-size: 30px; }
  h2 { font-size: 24px; }
  .field-row { grid-template-columns: 1fr; }
  .metric-tile { border-right: 0; border-bottom: 1px solid var(--color-divider); }
}

/* 表格在窄屏自己滚，页面主体不横向溢出。 */
.table-scroll { overflow-x: auto; }
@media (max-width: 900px) {
  .card > table, .card > .table { display: block; overflow-x: auto; }
}
```

- [ ] **Step 6: 运行测试确认通过**

Run: `cd web && npx vitest run tests/responsive.dom.test.tsx`
Expected: PASS。

- [ ] **Step 7: 跑全量测试**

Run: `cd web && npm test`
Expected: 全绿。若 `app-shell.dom.test.tsx` 因 `matchMedia` 未定义而失败，
在 `tests/helpers.tsx` 的 `resetBrowserState()` 里加一行默认桩：

```ts
  if (typeof matchMedia !== "function") {
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }));
  }
```

- [ ] **Step 8: 手工验收三个断点**

```bash
cd web && npm run dev
```

在浏览器开发者工具里依次设为 1440 / 1024 / 768 / 390 宽，逐个查看
`/management`、`/governance/audit`、`/apps/wiki`，确认：页面主体没有横向滚动条；
900 以下顶栏变成 `☰` 菜单；表格自己横向滚动。三个主题各看一遍。

- [ ] **Step 9: 更新 AGENTS.md**

修改 `AGENTS.md` §Web frontend：

1. 把 `styles/` 那条改为：「全局样式在 `web/src/styles/`，按层组织：`tokens.css`（设计 token 单一真源）→ `themes.css` → `base.css` → `components.css` → `layout.css` → `features/*.css`。新的特性样式在 `features/` 下新建文件。」
2. **删除**「Full-screen apps (wiki browse, todos) render outside `PortalShell`…」整条——全屏逃逸模式已移除，Wiki 与 Todos 在壳内。
3. 在按钮那条之后补一条：「状态徽标一律通过 `components/Badge.tsx` 或 `components/Tag.tsx` 渲染。设计系统是两色制：accent 表示需要注意/主行动/危险，neutral 表示常态；不要引入新的色相。」

- [ ] **Step 10: 提交**

```bash
git add web/src/styles/layout.css web/src/app/TopBar.tsx web/src/app/TopBarMenu.tsx \
        web/tests/responsive.dom.test.tsx web/tests/helpers.tsx AGENTS.md
git commit -m "feat(web): responsive top bar and layout breakpoints"
```

---

## 阶段 1 完成标准

- [ ] `cd web && npm test` 全绿
- [ ] `cd web && npm run build` 成功
- [ ] 三个主题（beige / dark / arcade）各手工过一遍，无硬编码颜色残留
- [ ] `LEGACY_ROUTES` 里每条旧 URL 在浏览器中都能 replace 到新 URL
- [ ] owner / admin / member 三种角色的顶栏项与落地页符合 `navModel` 的定义
- [ ] 1440 / 1024 / 768 / 390 四个宽度下页面主体无横向滚动
- [ ] `AGENTS.md` §Web frontend 与实际结构一致
- [ ] 红线 checklist 逐条勾选（见本文 Global Constraints）
