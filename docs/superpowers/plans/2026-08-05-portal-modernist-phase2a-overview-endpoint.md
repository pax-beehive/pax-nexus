# Modernist Portal 阶段 2a：Overview 聚合端点 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增只读端点 `GET /v1/admin/overview`，一次返回 Overview 落地页需要的全部数据：指标、分桶时间序列、Team Note 按 kind 的构成、以及跨四个来源汇流的 attention 队列。

**Architecture:** 四个来源分别归各自的上下文所有（operations 的时间序列、explorer 的笔记构成、onprem 的到期邀请与到期 enrollment、audit 的高危 finding），**组装只发生在 handler 层**——`internal/architecture/dependencies_test.go` 里 `{directory: "operations"}` 的允许导入列表是空的，域包之间不能互相引用，而 `teamnote/transport` 被允许导入 audit / deployment/onprem / explorer / operations。

**Tech Stack:** Go + Hertz（路由与模型由 `idl/team_memory.thrift` 生成）+ pgx/PostgreSQL。

**上游文档：** `docs/superpowers/specs/2026-08-04-portal-modernist-redesign-design.md` §5.1。本计划只覆盖后端；Overview 页面与删除 Pulse 在阶段 2b。

## Global Constraints

- **端点只读。** 不新增、不修改任何写路径。
- **域包不得跨上下文导入。** `internal/operations`、`internal/explorer` 的允许导入列表为空；新增代码若在这两个包里 import 其他 `internal/` 上下文，`internal/architecture/dependencies_test.go` 会直接失败。组装在 handler。
- **鉴权对齐既有面**：整个端点走 `h.authorizeOperations`（服务端 capability `view.operations`），与 `GetOperationsSummary` 完全一致。新增的团队级 enrollment 列举额外要求 owner/admin，与 Devices 列举一致。
- **scope 隔离照抄相邻查询**：凡是所查的表**有** `scope_id` 列（`extraction_runs`、`note_revisions`、`team_notes`、`agent_enrollments`、`onprem_invitations`），新查询必须带 `scope_id = $N`，用 `s.scopeID`。
- **`onprem_operation_events` 现在也有 scope 隔离**：`docs/superpowers/plans/2026-08-05-operation-events-tenant-isolation.md` 已经落地——该表加了 `scope_id` 列，`scanOperationSummary` / `ListEvents` / `scanAgentStats` / `Series` 的 events CTE 均已按 `s.scopeID` 过滤。本计划创建时这里曾是一个已知、用户裁定推迟修复的多租户泄漏（详见下方「已关闭的已知缺陷」）；泄漏已关闭，新查询按上一条「scope 隔离照抄相邻查询」处理即可，不再需要特殊对待这张表。
- **测试命令**：`make test-unit`（全量 Go 单测）；单包 `go test ./internal/<pkg>/... -count=1`；Postgres 适配器测试需要真实数据库，见 `internal/platform/postgres/operations_test.go` 的 suite 模式（每个 suite 建独立 schema 再 DROP）。`make lint` 必须绿。
- **IDL 是真源**：改 `idl/team_memory.thrift` 后必须 `make generate` 重新生成模型与路由，不要手改 `internal/teamnote/transport/httpapi/model/` 或 `router/` 下的文件。
- **提交粒度**：每个 Task 末尾提交一次，前缀 `feat(operations):` / `feat(onprem):` / `feat(api):` 等。

## 已关闭的已知缺陷

本节曾记录一个已知、有意推迟修复的多租户泄漏：`onprem_operation_events` 当时无 `scope_id` 列，
`scanOperationSummary` / `ListEvents` 均无 scope 过滤，Task 1 的时间序列里 `evidence` 与 `recalls`
两列因此也跨租户可见。用户 2026-08-05 裁定先做 Overview、隔离另开一条线修。

那条线已经落地并关闭了这个泄漏，见 `docs/superpowers/plans/2026-08-05-operation-events-tenant-isolation.md`：
表加了 `scope_id`，所有读路径（含本计划 Task 1 的 `Series`）都按调用方的 scope 过滤。下方 Task 1、
Task 5 里提到"不隔离"的 doc comment 指令均已随之更新为已隔离的表述。

---

## File Structure

**新建**

| 文件 | 职责 |
|---|---|
| `internal/operations/series.go` | `SeriesBucket` 类型与 `SeriesFilter`；纯域类型，无 SQL |
| `internal/platform/postgres/operations_series.go` | `Series` 的 SQL 实现，与 `operations.go` 分开以免后者继续膨胀（已 1023 行） |
| `internal/explorer/notemix.go` | `NoteKindCount` 类型与 `NoteMix` 查询端口 |
| `internal/platform/postgres/explorer_notemix.go` | 按 kind 分组的活跃笔记计数 SQL |
| `internal/teamnote/transport/httpapi/handler/overview_endpoint.go` | 端点组装：并发拉四个来源、超时降级、映射到 API 模型 |

**修改**

