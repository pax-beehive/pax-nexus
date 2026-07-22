# On-prem Operations Monitor and Storage Accounting

状态：已接受，后端 API 与测试已实现；Portal 页面不在本次实现范围

日期：2026-07-22

相关文档：

- [单团队本地部署与 PAX Memory 接口边界](./2026-07-21-single-team-on-prem-deployment.md)
- [On-prem Human Identity and Agent Registry](./2026-07-21-on-prem-identity-and-agent-registry.md)
- [Recall Stage Trace and Deterministic Replay](./2026-07-17-recall-replay-and-stage-trace.md)
- [Operations 领域语言](../../internal/operations/CONTEXT.md)
- [On-prem 前端接入指南](../on-prem-identity-frontend-integration.md)

## 摘要

单 Team on-prem 安装增加一个仅供 active Owner/Admin 使用的 `Operations` 管理面，用来回答：

- Agent 是否仍在写入和 recall；
- 请求成功、空结果、失败和超时分别有多少；
- Observation 进入 Session Lake 后是否及时完成 extraction；
- Session Lake、Team Memory 和其他数据分别占用多少空间、增长速度如何；
- 最近一次 recall 在检索、筛选、关系展开和预算阶段发生了什么。

Operations 与现有身份 `Audit Events`、产品 Recall Observation、结构化进程日志和 Evaluation
结果保持分离。PostgreSQL 继续作为 v1 唯一的持久化依赖：现有产品表仍是业务事实来源；新增
轻量、无内容的 `onprem_operation_events` 记录跨流程尝试和结果；新增版本化
`onprem_storage_snapshots` 保存容量历史。不在 v1 引入 Prometheus、ClickHouse 或独立
时序数据库。

Operations 页面和 API 不返回 raw query、Observation content、Team Note body、Memory Hit
text、Capsule payload、credential secret、任意请求 header 或未经整理的 error message。

## 背景

当前实现已经有几类相邻但目的不同的数据：

- `session_events` 和 `session_streams` 保存 Session Lake 事实与 cursor；
- `extraction_runs`、`note_candidates` 和 `extraction_episodes` 保存 extraction 状态；
- `team_notes`、`note_revisions`、`note_evidence` 和 `note_deliveries` 保存 Team Memory；
- `team_note_recall_observations` 为成功完成的 Team Note recall 保存七天诊断，其中 query
  只保存 digest，同时保存 exact envelope 和 Recall Trace；
- `onprem_audit_events` 保存身份、成员、Agent 和 credential 治理行为；
- `slog` JSON 记录运行日志，在数据库不可写时仍可能是唯一证据；
- Portal 已有独立的 `Audit Events` 管理页面。

这些数据足以还原部分运行状态，但没有统一回答以下问题：

1. recall 表只在成功事务中写入，因此看不到验证失败、授权拒绝、provider 错误、事务失败
   和超时；
2. “write number” 含义不明确：一个 Observation Batch 可以包含多个 Event，幂等重放不产生
   新 Event，而 extraction 可能产生零个或多个 Team Note revision；
3. 直接从文本日志做计数无法提供稳定语义、分页和按 Agent 过滤；
4. 每次打开页面扫描所有产品表会让管理页面反过来影响运行服务；
5. PostgreSQL 表大小、当前有效内容大小和删除后可复用但尚未归还给文件系统的空间不是同一
   个值；
6. 管理员需要诊断原因，但不应因此默认获得所有 Agent 的原始会话和记忆正文。

因此需要一个独立 Operations read model，并明确计数、隐私、保留和故障语义。

## 决策

### 1. Operations、Audit、产品诊断、日志和 Evaluation 是五个边界

| 数据 | 用途 | 内容策略 | 默认保留 | 权威性 |
| --- | --- | --- | --- | --- |
| Operation Event | 高频运行次数、结果、耗时和最近活动 | 仅 ID、分类和安全计数 | 7 天 | 运行尝试的管理投影 |
| Audit Event | 身份和治理责任链 | actor、action、target 和受控 metadata | 长期 | 治理事实 |
| Recall Observation / Extraction Run | 产品内部阶段诊断 | 产品拥有的详细 trace | 现有策略，Recall 为 7 天 | 产品诊断事实 |
| JSON process log | 启动、依赖和无法入库的故障 | 结构化、可外部采集 | 部署决定 | 故障排查证据 |
| Evaluation result | 固定 cohort 的质量与回归判断 | fixture、artifact 和 judge 指标 | 评测策略 | 离线质量证据 |

