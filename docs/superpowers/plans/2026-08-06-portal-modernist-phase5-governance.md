# Modernist Portal 阶段 5 · Governance 四屏 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Governance 分区的四屏按 Modernist 重画，并把 Memory explorer 改成双栏、右栏展示完整的六段溯源链。

**Architecture:** 四屏各自独立重画，互不依赖。溯源链由一个纯函数 `buildProvenance(detail)` 从**单次** `getTeamNote` 的响应派生——`ExplorerRevision` 每项自带 `extraction` / `evidence` / `candidate` / `deliveries`，顶层还有 `recall_observations`，六段齐全，零额外请求。Pipeline health 的三个 region hook 一个不改，只换呈现层，以免把一个已验收的隔离属性重新变成未验收。

**Tech Stack:** React 18 + TypeScript + react-router-dom；vitest + @testing-library/react；纯 CSS。

## Global Constraints

- **纯前端，零后端改动。** diff 只允许出现在 `web/` 与 `docs/` 下。
- 不引入任何新 npm 依赖。
- 按钮一律走 `web/src/components/Button.tsx`，禁止 `.btn.ghost` 这类**点分**写法（`<Link className="btn btn-ghost">` 是仓库既有约定，允许）。
- 样式只引用 `--color-*` / `--space-*` / `--font-*` / `--ceremony-*`；**禁止** `web/src/styles/tokens.css` 第二个 `:root` 块里的兼容别名（`--bg` `--muted` `--accent` `--text` `--border` `--surface` `--mono` 等）。
- 间距刻度只有 `--space-1/2/3/4/6/8`，**没有 `--space-5` 和 `--space-7`**。用了不存在的变量不报错、只静默失效。
- 新特性样式写进**新建**的 `web/src/styles/features/governance.css`，并在 `web/src/styles/index.css` 末尾追加 `@import`。
- 组件级测试若间接依赖 `AuthContext`（用了 `useErrorHandler` 就会），测试要包一层真的 `AuthProvider` 并桩掉它挂载时的 `GET /v1/me`——照 `web/tests/agent-identity.dom.test.tsx` 开头的写法。
- 提交用 `git commit -- <明确路径>` 的 **pathspec 形式**，`git add` 也只加明确路径。
- 提交信息用中文。

---

## 文件结构

**新建**

| 文件 | 职责 |
|---|---|
| `web/src/pages/governance/provenance.ts` | 从 `TeamNoteDetail` 派生六段链的纯函数 |
| `web/src/pages/governance/AuditRow.tsx` | 审计单行 + 就地展开详情 |
| `web/src/pages/governance/SessionFindings.tsx` | Findings 行卡视图 |
| `web/src/pages/governance/SessionDays.tsx` | 按天柱状图 |
| `web/src/pages/governance/PipelineMetrics.tsx` | 顶部六格指标条 |
| `web/src/pages/governance/NoteList.tsx` | Explorer 左栏 |
| `web/src/pages/governance/NoteProvenance.tsx` | 六段溯源链 |
| `web/src/pages/governance/NoteRecalls.tsx` | 召回决策表 |
| `web/src/styles/features/governance.css` | 本阶段特性样式 |

**重写**：`AdminAuditPage.tsx`、`AdminSessionAuditPage.tsx`、`AdminOperationsPage.tsx`、`AdminExplorerPage.tsx`
**删除**：`AdminTeamNoteDetailPage.tsx`
**不动**：`web/src/pages/operations/` 下的九个组件与 `hooks.ts`

**测试改动面**：新增 `governance-provenance.test.ts`、`governance-explorer.dom.test.tsx`、`governance-pipeline.dom.test.tsx`；改写既有的 `audit.dom.test.tsx`、`session-audit.dom.test.tsx`、`operations*.dom.test.tsx`、`explorer*.dom.test.tsx`（实现者需先 `ls web/tests | grep -iE "audit|operation|explorer|session"` 摸清实际文件名与数量，**不要相信这里的猜测**）。

---

## Task 1: 溯源链纯函数

**Files:**
- Create: `web/src/pages/governance/provenance.ts`
- Test: `web/tests/governance-provenance.test.ts`

