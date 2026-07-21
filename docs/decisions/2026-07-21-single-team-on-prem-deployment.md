# 单团队本地部署与 PAX Memory 接口边界

状态：已接受

日期：2026-07-21

相关文档：

- [代码模块架构](../code-architecture.md)
- [Team Note 设计](../../doc/team-note-design.md)
- [General Recall v3](./2026-07-16-general-recall-v3-optimization.md)
- [Hint Recall v0](./2026-07-16-hint-recall-v0.md)

## 背景

PAX Nexus 当前以一个 Team Memory 进程运行，依赖 PostgreSQL、River、抽取模型和
Embedding 模型。根组合代码负责读取环境变量、构造基础设施 Adapter、启动队列和
Hertz 服务，并在关闭时排空本进程拥有的任务。

现有核心通过认证后的 `scope_id` 隔离协作数据。HTTP Adapter 从 Bearer 凭证解析
scope，请求体不能选择 scope。PostgreSQL 的主键、关系和异步抽取任务都携带同一
`scope_id`。

第一版本地部署产品只服务一个受信任 Team，不需要 Team 创建、Team 选择、租户路由、
每 Team 数据库或多租户控制面。对外暴露一个可配置 Team ID 只会制造没有实际选择的
运维概念。另一方面，从核心和数据库中删除 `scope_id` 会波及 Session Lake、Team
Note、抽取任务、recall observation、队列、授权和评测，却不会改善单 Team 产品。

PAX 体系还需要一个统一的 Agent Memory 接入边界：Agent 通过 API 获得身份和凭证，
写入侧提交原始观察，recall 侧既能被动交付 Team Note，也能提示 LLM Wiki 中可能存在
的知识，并支持 Agent 主动进行语义 search/get。上述路径必须共享认证、预算和追踪，
但 Team Note 与 LLM Wiki 仍然是互不依赖的产品 Module。

## 决策

### 1. 一个安装实例就是一个 Team

将本地部署包装成独立的外层部署 Module。一个运行中的安装实例代表一个 Team，产品
接口不暴露 Team ID。

- 配置和 HTTP 请求都不包含 `team_id`；
- 不提供 Team CRUD、租户发现或租户选择接口；
- `user_id`、`agent_id` 和 `session_id` 是 Team 内身份，不是租户标识；
- 认证 Adapter 实现现有 `ScopeResolver` Seam，并向请求上下文注入固定且不可配置的
  内部 scope sentinel；
- 初始 sentinel 使用 `local-team`，以兼容当前本地默认数据；
- `scope_id` 保留在核心 Contract、PostgreSQL、索引、外键、队列任务和评测 fixture
  中，它是内部协作分区键，不是对外 Team ID。

本地部署组合位于外层目录，例如：

```text
cmd/team-memory-onprem/
internal/deployment/onprem/
deploy/onprem/
```

部署 Module 负责配置、Secret、具体 Adapter 构造、Schema 兼容检查、队列与 HTTP
生命周期、readiness 和优雅关闭。产品 Module 和评测 Module 不得反向依赖部署 Module。

### 2. 每个 Agent 使用独立凭证

一个 Agent Credential 固定绑定 `user_id + agent_id`。业务请求从认证 Principal 取得
这两个值，不能允许客户端在写入或 recall 请求体中冒充另一身份。所有 Principal 都映射
到同一个内部 `local-team` scope。

只设计 HTTP API，不设计 CLI：

| 操作 | HTTP API | 说明 |
| --- | --- | --- |
| 管理员签发一次性 enrollment | `POST /v1/admin/agent-enrollments` | 指定 `user_id`、`agent_id`、有效期和可选权限 |
| Agent 换取 API Key | `POST /v1/agent-enrollments/exchange` | enrollment 只能使用一次；明文 Key 只返回一次 |
| Agent 查看当前身份 | `GET /v1/agent-identity` | 返回凭证绑定的 Principal 和权限 |
| Agent 轮换凭证 | `POST /v1/agent-credentials/rotate` | 创建新 Key，并给旧 Key 一个短暂重叠窗口 |
| 管理员撤销凭证 | `DELETE /v1/admin/agent-credentials/{credential_id}` | 立即使指定凭证失效 |

