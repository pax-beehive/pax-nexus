# Knowledge Eval Platform 设计

日期：2026-07-29
状态：V1 已实现；真实 LLM Wiki builder 与 LoCoMo 对照实验接入中

## 1. 目标

做一个可持续迭代的知识系统评测平台：

1. 对一组输入数据运行某个 Builder，生成一个 Knowledge Artifact。
2. 既能直接评价 Artifact 本身，也能通过 Search、Get、Recall 等接口评价它的使用效果。
3. 支持固定问答，也支持 tester agent 多轮调用工具、操作环境，再由 benchmark 判断任务是否成功。
4. 每次 Builder、Artifact schema、Recall 实现、模型或配置变化后，都能重跑实验并保存一条可比较的记录。
5. 首先服务 LLM Wiki 和 PageWiki，同时允许 Team Note 接入同一套抽象。
6. 每个 Artifact 都能以只读、可复现的方式被人打开、浏览和与前序版本对比。

平台不规定 Wiki、PageWiki 或 Team Note 的内部 schema。Artifact 和 benchmark
的私有数据都以 opaque payload 保存，稳定的是它们对外声明的能力、版本和实验血缘。

## 2. 一句话模型

```text
Benchmark Group
    × Builder Arm
    × Checkpoint
        -> Knowledge Artifact
        -> Artifact Driver
            -> Subject（给 benchmark 使用的能力集合）
            -> Artifact View（给人浏览的只读视图）
    × Benchmark Arm
        -> Trial Result
        -> Run / Dashboard
```

这里不是无条件的全局笛卡尔积。只有同一个数据世界、同一个 checkpoint，
并且能力兼容的 Subject 和 Benchmark Arm 才能组成 Trial。

## 3. 核心概念

### 3.1 Benchmark Bundle 和 Group

一个 Benchmark Bundle 是一个带版本、不可变的评测数据包。它可以包含任意
schema，由对应的 Benchmark Adapter 解释。

Adapter 把 Bundle 枚举成若干 Group。每个 Group 代表一个独立的数据世界，
例如一个 LoCoMo conversation、一个 LongMemEval case，或一个 Team Note
协作场景。

一个 Group 至少分成两部分：

- `BuildInput`：Builder 可见，用来构建或更新 Artifact。
- `EvaluationInput`：只有 benchmark 侧可见，包含 gold answer、judge rubric、
  tester task、环境初始状态等。

两者都使用 `OpaqueRef`。平台不得把 evaluator-only 数据泄露给 Builder。

```go
type BenchmarkGroup struct {
    GroupID        string
    WorldID        string
    CheckpointID   string
    BuildInput     OpaqueRef
    EvaluationInput OpaqueRef
}
```

`WorldID` 标识连续的数据世界；`CheckpointID` 标识这个世界演进到哪一步。
同一 Wiki 的增量维护通过相同 `WorldID`、不同 `CheckpointID` 表达。

### 3.2 Knowledge Artifact

Knowledge Artifact 是 Builder 的持久化产物。它可以是文件树、Git revision、
数据库 snapshot、PageWiki 导出、Team Note snapshot，或未来尚未定义的结构。

```go
type OpaqueRef struct {
    Kind          string
    SchemaVersion string
    URI           string
    SHA256        string
}

type ArtifactRecord struct {
    ArtifactID   string
    Kind         string
    WorldID      string
    GroupID      string
    CheckpointID string
    BaseID       string
    Payload      OpaqueRef
    Provenance   Provenance
}
```

Core 只校验 identity、digest、血缘和生命周期，不反序列化 `Payload`。
`BaseID` 用于表达增量构建时的前序 Artifact。

### 3.3 Subject

Subject 是“可被评测的对象”。Artifact Driver 读取一种 Artifact schema，
将它暴露为一组小而稳定、带版本的能力：

```text
projection/wiki-corpus:v1
projection/team-note-snapshot:v1
recall/search:v1
recall/get:v1
recall/navigate:v1
recall/passive:v1
```

Subject 不是统一大接口。它只实现自己支持的小接口：

```go
type Subject interface {
    ID() string
    Capabilities() CapabilitySet
}

type Projector interface {
    Project(context.Context, ProjectionRequest) (OpaqueRef, error)
}

type Searcher interface {
    Search(context.Context, SearchRequest) (SearchResponse, error)
}

type Getter interface {
    Get(context.Context, GetRequest) (GetResponse, error)
}

type Navigator interface {
    Navigate(context.Context, NavigateRequest) (NavigateResponse, error)
}

type PassiveRecaller interface {
    Recall(context.Context, PassiveRecallRequest) (PassiveRecallResponse, error)
}
```

