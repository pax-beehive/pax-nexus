# Handoff — Modernist Portal 重构 与 operation-events 租户隔离

Date: 2026-08-05
Branch: `feat/portal-modernist-phase2`（worktree `.claude/worktrees/feat+portal-modernist-phase2`）
Base: `origin/main` @ `fb88630`

这份文档是一个断点，不是总结。目的是让接手的人**不必重新踩一遍已经踩过的坑**。

---

## 1. 三条线的位置

| 线 | 状态 |
|---|---|
| **Modernist 阶段 1**（设计系统 + 新外壳） | ✅ 已合并进 main（PR #78 → `feat/saas-team-devices-r2` → PR #77 squash 进 main `fb88630`）。分支与工作树已删 |
| **operation-events 租户隔离** | 🔶 4 个任务全部实施并审查完毕；全分支终审判 "ready with follow-ups"；**最终修复波进行中，未提交** |
| **Modernist 阶段 2a**（Overview 聚合端点） | ⏸ Task 1/5 完成并已在本分支上；Tasks 2–5 **暂停排队** |

### 本分支已提交的 10 个 commit

```
ff0ba65 chore(ci): run internal/app in integration-test
019880d test(operations): cross-tenant isolation through the service layer
424407f fix(operations): attribute recorded events to the acting team
2599136 fix(operations): scope the recall-diagnostic fallback's cross-tenant existence check
3d3c514 fix(operations): filter every operation-event read by scope
7c2289f feat(operations): give operation events an owning scope
56b3712 docs: operation events 租户隔离实施计划
bec13fa test(operations): cover the facts scope filter and fix an unfalsifiable boundary test
df9a441 feat(operations): bucketed throughput series for the overview
4e6c177 docs: 阶段 2a 实施计划——Overview 聚合端点
```

### 进行中、未提交的工作

最终修复波正在改这些文件（F3–F8，见 §3）：
`internal/app/wiring.go`、`internal/deployment/onprem/extraction_observer_test.go`、
新增 `internal/app/wiring_test.go`。若这份 handoff 被读到时它们仍未提交，从 §3 的清单继续。

---

## 2. 隔离修复做了什么

**问题**：`onprem_operation_events` 没有 `scope_id` 列。多团队 SaaS 下，任一团队的 owner 在
Operations 能看到其他团队的 observation/recall 计数、p50/p95、错误数，以及逐条事件的
`actor_agent_id` / `actor_user_id` / `actor_membership_id` / `session_id`。

不是某次改动引入的——随 operations 控制台（#69/#70）就存在，SaaS scope 化（#68）与多租户
加固（#71）都聚焦在**有** scope 列的卫星服务上，漏了这张表。同一个端点里另一半
（`scanExtractionSummary`、`AgentStats` 的笔记部分、`RecallDiagnostic`、storage）一直是正确
隔离的，混合数据掩盖了缺口。

**做法**：迁移 031 加列 + 回填 `local-team` + 两个以 `scope_id` 打头的索引；事件自带
`ScopeID`（与 `platformllm.UsageEvent` 同形，**不是**把 recorder 改成按 scope 构造的工厂）；
三个写入点各自解析 scope；八个读路径逐个过滤。

**证据**：RED 阶段实测出泄漏——Summary 数到 2 而非 1、ListEvents 返回两个 scope 的行、
Series 的 Evidence 是 15（本团队 4 + 外部 11）而非 4；加过滤后全绿。服务层端到端测试
（`TestSaaSWiringSuite/TestScopedOperationsServiceCrossTenantIsolation`）走的是
`scopedOperationsService.forPrincipal` 的真实解析路径，不是手工构造的两个 store。

---

## 3. 立刻要做的（最终修复波，进行中）

终审无 Critical。以下是它要求的修复，**F3/F4 是合并前必修**：

- **F3** — `extraction_observer_test.go:46` 的
  `TestNewExtractionObserverFallsBackWhenResolverReturnsSingleton` **名字是假的**：它注入的
  解析器直接返回 `LocalScopeID`，从不回落。删掉它，改在 `internal/app` 加一条断言
  `extractionEventScope(context.Background()) == onprem.LocalScopeID` 的测试（不需要数据库，
  能进 `make test-unit`）。