Operation Event 不能复用 `onprem_audit_events`。Audit 是低频、长期、不可变的治理记录；
Operation Event 是高频、有 retention 的运行投影。Operations 页面也不显示 Evaluation
accuracy、Token F1 或 cohort 结果，因为离线质量不代表当前实例健康。

Operations 不能替代基础设施监控。若 PostgreSQL 整体不可用，依赖同一数据库的 Operations
页面也不可用；此时 `/healthz`、容器状态和 JSON process log 才是证据。未来可以从同一稳定
指标语义导出 Prometheus，但 exporter 不属于 v1。

### 2. 不定义模糊的 `write_count` 或 `recall_count`

写入漏斗使用以下独立指标：

| 指标 | 定义 |
| --- | --- |
| Observation Requests | 对 `POST /v1/observations` 的已认证尝试数 |
| Successful Observation Requests | 完成合法 receipt 的请求数，包括纯幂等重放 |
| Input Events | 请求携带的 Event 总数 |
| Events Written | 成功插入 Session Lake 的新 Event 数 |
| Duplicate Events | 已存在且未再次写入的 Event 数 |
| Extraction Runs | worker 实际开始的 extraction attempt 数 |
| Completed / Quarantined / Failed Runs | 按 extraction durable outcome 分类的 run 数 |
| Team Note Revisions Admitted | 被确定性 admission 接受的 create、update、resolve revision 数 |

`Events Written` 以数据库实际插入行为为准，不能使用请求数组长度。幂等重放计入 request，
但 `Events Written=0` 且 `Duplicate Events>0`。Observation 成功也不承诺产生 Team Note。

recall 漏斗使用以下独立指标：

| 指标 | 定义 |
| --- | --- |
| Recall Requests | `memory.search`、`memory.get` 和兼容 `notes.recall` 的已认证尝试数，按 kind 分开 |
| Successful Recall Requests | 返回合法产品结果的请求数；正确的空结果也是成功 |
| Recalls With Evidence | search/recall 结果含至少一个 `evidence` disposition 的请求数 |
| Empty Successful Recalls | 成功但没有 hit/delivery 的请求数 |
| Memory Hits | Search 返回的 hit 数，按 evidence/hint/reference disposition 拆分 |
| Team Notes Delivered | 实际 claim 并交付给 session 的 Team Note revision 数 |
| Recall Latency | 一次完整外部调用的 duration；页面显示 p50/p95，而不是单次平均值 |

页面不得把 HTTP 2xx、`evidence_sufficient`、非空结果和 answer correctness 混为一个
“success”。answer correctness 只由 Evaluation 判断。

### 3. Operation Event 每个 attempt 一条，并在业务事务之外完成

新增 `onprem_operation_events`：

| Column | Constraint / meaning |
| --- | --- |
| `operation_event_id` | server-generated `BIGSERIAL`，用于稳定 cursor |
| `attempt_id` | server-generated opaque ID；每个 HTTP/worker attempt 唯一 |
| `operation_kind` | 稳定枚举，如 `observation.observe`、`memory.search`、`memory.get`、`team_note.recall`、`extraction.run`、`channel.send/accept/archive` |
| `outcome` | `succeeded`、`rejected`、`failed`、`timed_out` 或 `cancelled` |
| actor columns | actor kind 及可用的 user/membership/agent/credential ID；不保存 key |
| `session_id` | 可选的 session exact ID；不保存 task/thread 名称或内容 |
| `started_at`, `completed_at`, `duration_ms` | server clock 记录的 attempt 时间 |
| item counters | `input_items`、`accepted_items`、`duplicate_items`、`result_items`、`delivered_items` |
| token counters | 可用时记录 `input_tokens`、`output_tokens`；未知值为 null，不伪造 0 |
| `detail_kind`, `detail_id` | 可选引用 `recall_observation`、`extraction_run` 等产品诊断；不复制 payload |
| `error_code` | 稳定、低基数的安全诊断 code；不保存 error string 或 stack |