请求和响应协议本身也必须带版本。这样 Team Note 的 `RecallNotes` 不需要被伪装成
Wiki 的 `Search`，而 PageWiki 可以同时暴露 Search、Get 和 Navigate。

### 3.4 Artifact View

Artifact View 是 Knowledge Artifact 的人类可读视图。它解决“这个实验生成的
Wiki 或 Team Note 到底长什么样”，不参与 benchmark 的 pass/fail。

它和 projection 必须分开：

- projection 是稳定的机器评测协议，允许 benchmark 跨 Artifact schema 比较。
- Artifact View 是面向人的展示，可以保留产品自己的导航和阅读体验。
- raw view 用于调试原始文件、表、manifest 和中间数据。
- diff view 用于比较当前 Artifact 和 `BaseID` 指向的前序 Artifact。

Artifact View 由产物侧提供，因为只有产物侧理解原生 schema：

```go
type ArtifactViewProvider interface {
    RenderView(context.Context, ArtifactViewRequest) (ArtifactViewRecord, error)
}

type ArtifactViewRequest struct {
    Artifact     ArtifactRecord
    BaseArtifact *ArtifactRecord
    Kind         string
    ViewConfig   *OpaqueRef
}

type ArtifactViewRecord struct {
    ViewID          string
    ArtifactID      string
    Kind            string
    RendererID      string
    RendererVersion string
    Payload         OpaqueRef
    EntryPoint      string
}
```

第一版支持四种 `Kind`：

- `native`：尽量还原产品真实阅读体验。
- `canonical`：把 benchmark 使用的 projection 渲染成人可读形式。
- `raw`：只读浏览或下载原始 Artifact 和中间数据。
- `diff`：当前 Artifact 与 base Artifact 的结构和内容差异。

View 是 Artifact 的派生产物，可以首次打开时惰性生成，但生成后必须不可变。
缓存 identity 至少包含 Artifact digest、Renderer ID/version 和 view config digest。
Dashboard 只展示对应实验快照，不能跳到会继续变化的线上 Wiki 或数据库。

## 4. Adapter 放在哪里

系统有两个不同方向的 adapter。

### 4.1 Artifact Driver 放在产物侧

Artifact Driver 理解 Artifact 的原生 schema，并把它转成 Subject：

```go
type ArtifactDriver interface {
    Descriptor() ArtifactDriverDescriptor
    Open(context.Context, OpenArtifactRequest) (Subject, error)
}
```

它负责：

- 读取、校验特定 Artifact schema。
- 实现 projection，例如把不同 Wiki schema 投影成 `wiki-corpus:v1`。
- 暴露 Search、Get、Navigate 或 Passive Recall。
- 可选实现 `ArtifactViewProvider`，生成 native、canonical、raw 和 diff 视图。
- 应用 exposure config，例如索引版本、top-k、token budget、可见字段。
- 记录调用 trace 和中间观察。

它不负责：

- 解释 benchmark gold data。
- 决定 pass/fail。
- 运行 benchmark 的 tester environment。

建议位置：

```text
internal/eval/knowledgeeval/artifact/
  llmwiki/
  pagewiki/
  teamnote/
```

同一种产品出现新 Artifact schema 时，新增或升级 Artifact Driver，不修改
benchmark。

### 4.2 Benchmark Adapter 放在 benchmark 侧

Benchmark Adapter 理解 Benchmark Bundle 的 schema 和官方协议：

```go
type BenchmarkAdapter interface {
    Descriptor() BenchmarkDescriptor
    Plan(context.Context, BenchmarkPlanRequest) (BenchmarkPlan, error)
    Run(context.Context, BenchmarkRunRequest) (BenchmarkResult, error)
}
```

它负责：

- 校验和读取 benchmark bundle。
- 枚举 Group、checkpoint 和 benchmark arm。
- 切分 Builder 可见输入与 evaluator-only 输入。
- 声明需要的 Subject capabilities 和版本。
- 执行 artifact scorer、QA judge 或 tester-agent protocol。
- 定义 metric、reward、pass/fail 和聚合规则。