| 文件 | 变化 |
|---|---|
| `internal/operations/operations.go` | `Repository` 接口加 `Series` 方法 |
| `internal/explorer/explorer.go` | 服务加 `NoteMix` 方法（鉴权与既有 explorer 读一致） |
| `internal/deployment/onprem/registry.go` | 新增团队级 `ListExpiringEnrollments`；`RegistryStore` 接口加对应 store 方法 |
| `internal/platform/postgres/registry.go` | `ListExpiringEnrollments` 的 SQL |
| `idl/team_memory.thrift` | Overview 的请求/响应 struct + 端点声明 |
| `internal/teamnote/transport/httpapi/handler/dependencies.go` | `OperationsLifecycle` 加 `Series`；`ExplorerLifecycle` 加 `NoteMix`；`AgentRegistryLifecycle` 加 `ListExpiringEnrollments` |
| `internal/app/saas_wiring.go` | `scopedOperationsService` 补 `Series` 委派 |

---

## Task 1: operations 分桶时间序列

**Files:**
- Create: `internal/operations/series.go`
- Create: `internal/platform/postgres/operations_series.go`
- Modify: `internal/operations/operations.go`（`Repository` 接口）
- Test: `internal/platform/postgres/operations_series_test.go`

**Interfaces:**
- Consumes: 既有 `operations.TimeFilter{From, To time.Time; AgentID string}`
- Produces:
  - `type SeriesBucket struct { BucketAt time.Time; Evidence, Facts, Recalls int64 }`
  - `Repository.Series(ctx context.Context, filter TimeFilter, bucket time.Duration) ([]SeriesBucket, error)`
  - 返回值是**完整序列**：窗口内每个桶都有一行，没有数据的桶三个计数均为 0，按 `BucketAt` 升序。

- [ ] **Step 1: 写域类型**

创建 `internal/operations/series.go`：

```go
package operations

import "time"

// SeriesBucket is one time bucket of the Overview throughput series.
//
// Evidence and Recalls are derived from onprem_operation_events, Facts from
// note_revisions. Both sources are scope-isolated: each is filtered by its
// own table's scope_id column, so every field on this struct reflects only
// the caller's own team.
type SeriesBucket struct {
	BucketAt time.Time
	Evidence int64
	Facts    int64
	Recalls  int64
}
```

- [ ] **Step 2: 把 Series 加进 Repository 接口**

修改 `internal/operations/operations.go` 的 `Repository` 接口，在 `Summary` 之后插入一行：

```go
	Series(context.Context, TimeFilter, time.Duration) ([]SeriesBucket, error)
```

- [ ] **Step 3: 写失败的测试**

创建 `internal/platform/postgres/operations_series_test.go`。照抄同目录 `operations_test.go` 的
suite 结构（`SetupSuite` 建独立 schema、`TearDownSuite` DROP）——不要新造一套：

