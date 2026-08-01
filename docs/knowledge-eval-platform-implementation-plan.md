# Knowledge Eval Platform 实施计划

日期：2026-07-29
状态：V1 Complete；S20 Complete
设计文档：[knowledge-eval-platform-design.md](knowledge-eval-platform-design.md)

## 1. 交付原则

系统按可独立验收的 vertical slice 实现，不按“后端全部完成后再做 UI”拆分。

每个 slice 完成时必须同时具备：

1. 一个稳定的小接口或可观察行为。
2. 自动化测试；新增 Go package 聚合单测覆盖率不低于 75%。
3. 一个不依赖付费模型的 deterministic fixture 或 replay。
4. 一个可以在 Eval Lab Dashboard 中打开的结果或证据。
5. 明确的失败状态；不能要求用户查询数据库判断发生了什么。

Dashboard 同时展示两类进度：

- **Build Progress**：这个平台本身实现到哪一个 slice。
- **Run Progress**：某一次 eval 的 build、artifact、view、trial 和 attempt 状态。

在 Run Progress 接通以前，Build Progress 使用仓库内版本化 manifest，并随每个
slice 更新和部署。Run Progress 接通后，Dashboard 通过只读 Query API 获取数据；
底层是否使用数据库不影响使用者。

## 2. 任务地图

| ID | Slice | 状态 | 独立验收产物 |
| --- | --- | --- | --- |
| S00 | 架构边界和接口模型 | Complete | 设计文档；Artifact Driver、Benchmark Adapter、Binding 和 View 的归属明确 |
| S01 | 可见进度板 | Complete | 私有 Eval Lab 页面展示本任务表、当前任务、验收条件和已有 benchmark radar |
| S02 | Core identity | Complete | `OpaqueRef`、`ArtifactRecord`、capability 和 digest validation 的单测及示例 |
| S03 | Run lifecycle | Complete | Run/Trial/Attempt 状态机；fixture 覆盖成功、失败、重试和 ineligible |
| S04 | Run Query read model | Complete | 列表、详情、事件时间线、portable JSON snapshot 和分页只读 Hertz API |
| S05 | Artifact store + raw view | Complete | 目录/字节 artifact、SHA 校验、symlink 防护和固定 raw view |
| S06 | LLM Wiki Artifact Driver | Complete | 打开真实 workspace，声明 Project/Search/Get/Navigate capability |
| S07 | LLM Wiki native/diff view | Complete | Dashboard 可打开 native、canonical、raw 和 base/current diff |
| S08 | Binding compatibility planner | Complete | capability/version 不匹配产生明确的 `ineligible` reason |
| S09 | Builder runner | Complete | 固定 Group 构建 Artifact，保存 lineage、provenance、attempt 和阶段错误 |
| S10 | Artifact-quality benchmark | Complete | deterministic structure/citation scorer 输出 metric、case 和 evidence |
| S11 | Search/Get QA benchmark | Complete | 固定 QA cohort 区分 retrieval 与 reader failure |
| S12 | Tester-agent benchmark | Complete | tester 调 Search/Get/Navigate/Recall，保存 trajectory 和 terminal state |
| S13 | PageWiki Artifact Driver | Complete | 同一 quality/QA adapter 无产品特判地运行 PageWiki snapshot |
| S14 | 历史与 paired comparison | Complete | 基线/当前 Run 按 fingerprint 对齐，显示 delta 和不可比原因 |
| S15 | Team Note compatibility | Complete | snapshot 和 production `RecallNotes` wrapper 复用 runner/tester/dashboard |
| S16 | 真实 LLM Wiki LoCoMo 对照 | Complete | 保留 source-only baseline，真实 maintainer 完成 conv-26，并拆分 artifact、QA、retrieval 和 reader failure |
| S17 | API-driven Dashboard | Complete | 本地 Query API 扫描结果目录；前端通过分页 API 获取 Dataset、Run、Benchmark、Matrix 和 Artifact View |
| S18 | Dataset/Group catalog | Complete | Query API 合并 prepared manifests 与 run bundles；Dashboard 按 Dataset → Partition → Group 展示已运行和未运行世界，V2 按共享 trajectory environment 去重 |
| S19 | Local experiment tasks | Complete | Dashboard 预览并创建 baseline/maintainer 任务；后端单并发持久化队列执行真实 runner，支持幂等创建、显式付费确认、取消、事件、Run/Artifact 链接 |
| S20 | Local dataset install | Complete | Dashboard 从固定 revision 配方下载单个公开 Dataset；后端持久化任务完成下载、checksum/answer-blind 校验和 prepared split 生成，支持取消与失败追踪 |