它不负责：

- 理解每一种 Artifact 的私有 schema。
- 直接访问 LLM Wiki、PageWiki 或 Team Note 的内部表和文件。
- 决定哪个产品实现应该参加这次实验。

建议位置：

```text
internal/eval/knowledgeeval/benchmark/
  wikigenbench/
  locomo/
  longmemeval/
  tauknowledge/
  teamnotestage/
```

### 4.3 中间层只放 Binding

Binding 是声明式配置，不包含 schema 转换代码：

```go
type BindingSpec struct {
    ArtifactSelector   ArtifactSelector
    ArtifactDriverID   string
    ExposureConfig     *OpaqueRef
    BenchmarkRef       OpaqueRef
    BenchmarkAdapterID string
    BenchmarkConfig    *OpaqueRef
}
```

Runner 根据 Binding 选 Artifact Driver 和 Benchmark Adapter，检查 capability
兼容性，再展开 Trial。

因此，adapter 不是 `Artifact × Benchmark` 的成对实现。否则 M 种 Artifact
和 N 种 benchmark 最坏会产生 M×N 个 glue adapter。正确的连接点是带版本的
capability/projection contract：

```text
Artifact schema -> Artifact Driver -> capability <- Benchmark Adapter
```

只有当双方确实无法通过通用 capability 表达时，才允许一个显式、带版本、
有负责人和淘汰计划的 pair-specific bridge；它是例外，不是主模型。

## 5. Builder

Builder 是另一个独立黑盒，负责从 Group 的 `BuildInput` 构建 Artifact：

```go
type BuilderDriver interface {
    Descriptor() BuilderDescriptor
    Build(context.Context, BuildRequest) (ArtifactRecord, error)
}

type BuildRequest struct {
    Group         BenchmarkGroup
    BaseArtifact  *ArtifactRecord
    BuilderConfig *OpaqueRef
}
```

Builder arm 至少记录：

- Builder ID 和版本。
- 模型、prompt、tooling、代码 revision。
- 配置 digest。
- 输入 bundle、Group 和 checkpoint。
- base artifact。
- 随机种子、运行时间、token 和费用。

Builder Driver 和 Artifact Driver 分开。前者回答“怎样生成产物”，后者回答
“怎样读取并暴露产物”。同一个 Artifact 可以被多个 recall/index 配置打开，
而不需要重新构建。

## 6. 三种 Benchmark 模式

三种模式共用一个 `BenchmarkAdapter` 接口，区别只在所需 capability 和内部协议。

### 6.1 Artifact Quality

直接评价 Artifact：

```text
Subject --projection/wiki-corpus:v1--> benchmark scorer
```

可包含：

- 覆盖率和信息完整性。
- factuality、引用正确性和可验证性。
- 页面组织、主题结构、冗余和可读性。
- 新旧事实冲突、过期内容泄漏。
- 增量更新后的保留、修正和破坏率。

WIKIGENBENCH、STORM/FreshWiki、FActScore、ALCE 中合适的指标可以分别实现为
Benchmark Adapter 或可复用 scorer component。

### 6.2 QA

Benchmark Adapter 读取问题和 gold answer，通过 Subject 的 Search/Get 等能力
获取上下文，再运行固定 reader 或直接判断检索结果：

```text
question -> Search/Get -> context -> reader -> answer -> judge
```

必须分别记录 retrieval、packing、reader answer 和 judge 结果，不能只保存最终
pass/fail。

### 6.3 Tester Agent

Benchmark Adapter 创建 tester environment，把 Subject capabilities 注册为工具：

```text
tester agent <-> Search/Get/Navigate/Recall
             <-> benchmark environment
             -> terminal state
             -> success evaluator
```

职责边界：

- Search/Get/Recall 的实现和 trace 属于 Artifact Driver。
- task、环境状态机、允许动作、成功条件属于 Benchmark Adapter。
- tester model、prompt、budget 和 seed 是独立的 trial axis。

这样可以复用类似 τ-Knowledge 的多轮任务模式，也可以为 Team Note 定义团队协作
任务，而无需改变平台 Core。

## 7. 实验矩阵

### 7.1 Build Matrix

```text
Benchmark Group × Builder Arm × Checkpoint -> ArtifactRecord
```

同一 Group 可以比较不同 Builder、模型、prompt 或 Artifact schema。

### 7.2 Subject Matrix

