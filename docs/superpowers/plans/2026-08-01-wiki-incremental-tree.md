# Wiki 主题树增量维护(插入+分裂)Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 wiki 主题树维护从「整树 LLM 重建」换成 mdhub 式「单页增量插入 + 节点溢出分裂」,前端侧边栏换成单层下钻视图。

**Architecture:** 存储保留 `TopicTree{Topics, Placements}` JSONB 快照与全部现有 API。新增 `TreeNavigator` LLM 端口(单页选落点 + 节点分裂),service 层用 keyed 去重任务队列串行消费 insert/split 任务,替换现有脏标记+防抖整树重建。旧 `LLMTreeIndexer` 整体删除。前置修复:仓储层硬编码的两层深度校验改为注入的 MaxDepth。

**Tech Stack:** Go(后端,`llm.CompleteJSON` 走 DeepSeek)、React+TypeScript+Vitest(web)、原生 JS(内置 reader)。

**Spec:** `docs/superpowers/specs/2026-08-01-wiki-incremental-tree-design.md`

## Global Constraints

- 分支:`feat/wiki-incremental-tree`(stacked 于 `feat/wiki-reliability-ontology-rollup`)。
- 深度上限:`LLMWIKI_TREE_MAX_DEPTH`,默认 5(根主题=第 1 层);解析失败或 <1 启动报错(现有行为,勿改)。
- 分裂阈值:`treeMaxDirectPages = 10`(直挂页面数+直接子主题数,严格大于才分裂)。
- 分裂组约束:组数 2–6;每组 ≥2 页;组名 slug 化后非空、互不重复、不与现存兄弟主题 slug 重复;每个 slug 恰好出现一次;无未知 slug。
- 失败保旧树:任何 LLM/校验失败只 `logger.Warn`,树保持原状,绝不让错误传染发布路径。
- 主题 ID:`stableID("topic", parentID, slug)`;slug 由 `topicSlug()`(`nonSlugCharacter` 归一化)生成。
- 测试命令:后端 `go test ./internal/pagewiki/... ./internal/pagewiki/memory/...`;前端 `cd web && npx vitest run`。main 分支既有 flaky DB 测试与 3 个 lint 告警,与本工作无关,只要求本计划触碰的包测试全绿。
- 提交信息用中文,尾部带 `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`。