**Interfaces:**
- Consumes: `TeamNoteDetail`、`ExplorerRevision`、`ExplorerRecallUse` from `web/src/api/types.ts`
- Produces:
  ```ts
  export type ProvenanceStage = "source" | "extraction" | "candidate" | "revision" | "delivery";
  export interface ProvenanceStep {
    stage: ProvenanceStage;
    /** 阶段名的中文标签，直接渲染 */
    label: string;
    title: string;
    body: string;
    /** mono 小字的引用；无引用时为 undefined */
    ref?: string;
    /** 这一段没有记录 */
    missing: boolean;
  }
  export interface ProvenanceRevision {
    revision: number;
    createdAt: string;
    steps: ProvenanceStep[];   // 恒为 5 段，顺序固定
  }
  export function buildProvenance(detail: TeamNoteDetail): ProvenanceRevision[];
  export function describeRecall(use: ExplorerRecallUse): string;
  ```

**为什么是 5 段不是 6：** 前五段（源事件 / 抽取 / 候选 / 版本 / 投递）挂在**每个版本**上，第六段召回决策挂在**笔记**上，由 `NoteRecalls` 单独渲染。`buildProvenance` 只负责前五段。

- [ ] **Step 1: 写失败测试**

`web/tests/governance-provenance.test.ts`：

```ts
// 溯源链的派生。这是阶段 5 的核心不变量：六段链条完全由一次 getTeamNote
// 的响应构成，且任何一段缺失都只让那一段显示「没有记录」，不拖垮整条链。
import { describe, expect, it } from "vitest";
import { buildProvenance, describeRecall } from "../src/pages/governance/provenance";
import type {
  ExplorerRevision,
  TeamNoteDetail,
  TeamNoteSummary,
} from "../src/api/types";

function summary(overrides: Partial<TeamNoteSummary> = {}): TeamNoteSummary {
  return {
    note_id: "note_01", kind: "decision", subject: "用 Postgres 存 evidence",
    state: "live", origin_agent_id: "alice-codex", audience_agent_ids: [],
    revision: 2, created_at: "2026-08-01T00:00:00Z", updated_at: "2026-08-02T00:00:00Z",
    soft_expires_at: "2026-09-01T00:00:00Z", hard_expires_at: "2026-10-01T00:00:00Z",
    ...overrides,
  };
}

function revision(overrides: Partial<ExplorerRevision> = {}): ExplorerRevision {
  return {
    revision: 2, candidate_id: "cand_02", operation: "update",
    body: "决定用 Postgres 存 evidence，理由是……", related_subjects: [],
    created_at: "2026-08-02T00:00:00Z",
    extraction: {
      run_id: "run_02", user_id: "usr_01", agent_id: "alice-codex",
      session_id: "sess_09", from_sequence: 10, to_sequence: 42,
      model: "deepseek-v4", prompt_version: "p7", status: "completed",
      input_tokens: 1200, output_tokens: 300, created_at: "2026-08-02T00:00:00Z",
    },
    evidence: [
      { event_id: "evt_a", user_id: "usr_01", agent_id: "alice-codex", session_id: "sess_09",
        sequence: 10, type: "message", content: "…", visibility: "team",
        occurred_at: "2026-08-02T00:00:00Z", captured_at: "2026-08-02T00:00:01Z" },
      { event_id: "evt_b", user_id: "usr_01", agent_id: "alice-codex", session_id: "sess_09",
        sequence: 11, type: "message", content: "…", visibility: "team",
        occurred_at: "2026-08-02T00:00:02Z", captured_at: "2026-08-02T00:00:03Z" },
    ],
    deliveries: [],
    candidate: {
      candidate_id: "cand_02", action: "update", kind: "decision",
      subject: "用 Postgres 存 evidence", body: "…", origin_agent_id: "alice-codex",
      evidence_event_ids: ["evt_a", "evt_b"], admission_status: "admitted",
      created_at: "2026-08-02T00:00:00Z", resulting_note_id: "note_01",
    },
    ...overrides,
  };
}

function detail(revisions: ExplorerRevision[]): TeamNoteDetail {
  return {
    note: {
      summary: summary(), body: "…", origin_user_id: "usr_01",
      origin_session_id: "sess_09", related_subjects: [],
    },
    related_notes: [], revisions, recall_observations: [],
  };
}

describe("buildProvenance", () => {
  it("每个版本产出固定顺序的五段", () => {
    const [rev] = buildProvenance(detail([revision()]));
    expect(rev.revision).toBe(2);
    expect(rev.steps.map((s) => s.stage)).toEqual([
      "source", "extraction", "candidate", "revision", "delivery",
    ]);
    expect(rev.steps.every((s) => s.missing)).toBe(false);
  });

  it("源事件段带上条数与 session、引用是事件 id", () => {
    const [rev] = buildProvenance(detail([revision()]));
    const source = rev.steps[0];
    expect(source.missing).toBe(false);
    expect(source.title).toContain("2");
    expect(source.body).toContain("sess_09");
    expect(source.ref).toContain("evt_a");
  });

  it("抽取段带模型与 prompt 版本，引用是 run_id", () => {
    const [rev] = buildProvenance(detail([revision()]));
    const extraction = rev.steps[1];
    expect(extraction.title).toContain("deepseek-v4");
    expect(extraction.title).toContain("p7");
    expect(extraction.ref).toBe("run_02");
  });

  it("候选被拒时把拒绝理由写进说明", () => {
    const rejected = revision({
      candidate: {
        candidate_id: "cand_03", action: "create", kind: "fact", subject: "x", body: "y",
        origin_agent_id: "alice-codex", evidence_event_ids: [],
        admission_status: "rejected", rejection_reason: "duplicate_of_note_01",
        created_at: "2026-08-02T00:00:00Z",
      },
    });
    const [rev] = buildProvenance(detail([rejected]));
    expect(rev.steps[2].body).toContain("duplicate_of_note_01");
  });

  it("投递为空时该段标记 missing，其余段不受影响", () => {
    const [rev] = buildProvenance(detail([revision({ deliveries: [] })]));
    expect(rev.steps[4].missing).toBe(true);
    expect(rev.steps[4].body).toContain("没有");
    expect(rev.steps.slice(0, 4).every((s) => s.missing)).toBe(false);
  });

  it("源事件为空时只有那一段 missing", () => {
    const [rev] = buildProvenance(detail([revision({ evidence: [] })]));
    expect(rev.steps[0].missing).toBe(true);
    expect(rev.steps[1].missing).toBe(false);
  });

  it("多版本按 revision 逆序，最新在前", () => {
    const chain = buildProvenance(
      detail([revision({ revision: 1 }), revision({ revision: 3 }), revision({ revision: 2 })]),
    );
    expect(chain.map((r) => r.revision)).toEqual([3, 2, 1]);
  });

  it("revisions 为空时返回空数组（调用方渲染正向空态）", () => {
    expect(buildProvenance(detail([]))).toEqual([]);
  });
});

describe("describeRecall", () => {
  it("投递成功且无拒因时说命中并投递", () => {
    expect(
      describeRecall({
        observation_id: 1, recipient_agent_id: "bob-claude", recipient_session_id: "sess_10",
        occurred_at: "2026-08-03T00:00:00Z", delivered: true,
        rejection_reasons: [], budget_drop_reasons: [], hard_gate_failures: [],
      }),
    ).toContain("命中并投递");
  });

  it("把三类原因拼成一句话", () => {
    const text = describeRecall({
      observation_id: 2, recipient_agent_id: "bob-claude", recipient_session_id: "sess_10",
      occurred_at: "2026-08-03T00:00:00Z", delivered: false,
      rejection_reasons: ["low_similarity"], budget_drop_reasons: ["token_budget"],
      hard_gate_failures: ["audience_mismatch"],
    });
    expect(text).toContain("low_similarity");
    expect(text).toContain("token_budget");
    expect(text).toContain("audience_mismatch");
  });

  it("未投递且三类原因都空时不谎报原因", () => {
    const text = describeRecall({
      observation_id: 3, recipient_agent_id: "bob-claude", recipient_session_id: "sess_10",
      occurred_at: "2026-08-03T00:00:00Z", delivered: false,
      rejection_reasons: [], budget_drop_reasons: [], hard_gate_failures: [],
    });
    expect(text).not.toContain("命中并投递");
    expect(text).toMatch(/没有记录|未说明/);
  });
});
```