索引至少覆盖：

- `(started_at DESC, operation_event_id DESC)`；
- `(operation_kind, outcome, started_at DESC, operation_event_id DESC)`；
- `(actor_agent_id, started_at DESC, operation_event_id DESC)`。

HTTP Adapter 在认证完成后建立 attempt，并在 response outcome 已知后通过独立、短事务记录；
runtime 每个实际 extraction slice 完成后发出安全的 typed observation，由部署组合层转换为
Operation Event，因此一次 worker job 的零个或多个 slice 不会被错误折叠成一个 run。业务事务
回滚不能回滚 Operation Event，
否则最需要观察的失败会消失。记录失败不得改变已经确定的业务响应；Recorder 失败必须写
JSON process log，并增加仅存在于进程生命周期内的 dropped-observation 计数。

完全未认证的无效 credential 请求不逐条写数据库，避免攻击者放大存储；它们只进入受限
结构化日志，未来如需展示只导入聚合计数。已认证但授权不足或请求不合法的调用记录为
`rejected`，只保存稳定 error code。

### 4. 现有产品表继续拥有明细，Operations 只返回安全投影

Operations 不复制 Recall Trace、Extraction Trace、envelope 或 product body。

成功的 Team Note recall Operation Event 可引用现有 Recall Observation。详情 API 从产品表
读取后构造 allowlisted projection，包括：

- observation time、Agent ID、Session ID、duration、token budget；
- candidate/fusion/planned/delivered 数量；
- lane、hard-gate、disposition、rejection reason 和 budget drop 的安全枚举与计数；
- `evidence_sufficient` 和稳定 reason code。

详情 API 不返回 query digest、raw query、`FocusedQuery`、envelope items、RecalledNote text、
Candidate subject/body、task/thread display text 或未经审查的 JSON trace。即使这些字段已经
存在于产品诊断表中，Owner/Admin 的 `view.operations` 也不自动等同于读取所有产品内容。

Operations PostgreSQL Adapter 可以为计数和容量只读查询产品表及 PostgreSQL relation
statistics。这是跨上下文管理 read model 的显式例外；它不能更新产品表、重做
`PlanRecall`、推导 admission 决策或成为产品运行路径的依赖。

### 5. “当前大小”同时报告逻辑库存与物理分配

Operations 使用以下三个不同概念：

- `logical inventory`：当前对象数量和领域 payload bytes；不含索引和数据库页开销；
- `physical allocation`：`pg_total_relation_size` 所见的 table、index 和 TOAST bytes；删除
  数据后可能不会立即下降；
- `estimated reclaimable bytes`：只有 collector 能可靠估算时才返回 nullable 值，v1 不用
  `physical-logical` 冒充可回收空间。

页面必须同时显示 snapshot `captured_at`、age 和 `complete|partial` 状态，不能把旧 snapshot
标成实时值。bytes 使用 IEC 格式展示，但 API 始终返回原始整数。

Storage component 与 relation 归属为：

| Component | Logical inventory | Physical relations |
| --- | --- | --- |
| Session Lake | Event 数、Session Stream 数、未 extraction Event 数、最早/最新 Event、Event payload bytes | `session_events`、`session_streams` |
| Extraction | pending/running/completed/quarantined/failed Run、Candidate、Episode 数和 payload bytes | `extraction_runs`、`note_candidates`、`extraction_episodes` 及归属明确的 River relations |
| Team Memory | active/resolved/expired Note、Revision、Evidence、Delivery、relation reference 数、current/history/embedding bytes | `team_notes`、`note_revisions`、`note_evidence`、`note_deliveries` |
| Recall Diagnostics | Recall Observation、Hint Delivery 数和最早 expiry | `team_note_recall_observations`、`recall_hint_deliveries` |
| Capsule Channel | pending/accepted/archived Envelope 数和 payload bytes | `onprem_channel_envelopes` |
| Identity & Audit | User、Membership、Agent、active Credential、Human Session、Invitation、Audit Event 数 | `onprem_installation_state`、`onprem_users`、`onprem_memberships`、`onprem_human_sessions`、`onprem_membership_invitations`、`onprem_agents`、`onprem_agent_identities`、`agent_enrollments`、`agent_credentials`、`onprem_audit_events` |
| Operations | retained Operation Event 和 Storage Snapshot 数 | Operations 自有 relations |
| Other | 无领域 object count | database total 中未归类的 extension/catalog/user relation allocation |