### S20：本地 Dataset 安装

范围：

- 数据源配方只声明当前已支持的 LoCoMo、LongMemEval-S Cleaned 和
  LongMemEval-V2 Small；不建设通用 Dataset marketplace。
- API 启动参数 `-dataset-root` 决定服务器写入范围。Dashboard 展示该目录但不能
  提交任意 filesystem path。
- 下载固定 upstream revision；已存在的非空 raw 文件直接复用。
- 每个 Dataset 可独立下载和 prepare，不要求同时下载其他 benchmark。
- prepare 在 staging 目录完成 answer-blind、partition 和 reference 校验后，只替换
  所选 Dataset 的派生目录，不清理其他 Dataset 或已有实验结果。
- dataset install task 持久化 queued、running、completed、failed 和 cancelled，
  并允许对正在运行的下载/prepare 子进程请求取消。

验收：

- 新 data root 可以只安装一个 Dataset 并随后被现有 Registry/experiment runner 读取。
- 同一 Idempotency-Key 不会重复创建任务；同一 Dataset 不会同时安装两次。
- API 重启后保留历史；中断的 running task 明确变为 failed。
- 下载和 prepare 失败在 Dashboard 显示最后错误，不产生 ready manifest。

## 3. Slice 详情

### S00：架构边界和接口模型

范围：

- Knowledge Artifact 的 payload 对 Core opaque。
- Artifact Driver 属于产物侧。
- Benchmark Adapter 属于 benchmark 侧。
- Binding 只负责选择和 capability 匹配。
- Artifact View 与 benchmark projection 分离。

验收：

- 更换 Artifact schema 只需要新的 Artifact Driver。
- 更换 benchmark protocol 只需要新的 Benchmark Adapter。
- View renderer 变化不改变 Trial identity。

### S01：可见进度板

范围：

- 在现有 PAX Evaluation Radar 上增加 Build Progress。
- 状态只允许 `complete`、`current`、`planned`、`blocked`。
- 每个任务展示 goal、验收证据和依赖。
- 保留当前外部 benchmark radar，不把旧 pilot 冒充新平台结果。

验收：

- 打开一个私有 URL 就能看到当前完成数、当前任务和下一任务。
- 不需要本地环境、SQL 或数据库权限。
- 页面明确区分“平台构建进度”和“benchmark 实验结果”。

### S02：Core identity

范围：

- `OpaqueRef`
- `ArtifactRecord`
- `Capability` / `CapabilitySet`
- content digest、schema version、world/group/checkpoint identity validation

不包含：

- 数据库。
- Runner。
- 任何产品特定 schema。

验收：

- table-driven tests 覆盖合法、缺字段、digest 错误、版本不兼容和错误血缘。
- 一个 CLI 或 fixture 输出可被 Dashboard 展示的 Artifact summary。

### S03：Run lifecycle

范围：

- `Run`、`Trial`、`Attempt` 和事件。
- `planned -> building -> artifact_ready -> evaluating -> completed`。
- `failed`、`ineligible` 和 retry attempt。
- 纯内存 repository 和 deterministic fixture。

验收：

- 状态机拒绝非法跳转。
- 重试不覆盖前一次 Attempt。
- fixture 同时包含 completed、failed 和 ineligible trial。

### S04：Run Query read model

范围：

- `QueryService` 定义只读 Run list/detail/events 接口。
- portable JSON snapshot 保留为导出和审计格式；Dashboard transport 使用只读
  Hertz Query API，不暴露 store 或数据库字段。
- Dataset、Run、solution version 和 benchmark list 使用 cursor pagination 与
  服务端筛选；结果矩阵由后端聚合。
- V1 本地 Registry 每次请求重新扫描 `dataset-run.json`，因此新结果落盘后无需
  重建前端；后续可替换为 PostgreSQL store 而不改变 HTTP contract。
- Dataset detail API 从 source-only artifact 的不可变 `sources/*.md` 建立
  session 索引，支持分页和单 session HTML view；Dashboard `/dataset` 页面
  展示 session/turn 原文并关联使用该 Dataset 的 Runs。
- 当前 bundle 只持久化 question 数量和 benchmark case result，没有持久化原始
  question 文本。下一版 Dataset manifest 必须分别保存 answer-blind build input
  和 evaluator-only query/gold refs；现有 LoCoMo bundle 可从 prepared JSONL
  回填 manifest，无需重跑 maintainer。