## File Structure

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/pagewiki/types.go` | 改 | 新增 `DefaultTopicTreeMaxDepth = 5` 导出常量 |
| `internal/pagewiki/memory/repository.go` | 改 | 深度校验配置化(`NewRepository(options...)`) |
| `internal/pagewiki/postgres/repository.go` | 改 | 透传 memory options |
| `internal/pagewiki/ports.go` | 改 | 删 `TreeIndexer`,增 `TreeNavigator` 端口与输入/输出类型 |
| `internal/pagewiki/llm_tree_navigator.go` | 建 | LLM 实现:选落点 prompt、分裂 prompt、分裂校验+错误回填重试 |
| `internal/pagewiki/tree_maintenance.go` | 建 | service 侧:keyed 队列、insertPage、splitTopic、溢出检查、RebuildTopicTree |
| `internal/pagewiki/service.go` | 改 | 删脏标记/防抖,`WithTreeNavigator` 替换 `WithTreeIndexer`,入队点改造 |
| `internal/pagewiki/curation_service.go` | 改 | `markTreeDirty()` → `enqueueUnplacedInserts(ctx)` |
| `internal/pagewiki/llm_tree_indexer.go` | 删 | 连同其测试整体删除 |
| `internal/pagewiki/transport/httpapi/rebuild_topic_tree.go` | 建 | `POST /v1/wiki/topic-tree/rebuild` |
| `main.go` | 改 | maxDepth 注入仓储;navigator 替换 indexer 装配 |
| `web/src/components/wiki/TopicTree.tsx` | 改 | 重写为下钻式 `TopicTreePanel`(保留 `collectPages`) |
| `web/src/pages/WikiBrowsePage.tsx` | 改 | 接入 `TopicTreePanel`,持有 `topicPath` 状态 |
| `web/src/styles/wiki.css` | 改 | 面包屑与下钻列表样式 |
| `internal/pagewiki/transport/httpapi/assets/reader.js` / `reader.css` | 改 | 原生版下钻改造 |

---

### Task 1: 仓储深度校验配置化(修静默丢树 bug)

**Files:**
- Modify: `internal/pagewiki/types.go`(约 163 行 `TopicTree` 附近)
- Modify: `internal/pagewiki/memory/repository.go:41`(NewRepository)、`:501-507`(validateTopics)、`:1070-1075`(ReplaceTopicTree)
- Modify: `internal/pagewiki/postgres/repository.go:23-32`
- Modify: `main.go:206` 附近(仓储构造)与 `main.go:310-320`(maxDepth 解析,提取复用)
- Test: `internal/pagewiki/memory/repository_test.go`(现有 "three levels" 用例,约 699 行)

**Interfaces:**
- Consumes: 现有 `pagewiki.Topic/TopicTree`、`topicDepth(id, topics) int`(遇环返回 3 → 需同步改造,见下)。
- Produces: `pagewiki.DefaultTopicTreeMaxDepth = 5`;`memory.NewRepository(options ...Option)`;`memory.WithTopicTreeMaxDepth(depth int) Option`;`postgres.NewRepository(ctx, pool, scopeID, options ...memory.Option)`。后续任务假设仓储能接受 5 层树。

- [ ] **Step 1: 写失败测试**

在 `internal/pagewiki/memory/repository_test.go` 找到断言三层被拒的用例(搜 `"three levels"`),改为两个用例:深度=5(链式 5 个 Topic,`ParentID` 依次相连)通过 `ReplaceTopicTree`;深度=6 被拒且错误含 `exceeds`。另加一个用例:`NewRepository(memory.WithTopicTreeMaxDepth(2))` 时深度 3 被拒(保住可配置性)。构造链式主题的辅助:

```go
func chainTopics(depth int) []pagewiki.Topic {
	topics := make([]pagewiki.Topic, 0, depth)
	parent := ""
	for i := 0; i < depth; i++ {
		id := fmt.Sprintf("topic_%d", i)
		topics = append(topics, pagewiki.Topic{
			ID: id, ParentID: parent, Slug: fmt.Sprintf("t-%d", i), Title: fmt.Sprintf("T %d", i),
		})
		parent = id
	}
	return topics
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/pagewiki/memory/ -run TestReplaceTopicTree -v`
Expected: FAIL(深度 5 仍被 "exceeds two levels" 拒绝;`WithTopicTreeMaxDepth` 未定义编译错)

- [ ] **Step 3: 实现**

`types.go`(`TopicTree` 定义旁):

```go
// DefaultTopicTreeMaxDepth caps topic nesting when no override is
// configured; root topics are level 1.
const DefaultTopicTreeMaxDepth = 5
```

`memory/repository.go`:结构体加字段 `maxTopicDepth int`;构造器改为:

```go
type Option func(*Repository)

// WithTopicTreeMaxDepth overrides the topic nesting cap (root topics are
// level 1). Values below 1 are ignored.
func WithTopicTreeMaxDepth(depth int) Option {
	return func(r *Repository) {
		if depth >= 1 {
			r.maxTopicDepth = depth
		}
	}
}

func NewRepository(options ...Option) *Repository {
	r := &Repository{ /* 现有字段初始化保持不变 */ }
	r.maxTopicDepth = pagewiki.DefaultTopicTreeMaxDepth
	for _, option := range options {
		option(r)
	}
	return r
}
```

两处 `topicDepth(...) > 2` 均改为 `> r.maxTopicDepth`,错误文案 `"exceeds two levels"` 改为 `fmt.Sprintf` 带上限:`"%w: Topic %q exceeds %d levels"`。`topicDepth` 的遇环哨兵 `return 3` 改为 `return maxDepth + 1`:给函数加参数 `topicDepth(id string, topics map[string]pagewiki.Topic, maxDepth int) int`,两个调用点传 `r.maxTopicDepth`。

`postgres/repository.go:23`:

```go
func NewRepository(
	ctx context.Context, pool *pgxpool.Pool, scopeID string,
	options ...memory.Option,
) (*Repository, error) {
	// ...原有校验...
	repository := &Repository{pool: pool, scopeID: scopeID, memory: memory.NewRepository(options...)}
```

`main.go`:把 `llmwikiTreeMaxDepth` 解析(main.go:310-320 的循环内联代码)提取为:

```go
// parseTreeMaxDepth returns 0 when unset (callers apply the default).
func parseTreeMaxDepth(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 1 {
		return 0, fmt.Errorf("LLMWIKI_TREE_MAX_DEPTH must be a positive integer, got %q", raw)
	}
	return parsed, nil
}
```

原解析点改调此函数(报错文案保持原样包装)。`main.go:206` 仓储构造处解析一次,`>0` 时传 `memory.WithTopicTreeMaxDepth(depth)`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/pagewiki/... ./internal/pagewiki/memory/... && go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "fix(pagewiki): 主题树深度校验改用 MaxDepth 配置,修复深树被静默丢弃"
```

---

### Task 2: TreeNavigator 端口与类型

**Files:**
- Modify: `internal/pagewiki/ports.go`(`TreeIndexInput`/`TreeIndexer` 暂不删,Task 6 删)
- Test: `internal/pagewiki/contracts_test.go` 若有端口断言则顺带补(先读该文件确认形态)

**Interfaces:**
- Produces(后续所有任务依赖,签名逐字使用):

```go
// TreeChildTopic describes one direct child topic of the topic the
// navigator is currently looking at.
type TreeChildTopic struct {
	Slug  string
	Title string
	Pages int // subtree page count, shown to the LLM as a size hint
}

type TreePlacementAction string

const (
	TreePlacementStay   TreePlacementAction = "stay"
	TreePlacementEnter  TreePlacementAction = "enter"
	TreePlacementCreate TreePlacementAction = "create"
)

type TreePlacementInput struct {
	Page        PageCatalogEntry
	Path        []string // topic titles from root to the current topic; empty = root
	Children    []TreeChildTopic
	AllowCreate bool // false once the current topic sits at MaxDepth-1
	Directives  GenerationDirectives
}

// TreePlacementChoice: Enter targets an existing child slug; Create carries
// the new topic's display title (service derives the slug).
type TreePlacementChoice struct {
	Action TreePlacementAction
	Slug   string
	Title  string
}

type TreeSplitPage struct {
	Slug    string
	Title   string
	Summary string
}

type TreeSplitInput struct {
	Path       []string // topic titles from root to the topic being split; empty = root
	Pages      []TreeSplitPage
	Forbidden  []string // existing sibling child-topic slugs the new group slugs must avoid
	Directives GenerationDirectives
}

type TreeSplitGroup struct {
	Title string
	Pages []string // page slugs
}

type TreeNavigator interface {
	ChoosePlacement(context.Context, TreePlacementInput) (TreePlacementChoice, error)
	SplitTopic(context.Context, TreeSplitInput) ([]TreeSplitGroup, error)
}
```

- [ ] **Step 1: 添加类型**(纯类型无行为,不先写测试)——把上面代码原样加进 `ports.go`。
- [ ] **Step 2: 编译**

Run: `go build ./... && go test ./internal/pagewiki/ -run TestContracts -v`
Expected: PASS(无消费者,纯新增)

- [ ] **Step 3: Commit**

```bash
git add internal/pagewiki/ports.go && git commit -m "feat(pagewiki): 定义 TreeNavigator 增量树端口"
```

---

### Task 3: LLM TreeNavigator 实现

**Files:**
- Create: `internal/pagewiki/llm_tree_navigator.go`
- Test: `internal/pagewiki/llm_tree_navigator_test.go`

**Interfaces:**
- Consumes: Task 2 端口类型;`llm.ChatClient`/`llm.CompleteJSON`/`llm.ChatRequest`(形态照抄 `llm_tree_indexer.go:96-102`);`topicSlug()`(`llm_tree_indexer.go:372`,该文件 Task 6 才删,本任务把 `topicSlug` 与 `nonSlugCharacter` 引用保持原位不动);`generationDirectivesPrompt`(现有,搜索定义确认签名)。
- Produces: `NewLLMTreeNavigator(config LLMTreeNavigatorConfig) (*LLMTreeNavigator, error)`,config 字段 `Client llm.ChatClient; Model string; Logger *slog.Logger`。校验:Client/Model 必填,报错前缀 `"create Page Wiki tree navigator: "`。`var _ TreeNavigator = (*LLMTreeNavigator)(nil)`。

**行为规格:**

`ChoosePlacement`:单次 `llm.CompleteJSON[llmPlacementChoice](..., 2)`。请求 JSON(user message):

```go
type llmPlacementRequest struct {
	Page     llmTreePageView   `json:"page"`     // {slug,title,summary}
	Path     []string          `json:"path"`     // 根到当前的主题标题链
	Children []llmChildView    `json:"children"` // {slug,title,pages}
	MayCreate bool             `json:"may_create"`
}
type llmPlacementChoice struct {
	Action string `json:"action"` // "stay"|"enter"|"create"
	Slug   string `json:"slug,omitempty"`
	Title  string `json:"title,omitempty"`
}
```

system prompt(常量 `pageWikiPlacementPrompt`,中文注释、英文正文,风格对齐 `pageWikiTreeIndexerPromptTemplate`):你是团队 wiki 图书管理员,为一篇页面在主题树当前层选择去向;`enter` 只能用 children 里已有的 slug;`create` 仅当 `may_create` 为 true 且 children 中没有语义合适的主题,新主题标题是 1–3 词名词短语,禁止 Misc/Other/General 类兜底名;不确定就 `stay`;只返回一个 JSON 对象。解码后本地把非法 action、`enter` 配未知 slug、`create` 配空标题都归一为 `stay`(防幻觉兜底在实现层做齐,service 不再重复)。返回映射到 `TreePlacementChoice{Action, Slug, Title: strings.TrimSpace(title)}`。

`SplitTopic`:最多两轮。第一轮 `llm.CompleteJSON[llmSplitResponse](..., 1)`(注意 attempts=1,重试由本函数控制);解码成功后 `validateSplitGroups(groups, input)` 校验(全局约束节的 6 条,逐条返回中文错误串);失败则第二轮请求在 user message 末尾追加 `{"previous_error": "<错误串>"}` 重试一次;再失败返回错误(调用方 warn 放弃)。

```go
type llmSplitResponse struct {
	Groups []llmSplitGroup `json:"groups"`
}
type llmSplitGroup struct {
	Title string   `json:"title"`
	Pages []string `json:"pages"`
}

func validateSplitGroups(groups []llmSplitGroup, input TreeSplitInput) error
```

`validateSplitGroups` 规则(顺序即报错优先级):组数 2–6;每组 `topicSlug(title)` 非空;组 slug 互不重复且不在 `input.Forbidden`;每组 pages ≥2;所有组的 pages 并集与 `input.Pages` 的 slug 集完全相等且无重复(缺失/多余/重复分别报出具体 slug)。

- [ ] **Step 1: 写失败测试**——fake `llm.ChatClient`(照抄 `llm_tree_indexer_test.go` 的 fake 形态,先读该文件)覆盖:
  - ChoosePlacement:enter 已知 slug 原样返回;enter 未知 slug → stay;action 乱写 → stay;may_create=false 时 create → stay;create 带标题原样返回。
  - SplitTopic:合法两组直接过;第一轮少一个 slug → 第二轮请求体含 `previous_error` 且第二轮合法结果被采纳;两轮都非法 → 返回错误;组名撞 `Forbidden` → 报错文案含该 slug。
- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/pagewiki/ -run TestLLMTreeNavigator -v`
Expected: FAIL(类型未定义)

- [ ] **Step 3: 实现** `llm_tree_navigator.go`(按上面行为规格;两个 prompt 常量都写全,不留 TODO)。
- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/pagewiki/ -run TestLLMTreeNavigator -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(pagewiki): LLM TreeNavigator——单页落点选择与主题分裂"
```

---

### Task 4: service 增量维护——队列、insertPage、splitTopic

这是核心任务。旧机制(`treeDirty`/`markTreeDirty`/`debounceThenReindex`/`reindexTree`)在本任务被完整替换,`main.go` 同步改装配,任务结束时全仓可编译、全部既有测试通过或被有意更新。

**Files:**
- Create: `internal/pagewiki/tree_maintenance.go`
- Create: `internal/pagewiki/tree_maintenance_test.go`
- Modify: `internal/pagewiki/service.go`(结构体字段、`WithTreeIndexer`→`WithTreeNavigator`、`InjectSession` 入队点 :224-226、删 :310-418 旧机制)
- Modify: `internal/pagewiki/curation_service.go:209-211`
- Modify: `main.go`(装配:`"wiki-indexer"` metered client 复用为 navigator client,`WithTreeNavigator`)
- Modify: 既有引用 `FlushTreeReindex`/`markTreeDirty` 的测试(先 `grep -rn "FlushTreeReindex\|markTreeDirty\|TreeIndexer" --include="*_test.go" internal/` 列清单再逐个改)

**Interfaces:**
- Consumes: Task 2 端口、Task 1 的仓储(接受 MaxDepth 层树)、`stableID`(service.go:1102)、`topicSlug`、`treeMaxDirectPages`。
- Produces(Task 5/6 依赖):

```go
// service.go 结构体替换字段:
//   treeIndexer TreeIndexer / treeDirty chan / treeQuiet / treeMaxWait 删除
//   新增:
//     treeNavigator TreeNavigator
//     treeMaxDepth  int
//     treeTasks     chan treeTask
//     treeTaskKeys  map[string]struct{}
//     treeTaskMu    sync.Mutex
//   treeReindexMu 保留,语义 = 串行化树写入

type TreeMaintenanceConfig struct {
	Navigator TreeNavigator
	MaxDepth  int // 0 → DefaultTopicTreeMaxDepth
	Logger    *slog.Logger
}

func WithTreeNavigator(config TreeMaintenanceConfig) ServiceOption

// tree_maintenance.go:
type treeTask struct{ kind, id string } // kind: "insert"(id=pageID) | "split"(id=topicID,根="")
func (s *Service) enqueueTreeTask(task treeTask)         // keyed 去重,队满 warn 丢弃
func (s *Service) StartTreeMaintenance(ctx context.Context) // 签名不变,内部换成消费 treeTasks
func (s *Service) FlushTreeReindex(ctx context.Context)     // 签名不变,语义=同步排空队列
func (s *Service) RebuildTopicTree(ctx context.Context) error // Task 5 挂 HTTP
```

**行为规格:**

- `NewService` 初始化:`treeTasks = make(chan treeTask, 256)`、`treeTaskKeys = map[string]struct{}{}`。
- `enqueueTreeTask`:key=`task.kind+":"+task.id`;`treeTaskMu` 下已存在则返回;塞 channel,`default` 分支 warn `"Page Wiki tree task queue full"` 并把 key 回收。
- worker(`StartTreeMaintenance` 起的 goroutine)与 `FlushTreeReindex` 共用 `s.processTreeTask(ctx, task)`:先 `treeTaskMu` 下删 key,再 `treeReindexMu` 下执行;任何错误 `logger.Warn` 吞掉。`FlushTreeReindex` 循环 `select { case task := <-s.treeTasks: s.processTreeTask(...); default: return }`——排空即返回;与后台 worker 并发安全(两边都走同一 channel + 互斥执行)。
- `processTreeTask`:
  - `insert`:加载 catalog + tree;pageID 不在 catalog(已 retire)→ 跳过;已有 placement → 跳过(幂等)。从根迭代下钻:每层组装 `TreePlacementInput{Page, Path(标题链), Children(直接子主题:slug/title/子树页数), AllowCreate: depth < s.treeMaxDepth, Directives}`(directives 从 `repository.GenerationSettings` 读,失败 warn 用零值);`stay` → 落当前;`enter` → 下一层;`create` → `slug := topicSlug(choice.Title)`,slug 为空或与兄弟撞车则视为 stay,否则新建 `Topic{ID: stableID("topic", parentID, slug), ParentID: parentID, Slug: slug, Title: choice.Title}` 并落入。落点为根 → 不建 placement(仅当树被改动过才写回)。否则 append `PagePlacement{PageID, TopicID, Rank: 落点现有直挂页数}`,`ReplaceTopicTree` 写回。最后对落点主题(含根)做溢出检查:直挂活跃页面数+直接子主题数 > `treeMaxDirectPages` 且落点深度 < `s.treeMaxDepth` → `enqueueTreeTask(treeTask{"split", topicID})`。根深度视为 0,恒可分裂。
  - `split`:加载 catalog + tree;收集该主题(或根)直挂页面(根=无 placement 的活跃 catalog 页)。可移动页 <2 → 跳过。组装 `TreeSplitInput{Path, Pages(slug/title/summary), Forbidden(现有直接子主题 slug), Directives}` 调 `SplitTopic`;错误 warn 放弃。成功:每组建子主题(ID 规则同上;组 slug 与现存兄弟撞车理论上已被校验,再遇到则整次放弃 warn——防御深度),把组内页面的 placement 改指新主题(Rank=组内下标;根场景是新建 placement),单次 `ReplaceTopicTree` 写回。写回后对每个新子主题复查直挂数,仍超限且未达 MaxDepth → 再入队 split。
- `InjectSession`(service.go:224-226)替换为:

```go
	if s.treeNavigator != nil {
		for _, target := range run.Targets {
			if target.Status == TargetStatusSucceeded && target.PageID != "" {
				s.enqueueTreeTask(treeTask{kind: "insert", id: target.PageID})
			}
		}
	}
```

- `curation_service.go:209-211` 的 `if changed { s.markTreeDirty() }` 替换为 `if changed { s.enqueueUnplacedInserts(ctx) }`;该方法在 `tree_maintenance.go`:加载 catalog+tree,为每个无 placement 的页 `enqueueTreeTask({"insert", pageID})`(错误 warn 返回)。策展 retire 的页 placement 变 stale,`Navigation` 已会过滤(memory/repository.go:534-537),无需清理。
- `main.go`:`NewLLMTreeIndexer` 装配块(约 :349-352)换成 `NewLLMTreeNavigator`(client 仍用 `metered("wiki-indexer")` 的产物,计费 key 不变),`WithTreeIndexer(indexer, logger)` 换 `WithTreeNavigator(TreeMaintenanceConfig{Navigator: navigator, MaxDepth: maxDepth, Logger: logger})`。`service.StartTreeMaintenance(ctx)`(main.go:238)不变。

- [ ] **Step 1: 写失败测试** `tree_maintenance_test.go`,fake navigator(脚本化返回序列)+ `memory.NewRepository()`,全部经 `FlushTreeReindex` 同步驱动(不起 goroutine):
  - 插入:空树 stay → 留根无 placement;两层 enter 链 → placement 落对、Rank 正确;create → 新主题 ID=stableID 且页面落入;已有 placement 的页再入队 → navigator 零调用(幂等);retire 页入队 → 跳过。
  - 溢出:向同一主题塞第 11 页 → split 任务入队且 fake 收到 `SplitTopic` 调用;分裂结果两组 → 子主题建立、placement 迁移、Rank=组内序;`SplitTopic` 返回错 → 树保持原状。
  - 队列:同 key 双入队只处理一次;队满(用 1 容量的测试钩子或塞 257 个不同 key)不 panic。
  - `enqueueUnplacedInserts`:两页无 placement → 两个 insert 任务。
- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/pagewiki/ -run TestTreeMaintenance -v`
Expected: FAIL

- [ ] **Step 3: 实现**(按行为规格;同步删除 service.go:310-418 旧机制与 `treeReindexQuiet`/`treeReindexMaxWait` 常量;`grep` 修全部编译引用)。
- [ ] **Step 4: 全量测试**

Run: `go build ./... && go test ./internal/pagewiki/... ./internal/pagewiki/memory/... ./internal/pagewiki/postgres/...`
Expected: PASS(postgres 包若依赖 DB 而本机跳过,以既有跳过行为为准)

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(pagewiki): 主题树增量维护——keyed 队列、单页插入、溢出分裂"
```

---

### Task 5: RebuildTopicTree 与 HTTP 端点

**Files:**
- Modify: `internal/pagewiki/tree_maintenance.go`
- Create: `internal/pagewiki/transport/httpapi/rebuild_topic_tree.go`
- Modify: `internal/pagewiki/transport/httpapi/endpoints.go` 及路由注册(先读 `router/` 与一个既有 POST 端点如 `inject_file.go`,完全照其注册/鉴权/错误映射模式)
- Test: `internal/pagewiki/tree_maintenance_test.go` 追加;transport 层沿用 `contract_acceptance_test.go` 模式追加一条

**Interfaces:**
- Consumes: Task 4 的队列与 `processTreeTask`。
- Produces: `func (s *Service) RebuildTopicTree(ctx context.Context) error`——`treeReindexMu` 下 `ReplaceTopicTree(ctx, TopicTree{Topics: []Topic{}, Placements: []PagePlacement{}})` 清空,然后按 catalog 顺序(hydration 按 created_at 重放,即发布顺序)逐页 `enqueueTreeTask({"insert", ...})`,最后 `FlushTreeReindex(ctx)` 同步排空——端点返回时树已重建。navigator 未配置时返回 `ErrUnavailable`。HTTP:`POST /v1/wiki/topic-tree/rebuild` → 200 空 body / 503。

- [ ] **Step 1: 写失败测试**——service 层:3 页旧树打乱 → Rebuild 后每页恰好按 fake 脚本归位、旧主题不残留;navigator 缺失 → `ErrUnavailable`。transport 层:POST 打通返回 200。
- [ ] **Step 2: 确认失败** `go test ./internal/pagewiki/... -run 'TestRebuildTopicTree|TestContract' -v` → FAIL
- [ ] **Step 3: 实现**(端点文件结构逐字模仿 `inject_file.go` 的 handler/依赖注入/错误映射)。
- [ ] **Step 4: 确认通过** 同命令 → PASS
- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(pagewiki): RebuildTopicTree 全量重放与 HTTP 端点"
```

---

### Task 6: 删除旧整树索引器

**Files:**
- Delete: `internal/pagewiki/llm_tree_indexer.go`、`internal/pagewiki/llm_tree_indexer_test.go`
- Modify: `internal/pagewiki/ports.go`(删 `TreeIndexInput`/`TreeIndexer`)
- Modify: 残余引用(`grep -rn "TreeIndexer\|TreeIndexInput\|treeMinTopicPages\|WithTreeIndexer" --include="*.go" .` 清零)

**Interfaces:**
- Consumes: Task 3/4 已接管的 `topicSlug`、`treeMaxDirectPages`、`treeDefaultMaxDepth` —— 删除前把 `topicSlug` 函数与 `treeMaxDirectPages` 常量迁至 `tree_maintenance.go`,`treeDefaultMaxDepth` 的用途由 `DefaultTopicTreeMaxDepth` 取代(`NewLLMTreeIndexer` 里的默认化逻辑移入 `WithTreeNavigator`,Task 4 已做,这里只删)。
- Produces: 无新接口;仓库不再有整树重建路径。

- [ ] **Step 1: 迁移与删除**(如上)。
- [ ] **Step 2: 全量验证**

Run: `go build ./... && go test ./internal/pagewiki/... ./internal/pagewiki/memory/...`
Expected: PASS,且 `grep -rn "TreeIndexer" --include="*.go" . | grep -v docs` 无输出

- [ ] **Step 3: Commit**

```bash
git add -A && git commit -m "refactor(pagewiki): 删除整树 LLM 索引器,增量插入全面接管"
```

---

### Task 7: 前端下钻式侧边栏

**Files:**
- Modify: `web/src/components/wiki/TopicTree.tsx`(重写;`collectPages` 原样保留导出)
- Modify: `web/src/pages/WikiBrowsePage.tsx:20`(import)、`:266-280`(渲染段)、状态区(约 :30 附近)
- Modify: `web/src/styles/wiki.css`
- Test: `web/src/components/wiki/TopicTree.test.tsx`(新建,Vitest + Testing Library,若 `web` 尚无组件测试则同时确认 `vitest` 配置能跑 tsx——`npx vitest run` 空跑验证)

**Interfaces:**
- Consumes: `WikiNavigationTopic{id, slug, title, children?, pages?}`、`WikiNavigationPage{id, slug, title}`(`web/src/api/wiki.ts`)。
- Produces:

```tsx
export function collectPages(topics: WikiNavigationTopic[]): WikiNavigationPage[]; // 不变

export function TopicTreePanel({
  topics,          // WikiNavigationTopic[] 根主题
  rootPages,       // WikiNavigationPage[] 未归类页
  topicPath,       // string[] 当前下钻的 topic slug 链
  onNavigate,      // (path: string[]) => void
  selectedSlug,    // string
  onSelect,        // (slug: string) => void
}: { ... }): JSX.Element;
```

**行为规格:** 组件内按 `topicPath` 逐段在 `children` 里找 slug 下降;某段找不到(树刚重建)则截断到最后有效层并调用 `onNavigate` 修正。渲染:面包屑(`<nav aria-label="Topic path">`,首项「全部」,当前项 `aria-current="location"`,各项 button `onNavigate(path.slice(0, i))`)→ 当前层子主题列表(button,`onNavigate([...topicPath, child.slug])`,右侧淡色子树页数,沿用 `collectPages` 计数)→ 当前层直挂页面(现有 `wiki-page-link` 按钮样式与 `aria-current="page"` 逻辑照旧)。根层的直挂页面 = `rootPages`。`RootPageList`/`Topic` 两个旧导出删除。`WikiBrowsePage`:新增 `const [topicPath, setTopicPath] = useState<string[]>([])`;渲染段 :271-279 替换为单个 `<TopicTreePanel topics={topics} rootPages={rootPages} topicPath={topicPath} onNavigate={setTopicPath} selectedSlug={selectedSlug} onSelect={selectPage} />`;`pages`(上一页/下一页,:190)的 `collectPages` 全局顺序不动。css:`.wiki-breadcrumb`(横向 flex、wrap、分隔符 `›` 用 `::after`)、`.wiki-topic-item`(整行 button + 页数 badge),沿用现有变量与 `.wiki-page-link` 观感。

- [ ] **Step 1: 写失败测试**——三层假数据:根层渲染子主题+rootPages;点子主题 → `onNavigate` 收到 `['a']`;`topicPath=['a']` 渲染 a 的子层与面包屑「全部 / A」;点「全部」→ `onNavigate([])`;非法 path `['ghost']` → 渲染根层且 `onNavigate([])` 被调用。
- [ ] **Step 2: 确认失败** `cd web && npx vitest run` → FAIL
- [ ] **Step 3: 实现**(组件重写 + WikiBrowsePage 接入 + css)。
- [ ] **Step 4: 确认通过** `cd web && npx vitest run && npx tsc --noEmit`(若仓库用别的 typecheck 脚本以 package.json 为准)→ PASS
- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(web): wiki 侧边栏改为单层下钻+面包屑"
```

---

### Task 8: 内置 reader 下钻改造

**Files:**
- Modify: `internal/pagewiki/transport/httpapi/assets/reader.js:36-70`(`collectPages` 保留;`renderNavigation`/`renderTopic` 重写)及其调用点(搜 `renderNavigation(`)
- Modify: `internal/pagewiki/transport/httpapi/assets/reader.css:188-199` 附近
- Modify: `internal/pagewiki/transport/httpapi/assets/reader.html` 若面包屑需要新容器节点(先读该文件)

**Interfaces:**
- Consumes: 同一 navigation JSON(`roots`/`pages`)。
- Produces: 模块级 `let topicPath = []`;`renderNavigation(navigation)` 按 `topicPath` 下降(非法段重置为 `[]`),渲染面包屑(「全部」+各级 title,button 点击截断 path 后重新 `renderNavigation`)、当前层主题 button(点击 push slug 重渲)、当前层页面 button(`pageButton` 复用);`page-count` 与返回的全量 `collectPages` 顺序保持现状。轮询刷新(现有后台刷新逻辑)复用最新 navigation 重渲时保留 `topicPath`。

- [ ] **Step 1: 实现**(原生 JS 无测试设施,不新增;逻辑与 Task 7 组件一致)。
- [ ] **Step 2: 手工验证**——`go build ./...` 通过;若本机可起服务:起服打开 `/v1/wiki/reader`,验证下钻/面包屑/选中态;不可起则记录待 workstation 验收。
- [ ] **Step 3: Commit**

```bash
git add -A && git commit -m "feat(pagewiki): 内置 reader 侧边栏同步改为下钻式"
```

---

## Self-Review 记录

1. **Spec 覆盖**:§1 插入=Task 3+4;§2 分裂=Task 3(校验)+4(执行);§3 队列/入队点/Flush/Rebuild=Task 4+5;§4 深度修复=Task 1;§5 前端=Task 7+8;§6 测试分布各任务;删除清单=Task 4(防抖)+6(索引器);「每组≥2页」写入全局约束。无缺口。
2. **占位符**:无 TBD;两处「照既有文件模式」(Task 5 端点注册、Task 3 fake client)均指明了要先读的具体文件。
3. **类型一致性**:`TreeNavigator`/`TreePlacementInput`/`TreeSplitInput`/`treeTask`/`WithTreeNavigator`/`TopicTreePanel` 各任务引用与 Task 2/4/7 定义逐字一致;`topicSlug`/`stableID`/`treeMaxDirectPages` 迁移路径在 Task 6 明确。