```go
package postgres_test

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/operations"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/stretchr/testify/suite"
)

type operationsSeriesSuite struct {
	suite.Suite
	store      *postgres.Store
	operations *postgres.OperationsStore
	scope      string
	adminPool  *pgxpool.Pool
	schema     string
}

func TestOperationsSeriesSuite(t *testing.T) {
	suite.Run(t, new(operationsSeriesSuite))
}

func (s *operationsSeriesSuite) SetupSuite() {
	ctx := context.Background()
	dsn := testDSN(s.T())
	adminPool, err := pgxpool.New(ctx, dsn)
	s.Require().NoError(err)
	s.adminPool = adminPool
	s.schema = fmt.Sprintf("opseries_%d", time.Now().UnixNano())
	_, err = adminPool.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{s.schema}.Sanitize())
	s.Require().NoError(err)
	parsed, err := url.Parse(dsn)
	s.Require().NoError(err)
	query := parsed.Query()
	query.Set("search_path", s.schema+",public")
	parsed.RawQuery = query.Encode()
	store, err := postgres.Open(ctx, parsed.String())
	s.Require().NoError(err)
	s.Require().NoError(store.Migrate(ctx))
	s.store = store
	s.scope = "series-suite-scope"
	s.operations = store.Operations(s.scope)
}

func (s *operationsSeriesSuite) TearDownSuite() {
	if s.store != nil {
		s.store.Close()
	}
	if s.adminPool != nil {
		_, err := s.adminPool.Exec(
			context.Background(),
			"DROP SCHEMA "+pgx.Identifier{s.schema}.Sanitize()+" CASCADE",
		)
		s.NoError(err)
		s.adminPool.Close()
	}
}

// The series must be gap-free: a window with data only in the first and last
// bucket still returns one row per bucket, so the frontend can plot it without
// reconstructing missing time slots.
func (s *operationsSeriesSuite) TestSeriesReturnsEveryBucketIncludingEmptyOnes() {
	ctx := context.Background()
	base := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)

	s.recordObservation(ctx, base.Add(1*time.Minute), 7)
	s.recordRecall(ctx, base.Add(1*time.Minute))
	s.recordObservation(ctx, base.Add(52*time.Minute), 3)

	filter := operations.TimeFilter{From: base, To: base.Add(time.Hour)}
	buckets, err := s.operations.Series(ctx, filter, 10*time.Minute)
	s.Require().NoError(err)
	s.Require().Len(buckets, 6)

	for i, bucket := range buckets {
		s.Equal(base.Add(time.Duration(i)*10*time.Minute), bucket.BucketAt.UTC())
	}
	s.Equal(int64(7), buckets[0].Evidence)
	s.Equal(int64(1), buckets[0].Recalls)
	s.Equal(int64(0), buckets[1].Evidence)
	s.Equal(int64(0), buckets[1].Recalls)
	s.Equal(int64(3), buckets[5].Evidence)
}

// A row exactly on the window's upper bound belongs to the next window, not to
// the last bucket — the half-open interval must match Summary's.
func (s *operationsSeriesSuite) TestSeriesExcludesTheUpperBound() {
	ctx := context.Background()
	base := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	s.recordObservation(ctx, base.Add(time.Hour), 5)

	buckets, err := s.operations.Series(
		ctx,
		operations.TimeFilter{From: base, To: base.Add(time.Hour)},
		10*time.Minute,
	)
	s.Require().NoError(err)
	s.Require().Len(buckets, 6)
	for _, bucket := range buckets {
		s.Equal(int64(0), bucket.Evidence)
	}
}

func (s *operationsSeriesSuite) recordObservation(
	ctx context.Context, at time.Time, accepted int64,
) {
	s.T().Helper()
	attempt, err := operations.NewAttemptID()
	s.Require().NoError(err)
	_, err = s.operations.Record(ctx, operations.Event{
		AttemptID:     attempt,
		Kind:          operations.KindObservationObserve,
		Outcome:       operations.OutcomeSucceeded,
		Actor:         operations.Actor{Kind: "agent", AgentID: "series-agent"},
		StartedAt:     at,
		CompletedAt:   at.Add(20 * time.Millisecond),
		DurationMS:    20,
		InputItems:    accepted,
		AcceptedItems: accepted,
	})
	s.Require().NoError(err)
}

func (s *operationsSeriesSuite) recordRecall(ctx context.Context, at time.Time) {
	s.T().Helper()
	attempt, err := operations.NewAttemptID()
	s.Require().NoError(err)
	_, err = s.operations.Record(ctx, operations.Event{
		AttemptID:   attempt,
		Kind:        operations.KindMemorySearch,
		Outcome:     operations.OutcomeSucceeded,
		Actor:       operations.Actor{Kind: "agent", AgentID: "series-agent"},
		StartedAt:   at,
		CompletedAt: at.Add(30 * time.Millisecond),
		DurationMS:  30,
		ResultItems: 2,
	})
	s.Require().NoError(err)
}
```

> `operations.KindObservationObserve` / `KindMemorySearch` / `OutcomeSucceeded` 是
> `internal/operations/operations.go` 里已有的常量；若名字不同，以该文件为准，不要新造。

- [ ] **Step 4: 运行测试确认失败**

Run: `go test ./internal/platform/postgres/... -run TestOperationsSeriesSuite -count=1`
Expected: 编译失败——`s.operations.Series` 未定义。

- [ ] **Step 5: 实现 Series**

创建 `internal/platform/postgres/operations_series.go`：

```go
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/operations"
)

// Series returns one row per bucket across the whole window, including empty
// buckets, so callers can plot it without reconstructing gaps.
//
// Two sources, exactly as Summary splits them: Evidence and Recalls come from
// onprem_operation_events, Facts from note_revisions. Both are scope-isolated,
// each against its own table's scope_id column — the events CTE uses $6, the
// facts CTE uses $5. They are separate parameters on purpose even though both
// carry s.scopeID: the two CTEs read different tables and are not the same
// predicate merely written twice.
func (s *OperationsStore) Series(
	ctx context.Context,
	filter operations.TimeFilter,
	bucket time.Duration,
) ([]operations.SeriesBucket, error) {
	if bucket <= 0 {
		return nil, fmt.Errorf("series bucket must be positive: %w", operations.ErrInvalidInput)
	}
	seconds := int64(bucket / time.Second)
	if seconds <= 0 {
		return nil, fmt.Errorf("series bucket must be at least one second: %w", operations.ErrInvalidInput)
	}

	rows, err := s.pool.Query(ctx, `
