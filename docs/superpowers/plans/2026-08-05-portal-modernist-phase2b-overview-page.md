# Modernist Portal 阶段 2b:Overview 页面 + 删除 Pulse 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用真正的 Overview 落地页替换 `/overview` 上暂挂的 AdminPulsePage,消费阶段 2a 已合并的 `GET /v1/admin/overview`;三个小后端前置(已获用户裁定)先行;最后删除整个 Pulse。

**Architecture:** 后端前置只动三处已裁定的点(NoteMix 鉴权、notes_expiring_today 数据源、per-source 超时),端点契约(IDL)**不改**。前端页面由三个独立轮询 region 驱动(overview 聚合 / agent stats / 事件流,各 10s),区块级错误隔离沿用 `usePolledRegion` 契约;吞吐图按设计稿用 **每行独立归一化的三行条带**(small multiples,无双轴),纯 div + CSS token,不引入任何图表库。

**Tech Stack:** 后端 Go + Hertz + pgx;前端 React 18 + TypeScript + Vite 5 + react-router-dom 6,测试 Vitest + Testing Library。

**上游文档:** spec `docs/superpowers/specs/2026-08-04-portal-modernist-redesign-design.md` §5.1/§5.4/§6/§7-阶段2;设计稿 `docs/w.html`(自解压 bundle,Overview 屏已抽取要点进本计划,不必再解包)。

## Global Constraints