- **F4** — 从 `operations.Repository` 接口移除 `Series`（`operations.go:311`）以及它逼出来的
  零断言 stub（`onprem/operations_test.go:215`）。**保留** `series.go`、`operations_series.go`
  及其测试——具体方法仍可调用、SQL 被真实数据库验证过、且是隔离覆盖的一部分。
  阶段 2a Task 5 有消费者时再把接口方法加回去。
- **F5** — `operations_test.go:601` 只断言 `Error(err)`，收紧为
  `ErrorIs(err, operations.ErrInvalidInput)`。
- **F6** — 迁移 031 硬编码 `'local-team'`，与 `onprem.LocalScopeID` 无关联。加一条断言常量值的
  测试（注明迁移 031 是原因），或加交叉引用注释。
- **F8** — 两份计划文档都没写清实现到哪了。隔离计划的 checkbox 全未勾但四个任务都完成了；
  阶段 2a 计划 920 行、没有任何"暂停于 Task 1"的标记。**下一个 agent 会照着阶段 2a 计划
  重新实现 `series.go` / `operations_series.go` / `Repository.Series`——它们已经存在。**

---

## 4. 需要人裁定的两件事

### 4.1 回填的取舍必须进 PR 说明（终审判为 Important）

迁移把**全部历史事件行归给 `local-team`**。对单团队部署正确；但 staging 从 #75 起就在跑多团队
控制面，**部署后某个团队的 owner 打开 Operations 会发现历史记录整段消失**，而唯一记录这个决定的
地方是 `.sql` 文件里的注释。

且**不可逆**：回填后无法区分"本来就单团队"和"被归并过来"的行。

取舍本身是对的（真实归属已不可恢复，宁可少显示也不要错误归属），但必须写进 PR 说明与发布说明。

### 4.2 `StorageSnapshot` 在 SaaS 下的语义（尚未决定）

我一度把 `measureOperations` 记成"这张表剩下的一个未隔离读路径"，**这个记法是错的**。终审纠正：
整个存储快照都是全装机范围——七个测量函数（session lake、identity audit 等）加上数据库物理
字节全部无 scope，而整份快照通过 `scopedOperationsService.LatestStorage/ListStorage` 按团队分发。
只给 operations 一个组件加 scope 会让快照**内部自相矛盾**。

所以这不是本次修复的漏网之鱼，是 `StorageSnapshot` 这个面本身的设计问题：SaaS 下每个团队都能
读到跨租户的总行数、总字节、最早/最新时间戳。

**待定**：存储快照应为每团队还是保持系统级？建议单独立项——改法不是加过滤，七个测量函数的语义
都要重新定义。

---

## 5. 恢复阶段 2a 时要知道的

阶段 2a 计划：`docs/superpowers/plans/2026-08-05-portal-modernist-phase2a-overview-endpoint.md`

- Task 1 **已完成并在本分支上**（`series.go`、`operations_series.go` 及测试）。不要重做。
- Task 1 的 `Repository.Series` 接口方法会被 F4 移除——**Task 5 需要时再加回来**。
- 计划里的「已知缺陷（本计划有意不修）」段已改写为「已关闭的已知缺陷」并指向隔离计划。
  新查询按普通规则处理这张表即可，不再需要特殊对待。
- 该计划有两处**我没能写成完整代码块**并已标注：Task 3 的 store 扫描块留了 `...`（须照抄同文件
  `ListOwnedEnrollments` 的字段顺序与 `permissions` 解码），Task 5 的 handler 主体是逐条要求
  而非成品代码（约 200 行并发组装）。
- 阶段 2b（Overview 页面 + 删除 Pulse）的计划**尚未写**，等 2a 端点契约固化后再写。

---

## 6. 这条路上的坑（最值钱的部分）

按被踩的次数排序。

### 6.1 Postgres 测试套件在缺 DSN 时**静默跳过并打印 `ok`**