```text
ArtifactRecord × compatible Artifact Driver × Exposure Config -> Subject Variant
```

同一个 Artifact 可以比较不同索引、recall pipeline、top-k 或 token budget。

### 7.3 Evaluation Matrix

```text
Subject Variant
    JOIN Benchmark Group ON WorldID + GroupID + CheckpointID
    CROSS JOIN selected Benchmark Arms
    FILTER required capabilities
    -> Trials
```

有些 artifact-quality benchmark 可以声明为 group-independent；这类 arm 可以应用
到任意满足 projection capability 的 Artifact。依赖 gold data 的 QA 或 tester
benchmark 必须匹配同一 Group 和 checkpoint。

平台在启动前生成完整 Trial Plan。缺能力、版本不兼容、血缘不匹配都应显示为
`ineligible`，而不是在运行中变成模糊失败。

## 8. 运行与结果模型

```go
type TrialResult struct {
    TrialID      string
    RunID        string
    Status       TrialStatus
    Metrics      []Metric
    CaseResults  OpaqueRef
    Observations OpaqueRef
    RawReport    OpaqueRef
}

type Metric struct {
    Name  string
    Value float64
    Unit  string
}
```

每个结果必须保存以下 identity：

- Artifact kind、schema version、payload digest。
- Builder ID/version、config digest、代码 revision。
- Artifact Driver ID/version、exposure config digest。
- Benchmark bundle digest。
- Benchmark Adapter ID/version、benchmark config digest。
- Group、world、checkpoint、case。
- tester/reader/judge 模型与配置。
- seed、开始结束时间、token、费用和错误阶段。

Artifact View 单独记录 Renderer ID/version 和 view config digest。View 不参与
benchmark 计算，因此更换 Renderer 不能改变 Trial identity 或打断 metric series。

默认比较规则：

- 只有 benchmark bundle、adapter version、benchmark config 和 metric definition
  相同的结果才可直接比较。
- Artifact schema 可以不同；只要它们通过同一版本 projection/capability 接受
  同一个 benchmark，就可以比较。
- benchmark adapter 或 metric definition 变化后，必须形成新的 series，不能悄悄
  接到旧曲线上。

建议的生命周期：

```text
planned -> building -> artifact_ready -> evaluating -> completed
                                      \-> failed
planned --------------------------------> ineligible
```

重试产生新的 Attempt，但仍归属同一个 Trial；Run 是一次完整矩阵执行。

## 9. 模块划分

```text
internal/eval/knowledgeeval/
  core/
    identity.go          opaque refs、artifact、capability、result
    lifecycle.go
  artifact/
    llmwiki/             LLM Wiki artifact/build/subject drivers
    pagewiki/            PageWiki artifact/build/subject drivers
    teamnote/            Team Note snapshot/passive recall drivers
  benchmark/
    wikigenbench/
    locomo/
    longmemeval/
    tauknowledge/
    teamnotestage/
  binding/
    spec.go
    compatibility.go
  runner/
    planner.go
    builder.go
    evaluator.go
  registry/
    builders.go
    artifacts.go
    benchmarks.go
  artifactstore/
    store.go
  view/
    renderer.go
    gateway.go
  runstore/
    store.go
  dashboard/
    query.go
```

依赖方向：

```text
artifact drivers  -> core
benchmark adapters -> core
binding/runner     -> core + registries
view               -> core + artifactstore
dashboard          -> runstore + artifactstore + view
```

Artifact Driver 不依赖具体 Benchmark Adapter；Benchmark Adapter 不依赖具体
Artifact Driver。

## 10. 产品接入

### 10.1 LLM Wiki

第一批能力：

- `projection/wiki-corpus:v1`：页面、链接、引用、source anchors 的规范化只读视图。
- `recall/search:v1`
- `recall/get:v1`
- 可选 `recall/navigate:v1`
- `native` view：把固定 Git revision 渲染成当前 Wiki 的 HTML 阅读体验。
- `raw`/`diff` view：浏览 workspace 文件、manifest、source anchors 和 Git diff。

Artifact 可以先支持当前 filesystem/Git workspace，后续换 schema 时增加新
Artifact Driver。现有 `effecteval` 可作为 plumbing 迁移样例，但不作为最终
产品质量结论。

### 10.2 PageWiki

第一批能力：