- **后端端点契约冻结**:`GET /v1/admin/overview` 的 IDL/JSON 形状不改。阶段 2a 已在 main(PR #81)。**不要重实现任何 2a 已有的东西**——`overview_endpoint.go`、`Series`、`NoteMix`、`ListExpiringEnrollments` 全部已存在,先 `git log`/读码确认再动手。
- **前端零新增运行时依赖**:生产依赖只有 react / react-dom / react-router-dom / react-markdown / remark-gfm。图表手写。
- **颜色只用 CSS token**(`web/src/styles/tokens.css`),三主题(beige/dark/arcade)由 `<html data-theme>` 切换——新样式绝不硬编码色值;若需新 token 必须同时在 `themes.css` 三个主题块里给值。
- **按钮必须走 `components/Button.tsx`**;布局优先用 `components.css`/`layout.css` 的工具类(`.row .stack .section .toolbar .seg .tag .note`),`styles/features/` 仅限确实特殊的样式(本阶段允许新建 `features/overview-chart.css`,spec 点名)。
- **区块级错误隔离**(spec §6):每个 region 独立 `loading|ready|error`,失败区块显示错误+重试,其余照常;已展示过数据的 region 出错时保持 ready 并加「Auto-refresh failed; the … may be stale.」的 `.note.warn` 横幅(照抄 `AdminOperationsPage.tsx` 的逐区块写法)。
- **attention 为空是正向空态**(「Nothing needs you right now」),用 `components/EmptyState.tsx`,不是错误态。
- **轮询 10s**,走 `lib/usePolling.ts` / `lib/useRegion.ts` 既有契约(可见性门控、唤醒单刷、AbortSignal),不改这两个文件。
- **能力门控**:`hasServerCapability(me, "view.operations")`;路由用 `routes.tsx` 里已有的 `RequireCapability`(未授权 `<Navigate to={landingPath(me)} replace>`)。顶栏项在 `navModel.ts` 已按此隐藏——不要再造门控。
- **测试命令**:前端 `npm --prefix web test`(Vitest)与 `npm --prefix web run build`(含 `tsc --noEmit` 严格检查,这是前端唯一 lint 门);后端 `make lint`、`make test-unit`;Postgres 适配器测试必须带 `TEAM_MEMORY_TEST_POSTGRES_DSN='postgres://team_memory:team_memory@127.0.0.1:55432/team_memory?sslmode=disable'` 且用 `-v` 从 `=== RUN` 确认真的跑了(缺 DSN 会**静默跳过并打印 ok**);Postgres 包不并行跑。
- **每个 Task 末尾提交一次**;PR 描述附 spec §6.1 红线 checklist 逐条勾选。
- **文案是英文**,时间格式用 `lib/format.ts` 的 `formatTime`,不新造 formatter。

## 已裁定事项(2026-08-05,用户)

1. `notes_expiring_today` 补真实数据源(未来 24h 内 hard 过期的活跃笔记计数),字段保持 required。
2. 指标口径签字:`evidence_captured`=Observations.EventsWritten、`recalls_served`=Recalls.Succeeded、`recall_accept_rate`=WithEvidence/Requests。前端文案按此写。
3. NoteMix 这类**聚合**计数放开到 `view.operations`(Owner+Admin);笔记内容仍 Owner-only,不动全局能力表。
4. (工程决定)per-source 超时 5s,落 2a 计划遗留的「超时降级」承诺。
5. 7d 窗口在 EventRetention<7d 的部署会 400——前端沿用现有 generic-400 处理 + seg 按钮 title 提示(与 `AdminOperationsPage.tsx:108-121` 一致),不做 retention 探测(后端未暴露)。

---

## File Structure

**后端(修改,均为小改)**

| 文件 | 变化 |
|---|---|
| `internal/deployment/onprem/explorer.go` | NoteMix 鉴权换 `CapabilityViewOperations`;新增 `CountExpiringNotes` |
| `internal/explorer/notemix.go` | `NoteMixReader` 加 `CountExpiringNotes` 端口 |
| `internal/platform/postgres/explorer_notemix.go` | `CountExpiringNotes` SQL |
| `internal/app/saas_wiring.go` | `scopedExplorerService` 补 `CountExpiringNotes` 委派 |
| `internal/teamnote/transport/httpapi/handler/dependencies.go` | `ExplorerLifecycle` 加 `CountExpiringNotes` |
| `internal/teamnote/transport/httpapi/handler/overview_endpoint.go` | noteMix goroutine 顺带取 count;去掉恒 0 stub;六个 goroutine 各包 5s `context.WithTimeout` |

**前端(新建)**

| 文件 | 职责 |
|---|---|
| `web/src/pages/OverviewPage.tsx` | 页面组装:header + 三 region + 六区块 |
| `web/src/pages/overview/hooks.ts` | `useOverviewRegion` / `useWritersRegion` / `useFeedRegion`(从 AdminPulsePage 移植) |
| `web/src/pages/overview/MetricsRow.tsx` | 5 格指标行(`MetricTile`) |
| `web/src/pages/overview/ThroughputChart.tsx` | 三行 small-multiples 条带图 |
| `web/src/pages/overview/NoteMixBlock.tsx` | conic-gradient 环图 + 图例 |
| `web/src/pages/overview/WhoIsWriting.tsx` | agent stats 条形列表 |
| `web/src/pages/overview/AttentionQueue.tsx` | attention 列表 + Access 汇总条 |
| `web/src/pages/overview/EventsFeed.tsx` | 事件流(移植 held 模式) |
| `web/src/styles/features/overview-chart.css` | `.ov-*` 图表与区块网格样式 |

**前端(修改/删除)**

| 文件 | 变化 |
|---|---|
| `web/src/api/types.ts` | 加 Overview* 五个接口(逐字段镜像 Go 模型,snake_case) |
| `web/src/api/queries.ts` | 加 `getOverview` |
| `web/src/app/routes.tsx` | `/overview` 换挂 `OverviewPage` |
| `web/src/styles/index.css` | `+ features/overview-chart.css`,`- features/pulse.css`(Task 8) |
| `web/tests/operationsFixtures.ts` | fetch 路由加 `/v1/admin/overview` 分支 + `makeOverview` + `renderOverviewPage` |
| `web/tests/app-shell.dom.test.tsx` / `web/tests/admin-explorer.dom.test.tsx` | `/overview` 断言从 Team Pulse 改为 Overview |
| **删除**(Task 8) | `pages/AdminPulsePage.tsx`、`pages/pulse/`(AgentGrid/AgentPulseCard/KnowledgeFlow/LiveEventsFeed)、`styles/features/pulse.css`、`tests/admin-pulse.dom.test.tsx`、`lib/useCountUp.ts`、`lib/operations.ts` 的 Pulse 段(135-168 行:`AgentActivity`/`ACTIVE_MS`/`RECENT_MS`/`agentActivity`/`relativeAge`) |

---

## Task 1: NoteMix 聚合鉴权放开到 view.operations(后端)

**Files:**
- Modify: `internal/deployment/onprem/explorer.go:102`(NoteMix 的鉴权行)
- Modify: `internal/teamnote/transport/httpapi/handler/overview_endpoint.go`(GetOverview 与 logOverviewDegraded 的 doc comment 里「Admin 必然降级」的例子)
- Test: `internal/deployment/onprem/explorer_test.go`

**Interfaces:**
- Consumes: 既有 `onprem.CapabilityViewOperations`(Owner+Admin,见 `registry.go` 能力表)
- Produces: `ExplorerService.NoteMix` 对 Admin 放行。签名不变。

**背景**:用户裁定聚合计数随页面门槛走。`CapabilityViewTeamMemory`(Owner-only)继续保护笔记内容读取(`ListTeamNotes` 等),**只改 NoteMix 这一个方法**。

- [ ] **Step 1: 写失败的测试**

在 `internal/deployment/onprem/explorer_test.go` 的 `TestOwnerReadsNoteMix` 之后追加(fixture 照抄同文件 `activeOwner()` 的形状):

```go
// NoteMix is an aggregate count, so it follows the Overview page's own gate
// (view.operations, Owner+Admin) rather than the Owner-only note-content
// capability. Note CONTENT reads (ListTeamNotes etc.) stay Owner-only.
func (s *explorerServiceSuite) TestNoteMixAllowsAdminAndRejectsMember() {
	at := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	s.repository.mix = []explorer.NoteKindCount{{Kind: "decision", Count: 2}}

	admin := activeOwner()
	admin.Role = onprem.RoleAdmin
	mix, err := s.service.NoteMix(context.Background(), admin, at)
	s.Require().NoError(err)
	s.Equal(s.repository.mix, mix)

	member := activeOwner()
	member.Role = onprem.RoleMember
	_, err = s.service.NoteMix(context.Background(), member, at)
	s.Require().ErrorIs(err, onprem.ErrForbidden)
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/deployment/onprem -run TestExplorerServiceSuite -count=1 -v`
Expected: 新测试 FAIL——Admin 分支现在被 `CapabilityViewTeamMemory` 拒掉。member 分支应已通过。

- [ ] **Step 3: 改鉴权**

`internal/deployment/onprem/explorer.go` NoteMix 方法里:

```go
	if err := authorizeHumanCapability(principal, CapabilityViewOperations); err != nil {
		return nil, err
	}
```

同时把该方法的 doc comment 从「Same authorization as every other explorer read」改为:

```go
// NoteMix answers the Overview's live-note breakdown. As an aggregate count
// it follows the Overview page's own gate (view.operations, Owner+Admin) —
// note CONTENT reads on this service stay behind the stricter Owner-only
// CapabilityViewTeamMemory.
```

- [ ] **Step 4: 更新 handler 的过期注释**

`overview_endpoint.go` 的 `GetOverview` doc comment 第四段与 `logOverviewDegraded` 的注释都举了「NoteMix 对 Admin 必然 ErrForbidden」当例子——现在不成立了。把两处例子改为一般化表述(quiet-degrade 机制**保留**,它是防御纵深):

```go
// A degradable source can also fail with onprem.ErrForbidden rather than a
// genuine error when its own capability gate is stricter than this
// endpoint's. That is an expected authorization outcome, not a source
// failure: it is logged at Debug (see logOverviewDegraded) instead of Warn.
```

handler 测试 `TestNoteMixForbiddenForRoleDegradesQuietly` 用假实现直接返回 ErrForbidden,不依赖真实能力表,**不用改**。

- [ ] **Step 5: 全量验证 + 提交**

Run: `go build ./... && make test-unit && make lint`
Expected: 全绿。onprem explorer 既有测试(owner 路径)不受影响。

```bash
git add internal/deployment/onprem internal/teamnote/transport/httpapi/handler/overview_endpoint.go
git commit -m "feat(explorer): open the note-mix aggregate to view.operations"
```

---

## Task 2: notes_expiring_today 真实数据源(后端,端到端)

**Files:**
- Modify: `internal/explorer/notemix.go`(端口)
- Modify: `internal/platform/postgres/explorer_notemix.go`(SQL)
- Modify: `internal/deployment/onprem/explorer.go`(服务方法)
- Modify: `internal/app/saas_wiring.go`(scopedExplorerService 委派,照抄同文件 NoteMix 的形状)
- Modify: `internal/teamnote/transport/httpapi/handler/dependencies.go`(ExplorerLifecycle)
- Modify: `internal/teamnote/transport/httpapi/handler/overview_endpoint.go`(noteMix goroutine + 去 stub)
- Test: `internal/platform/postgres/explorer_notemix_test.go`、`internal/deployment/onprem/explorer_test.go`、`internal/teamnote/transport/httpapi/handler/overview_endpoint_test.go`

**Interfaces:**
- Produces:
  - `NoteMixReader.CountExpiringNotes(ctx context.Context, at time.Time, within time.Duration) (int64, error)`
  - `ExplorerService.CountExpiringNotes(ctx, principal HumanPrincipal, at time.Time, within time.Duration) (int64, error)`(鉴权同 Task 1 后的 NoteMix:`CapabilityViewOperations`)
  - 语义:在 `at` 时刻**活跃**(与 NoteMix 同一 effective-state 定义)且 `hard_expires_at <= at+within` 的笔记数。窗口统一 24h(与 attention 阈值一致);前端文案写「expiring in 24h」而非字面 today。

- [ ] **Step 1: 写失败的 Postgres 测试**

在 `internal/platform/postgres/explorer_notemix_test.go` 的既有 suite 里追加(fixture 助手照抄同文件 `insertNote`/`insertNoteInScope`;时间窗与既有用例错开,该套件有逐测试 truncate 则不必):

```go
// Only ACTIVE notes whose hard expiry falls inside the lookahead window
// count: already-expired, resolved, beyond-window, and other-scope notes
// must all be excluded — otherwise the Overview tile inflates.
func (s *explorerNoteMixSuite) TestCountExpiringNotesCountsOnlyActiveInWindowSameScope() {
	ctx := context.Background()
	at := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)

	s.insertNote(ctx, "x1", "decision", "active", at.Add(2*time.Hour))  // counted
	s.insertNote(ctx, "x2", "blocker", "active", at.Add(23*time.Hour)) // counted
	s.insertNote(ctx, "x3", "handoff", "active", at.Add(-time.Hour))   // already hard-expired
	s.insertNote(ctx, "x4", "decision", "resolved", at.Add(2*time.Hour))
	s.insertNote(ctx, "x5", "decision", "active", at.Add(48*time.Hour)) // beyond window
	s.insertNoteInScope(ctx, "other-scope", "x6", "decision", "active", at.Add(2*time.Hour))

	count, err := s.explorer.CountExpiringNotes(ctx, at, 24*time.Hour)
	s.Require().NoError(err)
	s.Equal(int64(2), count)
}
```

- [ ] **Step 2: 运行确认失败**

Run: `TEAM_MEMORY_TEST_POSTGRES_DSN='postgres://team_memory:team_memory@127.0.0.1:55432/team_memory?sslmode=disable' go test ./internal/platform/postgres -run TestExplorerNoteMixSuite -count=1 -v`
Expected: 编译失败——`CountExpiringNotes` 未定义。**确认 `=== RUN` 出现,ok 不算数。**

- [ ] **Step 3: 端口 + SQL**

`internal/explorer/notemix.go` 的 `NoteMixReader` 接口加:

```go
	// CountExpiringNotes counts notes that are ACTIVE at `at` (same
	// effective-state definition as NoteMix) and whose hard expiry falls
	// within (at, at+within]. It feeds the Overview's expiring-soon tile.
	CountExpiringNotes(ctx context.Context, at time.Time, within time.Duration) (int64, error)
```

`internal/platform/postgres/explorer_notemix.go` 追加(CASE 分支与同文件 `NoteMix` 逐字一致——两处「活跃」定义必须对得上账):

```go
// CountExpiringNotes counts live notes in this store's scope whose hard
// expiry falls inside the lookahead window. "Live" is byte-for-byte the same
// effective-state expression NoteMix uses above — the tile and the mix must
// agree on what counts as a live note.
func (s *ExplorerStore) CountExpiringNotes(
	ctx context.Context,
	at time.Time,
	within time.Duration,
) (int64, error) {
	var count int64
	err := s.pool.QueryRow(ctx, `
SELECT count(*)
FROM team_notes
WHERE scope_id = $3
  AND NOT (state = 'expired' OR hard_expires_at <= $1)
  AND NOT (state = 'resolved' OR (invalid_at IS NOT NULL AND invalid_at <= $1))
  AND hard_expires_at <= $2`, at, at.Add(within), s.scopeID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count postgres expiring notes: %w", err)
	}
	return count, nil
}
```

> 占位符对参数逐个数一遍:$1=at、$2=at+within、$3=scope,三个参数三处引用。

- [ ] **Step 4: 服务方法 + SaaS 委派**

`internal/deployment/onprem/explorer.go` 在 NoteMix 之后追加(鉴权与 Task 1 后的 NoteMix 一致):

```go
// CountExpiringNotes feeds the Overview's expiring-soon tile. Same gate as
// NoteMix: an aggregate count, so view.operations (Owner+Admin).
func (s *ExplorerService) CountExpiringNotes(
	ctx context.Context,
	principal HumanPrincipal,
	at time.Time,
	within time.Duration,
) (int64, error) {
	if err := authorizeHumanCapability(principal, CapabilityViewOperations); err != nil {
		return 0, err
	}
	return s.repository.CountExpiringNotes(ctx, at, within)
}
```

> `s.repository` 的真实字段名以该文件 NoteMix 的实现为准。

`internal/app/saas_wiring.go` 的 `scopedExplorerService` 照抄同 struct `NoteMix`(~:355)的 forPrincipal 委派形状补 `CountExpiringNotes`。

`internal/deployment/onprem/explorer_test.go`:`explorerRepository` 假实现补 `CountExpiringNotes`(记录参数、返回定值),并加服务层测试(照抄 `TestNoteMixAllowsAdminAndRejectsMember` 的三分支:owner/admin 放行拿到定值、member ErrForbidden)。

- [ ] **Step 5: 接进 handler,去掉 stub**

`dependencies.go` 的 `ExplorerLifecycle` 加:

```go
	CountExpiringNotes(ctx context.Context, principal onprem.HumanPrincipal, at time.Time, within time.Duration) (int64, error)
```

`overview_endpoint.go` 的 noteMix goroutine(~:133)扩为同一降级单元里的两个调用——note mix 与 expiring count 同上下文、同失败面,任一失败清空两者:

```go
	mix, mixErr := h.explorer.NoteMix(gctx, principal, now)
	if mixErr == nil {
		noteMix = mix
		expiring, expErr := h.explorer.CountExpiringNotes(gctx, principal, now, 24*time.Hour)
		if expErr == nil {
			notesExpiring = expiring
		} else {
			h.logOverviewDegraded("overview expiring-notes count degraded", expErr)
		}
	} else {
		h.logOverviewDegraded("overview note mix degraded", mixErr)
	}
```

(`notesExpiring` 是新的 goroutine 局部变量,声明处与 `noteMix` 并排;`gctx`/`now` 用该 goroutine 既有的上下文与时间来源,以现文件为准。)
`overviewResponseToAPI`(~:361)把 `NotesExpiringToday: 0` 的 stub 与它的注释删掉,改填真实值。

handler 测试:假 explorer(`explorer_endpoints_test.go` 或 `operations_endpoints_test.go` 里带 `noteMixCalls` 计数的那个)补 `CountExpiringNotes` stub + 调用计数;更新既有 Overview 测试的期望值(fixture 给定值→响应 `metrics.notes_expiring_today` 等于它);403 测试的零下游断言把新计数器纳入。

- [ ] **Step 6: 全量验证 + 提交**

Run: `TEAM_MEMORY_TEST_POSTGRES_DSN='...' go test ./internal/platform/postgres -run TestExplorerNoteMixSuite -count=1 -v`(GREEN,确认 `=== RUN`)
Run: `go build ./... && make test-unit && make lint`
Expected: 全绿。

```bash
git add internal/explorer internal/platform/postgres internal/deployment/onprem \
        internal/app internal/teamnote/transport/httpapi/handler
git commit -m "feat(explorer): count notes expiring within 24h for the overview tile"
```

---

## Task 3: per-source 超时降级(后端)

**Files:**
- Modify: `internal/teamnote/transport/httpapi/handler/overview_endpoint.go`
- Test: `internal/teamnote/transport/httpapi/handler/overview_endpoint_test.go`

**Interfaces:** 无新接口。行为:任一下游源挂起超过 5s 即按「该源失败」处理——非关键源降级、Summary 整体失败;不再挂满整个请求上下文。

- [ ] **Step 1: 写失败的测试**

包级超时变量便于测试注入(生产值 5s):

```go
// overviewSourceTimeout bounds each downstream read; a hung source degrades
// (or, for Summary, fails the request) instead of stalling the aggregate.
var overviewSourceTimeout = 5 * time.Second
```

测试(假 noteMix 阻塞到 ctx 结束;50ms 注入超时):

```go
func (s *overviewSuite) TestHungSourceDegradesAfterTimeout() {
	restore := overviewSourceTimeout
	overviewSourceTimeout = 50 * time.Millisecond
	defer func() { overviewSourceTimeout = restore }()

	s.explorer.noteMixBlock = true // fake: <-ctx.Done(); return ctx.Err()
	// ...既有 fixture 其余源正常...
	resp := s.getOverview("24h") // 照抄同文件既有请求 helper
	s.Require().Equal(200, resp.StatusCode)
	s.Empty(resp.Body.NoteMix)      // 该区块降级
	s.NotEmpty(resp.Body.Series)    // 其余区块完好
}
```

> 请求 helper、fixture 字段名以 `overview_endpoint_test.go` 现文件为准;`noteMixBlock` 在假实现里实现为 `if f.noteMixBlock { <-ctx.Done(); return nil, ctx.Err() }`。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/teamnote/transport/httpapi/handler -run TestGetOverview -count=1 -v`
Expected: 新测试超时/挂起方向失败(阻塞源拖住整个请求直到测试超时,或响应缺降级)。

- [ ] **Step 3: 实现**

六个 goroutine 各自包裹:

```go
	sctx, cancel := context.WithTimeout(gctx, overviewSourceTimeout)
	defer cancel()
```

调用一律用 `sctx`。Summary goroutine 同样包裹——超时即 `summaryErr`,整体失败(现有 fatal 语义不变)。

- [ ] **Step 4: 验证 + 提交**

Run: `go test ./internal/teamnote/transport/httpapi/handler -run TestGetOverview -count=1 -v && go build ./... && make test-unit && make lint`

```bash
git add internal/teamnote/transport/httpapi/handler
git commit -m "feat(api): bound each overview source read with a 5s timeout"
```

---

## Task 4: 前端 API 层与测试基建

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/api/queries.ts`
- Modify: `web/tests/operationsFixtures.ts`
- Test: `web/tests/overview-api.test.ts`(新)

**Interfaces:**
- Produces(后续 Task 全部依赖,名字以此为准):
  - `types.ts`:`OverviewMetrics` `OverviewSeriesPoint` `OverviewNoteMixEntry` `OverviewAttentionItem` `OverviewResponse`
  - `queries.ts`:`getOverview(window: TimeWindowPreset, signal?: AbortSignal): Promise<OverviewResponse>`
  - fixtures:`makeOverview(overrides?)`、`OperationsEndpoints.overview`、`renderOverviewPage(endpoints)`

- [ ] **Step 1: types(逐字段镜像 Go 模型,保持 snake_case,不做 camel 映射)**

`web/src/api/types.ts` 追加:

```ts
/** GET /v1/admin/overview — mirrors api.OverviewResponse verbatim. */
export interface OverviewMetrics {
  evidence_captured: number;
  live_notes: number;
  notes_expiring_today: number;
  recalls_served: number;
  recall_accept_rate: number;
  p50_ms?: number;
  p95_ms?: number;
  attention_count: number;
}

export interface OverviewSeriesPoint {
  bucket_at: string;
  evidence: number;
  facts: number;
  recalls: number;
}

export interface OverviewNoteMixEntry {
  kind: string;
  count: number;
  pct: number;
}

export interface OverviewAttentionItem {
  kind: string;
  severity: string;
  title: string;
  body: string;
  ref: string;
  target: string;
}

export interface OverviewResponse {
  from_time: string;
  to_time: string;
  generated_at: string;
  metrics: OverviewMetrics;
  series: OverviewSeriesPoint[];
  note_mix: OverviewNoteMixEntry[];
  attention: OverviewAttentionItem[];
}
```

- [ ] **Step 2: query**

`web/src/api/queries.ts`(紧挨 `getOperationsSummary`,复用同文件 `query()`):

```ts
export async function getOverview(
  window: TimeWindowPreset,
  signal?: AbortSignal,
): Promise<OverviewResponse> {
  return humanFetch<OverviewResponse>(`/v1/admin/overview${query({ window })}`, { signal });
}
```

(`TimeWindowPreset` 从 `../lib/operations` import,已存在。)

- [ ] **Step 3: fixtures**

`web/tests/operationsFixtures.ts`:
1. `makeOverview(overrides: Partial<OverviewResponse> = {}): OverviewResponse` —— 一份 6 桶(24h→8 桶用例另给)、4 kind note_mix、2 条 attention 的默认值,`{...defaults, ...overrides}`;
2. `OperationsEndpoints` 加 `overview?: (url: URL) => unknown`,`operationsFetch` 的路由里在既有分支旁加 `/v1/admin/overview` 分支(形状照抄 `/v1/admin/operations/summary` 分支——该 router 对未处理路径 throw,必须加);
3. `renderOverviewPage(endpoints)` 照抄 `renderPulsePage` 但等待 Overview 自己的 h1(Task 6 定为团队名或 "Overview",fixture 的 `opsMe()` 无 teams → 断言 `await screen.findByRole("heading", { name: "Overview" })`)。

- [ ] **Step 4: 单测**

`web/tests/overview-api.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { getOverview } from "../src/api/queries";
import { jsonResponse, stubFetch } from "./helpers";
import { makeOverview } from "./operationsFixtures";

describe("getOverview", () => {
  it("hits /v1/admin/overview with the window param and parses the body", async () => {
    const fetchMock = stubFetch((url) => {
      expect(url).toContain("/v1/admin/overview?window=7d");
      return jsonResponse(makeOverview());
    });
    const out = await getOverview("7d");
    expect(out.metrics.attention_count).toBe(makeOverview().metrics.attention_count);
    expect(fetchMock).toHaveBeenCalledOnce();
  });
});
```

> `stubFetch`/`jsonResponse` 的真实签名以 `tests/helpers.tsx` 为准,断言方式相应微调。

- [ ] **Step 5: 验证 + 提交**

Run: `npm --prefix web test && npm --prefix web run build`

```bash
git add web/src/api web/tests/operationsFixtures.ts web/tests/overview-api.test.ts
git commit -m "feat(web): overview API types, query, and test fixtures"
```

---

## Task 5: ThroughputChart + overview-chart.css

**Files:**
- Create: `web/src/pages/overview/ThroughputChart.tsx`
- Create: `web/src/styles/features/overview-chart.css`
- Modify: `web/src/styles/index.css`(`@import "./features/overview-chart.css";`)
- Test: `web/tests/overview-chart.dom.test.tsx`(新;直接 render 组件,不走整页)

**Interfaces:**
- Produces: `ThroughputChart({ series, window }: { series: OverviewSeriesPoint[]; window: TimeWindowPreset })`

**图表形制(设计稿定死,亦符合无双轴原则)**:三行 small multiples——Evidence in / Facts kept / Recalls served,每行一条 bucket 条带,**各自按本行峰值归一化**,行首色块+行名承担系列身份(不靠颜色单独区分),行尾 `peak N`;条带下方一排 bucket 标签。系列色(token,三主题下都已定义):Evidence `--color-neutral-400`、Facts `--color-accent`、Recalls `--color-neutral-700`。每格带原生 `title`(`"{label} · {value}"`)做轻量 hover。无动画。

- [ ] **Step 1: 写失败的测试**

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ThroughputChart } from "../src/pages/overview/ThroughputChart";
import { makeOverview } from "./operationsFixtures";