- [ ] **Step 2: 跑测试确认红**

Run: `npm --prefix web test -- governance-provenance`
Expected: FAIL —— 找不到模块

- [ ] **Step 3: 实现**

`web/src/pages/governance/provenance.ts`。要点：

- 五段的 `stage` / `label` 固定：`source`「源事件」、`extraction`「抽取」、`candidate`「候选」、`revision`「版本」、`delivery`「投递」
- 每段的 `missing` 判定：源事件看 `evidence.length === 0`；投递看 `deliveries.length === 0`；抽取与候选是必有对象，但 `status === ""` 之类的空值也要当 missing 处理
- `ref` 多条时写成 `evt_a 等 2 条`
- **`missing` 为真时 `body` 必须包含「没有」二字**（测试依赖）
- `describeRecall`：`delivered && 三个数组都空` → 含「命中并投递」；有原因 → 把三个数组的元素拼进去；未投递且无原因 → 「没有记录拒绝原因」

先读 `web/src/api/types.ts` 里 `ExplorerDelivery` 的实际字段再写投递段。

- [ ] **Step 4: 跑测试确认绿**

Run: `npm --prefix web test -- governance-provenance`
Expected: PASS（11 个用例）

- [ ] **Step 5: 变异验证**

把「多版本逆序」的排序去掉（保持原顺序），重跑：逆序那条必须红。改回。