API Key 只存不可逆摘要，记录 `credential_id`、Principal、权限、创建/过期/撤销时间和
最后使用时间。首次管理员凭证由部署 Secret 注入，不引入 Team bootstrap API。

Credential Store 是部署认证 Module 的 Interface，不进入 Team Note 或 LLM Wiki。
实现可以使用同一 PostgreSQL 实例中的独立表，也可以使用独立 Secret/Identity Store。

### 3. 写入语义与 paxm 对齐为 Observation

Team Memory 的输入是 Session Event，经过异步抽取后可能产生零到多个 Team Note 或
Wiki Page revision。它不是“一次 put 就得到一个可以按同一 ID 读回的 Memory”。因此
不能继续把该语义伪装成严格的 `paxm.put`。

在 paxm 中增加可选的 Observation Provider Interface：

```text
paxm.observeBatch -> ObservationReceipt
paxm.search       -> MemoryHit[]
paxm.get          -> MemoryDocument
paxm.capabilities -> observe_batch/search/get 能力声明
```

`observeBatch` 返回 ingestion receipt、幂等键和处理状态，不承诺写入项与派生记忆一一
对应。`search` 继续作为统一的检索 Interface。`get` 是可选 Interface，用于根据稳定
`MemoryRef` 读取完整 Wiki Page；只支持搜索、不支持文档读取的 Provider 可以不声明
`get`。

本服务的 HTTP 语义与上述 Contract 一致：

```text
POST /v1/observations
POST /v1/memory/search
POST /v1/memory/get
```

现有 `/v1/session-batches` 和 `/v1/notes/recall` 可以在迁移期作为内部或兼容接口保留。
所有 Hertz HTTP Interface 变更仍以 `idl/` 下的 Thrift 定义为 source of truth。

### 4. Recall Router 组合多条有类型的搜索路径

在 Team Note 与 LLM Wiki 之外增加外层 `Recall Router` Module。Router 依赖两个产品
暴露的小 Interface；两个产品彼此不能 import，也不能直接查询对方的 Store。

```text
paxm.search(intent=passive)
    -> Recall Router
       -> [parallel, cancellable] Team Note Interface -> Team Note Store -> PlanRecall
       -> [parallel, cancellable] LLM Wiki Hint Interface -> LLM Wiki Store
       <- early return sufficient evidence, otherwise compose at deadline
    <- MemoryHit[]

paxm.search(intent=active, source=llm_wiki)
    -> Recall Router
       -> LLM Wiki Search Interface -> LLM Wiki Store
       <- page references + snippets
    <- MemoryHit[]

paxm.get(page_ref)
    -> LLM Wiki Document Resolver -> LLM Wiki Store
    <- MemoryDocument
```

这里的分叉发生在产品 Module 的 Interface，而不是 Store Interface。Router 不读取产品
表，也不接收未经产品授权和筛选的原始行。它收集的是 Team Note 和 LLM Wiki 各自返回
的产品结果，再按 disposition、顶层预算和降级策略组合为 paxm result。

Router 预留注册多个 `SearchPath` 的 Seam，但当前定义三条路径：

1. **Team Note Evidence Path**：被动 recall 的主要证据路径。它调用现有
   `RecallNotes`，并保留 `PlanRecall` 对候选检索、关系展开、最终状态选择和 token
   packing 的所有权。Router 不复制或绕过 Team Note 排名策略。
2. **LLM Wiki Hint Path**：被动 recall 的导航提示路径。它只返回一个短提示和建议的
   search 入口，不返回 Wiki Page 全文，也不得把 snippet 当成事实证据。
3. **LLM Wiki Semantic Path**：Agent 主动调用的语义检索路径。`search` 返回轻量 page
   ref、标题、snippet、相关度和 provenance 摘要；Agent 再用 `get` 读取选中的完整页面。