`testDSN` 在 `TEAM_MEMORY_TEST_POSTGRES_DSN` 缺失时 `t.Skip`，`go test` 照样输出 `ok`。
只看 `ok` **完全分不出**测试跑没跑。跑这些包必须带：

```
TEAM_MEMORY_TEST_POSTGRES_DSN='postgres://team_memory:team_memory@127.0.0.1:55432/team_memory?sslmode=disable'
```

并从 `=== RUN` 行确认子测试真的执行了。

### 6.2 测试写了、审了、证明能失败——然后**从不在 CI 里运行**

服务层端到端测试住在 `internal/app`，而 `make integration-test` 的包清单是显式枚举的、原本
**不含** `internal/app`；`make test-unit` 又不设 DSN。两条路都不覆盖它。

这是我自己核验时撞到的，**三个 reviewer 都没发现**——因为他们都在验"测试写得对不对"，没人问
"**这个测试会不会被执行**"。测试的**有效性**和**可达性**是两个问题。

已修（`ff0ba65`），但下次加依赖数据库的测试时先问一句：它在哪个 CI 目标里？

### 6.3 Postgres 包必须串行（`-p 1`）

这些包共用同一个数据库。我把三个包并行跑时复现了 `TestChannelStoreSuite` 的**假失败**，
串行 3 次全绿、main 上也不出现。`make integration-test` 的 `-p 1` 是有意为之，新包要加进
**同一个**调用里而不是另起一行。

### 6.4 套件内的测试互相污染

Postgres 套件共享一个 schema、**没有逐测试清理**、testify 按字母序执行。已经害人两次：
- 计划里给的示例测试因为排在前面的 `TestAgentStatsAggregates...` 污染计数而不可用
- 序列套件的新测试和兄弟测试在时间窗上撞车

互不重叠是**靠约定不是靠结构**保证的。加测试时选好 scope id 与时间窗，并说明选了什么。

### 6.5 计划里的"枚举"是待验证的断言，不是已知事实

我在隔离计划里写死"四条读查询"，reviewer 自己 grep 出**第五条**
（`hasRecallDiagnosticEvent`，一个布尔型跨租户预言机）。终审又做了一遍全仓扫描，确认共 8 条。

凡是"这四处"、"这三个调用点"这类枚举，都要在 review 指令里明确要求**独立重新枚举**。

### 6.6 位置参数的静默错位

`Record` 的 INSERT 有 26 个位置参数。占位符错位**不报错**——只把值写进相邻列。加 scope 谓词
到读查询时同理。每改一条就数一次占位符和参数，别最后统一数。

### 6.7 曾出现一次伪造的实验记录

某轮报告贴的 RED 输出包含一条那个测试根本没有的断言——从上一轮复制来的。实验确实做过，但
**贴出的记录是编的**。只有 reviewer 自己重跑才发现。

此后所有派发都写明"贴逐字终端输出，复现不了就直说"，并在 re-review 里把**记录真实性**列为独立
检查项。后续几轮都核对一致。

### 6.8 编辑器诊断可能是 TDD 中间态的过期快照

有一次诊断报告说方法不存在、和实施者"全绿"的报告直接冲突。自己验证后发现诊断捕获的是
"测试和接口改动已落、实现还没写"的那一刻。**先自己验，别选边**。

---

## 7. 怎么继续

```bash
cd /Users/toddzheng/Workspace/golang/team-memory/.claude/worktrees/feat+portal-modernist-phase2
git log --oneline -3
git status --short          # 有未提交内容 = 修复波没跑完，见 §3
```

SDD ledger（含全部裁定与 deferred 清单，**gitignored、不在 commit 里**）：
- `.superpowers/sdd/2026-08-05-operation-events-tenant-isolation/progress.md`
- `.superpowers/sdd/2026-08-05-portal-modernist-phase2a-overview-endpoint/progress.md`

顺序建议：
1. 跑完 §3 的修复波，做一次 scoped re-review
2. 把 §4.1 写进 PR 说明，开 PR（base 是 `main`）
3. §4.2 交给人裁定，单独立项
4. 合并后回到阶段 2a Task 2–5（见 §5）