- [ ] **Step 6: 提交**

```bash
git add -- web/src/pages/governance/provenance.ts web/tests/governance-provenance.test.ts
git commit -m "feat(web): 溯源链派生纯函数，五段固定顺序且缺段独立降级" -- web/src/pages/governance/provenance.ts web/tests/governance-provenance.test.ts
```

---

## Task 2: Audit trail 重画

**Files:**
- Create: `web/src/pages/governance/AuditRow.tsx`
- Create: `web/src/styles/features/governance.css`（本任务建立，后续任务往同一文件追加）
- Modify: `web/src/styles/index.css`（末尾追加 `@import "./features/governance.css";`）
- Rewrite: `web/src/pages/AdminAuditPage.tsx`
- Test: 既有的审计 DOM 测试（先 `ls web/tests | grep -i audit` 确认文件名）

**Interfaces:**
- Consumes: `listAuditEvents`、`getAuditEvent` from `web/src/api/queries.ts`；`Seg`、`Kicker`、`Tag`、`Button` from `web/src/components/`
- Produces: `AuditRow({ event, directory, expanded, onToggle })`

**要做的：**

1. 页头：`<Kicker>Governance · 审计流水</Kicker>` + h1「发生过的一切，未经编辑」+ 说明段（spec §2.1 的原文）
2. `actor_kind` 的 `<select>` 换成 `<Seg>`：`全部` / `人` / `Agent` / `系统`，值分别是 `""` / `human` / `agent` / `system`
3. 行改成五列网格 `.gv-audit-row`：时间(mono) / 类别 tag / 操作者 / 动作(heading 字族) / 目标(mono)，整行是 `<button>`
4. 展开区就地渲染在行下方，背景是 `color-mix(in srgb, var(--color-text) 5%, transparent)`

**必须保留的既有行为（重画不得弄丢）：**
- `LabelDirectory` 的名字解析（`AdminAuditPage.tsx:14-38`）——**名字在前，原始 ID 以小字括号跟随**。设计稿明确要求原始标识符留在行上
- 名字解析失败只意味着显示原始 ID，不报错
- `action` / `target_id` 两个自由文本筛选框
- 展开一行只打一次 `getAuditEvent`

- [ ] **Step 1: 摸清现状**

```bash
ls web/tests | grep -i audit
```
读出实际的测试文件名，通读它，记下每一条断言在测什么。**重画不得删掉任何一条既有断言所保护的行为**——文案可以改，行为不能丢。

- [ ] **Step 2: 先改测试（红）**

按新文案与新结构更新既有断言，并**新增**三条：

```tsx
it("四个 Seg 档位各自发出正确的 actor_kind", async () => {
  // 全部 → 不带 actor_kind；人 → human；Agent → agent；系统 → system
  // 逐个点击后断言最近一次请求的 URL
});

it("名字解析失败时行仍渲染，显示原始 ID", async () => {
  // stub 让 /v1/admin/members 与 /v1/admin/agents 返回 403
  // 断言审计行仍在，且能看到原始 membership_id
});

it("展开一行只打一次 getAuditEvent", async () => {
  // 点开、收起、再点开，断言 GET /v1/admin/audit/:id 的调用次数
});
```

Run: `npm --prefix web test -- audit`
Expected: FAIL

- [ ] **Step 3: 实现重画**

样式追加到新建的 `web/src/styles/features/governance.css`：

```css
/* Governance 四屏。 */

.gv-head { display: flex; align-items: flex-end; justify-content: space-between; gap: var(--space-6); padding: var(--space-6) var(--space-4) var(--space-4); }
.gv-head p { font-size: 14px; opacity: 0.7; max-width: 74ch; margin: var(--space-2) 0 0; }

.gv-audit-row { display: grid; grid-template-columns: 82px 96px minmax(220px, 1fr) minmax(200px, 1fr) 150px; gap: var(--space-4); align-items: center; width: 100%; text-align: left; background: transparent; border: 0; border-bottom: 1px solid var(--color-divider); padding: var(--space-3) var(--space-4); cursor: pointer; font-family: var(--font-body); color: inherit; }
.gv-audit-row:hover { background: color-mix(in srgb, var(--color-text) 5%, transparent); }
.gv-audit-time { font-family: var(--font-mono); font-size: 12px; opacity: 0.55; }
.gv-audit-action { font-family: var(--font-heading); font-weight: var(--font-heading-weight); font-size: 13px; }
.gv-audit-target { font-family: var(--font-mono); font-size: 11px; opacity: 0.5; }
.gv-audit-open { border-bottom: 1px solid var(--color-divider); background: color-mix(in srgb, var(--color-text) 5%, transparent); padding: var(--space-3) var(--space-4) var(--space-3) 110px; font-family: var(--font-mono); font-size: 12px; line-height: 1.7; }
```