每个 physical relation 必须且只能属于一个 component。`database_physical_bytes` 使用
`pg_database_size`；`other_physical_bytes` 是数据库总量扣除同一 snapshot 中已分类 relation
之后的非负剩余。并发 DDL 或统计失败造成不一致时 snapshot 标为 `partial` 并携带安全
warning code，不强行修正成看似精确的数据。

Session Lake 的 `logical_payload_bytes` 至少覆盖 Event content 和 metadata；Team Memory
分别返回 current Note、revision history 和 embedding bytes，不能只返回 Note body 大小。
物理大小包含 dead tuples 和尚未归还给文件系统的页，因此 retention cleanup 成功后 object
count 可以下降而 physical bytes 暂时不下降，这是正常状态。

Team Note 状态计数使用 snapshot 时间的有效状态：已超过 hard expiry 的 Note 计入 expired，
即使存储 row 尚未被 lifecycle job 改写；resolved 与 active 则同时尊重已记录 state、
`invalid_at` 和有效期。Session Lake 的 unextracted Event 明确定义为 `extracted_at IS NULL`，
不从 River job 数量反推。

### 6. 容量由低频 collector 采样，管理请求不扫描产品表

新增版本化 `onprem_storage_snapshots`。每条 snapshot 包含：

- `snapshot_id`、`schema_version`、`captured_at`；
- `status=complete|partial` 和低基数 warning codes；
- `database_physical_bytes`；
- 按稳定 component key 组织的 logical counts、logical bytes、physical bytes 和可选
  estimated reclaimable bytes。

component measurements 可以使用带 `schema_version` 的 JSONB 保存，因为 snapshot 频率低、
字段随产品表演进，API 层仍必须返回 typed Thrift model，不能把任意 JSON 透传给前端。

Collector 在服务启动后生成初始 snapshot，此后默认每小时运行一次：

- 使用独立、只读、最多一个连接的 collector pool；该连接设置短 statement/lock timeout，
  每个 component 另有独立 deadline，并为 snapshot 写入预留时间；
- 同一时刻最多运行一个 collection；
- 一个 component 失败时保留其他 component 并标记 partial；
- API 只读取最近 snapshot，不触发全表 scan；
- collector 失败不影响 Observation、Recall、Channel 或 extraction；
- 未来需要更精细采样时先证明 collector 对 on-prem workstation 的负载可接受。

不使用 `pg_stat_user_tables.n_live_tup` 冒充精确 object count；如果某个精确 count 超出
statement budget，该 component 标记 partial。后续可以增加事务维护的 counter read model，
但必须保留幂等 replay 和 rebuild 校验，不能静默切换语义。

### 7. Admin API 使用独立 capability 和稳定分页

所有 HTTP Interface 仍以 `idl/team_memory.thrift` 为 source of truth。v1 增加：

```text
GET /v1/admin/operations/summary
GET /v1/admin/operations/events
GET /v1/admin/operations/recalls/:observation_id
GET /v1/admin/operations/storage
GET /v1/admin/operations/storage/history
```

授权规则：

- 新增 Human capability `view.operations`，只映射给 active Owner/Admin；
- Agent Credential 无论 permissions 如何都不能访问 `/v1/admin/operations/*`；
- Member v1 不能查看自己 Agent 的 Operations；未来若增加 self-service view，使用独立
  `/v1/me/agents/:agent_id/operations` 和更严格 projection，不能放宽 Admin API；