- 从 `Page`、`PageRevision`、`PageCitation`、`PageLink`、`TopicTree` 投影
  `wiki-corpus:v1`。
- 复用产品的 Search、GetPage/GetPageRevision、Navigation 和 backlinks。
- 从实验使用的 PageWiki snapshot 生成只读 `native` view；不能链接到持续变化
  的线上 PageWiki。

PageWiki 和 LLM Wiki 只要都实现 `wiki-corpus:v1`，就能参加相同 artifact
quality benchmark；只要都实现相同 Search/Get 版本，就能参加相同 QA benchmark。

### 10.3 Team Note

第一批能力：

- `projection/team-note-snapshot:v1`
- `recall/passive:v1`，映射现有 `RecallNotes`
- `native` view：只读查看 note、revision、relation、evidence 和 recall trace。

现有 extraction、stage、recall replay、recall v2/v3 评测可以逐步包成
Benchmark Adapter。迁移时仍须遵守现有边界：ingest、extraction、recall 和
answer judge 分阶段记录，缺失事实不能被错误归因给 recall。

## 11. Dashboard 需要的功能

第一版只做实验记录和比较，不做通用工作流编排 UI：

- 创建 Run：选择 bundle、Builder arms、Artifact drivers/exposure configs、
  Benchmark arms。
- 预览 Trial Plan：显示将运行、跳过和 `ineligible` 的组合及原因。
- Run 列表：状态、git revision、配置、开始时间、费用和总耗时。
- 对比页：按 Group/case 配对比较两次或多次 Run，展示 metric delta。
- Artifact Viewer：在 native、canonical、raw、diff 四种视图间切换。
- Drill-down：查看 build logs、Artifact、projection、retrieval trace、
  tester trajectory、judge 原始报告。
- Series 保护：版本或 metric definition 不兼容时禁止画成同一趋势线。
- 重新运行：复用旧 RunSpec，生成新 Run 和新 immutable records。

## 12. 实施顺序

### Phase 1：Core 和本地可运行闭环

1. 实现 opaque refs、capabilities、Run/Trial/Attempt 和两个 store。
2. 实现 registry、Binding compatibility 和 Trial Planner。
3. 接 LLM Wiki filesystem/Git Artifact Driver。
4. 接 LLM Wiki native/raw/diff View Provider 和只读 View Gateway。
5. 接一个 artifact-quality adapter 和一个 QA adapter。
6. 用 CLI 完成 build -> open -> eval -> persist -> compare。

### Phase 2：PageWiki 和 tester agent

1. PageWiki Artifact Driver 和 `wiki-corpus:v1` projection。
2. PageWiki snapshot 的 native/raw/diff View Provider。
3. PageWiki Search/Get/Navigate subject。
4. tester-agent runtime；先接一个小型内部任务集，再接外部协议。
5. 确认同一个 benchmark 能无产品特判地跑 LLM Wiki 和 PageWiki。

### Phase 3：Dashboard

1. Run/Trial 查询 API。
2. Run 创建、矩阵预览、历史趋势和 paired comparison。
3. Artifact Viewer，以及 trace、trajectory 和 judge report drill-down。

### Phase 4：Team Note 迁移

1. Team Note snapshot 和 passive recall Artifact Driver。
2. 把 deterministic recall replay 先包装为 Benchmark Adapter。
3. 再包装付费 end-to-end cohort 和现有 v2/v3 run history。

## 13. 首个验收场景

同一个 LoCoMo Group：

1. 两个 LLM Wiki Builder 配置分别生成 Artifact A/B。
2. 每个 Artifact 用两种 exposure config 打开，得到四个 Subject Variant。
3. 四个 Subject 都运行：
   - 一个 `wiki-corpus:v1` artifact-quality arm；
   - 一个 Search/Get QA arm；
   - 一个 tester-agent arm。
4. 平台持久化 12 个 Trial 的 metrics、case results 和 traces。
5. Dashboard 能按同一 Group 配对比较 A/B，并能下钻到失败问题、检索结果和
   Artifact 原文。
6. Dashboard 能打开每个 Artifact 的 native view，并查看 A/B 和增量 checkpoint
   之间的 diff；页面内容来自固定 Artifact snapshot，而不是线上状态。
7. 将 Artifact A 替换为另一种 schema，只新增 Artifact Driver；三个 benchmark
   均无需修改。

做到第 7 点，就证明 adapter 的位置和平台抽象是成立的。