在 `web/src/styles/index.css` 末尾追加 `@import "./features/governance.css";`。

- [ ] **Step 4: 跑测试确认绿 + 变异验证**

Run: `npm --prefix web test -- audit`
变异：把「系统」档的值从 `system` 改成 `human`，确认 Seg 那条测试变红。改回。

- [ ] **Step 5: 提交**

```bash
git commit -m "feat(web): Audit trail 按 Modernist 重画，筛选器改 Seg" -- <明确路径>
```

---

## Task 3: Session audit 的 Findings 行卡与三视图 Seg

**Files:**
- Create: `web/src/pages/governance/SessionFindings.tsx`
- Modify: `web/src/pages/AdminSessionAuditPage.tsx`（页头 + 三视图 Seg + 接入 Findings 行卡）
- Modify: `web/src/styles/features/governance.css`（追加）
- Test: 既有的 session-audit DOM 测试

**要做的：**

1. 页头：kicker `Governance · 会话审计` + h1「Agent 到底做了什么」+ 说明（spec §2.2 原文）
2. 三视图切换换成 `<Seg>`：`Findings` / `工具调用` / `按天`。**`?view=` 参数的读写逻辑不动**（阶段 4 已有，`AdminSessionAuditPage.tsx:563` 附近）
3. Findings 从表格改成行卡 `.gv-finding`：四列网格——severity tag / 标题+摘要+`kind · 证据 evt_…` / Agent+session / 「看这些调用」按钮
4. 「看这些调用」切到工具调用视图并把该 finding 的 `session_id` 填进工具调用视图的 session 过滤

**必须保留：** 三个视图各自的筛选器、阶段 4 加的 `?agent=` 预填（`agentInput` 与 `agentId` 两处初始值都要保住）。

- [ ] **Step 1: 摸清现状 + 改测试（红）**

`ls web/tests | grep -i session` 找到文件，通读。新增两条：

```tsx
it("三视图切换写进 URL 的 ?view=，刷新后停在同一视图", async () => { /* … */ });

it("Findings 的「看这些调用」切到工具调用视图并带上该 session", async () => {
  // 渲染一条 finding（session_id 可辨识），点按钮
  // 断言 view 变成 tools，且工具调用请求的 URL 带 session_id
});
```

阶段 4 的 `?agent=` 深链测试（`web/tests/agent-deeplink.dom.test.tsx`）**必须继续绿**——重画后跑一遍确认。

- [ ] **Step 2: 实现**

样式追加：

```css
.gv-finding { display: grid; grid-template-columns: 120px minmax(280px, 1fr) 200px auto; gap: var(--space-4); align-items: start; padding: var(--space-4); border-bottom: 1px solid var(--color-divider); }
.gv-finding-title { font-family: var(--font-heading); font-weight: var(--font-heading-weight); font-size: 15px; }
.gv-finding-summary { font-size: 13px; opacity: 0.72; margin-top: var(--space-1); max-width: 68ch; }
.gv-finding-ref { font-family: var(--font-mono); font-size: 11px; opacity: 0.45; margin-top: var(--space-2); }
```

- [ ] **Step 3: 跑测试 + 变异验证**

变异：把「看这些调用」的 session 传递去掉，确认对应测试变红。改回。
另跑 `npm --prefix web test -- agent-deeplink` 确认阶段 4 的深链没被弄坏。

- [ ] **Step 4: 提交**

---

## Task 4: Session audit 的按天柱状图

**Files:**
- Create: `web/src/pages/governance/SessionDays.tsx`
- Modify: `web/src/pages/AdminSessionAuditPage.tsx`（接入）
- Modify: `web/src/styles/features/governance.css`（追加）
- Test: 同上文件追加