- 后端逐请求强制 capability；前端隐藏导航不是授权。

查询规则：

- summary 默认 24 小时，events 默认最新 50 条；
- raw operation 时间范围不能超过 retention window；
- events 支持 `operation_kind`、`outcome`、`agent_id`、`from`、`to` 和 cursor；
- cursor 使用 `(started_at, operation_event_id)` 编码，排序稳定；limit 有 server maximum；
- storage current 返回最近 snapshot；history 支持 from/to，按 `captured_at` 排序；
- 所有 timestamp 是 RFC3339 UTC，前端负责本地化；
- response 包含 `generated_at` 或 snapshot `captured_at`，避免前端把不同时间基准的卡片拼成
  同一个精确瞬间。

summary 的 response 使用写入、extraction、recall、latency 分组字段，不返回通用
`write_count`。latency 只统计 `memory.search` 和兼容 `team_note.recall`；v1 在至少 2 个样本时
返回 p50、至少 20 个样本时返回 p95，样本不足返回 null 和 sample count。

### 8. Portal 增加 Operations 页面，Audit 保持独立

Portal Admin Console 增加 `/admin/operations`，导航名称为 `Operations`，与
`Audit Events` 并列而不是替代它。页面最小信息架构：

1. **Activity summary**：Observation requests、Events written、Recall requests、empty
   recalls、Team Notes delivered、errors 和 p50/p95；
2. **Pipeline health**：unextracted events、extraction backlog、run outcomes 和 oldest
   pending age；
3. **Storage**：Total Database、Session Lake、Team Memory、Recall Diagnostics 等 component
   的 logical/physical size、freshness 和 partial warning；
4. **Recent activity**：按时间、Agent、operation 和 outcome 过滤的 cursor table；
5. **Recall detail**：点击 recall row 显示安全的 stage projection，不显示 query 或内容。

前端必须区别：

- `0 results` 与 `failed`；
- `0 new events` 的成功幂等 replay 与写入失败；
- Observation accepted 与异步 extraction completed；
- object count 下降与 physical allocation 暂未下降；
- fresh complete snapshot、stale snapshot 和 partial snapshot；
- “尚无数据”与 API error。

页面可以轮询轻量 summary/events，但 storage 不需要高频刷新。浏览器不可把 Operations
response 持久化到 localStorage，也不可把详情内容写入 URL、analytics 或 console。

### 9. Retention 和 cleanup 是显式运维策略

默认策略：

- raw Operation Event：7 天；
- Recall Observation/Trace：保持现有 7 天；
- hourly Storage Snapshot：90 天；
- Audit Event：不由 Operations janitor 删除；
- Session Event、Team Note、Extraction、Capsule 的业务 retention：本 ADR 只展示，不擅自
  删除；每个产品需要独立 lifecycle 决策。

retention 通过部署配置调整，并设置安全上下界。cleanup 由周期 janitor 使用小批次和独立
timeout 执行，不在 Observation 或 Recall 请求事务里执行大范围 DELETE。Cleanup 记录一个
`system.retention` Operation Event，但不能为每个删除 row 生成事件。

Storage history 只说明增长和 allocation，不是 backup。Operations 页面必须指向部署 runbook
中的 PostgreSQL backup/restore 流程，不能让用户误以为 snapshot 可恢复数据。

### 10. Module 边界保持产品独立

建议模块边界：

```text
internal/operations/              domain terms, query contracts, projections
internal/platform/postgres/       operation repository and storage collector adapter
internal/deployment/onprem/       Human authorization and HTTP composition
web/                              Operations Portal page
```

Operation recording 发生在 HTTP Adapter 或部署组合 observer，不把 Portal、Human role、
PostgreSQL 或 Operations Store import 到 Session、Team Note、LLM Wiki 产品逻辑中。

Operations 可以消费产品调用的已完成结果，并可通过只读 PostgreSQL Adapter 统计存储；产品
调用不能依赖 Operation Event 成功写入。Architecture tests 应防止产品 Module 反向依赖
`internal/operations` 的管理查询或 `internal/deployment/onprem`。

## 边际场景