describe("ThroughputChart", () => {
  it("renders one normalized bar per bucket per row with per-row peaks", () => {
    const series = makeOverview().series; // 6 buckets, known values
    render(<ThroughputChart series={series} window="1h" />);
    expect(screen.getByText("Evidence in")).toBeInTheDocument();
    expect(screen.getByText("Facts kept")).toBeInTheDocument();
    expect(screen.getByText("Recalls served")).toBeInTheDocument();
    // 3 rows x 6 buckets
    expect(document.querySelectorAll(".ov-chart-cell")).toHaveLength(18);
    // the peak bucket of the evidence row fills its track
    const evidenceBars = document.querySelectorAll('[data-row="evidence"] .ov-chart-bar');
    const heights = Array.from(evidenceBars).map((b) => (b as HTMLElement).style.height);
    expect(heights).toContain("100%");
  });

  it("labels buckets by window granularity", () => {
    const series = makeOverview().series;
    render(<ThroughputChart series={series} window="7d" />);
    // 7d buckets label as month-day, hour windows as HH:mm — assert one known label
    expect(document.querySelectorAll(".ov-chart-tick")).toHaveLength(series.length);
  });
});
```

- [ ] **Step 2: 运行确认失败**

Run: `npm --prefix web test -- overview-chart`
Expected: FAIL——模块不存在。

- [ ] **Step 3: 实现组件**

`web/src/pages/overview/ThroughputChart.tsx`:

```tsx
import type { OverviewSeriesPoint } from "../../api/types";
import type { TimeWindowPreset } from "../../lib/operations";