**要做的：** 「按天」视图从表格改成柱状图。上方是等宽柱子（高度按当天 `event_count` 相对最大值的百分比），柱顶标数字；下方每列是日期（heading 字族）+ 「N 会话 · M 调用」+ 高危数（朱红）。

**纯 CSS，不引依赖。** 柱高用 inline `style={{ height: pct + "%" }}` ——这是**数据驱动的尺寸**，不是间距，不违反「间距用工具类」的约束。

- [ ] **Step 1: 写失败测试**

```tsx
it("柱高与当天事件数成比例", async () => {
  // 三天数据：event_count 分别 10 / 5 / 0
  // 断言三根柱子的 style.height 分别是 100%、50%、0%（或你实现的等价形式）
});

it("每列下方给出会话数、调用数与高危数", async () => { /* … */ });

it("全部为 0 时不产生 NaN 或除零", async () => {
  // 三天 event_count 都是 0；断言页面正常渲染、没有 "NaN"
});
```

第三条很重要——`max` 为 0 时 `count / max` 会得到 `NaN`。

- [ ] **Step 2: 实现 + 变异验证**

样式：

```css
.gv-days { display: grid; gap: var(--space-4); align-items: end; height: 200px; border-bottom: 2px solid var(--color-divider); }
.gv-day-col { display: flex; flex-direction: column; justify-content: flex-end; height: 100%; gap: var(--space-2); }
.gv-day-bar { background: var(--color-accent); min-height: 2px; }
.gv-day-count { font-family: var(--font-heading); font-weight: var(--font-heading-weight); font-size: 12px; font-variant-numeric: tabular-nums; }
.gv-day-labels { display: grid; gap: var(--space-4); padding-top: var(--space-3); }
.gv-day-high { font-size: 11px; color: var(--color-accent-700); }
```

列数由数据条数决定，用 inline `gridTemplateColumns: repeat(N, 1fr)`。

变异：把柱高公式从 `count / max` 改成常量，确认比例那条测试变红。改回。

- [ ] **Step 3: 提交**

---

## Task 5: Pipeline health 六格指标条与外壳重画

**Files:**
- Create: `web/src/pages/governance/PipelineMetrics.tsx`
- Rewrite: `web/src/pages/AdminOperationsPage.tsx`（**只换外壳与呈现**）
- Modify: `web/src/styles/features/governance.css`（追加）
- Test: 既有 operations DOM 测试 + 新建 `web/tests/governance-pipeline.dom.test.tsx`

**⚠️ 硬约束：`web/src/pages/operations/` 下的九个组件与 `hooks.ts` 一个字都不要改。** 那三个 region hook（`useSummaryRegion` / `useEventsRegion` / `useStorageRegion`）是既有的、已被测试覆盖的区块隔离机制。重写它们只会让一个已验收的属性重新变成未验收。本任务只在 `AdminOperationsPage` 里换外壳。

**六格取值**（来自 `getOperationsSummary` 的 `OperationsSummary`）：

| 格 | 字段 | 副标 |
|---|---|---|
| 扣下待查 | `extraction.quarantined` | — |
| 失败 | `extraction.failed` | — |
| 排队中 | `extraction.unextracted_events` | 最老的未抽取事件 = `extraction.oldest_unextracted_at` 的相对时间 |
| 典型延迟 | `latency.p50_ms` | `p50` |
| 最坏情况 | `latency.p95_ms` | `p95` |
| 空手而归 | `recalls.empty` | 「证据不足或预算不够」 |

- [ ] **Step 1: 写失败测试**

新建 `web/tests/governance-pipeline.dom.test.tsx`：

```tsx
it("六格各自取自正确的字段", async () => {
  // 用六个互不相同的数字构造 summary，逐格断言
  // 关键：数字要互不相同，否则字段接错也测不出来
});

it("summary 区块失败时，另两个区块仍渲染", async () => { /* … */ });
it("events 区块失败时，另两个区块仍渲染", async () => { /* … */ });
it("storage 区块失败时，另两个区块仍渲染", async () => { /* … */ });
```

后三条是**上位 spec 给本阶段的验收项**（区块级错误隔离），必须逐块各写一条。

- [ ] **Step 2: 实现 + 变异验证**

样式：

```css
.gv-metrics { display: grid; grid-template-columns: repeat(6, 1fr); border-top: 2px solid var(--color-divider); border-bottom: 2px solid var(--color-divider); }
.gv-metric { padding: var(--space-3) var(--space-4); border-right: 1px solid var(--color-divider); }
.gv-metric:last-child { border-right: 0; }
@media (max-width: 900px) { .gv-metrics { grid-template-columns: repeat(3, 1fr); } }
```