`get` 不是 SearchPath。它是独立的 `DocumentResolver` Interface，因为读取一个已知引用
与搜索、排名和提示是不同职责。

统一 search result 增加可选的 typed disposition：

```text
evidence   可作为答案证据注入上下文
hint       只提示 Agent 值得主动搜索，不能作为事实引用
reference  主动 search 命中的文档引用，需要 get 后才能读取完整内容
```

该字段应作为 paxm `MemoryHit` 的向后兼容扩展；旧客户端可以继续读取 text、score 和
metadata，新客户端能够避免把 hint/reference 当作 evidence。

#### 被动 recall 策略

启用 Wiki Hint Path 时，Router 使用 speculative fan-out，而不是串行 fallback：

1. 在同一请求 deadline 下，并行启动 Team Note Evidence Path 和 LLM Wiki Hint Path；
2. 两条路径都必须廉价、有界并支持 context cancellation；被动 Wiki 路径只检索 hint
   lead，不能读取页面全文或执行无界 Agent/LLM 循环；
3. Router 作为 Arbiter 监听两个结果。如果 Team Note 先返回并通过 Evidence
   Sufficiency Gate，立即取消 Wiki path 并把 evidence 返回给 paxm；
4. 如果 Team Note 先返回但证据不充分，Router 等待已经并行执行的 Wiki path，直到它
   返回或共享 deadline 到达；
5. 如果 Wiki hint 先返回，Router 暂存它并继续等待 Team Note。Hint 不能抢先替代事实
   evidence；
6. 到达 deadline 时，Router 使用所有已完成且通过授权的结果，并在 trace 中记录 timeout
   或 degraded path；
7. Team Note evidence 优先占用共享 token budget；Wiki hint 最多一个，并只使用剩余
   预算；
8. 被动流程绝不自动调用 Wiki `get`。

并行阶段只允许 Wiki Store/index 的 hint-lead 检索。若 hint 文本需要额外 LLM 合成，
应使用预计算结果，或在 Team Note 判定不充分后才进入第二阶段；不能为每次 passive
recall 无条件启动昂贵的 LLM 调用。

Evidence Sufficiency 不能等同于“最高 relevance 很高”。它是 `PlanRecall` 返回的可审计
决策摘要，至少考虑：

- 请求要求的 fact slot 覆盖率；
- 已选 evidence 的最低 Evidence Confidence；
- temporal、superseded、conflict 和 provenance hard gate；
- 是否有 answer-bearing evidence 因 token budget 被丢弃。

只有固定 cohort 校准后的组合 Gate 才能触发 early return。不能因为一个 case 调整全局
confidence threshold。Router 只消费 Sufficiency 决策，不重新实现 Team Note 的评分。

现有 `RecallNotes` 方法保持不变，但 `NoteEnvelope` 需要向后兼容地增加一个由
`PlanRecall` 产生的 decision summary，例如：

```go
type RecallDecisionSummary struct {
    EvidenceSufficient bool               `json:"evidence_sufficient"`
    ReasonCodes        []RecallReasonCode `json:"reason_codes,omitempty"`
}
```

详细 coverage、scorecard 和 budget drop 留在 Recall Trace；Router 的 Interface 只需要
`EvidenceSufficient` 和稳定 reason code。这样 early-return policy 仍封装在 Team Note
Module 内，不会在 Router 中形成第二套 evidence 判断。

取消必须沿 context 传播到 Store Adapter。实现不得为 early return 泄漏 goroutine、连接
或后台查询；trace 记录 `early_return`、`path_cancelled`、各路径完成时间和被取消的推测
工作量。这样既降低低置信度请求的尾延迟，也能观察并控制并行检索带来的额外负载。

现有 Hint Recall 在真实 Agent pilot 中没有提升准确率且输入 token 增加 14.9 倍，因此
Wiki Hint Path 初始保持关闭或 shadow/evaluation 状态。只有固定 replay 与真实 Agent
cohort 同时证明有效，才能成为默认路径。

#### 主动 Wiki search/get 策略