const ROWS = [
  { key: "evidence", label: "Evidence in", token: "var(--color-neutral-400)" },
  { key: "facts", label: "Facts kept", token: "var(--color-accent)" },
  { key: "recalls", label: "Recalls served", token: "var(--color-neutral-700)" },
] as const;

function bucketLabel(iso: string, window: TimeWindowPreset): string {
  const d = new Date(iso);
  if (window === "7d") {
    return `${d.getMonth() + 1}/${d.getDate()}`;
  }
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  return `${hh}:${mm}`;
}

/** Three small-multiple bar strips — one per measure, each normalized to its
 * own peak (shown at the right), so three different scales never share an
 * axis. Row swatch + name carry series identity; color is never the only cue. */
export function ThroughputChart({
  series,
  window,
}: {
  series: OverviewSeriesPoint[];
  window: TimeWindowPreset;
}) {
  return (
    <div className="ov-chart">
      <div className="ov-chart-note">
        Three separate scales — each row is drawn against its own peak, shown at the right.
      </div>
      {ROWS.map((row) => {
        const values = series.map((p) => p[row.key]);
        const peak = Math.max(1, ...values);
        return (
          <div key={row.key} data-row={row.key}>
            <div className="ov-chart-rowhead">
              <span className="ov-chart-series">
                <span className="ov-chart-swatch" style={{ background: row.token }} />
                {row.label}
              </span>
              <span className="ov-chart-peak">peak {Math.max(0, ...values)}</span>
            </div>
            <div className="ov-chart-track">
              {series.map((p) => (
                <div key={p.bucket_at} className="ov-chart-cell" title={`${bucketLabel(p.bucket_at, window)} · ${p[row.key]}`}>
                  <div
                    className="ov-chart-bar"
                    style={{ height: `${(p[row.key] / peak) * 100}%`, background: row.token }}
                  />
                </div>
              ))}
            </div>
          </div>
        );
      })}
      <div className="ov-chart-ticks">
        {series.map((p) => (
          <span key={p.bucket_at} className="ov-chart-tick">
            {bucketLabel(p.bucket_at, window)}
          </span>
        ))}
      </div>
    </div>
  );
}
```

- [ ] **Step 4: 样式**

`web/src/styles/features/overview-chart.css`(只引用 token/既有变量;同时容纳 Task 6/7 的区块网格类):

```css
/* Overview 吞吐图:三行 small multiples,行内各自归一化。 */
.ov-chart { display: grid; gap: 10px; }
.ov-chart-note { font-size: 11px; opacity: 0.5; }
.ov-chart-rowhead { display: flex; justify-content: space-between; font-size: 11px; margin-bottom: 3px; }
.ov-chart-series { display: flex; align-items: center; gap: 6px; }
.ov-chart-swatch { width: 9px; height: 9px; display: inline-block; }
.ov-chart-peak { opacity: 0.55; }
.ov-chart-track { display: flex; align-items: flex-end; gap: 10px; height: 46px; border-bottom: 1px solid var(--color-divider); }
.ov-chart-cell { flex: 1; height: 100%; display: flex; align-items: flex-end; justify-content: center; }
.ov-chart-bar { width: 100%; min-height: 1px; }
.ov-chart-ticks { display: flex; gap: 10px; }
.ov-chart-tick { flex: 1; text-align: center; font-size: 10px; opacity: 0.55; }