变异：把「空手而归」的字段从 `recalls.empty` 改成 `recalls.succeeded`，确认六格那条测试变红（这就是为什么六个数字必须互不相同）。改回。

- [ ] **Step 3: 提交**

---

## Task 6: Explorer 双栏骨架与左栏

**Files:**
- Create: `web/src/pages/governance/NoteList.tsx`
- Rewrite: `web/src/pages/AdminExplorerPage.tsx`（改成双栏容器）
- Modify: `web/src/app/routes.tsx`（`/governance/memory/:noteId` 也渲染 `AdminExplorerPage`）
- Modify: `web/src/styles/features/governance.css`（追加）
- Test: 新建 `web/tests/governance-explorer.dom.test.tsx`

**要做的：**

1. `AdminExplorerPage` 变成双栏容器：左 340px `NoteList`，右侧根据 `useParams().noteId` 决定渲染详情（Task 7）还是空态
2. 路由 `/governance/memory/:noteId` 从 `AdminTeamNoteDetailPage` 改成 `AdminExplorerPage`（**本任务先让它渲染一个占位的右栏，Task 7 再填内容**）
3. 左栏的笔记链接从 `/admin/explorer/notes/:id` 改成 `/governance/memory/:id`
4. 窄屏折叠：无 `:noteId` 时只显示列表，有 `:noteId` 时只显示详情 + 一个返回列表的链接

**本任务不删 `AdminTeamNoteDetailPage.tsx`**（Task 7 删）。

- [ ] **Step 1: 写失败测试**

```tsx
it("/governance/memory 渲染左栏，且不发 getTeamNote", async () => {
  // 断言列表在，且没有 GET /v1/admin/team-notes/:id 的调用
});
it("左栏链接指向 /governance/memory/:id", async () => { /* … */ });
it("左栏取数失败时塌成可重试错误", async () => { /* … */ });
```

- [ ] **Step 2: 实现 + 提交**

样式：

```css
.gv-explorer { display: grid; grid-template-columns: 340px 1fr; border-top: 2px solid var(--color-divider); }
.gv-note-list { border-right: 2px solid var(--color-divider); }
.gv-note-item { display: block; width: 100%; text-align: left; background: transparent; border: 0; border-bottom: 1px solid var(--color-divider); padding: var(--space-3) var(--space-4); cursor: pointer; font-family: var(--font-body); color: inherit; }
.gv-note-item:hover, .gv-note-item.on { background: color-mix(in srgb, var(--color-text) 5%, transparent); }
@media (max-width: 900px) { .gv-explorer { grid-template-columns: 1fr; } .gv-note-list { border-right: 0; } }
```

---

## Task 7: Explorer 右栏溯源链与召回表

**Files:**
- Create: `web/src/pages/governance/NoteProvenance.tsx`、`web/src/pages/governance/NoteRecalls.tsx`
- Modify: `web/src/pages/AdminExplorerPage.tsx`（填入右栏）
- Delete: `web/src/pages/AdminTeamNoteDetailPage.tsx`
- Modify: `web/src/styles/features/governance.css`（追加）
- Test: `web/tests/governance-explorer.dom.test.tsx` 追加

**Interfaces:**
- Consumes: `buildProvenance` / `describeRecall`（Task 1）；`getTeamNote` from `web/src/api/queries.ts`

**要做的：**

1. 右栏三块：笔记头 / 「它是怎么来的」（`NoteProvenance`）/「每一次被端到 Agent 面前」（`NoteRecalls`）
2. **一次 `getTeamNote` 撑起整条链**——不许再打第二个请求
3. 每段缺失时渲染「这一段没有记录」；`revisions` 为空时整块走 `EmptyState`
4. 删除 `AdminTeamNoteDetailPage.tsx`，跑 `npm --prefix web run build` 把残留引用改干净

- [ ] **Step 1: 写失败测试**

```tsx
it("一次 getTeamNote 撑起整条链，不发第二个请求", async () => {
  // 断言 GET /v1/admin/team-notes/note_01 只被调用一次
  // 且五段的阶段名都出现在页面上
});
it("某段缺失时只有那段显示「没有记录」", async () => { /* … */ });
it("revisions 为空时整块走正向空态而不是错误", async () => { /* … */ });
it("召回表把三类原因拼成一句话", async () => { /* … */ });
it("右栏取数失败时左栏仍可用", async () => { /* … */ });
```

- [ ] **Step 2: 实现**

样式：