- 主动 `search` 明确选择 LLM Wiki source，不先查询 Team Note；
- search 只返回有限 snippet 和稳定 `MemoryRef`，避免一次调用把整页塞入上下文；
- `get` 必须重新执行当前 Principal、scope、visibility 和 provenance 授权检查，不能把
  之前的 search hit 当作永久授权；
- `get` 有独立的文档大小/token 上限；
- Agent 可以根据首次结果重写 query 并继续 search，而不是让 Router 隐式执行无界循环。

#### 预算、错误与追踪

Router 拥有跨路径的顶层预算分配和降级策略；每个产品仍拥有本路径内部的检索和排序。

- Team Note 是被动 evidence 的主要路径；Wiki hint 失败不能阻塞已有 evidence；
- Agent 显式调用 Wiki search/get 时，该路径失败必须作为请求错误返回，不能静默伪装成
  空结果；
- 顶层 Recall Trace 记录路径的 eligible/run/skipped 状态、延迟、候选数量、拒绝原因、
  budget drop 和错误；
- Team Note `PlanRecall` trace 与 Wiki trace 作为子 trace 保留，不能只汇总成一个最终
  relevance score。

### 5. 保持产品 Module 解耦

建议的边界为：

```text
internal/teamnote/       Team Note 产品与现有 RecallNotes/PlanRecall
internal/llmwiki/        Wiki 维护、Hint discovery、semantic search、page get
internal/recall/         外层 Router、路径策略、共享预算与顶层 trace
internal/deployment/     认证、Credential Store、组合和运行生命周期
```

`internal/recall` 是组合 Module，不拥有 Team Note 或 Wiki 数据。Architecture Test 应保证：

- `teamnote` 不 import `llmwiki` 或 `recall`；
- `llmwiki` 不 import `teamnote` 或 `recall`；
- 两个产品只通过外层 Router 的 consumer-owned Interface 被组合；
- transport 和 deployment 可以依赖 Router，Router 不依赖具体 HTTP 或 PostgreSQL
  Adapter。

现有 Team Note Runtime Interface 保持不变：

```go
type Runtime interface {
    ObserveSession(context.Context, SessionBatch) (IngestReceipt, error)
    RecallNotes(context.Context, RecallRequest) (NoteEnvelope, error)
}
```

LLM Wiki 的领域词汇需要增加 **Passive Wiki Hint**：它是对 Active Recall 的导航提示，
不是被动交付 Wiki Page。这样既支持 hint-based passive recall，也保持“Wiki 页面通过主动
search/get 浏览”的产品边界。

## 数据库影响

单 Team scope 决策本身不需要删除字段、修改主键或迁移现有 Team Note 数据。当前使用
`local-team` 的数据库可以直接沿用。

但新增能力有各自独立的增量持久化需求：

- 动态 Agent enrollment、轮换和撤销需要 Credential Store；
- LLM Wiki 落地时需要自己的 page、revision、index、provenance 和 maintenance cursor
  Store；
- Recall Router 本身应尽量无状态，trace 可以进入现有 observation/telemetry Store 或
  独立 trace Adapter。

这些变化不得修改 Team Note 表的语义，也不得把 Team Note 和 Wiki 合并成统一记忆表。
同一 PostgreSQL 实例可以承载多个 Module 的表，但 Schema 所有权必须分开。具体 Wiki
Schema 和索引方案由单独 ADR 决定。

如果现有数据库包含其他 scope，或包含多个不同 scope，本地部署不能在启动时任意选择
一个 scope。需要另行评审的显式导入或 normalization 流程，以避免恢复或配置错误后暴露
错误的协作数据。

## 被否决的方案

### 从核心和数据库删除 `scope_id`

初版本地部署不采用。它把部署选择耦合为高风险的 Schema 和领域重写，却没有给单 Team
运维者带来可见收益。

### 暴露一个可配置 Team ID

不采用。只有一个合法选择的值不是有用配置，还会制造 scope drift、备份恢复歧义和并不
承诺的多租户契约。

### 所有 API Key 都是 Team 级共享 Key