/* Overview 区块网格(Task 6/7 使用)。 */
.ov-metrics { display: grid; grid-template-columns: repeat(5, 1fr); border-top: 2px solid var(--color-divider); border-bottom: 2px solid var(--color-divider); }
.ov-mid { display: grid; grid-template-columns: 1.5fr 1fr 1fr; border-bottom: 2px solid var(--color-divider); }
.ov-bottom { display: grid; grid-template-columns: 1.55fr 1fr; }
.ov-block { padding: 18px 24px 20px; border-right: 1px solid var(--color-divider); }
.ov-block:last-child { border-right: 0; }
.ov-donut { width: 96px; height: 96px; border-radius: 50%; flex: none; }
.ov-writer-track { background: var(--color-neutral-200); height: 6px; }
.ov-writer-bar { height: 100%; background: var(--color-accent); }
@media (max-width: 900px) {
  .ov-metrics { grid-template-columns: repeat(2, 1fr); }
  .ov-mid, .ov-bottom { grid-template-columns: 1fr; }
  .ov-block { border-right: 0; border-bottom: 1px solid var(--color-divider); }
}
```

`web/src/styles/index.css` 在 features 导入区加 `@import "./features/overview-chart.css";`。

- [ ] **Step 5: 验证 + 提交**

Run: `npm --prefix web test -- overview-chart && npm --prefix web run build`

```bash
git add web/src/pages/overview/ThroughputChart.tsx web/src/styles
git add web/tests/overview-chart.dom.test.tsx
git commit -m "feat(web): hand-rolled small-multiples throughput chart for the overview"
```

---

## Task 6: OverviewPage 骨架 + 指标行 + 记忆构成 + 谁在写 + 路由切换

**Files:**
- Create: `web/src/pages/OverviewPage.tsx`、`web/src/pages/overview/hooks.ts`、`web/src/pages/overview/MetricsRow.tsx`、`web/src/pages/overview/NoteMixBlock.tsx`、`web/src/pages/overview/WhoIsWriting.tsx`
- Modify: `web/src/app/routes.tsx`(`/overview` 换挂 `OverviewPage`,删 `AdminPulsePage` import)
- Modify: `web/tests/app-shell.dom.test.tsx:93-115`、`web/tests/admin-explorer.dom.test.tsx:135`(Team Pulse 断言→Overview)
- Test: `web/tests/overview-page.dom.test.tsx`(新)

**Interfaces:**
- Consumes: Task 4 的 `getOverview`/类型/fixtures;Task 5 的 `ThroughputChart`;既有 `listOperationsAgentStats` + `timeWindow`(`lib/operations.ts`)、`usePolledRegion`、`Seg`、`MetricTile`、`Card`、`EmptyState`、`RegionError`、`currentTeam`(`lib/teams.ts`)、`formatTime`(`lib/format.ts`)
- Produces(Task 7 依赖):`OverviewPage` 内的区块插槽结构;`hooks.ts` 导出 `useOverviewRegion(window)`、`useWritersRegion(window)`(Task 7 再加 feed)

**页面结构**(设计稿):header(kicker `TODAY · {date}` + h1 团队名 + Live 点 + `Seg` 窗口选择)→ `.ov-metrics` 指标行 → `.ov-mid`(吞吐图 | 记忆构成 | 谁在写)→ `.ov-bottom`(attention | 事件流,Task 7 填,本 Task 先渲染空容器)。

- [ ] **Step 1: 写失败的测试**

`web/tests/overview-page.dom.test.tsx` 核心用例(fixture 走 Task 4 的 `renderOverviewPage`):

```tsx
// 1. 指标行渲染 fixture 值
it("renders the five metric tiles from the aggregate", async () => {
  await renderOverviewPage({ overview: () => jsonResponse(makeOverview()) , /* ...agents/events 分支给最小成功响应... */ });
  expect(screen.getByText("Evidence captured")).toBeInTheDocument();
  expect(screen.getByText("Needs a person")).toBeInTheDocument();
  // makeOverview() 的已知 attention_count 值
});