```css
.gv-prov-step { display: grid; grid-template-columns: 130px 1fr; gap: var(--space-4); padding: var(--space-4) 0; border-top: 1px solid var(--color-divider); }
.gv-prov-stage { font-size: 10px; letter-spacing: 0.1em; text-transform: uppercase; color: var(--color-accent-700); }
.gv-prov-title { font-family: var(--font-heading); font-weight: var(--font-heading-weight); font-size: 14px; }
.gv-prov-body { font-size: 13px; opacity: 0.72; margin-top: var(--space-1); max-width: 74ch; }
.gv-prov-ref { font-family: var(--font-mono); font-size: 11px; opacity: 0.45; margin-top: var(--space-1); }
.gv-prov-missing { opacity: 0.5; }
```

- [ ] **Step 3: 删除旧页面 + 跑构建**

```bash
git rm web/src/pages/AdminTeamNoteDetailPage.tsx
npm --prefix web run build
```
把残留引用（`routes.tsx` 的 import 等）改干净。

- [ ] **Step 4: 变异验证**

把 `buildProvenance` 的调用改成只取第一个版本，确认「五段都出现」那条测试仍绿还是变红——**如果仍绿，说明测试没有真正覆盖多版本，补一条**。

- [ ] **Step 5: 提交**

---

## Task 8: 三主题验证与修复

**Files:**
- Modify: `web/src/styles/features/governance.css`（按实测结果）
- 可能 Modify: `web/src/styles/themes.css`

**这是本阶段的收尾任务，也是阶段 4 建立的必做流程。**

阶段 4 用这个方法查出过一个 Critical：令牌仪式在 arcade 主题下对比度 1.14:1、文字几乎不可见，根因是 `--color-accent` 与 `--color-accent-100` 在三个主题里**角色互换**（arcade 把红色用作页面底色，accent 阶被整体重映射成墨色系）。**任何「accent 恒为红」的假设都会在 arcade 下崩掉。**

- [ ] **Step 1: 搭验证页**

复用 `.superpowers/sdd/2026-08-06-portal-modernist-phase4-agent-detail/theme-check.html`（在主仓库同名目录下也有一份）。照它的结构做一份 Governance 版：引用编译后的 CSS，用**本阶段的真实类名**（`.gv-*`）摆出四屏的代表性区块，页面内嵌 WCAG 对比度计算脚本 + 「未解析变量」检查。

```bash
npm --prefix web run build
cp <验证页> web/dist/ && cd web/dist && python3 -m http.server 8899
```
注意 `<link>` 引用的 CSS 文件名带 hash，每次 build 都会变。

- [ ] **Step 2: 三主题逐一实测**

对每个主题记录：
- **未解析变量清单**（必须为空——引用了不存在的变量不会报错，只会静默失效）
- 每个新增类的前景/背景对比度

**重点盯：**
- `.gv-day-bar` 用了 `var(--color-accent)`——在 arcade 下那是**墨色**不是红色，柱子会和文字撞色
- `.gv-day-high` / `.gv-prov-stage` 用了 `--color-accent-700`
- 所有 `opacity: 0.45 ~ 0.72` 的次要文字压在 arcade 的饱和红底（`#dd2b0f`）上

- [ ] **Step 3: 修到三主题全过 AA（≥ 4.5）**

修法参考阶段 4：需要主题稳定的颜色时，新增专属 token（在 `tokens.css` 给默认值、在 `themes.css` 按主题覆盖），不要直接用会被重映射的 `--color-accent*`。

- [ ] **Step 4: 记录并提交**

把三主题**修复前后**的完整对比度数字表写进报告——这是这次验证唯一的证据。
删掉塞进 `web/dist/` 的验证页。

---

## 收尾检查

- [ ] `npm --prefix web test` 全绿
- [ ] `npm --prefix web run build` 干净（只剩既有的 chunk-size 警告）
- [ ] `git diff --stat main...HEAD -- . ':(exclude)web' ':(exclude)docs'` 为空
- [ ] `grep -rn "\.btn\.ghost" web/src` —— 新增代码零命中
- [ ] `grep -rnE "var\(--(bg|muted|accent|text|border|surface|mono)\)" web/src/styles/features/governance.css` —— 应为 0
- [ ] `grep -rn "AdminTeamNoteDetailPage" web/src web/tests` —— 应为 0
- [ ] 阶段 4 的 `web/tests/agent-deeplink.dom.test.tsx` 仍绿（`?agent=` 深链没被重画弄坏）
- [ ] 三主题实测记录已写进报告，**不允许「应该没问题」式的推断**