WITH bucket_starts AS (
    SELECT generate_series(
        to_timestamp(floor(extract(epoch FROM $1::timestamptz) / $3) * $3),
        $2::timestamptz - interval '1 microsecond',
        make_interval(secs => $3)
    ) AS bucket_at
),
events AS (
    SELECT
        to_timestamp(floor(extract(epoch FROM started_at) / $3) * $3) AS bucket_at,
        COALESCE(sum(accepted_items) FILTER (
            WHERE operation_kind = 'observation.observe'), 0) AS evidence,
        count(*) FILTER (
            WHERE operation_kind IN ('memory.search', 'memory.get', 'team_note.recall')
        ) AS recalls
    FROM onprem_operation_events
    WHERE started_at >= $1 AND started_at < $2
      AND ($4 = '' OR actor_agent_id = $4)
      AND scope_id = $6
    GROUP BY 1
),
facts AS (
    SELECT
        to_timestamp(floor(extract(epoch FROM revisions.created_at) / $3) * $3) AS bucket_at,
        count(*) AS facts
    FROM note_revisions revisions
    JOIN team_notes notes
      ON notes.scope_id = revisions.scope_id AND notes.note_id = revisions.note_id
    WHERE revisions.scope_id = $5
      AND revisions.created_at >= $1 AND revisions.created_at < $2
      AND ($4 = '' OR notes.origin_agent_id = $4)
    GROUP BY 1
)
SELECT
    bucket_starts.bucket_at,
    COALESCE(events.evidence, 0),
    COALESCE(facts.facts, 0),
    COALESCE(events.recalls, 0)
FROM bucket_starts
LEFT JOIN events ON events.bucket_at = bucket_starts.bucket_at
LEFT JOIN facts ON facts.bucket_at = bucket_starts.bucket_at
ORDER BY bucket_starts.bucket_at`,
		filter.From, filter.To, seconds, filter.AgentID, s.scopeID, s.scopeID)
	if err != nil {
		return nil, fmt.Errorf("query postgres operation series: %w", err)
	}
	defer rows.Close()

	var buckets []operations.SeriesBucket
	for rows.Next() {
		var item operations.SeriesBucket
		if err := rows.Scan(&item.BucketAt, &item.Evidence, &item.Facts, &item.Recalls); err != nil {
			return nil, fmt.Errorf("scan postgres operation series: %w", err)
		}
		buckets = append(buckets, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres operation series: %w", err)
	}
	return buckets, nil
}
```

> `operations.ErrInvalidInput` 已存在（`handler/operations_endpoints.go` 里用过）。若
> `OperationsStore` 的读查询在相邻代码里走的是 `s.readPool` 而不是 `s.pool`，照抄
> `scanOperationSummary` 的选择，保持一致。

- [ ] **Step 6: 运行测试确认通过**

Run: `go test ./internal/platform/postgres/... -run TestOperationsSeriesSuite -count=1 -v`
Expected: 两个用例均 PASS。

- [ ] **Step 7: 更新 mock 并跑全量**

`Repository` 接口变了，mock 需要重新生成：

Run: `make mocks`
Run: `go build ./... && make test-unit`
Expected: 编译通过，全量单测绿。若有实现了 `operations.Repository` 的假实现（测试替身）编译失败，
给它补一个返回空切片的 `Series` 方法——不要改接口来迁就它。

- [ ] **Step 8: 提交**

```bash
git add internal/operations internal/platform/postgres/operations_series.go \
        internal/platform/postgres/operations_series_test.go
git add -u
git commit -m "feat(operations): bucketed throughput series for the overview"
```

---

## Task 2: explorer 笔记构成

**Files:**
- Create: `internal/explorer/notemix.go`
- Create: `internal/platform/postgres/explorer_notemix.go`
- Modify: `internal/explorer/explorer.go`（服务方法）
- Test: `internal/platform/postgres/explorer_notemix_test.go`

**Interfaces:**
- Consumes: 无（explorer 的允许导入列表为空）
- Produces:
  - `type NoteKindCount struct { Kind string; Count int64 }`
  - store 端口 `NoteMix(ctx context.Context, at time.Time) ([]NoteKindCount, error)`
  - 服务方法签名与 explorer 既有读方法一致（同样的 principal 鉴权），返回按 `Count` 降序、
    同数时按 `Kind` 升序，保证输出稳定。

- [ ] **Step 1: 写域类型与端口**

创建 `internal/explorer/notemix.go`：

```go
package explorer

import (
	"context"
	"time"
)

// NoteKindCount is the number of currently live Team Notes of one kind.
// "Live" is the same effective state the storage snapshot uses: a note that is
// neither resolved nor past its hard expiry at the given instant.
type NoteKindCount struct {
	Kind  string
	Count int64
}

// NoteMixReader answers the Overview's "what the team remembers" breakdown.
type NoteMixReader interface {
	NoteMix(ctx context.Context, at time.Time) ([]NoteKindCount, error)
}
```

- [ ] **Step 2: 写失败的测试**

创建 `internal/platform/postgres/explorer_notemix_test.go`。suite 结构照抄
`internal/platform/postgres/explorer_test.go`（若不存在则照抄 `operations_test.go`）。
核心断言：