// 2. 窗口切换带参重取且桶数变化
it("refetches with the selected window", async () => { /* 点 7d,断言 fetch 收到 window=7d,ticks 变 7 个 */ });

// 3. 区块隔离:overview 聚合 500,who-is-writing 仍渲染
it("keeps the writers block alive when the aggregate fails", async () => {
  await renderOverviewPage({ overview: () => apiErrorResponse(500, "internal", "boom"), /* agents 正常 */ });
  // 指标区显示 RegionError,writers 区显示 fixture agent 名
});

// 4. note_mix 为空是空态不是错误(Admin 场景)
it("renders a positive empty state for an empty note mix", async () => {
  await renderOverviewPage({ overview: () => jsonResponse(makeOverview({ note_mix: [] })) });
  expect(screen.getByText(/No live notes/)).toBeInTheDocument();
});

// 5. 无能力重定向(照抄 app-shell 既有 gating 断言方式)
it("redirects to /management without view.operations", async () => { /* me 无 capability,断言 Management 页 heading */ });
```

(逐条写完整——上面省略号处用 `agentStatsPage([...])` / `eventsPage([...])` 既有 fixture 填。)

- [ ] **Step 2: 运行确认失败**

Run: `npm --prefix web test -- overview-page`
Expected: FAIL——OverviewPage 不存在(fixture 路由已在 Task 4 就绪)。

- [ ] **Step 3: hooks**

`web/src/pages/overview/hooks.ts`:

```ts
import { getOverview, listOperationsAgentStats } from "../../api/queries";
import type { OverviewResponse, OperationsAgentStats } from "../../api/types";
import { usePolledRegion } from "../../lib/useRegion";
import { timeWindow, type TimeWindowPreset } from "../../lib/operations";

export const OVERVIEW_POLL_MS = 10_000;

export function useOverviewRegion(window: TimeWindowPreset) {
  return usePolledRegion<OverviewResponse>(
    (signal) => getOverview(window, signal),
    OVERVIEW_POLL_MS,
    [window],
  );
}

export function useWritersRegion(window: TimeWindowPreset) {
  return usePolledRegion<OperationsAgentStats[]>(
    async (signal) => {
      const page = await listOperationsAgentStats(timeWindow(window), signal);
      return page.items;
    },
    OVERVIEW_POLL_MS,
    [window],
  );
}
```

> `usePolledRegion` 回调的真实形参(是否带 prev、onAuthError 位置)以 `lib/useRegion.ts` 为准,微调而不改该文件。

- [ ] **Step 4: 区块组件**

`MetricsRow.tsx`(`MetricTile` 的 props 是 `{label, value, unit?, note?}`):

```tsx
import { MetricTile } from "../../components/MetricTile";
import type { OverviewMetrics } from "../../api/types";

function seconds(ms: number | undefined): string {
  return ms === undefined ? "—" : `${(ms / 1000).toFixed(1)}`;
}

export function MetricsRow({ metrics }: { metrics: OverviewMetrics }) {
  const acceptPct = Math.round(metrics.recall_accept_rate * 100);
  return (
    <div className="ov-metrics">
      <MetricTile label="Evidence captured" value={metrics.evidence_captured} note="evidence events written to the lake" />
      <MetricTile label="Facts in circulation" value={metrics.live_notes} note={`live team notes · ${metrics.notes_expiring_today} expiring in 24h`} />
      <MetricTile label="Context handed to agents" value={metrics.recalls_served} note={`recalls served · ${acceptPct}% with evidence`} />
      <MetricTile label="Time to remember" value={seconds(metrics.p95_ms)} unit="s" note={`p95 · p50 ${seconds(metrics.p50_ms)}s`} />
      <MetricTile label="Needs a person" value={metrics.attention_count} note="items in the queue below" />
    </div>
  );
}
```

`NoteMixBlock.tsx`——conic-gradient 环图 + 图例。kind→token 固定顺序(不随数据轮换,>4 kind 折入 Other):

```tsx
import type { OverviewNoteMixEntry } from "../../api/types";
import { Card } from "../../components/Card";
import { EmptyState } from "../../components/EmptyState";