不采用。共享 Key 无法可靠归因、单独撤销或限制某个 Agent。Agent Credential 应绑定
Principal，同时仍然映射到固定内部 scope。

### 把 Session Event 写入伪装成 `paxm.put`

不采用。异步的零到多派生结果无法满足 put/search 同 ID、同内容的 stored-memory 语义。

### 让 paxm 把 Team Note 和 Wiki 配成两个无类型 Provider 后自行混排

不采用。paxm 可以继续负责跨 Provider profile 路由，但 PAX Nexus 内部必须知道
evidence、hint 和 reference 的不同安全语义，并统一处理授权、提示数量和共享预算。

### 在被动 recall 中自动读取 Wiki 全文

不采用。它会放大延迟和 token 使用，并把“这里可能值得查”误变为未经 Agent 选择的事实
上下文。

## 结果与代价

- 新安装不需要 Team bootstrap 或 Team 选择；
- 所有 Agent Credential 共享一个内部 scope，但拥有独立 Principal 和生命周期；
- PAX Memory 写入、search 和 get 具有清晰且可做 conformance test 的语义；
- Team Note 与 LLM Wiki 可以独立演进，Recall Router 可以增加新路径而不扩大产品
  Interface；
- 多路径 recall 需要额外的预算策略、typed result、trace 和跨路径评测；
- scope 决策不改现有数据库，但 Credential Store 与真正的 LLM Wiki 会带来独立的增量
  Schema；
- 未来多 Team hosted 产品可以替换外层认证与 scope routing Adapter，复用 scope-aware
  core，不改变单 Team 本地部署契约。

## 实现状态

本决策已实现单 Team Credential Store、Agent enrollment/exchange/rotation/revocation、
Observation API、typed Memory search/get Contract、Team Note Evidence Sufficiency 摘要，
以及 Team Note 与 Wiki hint 的可取消并行 Recall Router。所有 HTTP Contract 由
`idl/team_memory.thrift` 生成 Hertz model/router。

Wiki hint 默认关闭。LLM Wiki page/revision/index Store 与其授权感知的 search/get Adapter
仍属于本决策明确列出的非目标；在该 Adapter 注入前，显式 Wiki search/get 返回路径不可用，
启动配置也不允许开启 passive Wiki hint。这样避免用空实现把未落地的 Wiki 能力伪装成可用。

## 验收标准

- 支持的本地部署配置和 HTTP Interface 不包含 Team ID；
- 有效 Agent Key 解析为绑定的 `user_id + agent_id` 和固定内部 scope；请求体不能覆盖
  Principal 或 scope；
- enrollment 一次性使用，Key 明文只返回一次，轮换和撤销行为可测试；
- `observeBatch` 返回 ingestion receipt，不承诺派生记忆的一一对应 ID；
- 启用 Wiki Hint 时，两条被动检索路径在同一 deadline 下并行启动；Team Note 的
  `EvidenceSufficient` 可触发取消 Wiki path 并 early return；
- 被动 recall 始终优先交付 Team Note evidence，Wiki hint 最多一个且不会触发 Wiki
  page get；
- 主动 Wiki search 返回稳定 page ref，get 重新授权并返回带 provenance 的页面；
- 一个可选路径失败时按策略降级；显式调用的必需路径失败时返回可判断的错误；
- Recall Trace 能区分三条路径及其候选、拒绝、budget drop、early return、取消和超时；
- Team Note 与 LLM Wiki 的依赖方向由 architecture test 保护；
- 当前 `local-team` 数据无需 scope 或 Team Note Schema 迁移即可读取；
- IDL 改动运行 `make generate`，所有新增手写包达到覆盖率门槛，并通过 `make lint test`。

## 非目标

- 在一个安装中支持多个 Team；
- 增加 hosted 多租户控制面；
- 在本 ADR 中确定 LLM Wiki 的具体 Schema、索引算法或维护模型；
- 默认启用未经固定 replay 和真实 Agent cohort 验证的 Wiki Hint Path；
- 设计 CLI；
- 选择或捆绑某个特定的本地抽取/Embedding 模型运行时。