```go
// Only live notes count: a resolved note and a hard-expired note must not
// appear, otherwise the Overview mix would grow monotonically and stop
// describing what the team currently remembers.
func (s *explorerNoteMixSuite) TestNoteMixCountsOnlyLiveNotesPerKind() {
	ctx := context.Background()
	at := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)

	s.insertNote(ctx, "n1", "decision", "active", at.Add(24*time.Hour))
	s.insertNote(ctx, "n2", "decision", "active", at.Add(24*time.Hour))
	s.insertNote(ctx, "n3", "blocker", "active", at.Add(24*time.Hour))
	s.insertNote(ctx, "n4", "blocker", "resolved", at.Add(24*time.Hour))
	s.insertNote(ctx, "n5", "handoff", "active", at.Add(-time.Hour)) // hard-expired

	mix, err := s.explorer.NoteMix(ctx, at)
	s.Require().NoError(err)
	s.Require().Len(mix, 2)
	s.Equal(explorer.NoteKindCount{Kind: "decision", Count: 2}, mix[0])
	s.Equal(explorer.NoteKindCount{Kind: "blocker", Count: 1}, mix[1])
}

// Another scope's notes must never appear — team_notes carries scope_id and
// this query must filter on it.
func (s *explorerNoteMixSuite) TestNoteMixIsScopeIsolated() {
	ctx := context.Background()
	at := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	s.insertNoteInScope(ctx, "other-scope", "x1", "decision", "active", at.Add(24*time.Hour))

	mix, err := s.explorer.NoteMix(ctx, at)
	s.Require().NoError(err)
	s.Empty(mix)
}
```

`insertNote` / `insertNoteInScope` 直接 INSERT 进 `team_notes`，字段取自
`internal/platform/postgres/migrations/` 里该表的定义；`insertNote` 用 `s.scope`。

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./internal/platform/postgres/... -run TestExplorerNoteMixSuite -count=1`
Expected: 编译失败——`NoteMix` 未定义。

- [ ] **Step 4: 实现 SQL**

创建 `internal/platform/postgres/explorer_notemix.go`：

```go
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/explorer"
)