const KIND_TOKENS = [
  "var(--color-accent)",
  "var(--color-neutral-700)",
  "var(--color-accent-2-500)",
  "var(--color-neutral-400)",
] as const;

export function NoteMixBlock({ mix }: { mix: OverviewNoteMixEntry[] }) {
  if (mix.length === 0) {
    return <EmptyState title="No live notes yet" body="The mix fills in as agents write team notes." />;
  }
  const top = mix.slice(0, KIND_TOKENS.length - 1);
  const rest = mix.slice(KIND_TOKENS.length - 1);
  const entries = rest.length > 0
    ? [...top, { kind: "other", count: rest.reduce((n, e) => n + e.count, 0), pct: rest.reduce((n, e) => n + e.pct, 0) }]
    : mix;
  let acc = 0;
  const stops = entries.map((entry, i) => {
    const from = acc;
    acc += entry.pct;
    return `${KIND_TOKENS[i]} ${from}% ${acc}%`;
  });
  const total = mix.reduce((n, e) => n + e.count, 0);
  return (
    <Card title="What the team remembers" meta={`${total} live facts`}>
      <div className="row" style={{ alignItems: "center", gap: 22 }}>
        <div className="ov-donut" style={{ background: `conic-gradient(${stops.join(", ")})` }} role="img" aria-label="Live note mix by kind" />
        <div className="stack" style={{ gap: 9, flex: 1 }}>
          {entries.map((entry, i) => (
            <div key={entry.kind} className="row" style={{ gap: 8, fontSize: 12 }}>
              <span className="ov-chart-swatch" style={{ background: KIND_TOKENS[i] }} />
              <span style={{ flex: 1 }}>{entry.kind}</span>
              <b>{entry.count}</b>
              <span style={{ opacity: 0.5 }}>{Math.round(entry.pct)}%</span>
            </div>
          ))}
        </div>
      </div>
    </Card>
  );
}
```

> 上面 `.row`/`.stack` 若不接受 inline gap,以 `layout.css` 现有工具类为准换成对应类;禁止为此新发明全局类。`--color-accent-2-500` 若 token 名不同(查 `tokens.css` 的 accent-2 梯级命名),用实际名。

`WhoIsWriting.tsx`——`events_written` 降序取前 5,条按最大值归一化:

```tsx
import type { OperationsAgentStats } from "../../api/types";
import { Card } from "../../components/Card";
import { EmptyState } from "../../components/EmptyState";

export function WhoIsWriting({ agents }: { agents: OperationsAgentStats[] }) {
  const writers = [...agents].sort((a, b) => b.events_written - a.events_written).slice(0, 5);
  if (writers.length === 0) {
    return <EmptyState title="No agent activity yet" body="Writers appear as agents record sessions." />;
  }
  const peak = Math.max(1, ...writers.map((w) => w.events_written));
  return (
    <Card title="Who is writing" meta="evidence events">
      <div className="stack" style={{ gap: 12 }}>
        {writers.map((w) => (
          <div key={w.agent_id}>
            <div className="row" style={{ justifyContent: "space-between", fontSize: 12 }}>
              <span>{w.display_name || w.agent_id}</span>
              <b>{w.events_written}</b>
            </div>
            <div className="ov-writer-track"><div className="ov-writer-bar" style={{ width: `${(w.events_written / peak) * 100}%` }} /></div>
            <div style={{ fontSize: 11, opacity: 0.5 }}>{w.notes_authored} notes authored</div>
          </div>
        ))}
      </div>
    </Card>
  );
}
```

- [ ] **Step 5: 页面组装 + 路由**

`OverviewPage.tsx`:窗口 state `useState<TimeWindowPreset>("24h")`;header 用 `Seg`(`label="Time window"`, options 1h/24h/7d);h1 = `currentTeam(me)?.name ?? "Overview"`(`me` 按其他页面的取用方式,props 或 context,以 `routes.tsx` 挂法为准);kicker 行 `TODAY · {new Date().toLocaleDateString()}`。三段网格:

- `.ov-metrics`:overview region ready→`<MetricsRow/>`;loading→既有 loading 写法;error(无旧数据)→`<RegionError error={region.error} onRetry={region.retry}/>`;ready 且 `region.error`→数据 + `.note.warn` stale 横幅(逐区块,照抄 `AdminOperationsPage`)。
- `.ov-mid`:三个 `.ov-block`——`<ThroughputChart series window/>`(同 overview region)、`<NoteMixBlock mix/>`(同 region)、`<WhoIsWriting agents/>`(writers region,独立失败面)。
- `.ov-bottom`:本 Task 渲染两个空 `.ov-block` 容器占位(`<section aria-label="Needs your attention"/>`、`<section aria-label="What just happened"/>`),Task 7 填。

`routes.tsx`:`/overview` 的 element 从 `AdminPulsePage` 换成 `OverviewPage`(`RequireCapability` 包裹保持不变),删除 `AdminPulsePage` import(文件本身 Task 8 再删)。更新 `app-shell.dom.test.tsx` 与 `admin-explorer.dom.test.tsx` 里对 `/overview` 的 Team Pulse 断言为 Overview 的 h1。

- [ ] **Step 6: 验证 + 提交**

Run: `npm --prefix web test && npm --prefix web run build`
Expected: 新测试 5 条全绿;app-shell/admin-explorer 更新后的断言绿;admin-pulse.dom.test.tsx 仍绿(页面文件还在,只是不再挂路由——它用 `renderApp({route:"/overview"})` 的话会开始失败,**若失败,在本 Task 先把它改为直接 render `<AdminPulsePage/>` 或标 `it.skip` 并注明 Task 8 删除**,不许留红)。

```bash
git add web/src/pages web/src/app/routes.tsx web/tests
git commit -m "feat(web): overview landing page — metrics, throughput, note mix, writers"
```

---

## Task 7: attention 队列 + Access 汇总条 + 事件流

**Files:**
- Create: `web/src/pages/overview/AttentionQueue.tsx`、`web/src/pages/overview/EventsFeed.tsx`
- Modify: `web/src/pages/overview/hooks.ts`(移植 feed hook)、`web/src/pages/OverviewPage.tsx`(填 `.ov-bottom`)
- Test: `web/tests/overview-page.dom.test.tsx`(追加用例)

**Interfaces:**
- Consumes: overview region 的 `attention`;既有 `listOperationEvents`(事件流)、`listAllMembers`/`listDevices`/`listAdminAgents`(Access 条,一次性);`AdminPulsePage.tsx:105-174` 的 `useFeedRegion` + `pulse/LiveEventsFeed.tsx` 的 held 模式(移植后 Task 8 删原件)
- Produces: 完整六区块页面

- [ ] **Step 1: 写失败的测试(追加到 overview-page.dom.test.tsx)**

```tsx
// 6. attention 渲染 + CTA 跳转
it("renders attention items and navigates on the CTA", async () => {
  // makeOverview 默认含 {kind:"finding",severity:"high",target:"/governance/sessions",...}
  // 点 CTA 后断言落到 session audit 页 heading(fixture 需喂该页端点或断言 URL)
});