| 场景 | 决定 |
| --- | --- |
| recall 正确返回零 hit | `outcome=succeeded`，同时增加 empty recall，不增加 delivered |
| 同一个 Observation 幂等重放 | request/success +1，duplicate 增加，Events Written 不增加 |
| Observation 已接受但 extraction 尚未开始 | 写入漏斗显示 accepted；backlog/oldest age 显示异步差距 |
| extraction deterministic quarantine | run 记 quarantined，不计 failed；admitted revisions 为 0 |
| 业务事务失败但 PostgreSQL 仍可写 | 独立 Recorder 事务保存 failed/rejected Operation Event |
| PostgreSQL 完全不可用 | 无法持久化 Operation Event；JSON log/health 是唯一证据 |
| client 断开或 context cancelled | outcome=cancelled；若 server deadline 到期则 timed_out |
| 同一个 request 重试两次 | 两个 attempt；幂等业务 counter 仍按实际 new/duplicate 结果计算 |
| Recall Observation 已过期但 Operation Event 仍可见 | 详情返回 `diagnostic_expired`，列表和计数仍可显示安全字段 |
| 删除过期诊断后磁盘未缩小 | object/logical size 下降，physical allocation 可保持不变 |
| storage collector 某表超时 | snapshot partial；不复用旧 component 值冒充本次结果 |
| Agent 已 retired 或 Membership removed | activity 保留 raw opaque ID；label enrichment 是非权威显示 |
| 服务升级前没有 Operation Event | 不从日志猜测回填；首次启动只产生当前 Storage Snapshot |

## 考虑过的方案

### 复用 Audit Event

拒绝。高频运行 activity 会污染长期治理责任链，并迫使 Audit 接受 retention、性能字段和
大量正常空结果。

### 只解析 JSON logs

拒绝。日志在不同部署中 retention 不确定，难以提供稳定分页、幂等语义、按 Agent 授权和
精确的 new-vs-duplicate item count。日志仍作为数据库不可用时的必要补充。

### 页面实时聚合所有产品表

拒绝。管理访问会触发不可控 scan，容量计算会影响业务负载，也无法保留增长历史。使用
低频、受 timeout 约束的 Storage Snapshot。

### 把完整 Recall Observation 暴露给 Owner/Admin

拒绝。现有 envelope 和部分 trace 字段包含产品内容或派生 query。Operations capability 是
运行管理能力，不是全量内容读取授权。

### v1 引入独立时序数据库

拒绝。单 workstation 部署已有 PostgreSQL；当前数据量和保留窗口不值得增加备份、升级、
认证和故障模式。稳定指标语义保留未来 exporter 的可能性。

## 后果

- 运营人员能够从一个页面观察 write、extraction、recall、recent activity 和容量增长；
- 成功空 recall、幂等 replay 和失败不再混在一个数字里；
- 现有 Recall Trace 被复用但通过安全 projection 暴露，不建立第二套 recall 判断；
- PostgreSQL 增加两类小型管理数据和周期 collector/janitor；
- 同库 Operations 无法覆盖数据库整体不可用，需要保留 health/log 运维路径；
- 物理容量是 snapshot，不是强一致实时 counter；UI 必须显示 freshness/partial；
- 本 ADR 不自动引入 alerting，也不决定 Session Lake 或 Team Memory 的删除策略。

## 实施顺序

1. IDL 增加 Operations typed models、admin endpoints 和 `view.operations` capability；
2. 新增 Operation Event repository/recorder，并覆盖 observation、search/get、compat recall 和
   extraction worker；
3. 新增 Storage Snapshot collector、retention janitor 和 PostgreSQL migration；
4. 实现 summary/events/storage/recall projection handlers；
5. Portal 增加 Operations 页面、过滤、stale/partial 状态和 recall detail；
6. 增加 PostgreSQL adapter tests、HTTP role/privacy tests、frontend tests，以及 public-HTTP
   Docker E2E：write -> extraction -> recall -> Operations counts/detail/storage；
7. 在 workstation deployment instruction 中补充 retention、容量阈值、backup 和磁盘满时
   的恢复操作。