// NoteMix counts live Team Notes per kind for the store's scope.
//
// "Live" reuses the same effective-state expression the storage snapshot uses
// (see scanTeamNoteStorage): a note is expired when its state says so or its
// hard expiry has passed, resolved when its state says so or invalid_at has
// passed, otherwise active. Keeping one definition matters — two different
// notions of "live" on the same page would not add up.
func (s *ExplorerStore) NoteMix(
	ctx context.Context,
	at time.Time,
) ([]explorer.NoteKindCount, error) {
	rows, err := s.pool.Query(ctx, `
WITH effective_notes AS (
    SELECT kind, CASE
        WHEN state = 'expired' OR hard_expires_at <= $1 THEN 'expired'
        WHEN state = 'resolved' OR (invalid_at IS NOT NULL AND invalid_at <= $1) THEN 'resolved'
        ELSE 'active'
    END AS effective_state
    FROM team_notes
    WHERE scope_id = $2
)
SELECT kind, count(*)
FROM effective_notes
WHERE effective_state = 'active'
GROUP BY kind
ORDER BY count(*) DESC, kind ASC`, at, s.scopeID)
	if err != nil {
		return nil, fmt.Errorf("query postgres note mix: %w", err)
	}
	defer rows.Close()

	var mix []explorer.NoteKindCount
	for rows.Next() {
		var item explorer.NoteKindCount
		if err := rows.Scan(&item.Kind, &item.Count); err != nil {
			return nil, fmt.Errorf("scan postgres note mix: %w", err)
		}
		mix = append(mix, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres note mix: %w", err)
	}
	return mix, nil
}
```

> `ExplorerStore` 的字段名以 `internal/platform/postgres/explorer.go` 为准（scope 字段可能叫
> `scopeID`）。若该 store 的 scope 是逐次传入而非构造时固定（`store.Explorer()` 的注释提到
> explorer 是 scope-per-call），把 scope 作为参数传进来，签名相应改为
> `NoteMix(ctx, scopeID string, at time.Time)`，并同步 `NoteMixReader`。**以既有代码为准。**

- [ ] **Step 5: 在 explorer 服务上暴露 NoteMix**

修改 `internal/explorer/explorer.go`，照抄同文件既有读方法（如 `ListTeamNotes`）的鉴权与
错误包装形状，新增：

```go
// NoteMix answers the Overview's live-note breakdown. Same authorization as
// every other explorer read: the caller must hold the team-memory capability.
func (s *Service) NoteMix(
	ctx context.Context,
	principal Principal,
	at time.Time,
) ([]NoteKindCount, error) {
	if err := s.authorize(principal); err != nil {
		return nil, err
	}
	return s.reader.NoteMix(ctx, at)
}
```

> `Principal` / `s.authorize` / `s.reader` 的真实名字以 `explorer.go` 为准；不要新造鉴权路径。

- [ ] **Step 6: 运行测试确认通过**

Run: `go test ./internal/explorer/... ./internal/platform/postgres/... -run 'Explorer' -count=1`
Expected: PASS，含 scope 隔离用例。

- [ ] **Step 7: 提交**

```bash
git add internal/explorer internal/platform/postgres/explorer_notemix.go \
        internal/platform/postgres/explorer_notemix_test.go
git add -u
git commit -m "feat(explorer): live team-note breakdown by kind"
```

---

## Task 3: 团队级到期 enrollment 列举

**Files:**
- Modify: `internal/deployment/onprem/registry.go`
- Modify: `internal/platform/postgres/registry.go`
- Test: `internal/deployment/onprem/registry_test.go`（鉴权）、`internal/platform/postgres/registry_test.go`（SQL）

**Interfaces:**
- Consumes: 既有 `onprem.AgentEnrollmentMetadata`、`onprem.HumanPrincipal`
- Produces:
  - `RegistryService.ListExpiringEnrollments(ctx, principal HumanPrincipal, before time.Time, limit int) ([]AgentEnrollmentMetadata, error)`
  - `RegistryStore.ListExpiringEnrollments(ctx, scopeID string, before time.Time, limit int) ([]AgentEnrollmentMetadata, error)`

**背景**：既有的 `ListEnrollments` 是**按 Agent** 查的（签名要求 `agentID`，且先 `GetOwnedAgent`
鉴权），所以拿不到团队范围内所有即将过期的 enrollment。Overview 的 attention 队列需要后者。
enrollment 是 owner 私有制品，团队级读取是**新的访问面**，因此鉴权收紧到 owner/admin，与
Devices 列举一致。

- [ ] **Step 1: 写失败的鉴权测试**

在 `internal/deployment/onprem/registry_test.go` 追加。照抄同文件
`TestCreateDeviceEnrollmentRequiresOwnerOrAdmin` 的结构：

```go
// Enrollments are owner-private artifacts; a team-wide listing is a new read
// surface, so it is restricted to owner/admin exactly like the device listing.
func (s *registrySuite) TestListExpiringEnrollmentsRequiresOwnerOrAdmin() {
	member := activeMember()
	member.Role = onprem.RoleMember
	_, err := s.service.ListExpiringEnrollments(
		context.Background(), member, time.Now().Add(24*time.Hour), 20,
	)
	s.Require().ErrorIs(err, onprem.ErrForbidden)

	admin := activeMember()
	admin.Role = onprem.RoleAdmin
	_, err = s.service.ListExpiringEnrollments(
		context.Background(), admin, time.Now().Add(24*time.Hour), 20,
	)
	s.Require().NoError(err)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/deployment/onprem/... -run TestListExpiringEnrollmentsRequiresOwnerOrAdmin -count=1`
Expected: 编译失败——方法不存在。

- [ ] **Step 3: 实现服务方法**

在 `internal/deployment/onprem/registry.go` 的 `ListEnrollments` 之后追加：

```go
// ListExpiringEnrollments returns pending enrollments across the whole team
// whose one-time token expires before `before`, soonest first.
//
// Unlike ListEnrollments this is not owner-scoped, so it is a new read surface
// over an otherwise owner-private artifact — authorization matches the device
// listing (owner/admin only), not the per-agent enrollment listing.
func (s *RegistryService) ListExpiringEnrollments(
	ctx context.Context,
	principal HumanPrincipal,
	before time.Time,
	limit int,
) ([]AgentEnrollmentMetadata, error) {
	if err := authorizeRegistryAdmin(principal); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.store.ListExpiringEnrollments(ctx, principal.ScopeID, before, limit)
}
```

> `authorizeRegistryAdmin` 是该文件里 Devices 列举用的同一个鉴权函数；真实名字以
> `registry.go` 为准（`CreateDeviceEnrollment` 用的是哪个就用哪个）。同时把
> `ListExpiringEnrollments` 加进该文件里的 `RegistryStore` 接口。

- [ ] **Step 4: 实现 store SQL**

在 `internal/platform/postgres/registry.go` 追加。字段与既有 enrollment 查询保持一致
（参考同文件 `ListOwnedEnrollments` 的 SELECT 列表）：

```go
// ListExpiringEnrollments returns unclaimed, unrevoked enrollments in one scope
// whose token expiry falls before the cutoff, soonest first.
func (s *RegistryStore) ListExpiringEnrollments(
	ctx context.Context,
	scopeID string,
	before time.Time,
	limit int,
) ([]onprem.AgentEnrollmentMetadata, error) {
	rows, err := s.pool.Query(ctx, `
SELECT enrollment_id, agent_id, credential_label, permissions,
       created_at, expires_at, credential_expires_at
FROM agent_enrollments
WHERE scope_id = $1
  AND consumed_at IS NULL
  AND revoked_at IS NULL
  AND expires_at < $2
ORDER BY expires_at ASC, enrollment_id ASC
LIMIT $3`, scopeID, before, limit)
	if err != nil {
		return nil, fmt.Errorf("query postgres expiring enrollments: %w", err)
	}
	defer rows.Close()
	// scan into []onprem.AgentEnrollmentMetadata following the shape used by
	// ListOwnedEnrollments in this file — reuse its row-scanning helper if one
	// exists rather than duplicating the field order.
	...
}
```

> 上面的 `...` 是**唯一允许你自己补的部分**，且必须照抄同文件 `ListOwnedEnrollments` 的扫描逻辑
> （包括 `permissions` 数组的解码与 `status` 的推导方式）。不要新造字段顺序。若 `agent_enrollments`
> 没有 `scope_id` 列，**停下来报 NEEDS_CONTEXT**，不要静默去掉过滤。

- [ ] **Step 5: 写 store 的 SQL 测试**

在 `internal/platform/postgres/registry_test.go` 追加：已消费的、已吊销的、以及过期时间在
cutoff 之后的三种 enrollment 都不得出现；另一个 scope 的不得出现；结果按 `expires_at` 升序。

- [ ] **Step 6: 运行测试确认通过**

Run: `go test ./internal/deployment/onprem/... ./internal/platform/postgres/... -run 'Enrollment' -count=1`
Expected: PASS。

- [ ] **Step 7: 更新 mock 并跑全量**

Run: `make mocks && go build ./... && make test-unit`

- [ ] **Step 8: 提交**

```bash
git add -u
git commit -m "feat(onprem): team-wide listing of expiring agent enrollments"
```

---

## Task 4: IDL 与生成

**Files:**
- Modify: `idl/team_memory.thrift`
- Generated (由 `make generate` 产出，不要手改): `internal/teamnote/transport/httpapi/model/teammemory/api/*`、`.../router/teammemory/api/*`

**Interfaces:**
- Produces: API 模型 `OverviewResponse` 及其嵌套 struct；路由 `GET /v1/admin/overview`

- [ ] **Step 1: 在 IDL 里加 struct 与端点**

在 `idl/team_memory.thrift` 的 `OperationsSummaryResponse` 之后插入。字段名必须与 spec §5.1
的 JSON 一致（前端按那份契约写）：

```thrift
struct OverviewRequest {
  1: optional string window (api.query="window")
}

struct OverviewMetrics {
  1: required i64 evidence_captured
  2: required i64 live_notes
  3: required i64 notes_expiring_today
  4: required i64 recalls_served
  5: required double recall_accept_rate
  6: optional i64 p50_ms
  7: optional i64 p95_ms
  8: required i64 attention_count
}

struct OverviewSeriesPoint {
  1: required string bucket_at
  2: required i64 evidence
  3: required i64 facts
  4: required i64 recalls
}

struct OverviewNoteMixEntry {
  1: required string kind
  2: required i64 count
  3: required double pct
}

struct OverviewAttentionItem {
  1: required string kind
  2: required string severity
  3: required string title
  4: required string body
  5: required string ref
  6: required string target
}

struct OverviewResponse {
  1: required string from_time
  2: required string to_time
  3: required string generated_at
  4: required OverviewMetrics metrics
  5: required list<OverviewSeriesPoint> series
  6: required list<OverviewNoteMixEntry> note_mix
  7: required list<OverviewAttentionItem> attention
}
```

在服务定义里（`GetOperationsSummary` 那一行附近）加：

```thrift
  OverviewResponse GetOverview(1: OverviewRequest request) (api.get="/v1/admin/overview")
```

- [ ] **Step 2: 重新生成**

Run: `make generate`
Expected: `internal/teamnote/transport/httpapi/model/teammemory/api/` 下出现 Overview 相关模型；
`router/teammemory/api/team_memory.go` 里出现 `_admin.GET("/overview", ...)`；
`handler/` 下生成一个 `get_overview.go` 桩。

- [ ] **Step 3: 确认生成物已注册且能编译**

Run: `go build ./...`
Expected: 成功。若生成的桩 handler 与 Task 5 要写的方法重名，保留生成的桩，Task 5 在
`overview_endpoint.go` 里实现 `(h *Handler) GetOverview`，由桩转调——照抄
`handler/get_operations_summary.go` 与 `operations_endpoints.go` 的分工。

- [ ] **Step 4: 提交**

```bash
git add idl/team_memory.thrift internal/teamnote/transport/httpapi
git commit -m "feat(api): declare the read-only overview aggregate endpoint"
```

---

## Task 5: handler 组装

**Files:**
- Create: `internal/teamnote/transport/httpapi/handler/overview_endpoint.go`
- Modify: `internal/teamnote/transport/httpapi/handler/dependencies.go`
- Modify: `internal/app/saas_wiring.go`
- Test: `internal/teamnote/transport/httpapi/handler/overview_endpoint_test.go`

**Interfaces:**
- Consumes: Task 1 `Series`、Task 2 `NoteMix`、Task 3 `ListExpiringEnrollments`，以及既有
  `h.operations.Summary`、`h.sessionAudit.ListFindings`、`h.identity.ListInvitations`
- Produces: `(h *Handler) GetOverview(ctx, c)`

- [ ] **Step 1: 扩接口**

修改 `dependencies.go`：`OperationsLifecycle` 加
`Series(context.Context, onprem.HumanPrincipal, operations.TimeFilter, time.Duration) ([]operations.SeriesBucket, error)`；
`ExplorerLifecycle` 加 `NoteMix(context.Context, onprem.HumanPrincipal, time.Time) ([]explorer.NoteKindCount, error)`；
`AgentRegistryLifecycle` 加 `ListExpiringEnrollments(context.Context, onprem.HumanPrincipal, time.Time, int) ([]onprem.AgentEnrollmentMetadata, error)`。

同步在 `internal/app/saas_wiring.go` 的 `scopedOperationsService` 上补 `Series` 委派，照抄同文件
`Summary` 的三行形状（`forPrincipal` → 委派）。

- [ ] **Step 2: 写失败的测试**

创建 `overview_endpoint_test.go`。照抄同目录既有 handler 测试的构造方式（假实现 + `app.RequestContext`）。
必测四条：

1. **窗口决定分桶**：`window=1h` → 6 个桶且每个 10 分钟；`24h` → 8 个桶；`7d` → 7 个桶；
   非法 window → 400，不落到默认值。
2. **单个来源失败不拖垮整页**：让 `ListFindings` 返回 error，断言响应仍是 200，
   `attention` 里没有 finding 条目但其余区块完整。**这是本端点的核心契约**——Overview 是落地页，
   一个来源挂掉不能变成白屏。
3. **attention 排序与上限**：四个来源混合后按 severity 再按时间排序，总数截断到 20，
   `metrics.attention_count` 是**截断前**的总数。
4. **鉴权**：无 `view.operations` capability 时 403，且**不发起任何下游调用**（用假实现的调用计数断言）。

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./internal/teamnote/transport/httpapi/handler/... -run TestGetOverview -count=1`
Expected: FAIL。

- [ ] **Step 4: 实现端点**

创建 `overview_endpoint.go`。要点，逐条落实：

- 鉴权走 `h.authorizeOperations(ctx, c)`，与 `GetOperationsSummary` 完全一致。
- `window` 只接受 `1h` / `24h` / `7d`，缺省 `24h`，其余值返回 400（走 `h.writeOperationsError`
  配 `operations.ErrInvalidInput`）。分桶固定：`1h`→10 分钟、`24h`→3 小时、`7d`→24 小时。
  **分桶由服务端决定，不接受客户端传入。**
- 六个下游调用并发发起（`errgroup` 或 `sync.WaitGroup` + 互斥写入），**每个来源单独降级**：
  失败只清空该来源并记一条 `h.logger.Warn`，不让整个请求失败。唯一例外是
  `h.operations.Summary`——它提供 metrics 主体，失败则整体返回错误。
- attention 四个来源的映射：
  - `sessionAudit.ListFindings{Severity: "high"}` 与 `{Severity: "critical"}` → `kind="finding"`，
    `target=/governance/sessions`
  - `summary.Extraction.Quarantined > 0` → **一条聚合项**（不是 N 条），
    `kind="quarantine"`，`target=/governance/pipeline`
  - `identity.ListInvitations{Status: pending}` 里 `ExpiresAt` 在 24 小时内的 →
    `kind="invitation"`，`target=/management/invitations`
  - `registry.ListExpiringEnrollments(before = now+24h)` → `kind="enrollment"`，
    `target=/management/agents/<agent_id>`
- `onprem_operation_events` 的 scope 隔离已经落地（见「已关闭的已知缺陷」），所以端点的 doc
  comment 不需要再声明 series 的 `evidence` / `recalls` 跨租户可见——它们和其他字段一样只反映
  调用方自己团队的数据，正常写文档即可，不必额外免责声明。

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/teamnote/transport/httpapi/handler/... -run TestGetOverview -count=1 -v`
Expected: 四条全 PASS。

- [ ] **Step 6: 全量验证**

Run: `make lint`
Run: `make test-unit`
Expected: 均绿。覆盖率门禁（80% 手写代码）若因新文件下降，补测试而不是调门禁。

- [ ] **Step 7: 提交**

```bash
git add internal/teamnote/transport/httpapi/handler internal/app/saas_wiring.go
git commit -m "feat(api): assemble the overview aggregate from four owning contexts"
```

---

## 阶段 2a 完成标准

- [ ] `make lint` 与 `make test-unit` 全绿
- [ ] `GET /v1/admin/overview?window=1h|24h|7d` 三个窗口各返回正确桶数（6 / 8 / 7）
- [ ] 任一非关键来源失败时端点仍返回 200，对应区块为空
- [ ] 无 `view.operations` 时 403 且不发起下游调用
- [ ] `note_mix` 与 `ListExpiringEnrollments` 的 scope 隔离各有一条独立测试
- [ ] 阶段 2b（Overview 页面 + 删除 Pulse）另出计划
