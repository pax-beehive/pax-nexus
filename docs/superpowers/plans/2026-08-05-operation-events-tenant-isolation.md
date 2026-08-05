# Operation Events 租户隔离 实施计划

> **STATUS (2026-08-05): COMPLETE.** All four tasks landed on `feat/portal-modernist-phase2`:
> `7c2289f` (Task 1: scope column + attribution), `3d3c514` (Task 2: filter every read),
> `2599136` (fix: scope the recall-diagnostic fallback's cross-tenant existence check),
> `424407f` (Task 3: attribute recorded events to the acting team), `019880d` (Task 4:
> cross-tenant isolation through the service layer). The checkboxes below were left unticked
> by the implementer and do not reflect this — treat this header, not the checkboxes, as the
> source of truth for what is done.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `onprem_operation_events` 携带 `scope_id`，写入时记录事件所属团队、读取时按调用方团队过滤，消除多团队部署下 Operations 数据的跨租户可见。

**Architecture:** 事件自带 scope（`operations.Event.ScopeID`），与仓库里 `platformllm.UsageEvent.ScopeID` 的既有形状一致；写入端三个调用点各自解析出 scope，读取端沿用 store 构造时绑定的 `s.scopeID`。recorder 保持进程级单例，不改成按 scope 构造。

**Tech Stack:** Go + pgx/PostgreSQL。

## 背景与证据

`onprem_operation_events`（`migrations/018_onprem_operations.sql`）没有 `scope_id` 列，读它的查询也都没有 scope 过滤：

- `scanOperationSummary` —— WHERE 只有时间窗 + 可选 `actor_agent_id`
- `ListEvents` —— WHERE 只有时间窗 + kind/outcome/agent
- `scanAgentStats` 的事件 CTE —— 同上
- `Series`（2026-08-05 新增）—— 同上

后果：多团队 SaaS 下任一 team 的 owner 在 Operations 与 Overview 能看到**其他 team** 的
observation/recall 计数、p50/p95、错误数，以及逐条事件的 `actor_agent_id` / `actor_user_id` /
`actor_membership_id` / `session_id`。

同一端点的另一半是正确隔离的（`scanExtractionSummary`、`AgentStats` 的笔记部分、
`RecallDiagnostic`、storage 都带 `scope_id = $N`），SaaS 读侧的
`scopedOperationsService.forPrincipal` 也确实按请求方 team 绑了 scope——只是那个 scope 对一张
没有 scope 列的表不起作用。混合数据是它长期未被发现的原因。

不是某一次改动引入的：随 operations 控制台（#69/#70）就存在，SaaS scope 化（#68）与多租户
加固（#71）都聚焦在有 scope 列的卫星服务上。

## Global Constraints

- **不改变任何事件的语义**，只增加归属信息。计数口径、outcome 判定、延迟分位的算法一律不动。
- **写入端三个调用点各自解析 scope**，不要把 recorder 改成按 scope 构造的工厂——那会让调用链每
  一层都得传 scope，而事件自带归属是仓库既有形状（见 `platformllm.UsageEvent.ScopeID`）。
- **依赖规则**：`internal/deployment` 的允许导入列表是 `{"explorer", "operations", "teamnote/runtime"}`，
  **不含** `teamnote` 或 `session`。所以 `extraction_observer.go` **不能**自己调
  `teamnote.ScopeFromContext`；解析器由组合根 `internal/app` 注入，与同处的
  `extractionUsageRecorder` 完全同构。
- **回填用 `onprem.LocalScopeID`**（值为 `local-team`）。既有单团队部署的历史事件全部属于那个 scope，
  回填后语义正确；多团队部署的历史事件无法追溯归属，同样落到该 scope 并因此对所有团队不可见——
  这是有意的保守选择，宁可少显示也不要错误归属。
- **索引必须跟上**。现有两个索引以 `started_at` 或 `(operation_kind, outcome, started_at)` 打头；
  加了 scope 过滤后必须有以 `scope_id` 打头的等价索引，否则每次查询退化成全表扫。
- **测试命令**：`make test-unit`；Postgres 适配器测试需要真实库，用
  `TEAM_MEMORY_TEST_POSTGRES_DSN='postgres://team_memory:team_memory@127.0.0.1:55432/team_memory?sslmode=disable'`。
  **注意：缺这个环境变量时整个 suite 静默 skip 但仍打印 `ok`**——跑测试必须带上它并确认子测试真的执行了。
  `make lint` 必须绿。
- **提交粒度**：每个 Task 末尾提交一次。

## 连带影响（务必知情）

修复落地后 **Overview 与 Operations 的数字会变小**（只剩本团队）。阶段 2a Task 1 已有的
`TestOperationsSeriesSuite` 断言基于"事件表无 scope 过滤"，其中 `Evidence`/`Recalls` 相关断言
需要跟着调整；`facts` 相关断言不受影响（那半本来就隔离）。这是正确方向，不要误判为回归。

---

## File Structure

| 文件 | 变化 |
|---|---|
| `internal/platform/postgres/migrations/031_operation_events_scope.sql` | 新建：加列、回填、置 NOT NULL、建索引 |
| `internal/operations/operations.go` | `Event` 加 `ScopeID`；`Validate` 要求非空 |
| `internal/platform/postgres/operations.go` | `Record` 写入 `event.ScopeID`；三处读查询加 `scope_id = $N` |
| `internal/platform/postgres/operations_series.go` | 事件 CTE 加 `scope_id = $N`（facts CTE 已有） |
| `internal/teamnote/transport/httpapi/handler/onprem_endpoints.go` | 记录事件时带上 principal 的 scope |
| `internal/deployment/onprem/extraction_observer.go` | 接收注入的 scope 解析器并填 `ScopeID` |
| `internal/app/app.go` | 构造 observer 时注入解析器 |
| `internal/app/operations.go` | 保留期事件写系统 scope |

---

## Task 1: 表结构与事件归属

**Files:**
- Create: `internal/platform/postgres/migrations/031_operation_events_scope.sql`
- Modify: `internal/operations/operations.go`
- Modify: `internal/platform/postgres/operations.go`（`Record` 的 INSERT）
- Test: `internal/platform/postgres/operations_test.go`（既有 suite 追加）

**Interfaces:**
- Produces: `operations.Event.ScopeID string`；`Event.Validate()` 在 `ScopeID` 为空时返回
  `ErrInvalidInput` 包装的错误；`Record` 将其写入新列。

- [ ] **Step 1: 写迁移**

创建 `internal/platform/postgres/migrations/031_operation_events_scope.sql`。分三步而不是一步
`ADD COLUMN NOT NULL`，这样已有数据的库也能安全升级：

```sql
-- Operation events were written without an owning scope, so in a multi-team
-- deployment every team's Operations view read every other team's rows. The
-- backfill attributes all historical rows to the single-tenant scope: for an
-- on-prem install that is exactly right, and for a multi-team install the
-- attribution is unrecoverable, so parking them where no team sees them is the
-- conservative choice.
ALTER TABLE onprem_operation_events
    ADD COLUMN IF NOT EXISTS scope_id TEXT;

UPDATE onprem_operation_events SET scope_id = 'local-team' WHERE scope_id IS NULL;

ALTER TABLE onprem_operation_events
    ALTER COLUMN scope_id SET NOT NULL;

ALTER TABLE onprem_operation_events
    ADD CONSTRAINT onprem_operation_events_scope_not_blank
    CHECK (btrim(scope_id) <> '');

-- The two pre-existing indexes lead with started_at / operation_kind. Every
-- read now filters on scope_id first, so without these the queries degrade to
-- a full scan once more than one scope exists.
CREATE INDEX IF NOT EXISTS onprem_operation_events_scope_time_idx
    ON onprem_operation_events (scope_id, started_at DESC, operation_event_id DESC);

CREATE INDEX IF NOT EXISTS onprem_operation_events_scope_kind_outcome_idx
    ON onprem_operation_events (
        scope_id, operation_kind, outcome, started_at DESC, operation_event_id DESC
    );
```

> 迁移文件的编号与命名必须延续既有序列（当前最大是 `030_saas_team_devices.sql`）。若迁移是
> 按嵌入目录顺序执行的，确认新文件被自动收录，不需要另注册。

- [ ] **Step 2: 写失败的测试**

在 `internal/platform/postgres/operations_test.go` 的既有 suite 里追加。**不要新建 suite**：

```go
// An event must carry the scope it belongs to. Recording without one is a
// programming error, not a runtime condition — catching it at the boundary
// stops unattributed rows from entering the table at all.
func (s *operationsStoreSuite) TestRecordRejectsAnEventWithoutAScope() {
	attempt, err := operations.NewAttemptID()
	s.Require().NoError(err)
	_, err = s.operations.Record(context.Background(), operations.Event{
		AttemptID:   attempt,
		Kind:        operations.KindObservationObserve,
		Outcome:     operations.OutcomeSucceeded,
		Actor:       operations.Actor{Kind: "agent", AgentID: "scope-test-agent"},
		StartedAt:   s.now,
		CompletedAt: s.now,
	})
	s.Require().Error(err)
}

// The recorded row must carry the scope from the EVENT, not the one the store
// happens to be constructed with — the writer is a process-level singleton
// serving every team, so binding to the store's scope would attribute every
// team's traffic to whichever scope the process was wired with.
func (s *operationsStoreSuite) TestRecordPersistsTheEventsOwnScope() {
	ctx := context.Background()
	attempt, err := operations.NewAttemptID()
	s.Require().NoError(err)
	recorded, err := s.operations.Record(ctx, operations.Event{
		ScopeID:     "some-other-team",
		AttemptID:   attempt,
		Kind:        operations.KindObservationObserve,
		Outcome:     operations.OutcomeSucceeded,
		Actor:       operations.Actor{Kind: "agent", AgentID: "scope-test-agent"},
		StartedAt:   s.now,
		CompletedAt: s.now,
	})
	s.Require().NoError(err)

	var stored string
	err = s.store.Pool().QueryRow(ctx,
		`SELECT scope_id FROM onprem_operation_events WHERE operation_event_id = $1`,
		recorded.ID,
	).Scan(&stored)
	s.Require().NoError(err)
	s.Equal("some-other-team", stored)
}
```

> `recorded.ID` 的真实字段名以 `operations.Event` 为准（可能叫 `EventID`）。`s.store.Pool()`
> 是否导出同样以既有代码为准；若没有，用 suite 已有的取行方式。

- [ ] **Step 3: 运行测试确认失败**

Run:
```
TEAM_MEMORY_TEST_POSTGRES_DSN='postgres://team_memory:team_memory@127.0.0.1:55432/team_memory?sslmode=disable' \
  go test ./internal/platform/postgres -run 'TestOperationsStoreSuite/TestRecord.*Scope' -count=1 -v
```
Expected: 编译失败——`operations.Event` 没有 `ScopeID` 字段。

- [ ] **Step 4: 加域字段与校验**

在 `internal/operations/operations.go` 的 `Event` 结构体最前面加字段（放最前是因为它是归属信息，
不是度量）：

```go
	// ScopeID is the team the attempt belongs to. The recorder is a
	// process-level singleton shared by every team, so the scope travels on the
	// event rather than on the store — same shape as platformllm.UsageEvent.
	ScopeID string
```

在 `Validate()` 里，与既有的必填校验并列加一条：`strings.TrimSpace(e.ScopeID) == ""` 时返回
包装 `ErrInvalidInput` 的错误，措辞与该函数里既有的必填校验保持一致。

- [ ] **Step 5: 让 Record 写入**

修改 `internal/platform/postgres/operations.go` 的 `Record`：在列清单最前面加 `scope_id`，
`VALUES` 增加一个占位符并整体后移，参数列表最前面加 `event.ScopeID`。**逐个核对占位符编号**——
这个 INSERT 有 25 个参数，错位一个就会把数据写进相邻列而不报错。

- [ ] **Step 6: 运行测试确认通过**

Run: 同 Step 3 的命令
Expected: 两个用例 PASS。

- [ ] **Step 7: 修补现有调用点使其编译**

`Validate` 现在要求 `ScopeID`，所有构造 `Event` 的地方（含测试）都会开始失败。本 Task 只做**最小**
修补让编译与既有测试通过：给现有的三个生产调用点与测试夹具填上 `onprem.LocalScopeID` 或测试用的
scope。**真正的按调用方解析放在 Task 3**，不要在这里提前做。

Run: `make test-unit`
Expected: 0 失败。

- [ ] **Step 8: 提交**

```bash
git add internal/platform/postgres/migrations/031_operation_events_scope.sql
git add -u
git commit -m "feat(operations): give operation events an owning scope"
```

---

## Task 2: 读侧过滤与隔离测试

**Files:**
- Modify: `internal/platform/postgres/operations.go`（`scanOperationSummary`、`ListEvents`、`scanAgentStats` 的事件 CTE）
- Modify: `internal/platform/postgres/operations_series.go`（事件 CTE）
- Test: `internal/platform/postgres/operations_test.go`、`operations_series_test.go`

**Interfaces:**
- Consumes: Task 1 的 `scope_id` 列
- Produces: 四条读查询全部按 `s.scopeID` 过滤

- [ ] **Step 1: 写失败的隔离测试**

在 `operations_test.go` 追加。这是本计划的**核心断言**——它直接对应被修复的泄漏：

```go
// The leak this migration closes: two teams' events live in one table, and
// before the scope predicate every team's Operations view counted every other
// team's traffic. Summary, ListEvents and AgentStats must each see only their
// own scope.
func (s *operationsStoreSuite) TestReadsAreScopeIsolated() {
	ctx := context.Background()
	window := operations.TimeFilter{From: s.now.Add(-time.Hour), To: s.now.Add(time.Hour)}

	s.recordScopedObservation(ctx, s.scope, "own-agent")
	s.recordScopedObservation(ctx, "a-different-team", "foreign-agent")

	summary, err := s.operations.Summary(ctx, window, s.now)
	s.Require().NoError(err)
	s.Equal(int64(1), summary.Observations.Requests)

	events, err := s.operations.ListEvents(ctx, operations.EventFilter{TimeFilter: window, Limit: 50})
	s.Require().NoError(err)
	s.Require().Len(events, 1)
	s.Equal("own-agent", events[0].Actor.AgentID)
}
```

`recordScopedObservation(ctx, scopeID, agentID string)` 是本文件里的新 helper，构造一个带
`ScopeID` 的 `operations.Event` 并 `Record`。

在 `operations_series_test.go` 追加对应的一条：另一个 scope 的事件不得进入 `Evidence`/`Recalls`。

- [ ] **Step 2: 运行测试确认失败**

Run:
```
TEAM_MEMORY_TEST_POSTGRES_DSN='...' go test ./internal/platform/postgres \
  -run 'TestOperationsStoreSuite/TestReadsAreScopeIsolated|TestOperationsSeriesSuite' -count=1 -v
```
Expected: FAIL——两个 scope 的事件都被算进去（Requests 是 2，ListEvents 返回 2 条）。

- [ ] **Step 3: 加过滤**

四处各加一个 `scope_id` 谓词，参数用 `s.scopeID`：

- `scanOperationSummary`：`WHERE started_at >= $1 AND started_at < $2 AND ($3 = '' OR actor_agent_id = $3)`
  → 追加 ` AND scope_id = $4`，参数列表末尾补 `s.scopeID`
- `ListEvents`：同样追加，注意该查询已有 6 个占位符，新参数排在最后
- `scanAgentStats` 的事件 CTE：追加
- `operations_series.go` 的 `events` CTE：追加。**`facts` CTE 已经有 `revisions.scope_id = $5`，
  不要动它**，也不要因为"看起来重复"而合并两个 scope 参数——它们来自不同的表。

**逐个核对占位符编号**：这几条查询的参数是位置绑定的，插错位置不会报错，只会静默返回错误结果。

- [ ] **Step 4: 运行测试确认通过**

Run: 同 Step 2
Expected: PASS。

- [ ] **Step 5: 验证索引真的被用上**

Run:
```
TEAM_MEMORY_TEST_POSTGRES_DSN='...' go test ./internal/platform/postgres -run TestOperationsStoreSuite -count=1
```
然后手工确认查询计划走了新索引——连上库执行：

```sql
EXPLAIN SELECT count(*) FROM onprem_operation_events
WHERE scope_id = 'local-team' AND started_at >= now() - interval '1 day' AND started_at < now();
```

Expected: 计划里出现 `onprem_operation_events_scope_time_idx`（数据量极小时 Postgres 可能仍选
Seq Scan，那是正常的——把实际输出贴进报告，不要为了让计划"好看"去造数据）。

- [ ] **Step 6: 全量验证并提交**

Run: `make test-unit`、`make lint`

```bash
git add -u
git commit -m "fix(operations): filter every operation-event read by scope"
```

---

## Task 3: 写侧三个调用点

**Files:**
- Modify: `internal/teamnote/transport/httpapi/handler/onprem_endpoints.go`
- Modify: `internal/deployment/onprem/extraction_observer.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/operations.go`
- Test: `internal/deployment/onprem/extraction_observer_test.go`、handler 既有测试

**Interfaces:**
- Consumes: Task 1 的 `Event.ScopeID`
- Produces: `onprem.NewExtractionObserver(recorder operations.Recorder, scopeOf func(context.Context) string, logger *slog.Logger) func(context.Context, teamruntime.ExtractionObservation)`

**背景**：三个调用点各自的 scope 来源不同，且 `deployment/onprem` **不被允许**导入 `teamnote` 或
`session`（依赖规则里 `deployment` 的允许列表是 `{"explorer", "operations", "teamnote/runtime"}`），
所以 observer 不能自己读 context 里的 scope，必须由组合根注入解析器。

- [ ] **Step 1: HTTP 路径**

`internal/teamnote/transport/httpapi/handler/onprem_endpoints.go` 记录事件处，把已认证 principal
的 scope 填进 `event.ScopeID`。该函数已持有 principal（同一处已用它做鉴权），直接取用即可。

- [ ] **Step 2: 抽取 observer 接收解析器**

修改 `NewExtractionObserver` 的签名，新增一个 `scopeOf func(context.Context) string` 参数，
在构造 `operations.Event` 时填 `ScopeID: scopeOf(ctx)`。observer 自身不 import `teamnote`/`session`。

在 `internal/deployment/onprem/extraction_observer_test.go` 加一条：注入一个返回固定值的解析器，
断言记录到的事件带着那个 scope。若该文件不存在则新建，用 fake recorder 捕获事件。

- [ ] **Step 3: 组合根注入**

`internal/app/app.go` 构造 observer 处（当前是 `onprem.NewExtractionObserver(operationRecorder, logger)`），
改为传入一个解析器。**照抄同仓库 `internal/app/wiring.go` 里 `extractionUsageRecorder` 的写法**——
它已经在解决同一个问题：

```go
// The extraction context carries the scope; fall back to the single-tenant
// scope when it does not, exactly as extractionUsageRecorder does.
func extractionEventScope(ctx context.Context) string {
	scopeID, err := teamnote.ScopeFromContext(ctx)
	if err != nil || strings.TrimSpace(scopeID) == "" {
		return onprem.LocalScopeID
	}
	return scopeID
}
```

放在 `internal/app` 里（该包被允许导入 `teamnote`），构造 observer 时传进去。

- [ ] **Step 4: 保留期任务**

`internal/app/operations.go` 记录 `system.retention` 事件处，填
`ScopeID: onprem.LocalScopeID`。保留期清理是进程级维护动作，不属于任何团队；写在系统 scope 下
意味着 SaaS 各团队看不到它——这是正确的，不要改成按团队写。在该文件旁的注释里写明这个理由。

- [ ] **Step 5: 全量验证**

Run: `make test-unit`、`make lint`
Expected: 均绿。

- [ ] **Step 6: 提交**

```bash
git add -u
git commit -m "fix(operations): attribute recorded events to the acting team"
```

---

## Task 4: 端到端隔离与文档

**Files:**
- Test: `internal/deployment/onprem/operations_test.go` 或 handler 集成测试（择既有 suite 追加）
- Modify: `docs/superpowers/plans/2026-08-05-portal-modernist-phase2a-overview-endpoint.md`（移除已失效的「已知缺陷」段）
- Modify: `docs/superpowers/specs/2026-08-04-portal-modernist-redesign-design.md`（若其中提到该缺陷）

**Interfaces:**
- Consumes: Task 1–3 的全部改动

- [ ] **Step 1: 写服务层端到端隔离测试**

在既有的 onprem operations 服务测试里追加：两个不同 `ScopeID` 的 principal，各自记录事件，
断言各自的 `Summary` / `ListEvents` 只看到自己的。这一条覆盖的是
`scopedOperationsService.forPrincipal` → store → SQL 的完整链路，而不只是 SQL。

- [ ] **Step 2: 运行并确认**

Run: `make test-unit`
Expected: 新用例 PASS。

- [ ] **Step 3: 证明它抓得住回归**

把 Task 2 中任一处 `scope_id = $N` 谓词临时去掉，运行该测试，确认 **RED**，还原后确认绿。
两次运行的真实输出都贴进报告。

- [ ] **Step 4: 清理已失效的文档**

- 阶段 2a 计划里的「已知缺陷（本计划有意不修）」整段删除，改为一句指向本计划的说明。
- 阶段 2a Task 1 的 `Series` doc comment 里关于"这两列不隔离"的句子改写为已隔离。
- `internal/operations/series.go` 里 `SeriesBucket` 的注释同样更新。

**这一步不是可选的收尾**：留着过时的"已知缺陷"说明会让下一个读代码的人以为泄漏还在，
或者反过来——以为别处也有同样的豁免。

- [ ] **Step 5: 提交**

```bash
git add -u
git commit -m "test(operations): cross-tenant isolation through the service layer"
```

---

## 完成标准

- [ ] `make test-unit`、`make lint` 全绿
- [ ] Postgres suite 带 DSN 真实执行（不是静默 skip）并通过
- [ ] 两个 scope 各自写入后，`Summary` / `ListEvents` / `AgentStats` / `Series` 都只看到自己的
- [ ] 去掉任一 `scope_id` 谓词都会让隔离测试变红（已实测并贴出输出）
- [ ] 迁移在已有数据的库上可重复执行（`IF NOT EXISTS` / 回填幂等）
- [ ] `EXPLAIN` 输出已贴进报告（无论是否走索引）
- [ ] 阶段 2a 计划与代码注释里关于该缺陷的表述已全部更新