- Dashboard 展示 Run 总进度、阶段、case、失败原因和 event timeline。

验收：

- fixture run 在页面可见。
- 后端 store 可以替换，前端协议不变。
- 页面没有任何 SQL、table name 或 DB-specific 字段。

### S05：Artifact store + raw view

范围：

- 存取 immutable opaque payload。
- 写入时计算并校验 SHA-256。
- `ArtifactViewRecord` 和 raw download/browser。
- 路径遍历、symlink escape 和 MIME 安全限制。

验收：

- 导入一个目录型 fixture 和一个单文件 fixture。
- 修改 payload 后 digest 校验失败。
- Dashboard 能打开固定 snapshot；不能跳到 live path。

### S06：LLM Wiki Artifact Driver

范围：

- 支持当前 filesystem/Git workspace schema。
- 读取 Git revision、manifest、Wiki pages、Sources 和 anchors。
- 声明 `projection/wiki-corpus:v1` 及已实现的 recall capabilities。

验收：

- 用当前 repo 的小型 fixture 打开一个 Artifact。
- schema/version 不支持时产生 typed error。
- Benchmark package 不 import LLM Wiki 私有 package。

### S07：LLM Wiki native/diff view

范围：

- `native`：固定 revision 的 Wiki 阅读体验。
- `canonical`：规范化 projection。
- `raw`：workspace、manifest 和中间数据。
- `diff`：base/current 页面、引用和结构变化。

验收：

- 页面、内部链接和 Source anchor 可点击。
- Diff 明确新增、修改、删除和引用变化。
- View renderer version 记录在 View，不影响 benchmark series。

### S08：Binding compatibility planner

范围：

- Registry、BindingSpec 和 capability/version matching。
- 展开 Subject × selected Benchmark Arms。
- 在执行前生成 immutable Trial Plan。

验收：

- compatible、missing capability、version mismatch、wrong checkpoint 均有测试。
- Dashboard 在运行前显示会执行和不会执行的组合及原因。

### S09：Builder runner

范围：

- Benchmark Group × Builder Arm × Checkpoint。
- base artifact 增量构建。
- provenance、config digest、code revision、seed、token、费用和阶段错误。

验收：

- deterministic fake Builder 跑通 success/failure/retry。
- LLM Wiki Builder 对一个小 Group 生成真实 Artifact。
- 构建失败仍保留 logs 和 attempt，不产生伪 Artifact。

### S10：Artifact-quality benchmark

范围：

- 第一版先使用 deterministic structural/citation scorer。
- 输出 metric、case result、observation 和 raw report。
- 为外部 Wiki benchmark adapter 留接口，不在 Core 固化字段。

验收：

- 同一 fixture 的 good/bad Wiki 得分方向正确。
- Dashboard 可从总分下钻到失败页面和证据。
- scorer version 变化会开启新的 series。

### S11：Search/Get QA benchmark

范围：

- V1 先接一个固定、无模型费用的 QA cohort；外部 LoCoMo adapter 使用同一接口后续接入。
- Question、gold 和 judge protocol 仍为 benchmark-private payload。
- retrieval、relation/expansion、packing、reader、judge 分阶段记录。

验收：

- 同一 Artifact 比较两个 exposure config。
- 能区分“Artifact 没有事实”“recall 没取到”“reader/judge 失败”。
- Dashboard 显示 paired case delta 和失败阶段。

### S12：Tester-agent benchmark

范围：

- tester model/config 为独立 trial axis。
- Subject capability 注册为 tester tools。
- Benchmark Adapter 持有环境、动作规则和成功条件。

验收：

- deterministic fake tester 覆盖 success、tool error、budget exhausted。
- 一个真实 tester task 跑通。
- Dashboard 可重放 trajectory，不需要读原始日志。

### S13：PageWiki Artifact Driver

范围：

- 从固定 PageWiki snapshot 读取 Page、Revision、Citation、Link 和 TopicTree。
- 实现相同 `wiki-corpus:v1` projection。
- 暴露 Search、Get 和 Navigate。
- V1 提供 snapshot native/raw view；跨版本差异由 S14 的 paired comparison 表达。

验收：

- S10 和 S11 的 Benchmark Adapter 不改代码即可运行。
- Dashboard 可并排打开 LLM Wiki 和 PageWiki。

### S14：历史与 paired comparison