// 7. attention 空 → 正向空态
it("shows the positive empty state when nothing needs attention", async () => {
  await renderOverviewPage({ overview: () => jsonResponse(makeOverview({ attention: [], metrics: { ...makeOverview().metrics, attention_count: 0 } })) });
  expect(screen.getByText("Nothing needs you right now")).toBeInTheDocument();
});

// 8. 事件流 held 模式:滚动后轮询到新事件 → 横幅出现,点 Show 应用
it("holds new events while the reader has scrolled", async () => { /* 照抄 admin-pulse.dom.test.tsx 的既有 held 用例改造 */ });

// 9. 事件流失败只影响该区块
it("isolates an events-feed failure", async () => { /* events 分支 500,断言 feed 区 RegionError、指标区照常 */ });
```

- [ ] **Step 2: 运行确认失败**

Run: `npm --prefix web test -- overview-page`

- [ ] **Step 3: AttentionQueue**

severity→两色制:`high`/`critical` → `.tag.tag-attention`(accent),其余 `.tag`(neutral)——不引入四色梯度(设计系统约束)。CTA 文案按 kind:finding→`Review`、quarantine→`Inspect`、invitation/enrollment→`Manage`;点击 `navigate(item.target)`(react-router `useNavigate`)。空态 `EmptyState({ title: "Nothing needs you right now", body: "Findings, quarantines, and expiring access will land here." })`。列表项结构照设计:tag | title/body/`ref`(mono, opacity .45) | CTA `Button variant secondary`(以 `Button.tsx` 实际 variant API 为准)。

底部 **Access 汇总条**:挂载时一次性 `Promise.allSettled([listAllMembers(), listDevices({}), listAdminAgents({})])`(**实参形状以 `queries.ts` 各函数真实签名为准**,列表长度即计数,>100 截断值可接受);全部 fulfilled 才渲染 `"{p} people · {d} machines · {a} agents"` + `Open Management →`(Link to `/management`);任一 rejected 则整条不渲染(静默,不占 region 状态)。不轮询。

- [ ] **Step 4: EventsFeed 移植**

把 `AdminPulsePage.tsx` 的 `useFeedRegion`(105-174 行:`scrolledRef`、`pending` 暂存、`freshIds`)移植进 `pages/overview/hooks.ts`,`LiveEventsFeed.tsx` 的渲染移植为 `EventsFeed.tsx`,保持机制,只改壳与文案:

- 横幅文案改设计稿:`{n} new events held while you read` + `Button` 文案 `Show`;
- 容器 id 换 `overview-feed`;样式类不再用 `.pulse-feed*`(pulse.css 要删),沿用 `.note`/工具类 + `overview-chart.css` 里补最小必要的 `.ov-feed-item`(含 reduced-motion 下关闭动画——若保留 slide-in/flash 动画,连同 keyframes 迁进 `overview-chart.css`);
- 底部链接行:`Full activity stream`→`/governance/audit`、`Pipeline health`→`/governance/pipeline`(用 `Link`);
- 每页 20 条(`FEED_SIZE` 常量随迁)。

`.ov-bottom` 两个容器填实;attention 区标题 `Needs your attention`,事件区 `What just happened`。

- [ ] **Step 5: 验证 + 提交**

Run: `npm --prefix web test && npm --prefix web run build`

```bash
git add web/src/pages web/src/styles/features/overview-chart.css web/tests/overview-page.dom.test.tsx
git commit -m "feat(web): overview attention queue, access strip, and held-events feed"
```

---

## Task 8: 删除 Pulse + 清理 + 全量验收

**Files:**
- Delete: `web/src/pages/AdminPulsePage.tsx`、`web/src/pages/pulse/`(整目录)、`web/src/styles/features/pulse.css`、`web/tests/admin-pulse.dom.test.tsx`、`web/src/lib/useCountUp.ts`
- Modify: `web/src/styles/index.css`(去掉 pulse.css import)、`web/src/lib/operations.ts`(删 135-168 行 Pulse 段:`AgentActivity`/`ACTIVE_MS`/`RECENT_MS`/`agentActivity`/`relativeAge`)、`web/src/styles/tokens.css:107` 与 `web/src/styles/components.css:185`(注释里的 pulse 例子改成 overview)
- Test: 既有全量

- [ ] **Step 1: 删除与清理**

按上表删;`grep -rn "pulse\|Pulse\|useCountUp\|agentActivity\|relativeAge" web/src web/tests` 清到只剩无关命中(如注释已改)。`legacyRoutes.ts:22` 的 `/admin/pulse → /overview` **保留不动**。

- [ ] **Step 2: 全量验证**

Run: `npm --prefix web test && npm --prefix web run build`
Expected: 全绿;`tsc --noEmit` 无未用导出/断链。
Run: `make lint && make test-unit`(后端未动也要跑,门禁习惯)
Run: `grep -rn "AdminPulsePage" web/ || echo CLEAN`
Expected: CLEAN。

- [ ] **Step 3: 验收清单(逐条核,写进 PR 描述)**

- [ ] 断掉任一区块端点其余仍可用(测试 3/9 覆盖,手工再过一遍 dev server 更好)
- [ ] 1h/24h/7d 切换正确(桶数 6/8/7,测试 2 覆盖)
- [ ] 无 `view.operations` 时顶栏无 Overview 项(阶段 1 已有 navModel 测试)且直连 `/overview` 重定向(测试 5)
- [ ] attention 空为正向空态(测试 7)
- [ ] Admin 的 note_mix 空渲染空态而非错误(测试 4;后端 Task 1 放开后 Admin 实际拿得到数据,空态仅剩真实无笔记场景)
- [ ] spec §6.1 八条红线逐条勾选进 PR 描述
- [ ] `/admin/pulse` 旧链接仍落 `/overview`(legacy-routes 测试)

- [ ] **Step 4: 提交**

```bash
git add -A web
git commit -m "feat(web)!: replace the pulse page with the overview landing page"
```

---

## 阶段 2b 完成标准

- [ ] `make lint`、`make test-unit`、`npm --prefix web test`、`npm --prefix web run build` 全绿
- [ ] `/overview` 渲染六区块真页面,10s 轮询,三 region 独立降级
- [ ] `notes_expiring_today` 显示真实计数(Task 2),Admin 能看到 note mix(Task 1)
- [ ] Pulse 全部代码与样式删净,旧路由重定向保留
- [ ] 阶段 3(Management 访问树)另出计划

## 已知遗留(本计划有意不做)

- 7d 窗口在短 retention 部署 400:沿用 generic-400 + title 提示;retention 暴露给前端另立议题。
- 后端 attention 排序 tie-break(时间升序)与「已过期 pending 邀请仍列出」维持 2a 现状;产品若要改在阶段 3 前单独提。
- 设计稿 header 副标题(「memory pipeline nominal · last extraction 40s ago」)无数据源,本阶段不做;Access 条已覆盖人/机/Agent 计数。