范围：

- 保存 RunSpec，支持 rerun。
- 按 world/group/checkpoint/case 对齐。
- series compatibility guard。
- regression list、metric delta 和费用/延迟趋势。

验收：

- 可比较结果正确配对。
- bundle、adapter 或 metric definition 不同的结果显示 `incomparable`。
- 用户可以从 regression 直接跳到 Artifact View 和 case trace。

### S15：Team Note compatibility

范围：

- Team Note snapshot Artifact Driver。
- `recall/passive:v1` 映射 `RecallNotes`。
- 先包装 deterministic recall replay，再包装付费 cohort。

验收：

- V1 replay 展示 candidate、selection、budget drop 和 tester judge；付费 cohort
  继续沿用既有 extraction observation 与 recall stage trace。
- 缺失事实不会被错误归因给 recall。
- 复用 Run/Trial/Attempt、Dashboard 和 comparison，不建立 Team Note 专属平台。

### S16：真实 LLM Wiki LoCoMo 对照

范围：

- 将现有受限 filesystem maintainer 接入 `BuilderDriver`，不复制另一套 Wiki
  生成逻辑。
- source-only 目录复制继续作为显式 baseline arm，不再作为真实 Builder 的替代品。
- 用 LoCoMo train `conv-26` 的 answer-blind maintainer input 构建 Wiki；reader
  query 与 evaluator gold 只进入 benchmark adapter。
- 对 baseline 与 maintained Wiki 运行相同 artifact-quality 与 Search/Get QA
  adapters。
- 分别报告 artifact 缺失、retrieval failure 和 reader failure，不能只报告最终
  answer accuracy。

验收：

- Builder provenance 记录模型、运行审计、输入/输出 token 和 validator 结果。
- 两个 arm 的 Run、Artifact View、case result 和 paired delta 可由 Dashboard
  打开。
- source-only 与 maintained Wiki 使用相同问题 cohort 和 scorer fingerprint。
- 交付记录真实运行是否完成；缺少模型凭据或 provider 故障显示为 build failure，
  不生成伪 maintained Artifact。

## 4. 推荐执行批次

### Batch A：先让一切可见

`S01 -> S02 -> S03 -> S04 -> S05`

结束时用户可以在 Dashboard 看到真实 Run lifecycle 和 Artifact raw view。即使还没
接一个正式 benchmark，平台也已经可观察。

### Batch B：LLM Wiki 最小闭环

`S06 -> S07 -> S08 -> S09 -> S10`

结束时可以构建 LLM Wiki、直接评分、查看 Artifact 和 diff，并保存一次完整 Run。

### Batch C：使用效果

`S11 -> S12 -> S14`

结束时可以跑 QA 和 tester agent，并与历史 Run 做 paired comparison。

### Batch D：证明抽象可复用

`S13 -> S15`

PageWiki 优先于 Team Note。两个产品接入后，如果 benchmark adapters 无需产品
特判，说明核心抽象成立。

## 5. 进度更新约定

以后每完成一个 slice：

1. 将该 slice 状态改为 `complete`。
2. 只有一个 slice 可以是 `current`。
3. 在 Dashboard 上附上测试结果、演示 Run、Artifact/View 或失败证据。
4. 部署新的私有 Eval Lab 版本。
5. 在交付消息中给出 Dashboard 链接和本次新增的可点击证据。

如果一个任务因外部依赖阻塞，状态改为 `blocked`，并展示阻塞原因和仍可继续的
下一片；不得让进度停留在含糊的“running”。

## 6. V1 验收入口

- `go run ./cmd/knowledge-eval-demo -output <directory>` 会重建完整 deterministic
  acceptance bundle。
- bundle 固定包含 LLM Wiki baseline/current、PageWiki 和 Team Note 四个 Run。
- Eval Lab 直接展示 run/trial/case/metric/event、artifact views 和 paired delta。
- V1 已包含本地只读 HTTP Query transport；仍不包含 PostgreSQL durable run
  store 或公网部署。它们是存储与部署扩展，不改变 Core、Driver、Adapter、
  Binding、View 和 HTTP contract。

## 7. 当前交接入口

- 交接说明：[knowledge-eval-platform/README.md](knowledge-eval-platform/README.md)
- 当前实验：LoCoMo train `conv-26`
- 当前对照：`source-only-baseline` 与真实 filesystem maintainer
- Dashboard：`web/llmwiki-benchmark-dashboard`，本地运行 `npm run dev`
