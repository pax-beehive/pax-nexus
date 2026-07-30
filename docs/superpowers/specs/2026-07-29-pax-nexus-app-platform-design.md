# PAX Nexus Application Platform — 架构愿景设计

日期：2026-07-29（同日修订：app 模型从 "manifest 存于 nexus" 改为 "自有状态 + read/report 契约"）
状态：MVP（LLM todo list app）已在 feat/todoapp-mvp 分支实现（含 read.list 原语、app:todo report 通道、门户页面）；后续见 §6 落地顺序

## 1. 愿景

把 pax-nexus（Evidence Lake + Team Memory + LLM Wiki）从"一组内置产品"演进为
"一个可在其上搭建 application 的知识底座"：

```
        ┌────────────── app（自有数据库、自有状态、自有 UI）──────────────┐
        │                                                              │
   read │ 拉取组织知识（recall/wiki 查询）        report │ 上报用户行为    │
        ▼                                              ▼               │
┌─────────────────────────── pax-nexus ────────────────────────────────┐
│  knowledge（team memory / wiki）◀── 提炼 ── session lake（evidence）   │
│                                              ▲                       │
│              paxm / connectors / voice-videos┘（其余输入源）           │
└───────────────────────────────────────────────────────────────────────┘
```

下层组件在代码库中已经存在（见 `CONTEXT-MAP.md`）。核心工程不是新组件，
而是一次**边界翻转**：应用成为 nexus 契约的消费者。

## 2. 已确认的产品裁定

| 问题 | 裁定 |
|------|------|
| application 的作者与用户 | 第一方开发 preset application，与 pax-nexus 同版本打包 ship 给客户 |
| 自动生成发生在哪里 | 客户部署现场，daily 使用过程中持续做 pattern recognition |
| application 的最终形态 | Web 页面/视图 + 自动化任务（不做对话式 macro） |
| app 与 nexus 的关系 | app 自有数据库与状态；与 nexus 只有 read + report 两个动词 |
| MVP | 基于记忆知识库的 LLM todo list app |

## 3. App 模型：自有状态 + read/report 契约

app 拥有自己的数据库和状态，nexus 对 app 只暴露两个动词：

- **read**：拉取组织知识（recall/wiki 查询），app 据此派生自己的数据。
- **report**：上报用户在 app 内的行为，作为一条新的 evidence source
  进入 session lake，供未来的知识提炼消费。

该模型的三个关键性质：

1. **app 是与 paxm、connectors 平级的第四路输入源。**
   Evidence Lake 本就 source-agnostic，report 通道复用现有 ingest 路径，
   source 标记为 `app:<name>`。
2. **app 永远不直接写知识。** report 只进不可变的 session lake，
   是否进入知识由提炼管线（teamnote 抽取、pagewiki 维护）决定。
3. **app 的技术形态被解耦。** 契约只有 read + report：
   preset app 可以是真代码（进程内模块或独立服务，用 read/report SDK），
   表达力无上限；自动生成的 app 跑在受限的 manifest 托管 runtime 上
   （声明式 triggers/tasks/views + 平台供给的通用状态存储）。
   manifest 从"唯一形态"降级为"generated app 的托管形态"。

### provenance 细分（report schema 的硬性要求）

- **app 观察到的人的行为**（接受/完成/忽略/纠正）：高价值信号，进提炼。
- **app 自己生成的内容**（建议正文、摘要正文）：不是人的行为，
  不上报或标记后排除在模式识别之外。
  两类必须在 report schema 中显式区分，不允许事后推断。

### 自有数据库的供给方式

同一 Postgres 实例内每 app 一个 schema（`app_<name>`），平台负责建；
将来 generated app 使用平台提供的通用 JSONB 状态存储。
不做每 app 独立数据库实例（on-prem 运维成本不可接受）。

### 已否决的方案

- **artifact 存于 nexus 数据库**（本设计初版）：让知识层替应用层保管状态，边界糊。
- **App 即生成代码 + 客户现场沙箱执行**：无人审核 + 任意代码执行的组合，
  现阶段风险收益不成比例。生成的 app 限定为 manifest。
- **先做完整对外平台化**：app 均为第一方同版本 ship，跨进程/跨版本契约
  成本花在没有第三方的地方。

## 4. 主要难点（按严重程度排序）

1. **report 的语义粒度。** 原始点击流会把 session lake 灌成垃圾场，太抽象又丢信号。
   定在"语义事件"级：用户查看/采纳/纠正/忽略了什么知识。事件词表在 MVP 中打磨。
2. **契约冻结时机。** recall 语义仍在演进；read 契约需区分"保证语义"与
   "尽力语义"，并版本化。
3. **授权与数据边界。** app 级 read scope 与 report 身份是 on-prem identity
   之上的新增维度；generated app 默认最小权限。
4. **app 自有数据的新鲜度。** 知识更新后 app 派生数据会陈旧；
   第一版任务触发时重新 read（拉模式），订阅/失效通知后置。
5. **反馈污染。** 靠 provenance 细分（见 §3）解决；pattern recognition
   一律排除 app 生成的内容，仅消费人的行为事件。
6. **模式识别冷启动。** 小团队 session 中真实模式稀疏；置信阈值 +
   人工确认闸门，冷启动期禁止无确认自动上线。
7. **voice/video 是独立大工程。** 仅是 ingest 侧多一路，独立排期，不阻塞平台化。

## 5. MVP：基于记忆知识库的 LLM todo list app

选 todo 的理由：它天生有自有状态（任务与状态明确是 app 数据）；
read 通道有杀手级用法（从 session 证据提炼行动项建议）；
report 事件天然是语义级的；且它补上 team memory 的盲区——
session 记录**意图**（"我们决定做 X"），todo 上报**结果**（"X 完成/被放弃"），
忽略建议本身就是对知识质量的纠错反馈。

### 功能切法（按价值排序）

1. 手动增删改完成 todo（基线）。
2. "从团队记忆生成建议"任务（定时 + 手动触发）：
   recall 搜索 → LLM 抽行动项（**必须带引用，无引用不出建议**）→
   建议收件箱 → 接受成为 todo / 忽略。
3. report 三类事件（source `app:todo`）：接受建议、完成任务、忽略建议。
4. 验收：explorer 中可见 `app:todo` 来源的 evidence stream
   （闭环入口已验证；提炼管线消费该流不在 MVP 内）。

### recall 冒烟测试结果（2026-07-29，对 workstation 真实数据）

- **行动项存在且已半结构化。** team note 抽取管线已产出
  `kind=blocker/handoff` 且带 `certainty=unresolved/resolved` 标记的 note；
  真实样例：`[blocker certainty=unresolved] Team provider credential rejected;
  paxm LTM write failed. Requires paxl device connect onprem ... first.`
  ——这就是一条现成的 todo 建议。
- **passive recall 不是建议任务的正确读取原语。**
  枚举式查询（"有哪些待办"）与通用英文查询全部 0 命中；
  只有已知主题的具体查询能命中。passive 路径为高精度被动注入调参，
  无法做"找出所有未解决行动项"。
- **跨语言 gap**：中文查询打不中英文 note，MVP 内查询语言需与 note 语言对齐。
- **推论（改变 read 契约设计）**：read 需要两个原语——
  `read.search`（语义检索，现有 recall）与
  `read.list`（结构化枚举：按 kind/certainty/时间窗过滤，**现无 API，
  是 MVP 要新增的第一个平台能力**，实现上是 NoteStore 的一个过滤查询）。
  建议流水线主路径走 `read.list(kind in blocker,handoff; certainty=unresolved)`，
  LLM 负责改写成可执行的 todo 文案并附引用；`read.search` 仅作上下文补充。
  blocker 翻转为 resolved 时，app 还可自动提示关闭对应 todo。

### MVP 内必须面对的风险

- **建议质量冷启动**：建议强制带 citation；dismiss 交互零摩擦。
  （冒烟测试已证实真实数据中存在可用行动项，风险降级。）
- **重复建议**：app 自有库存建议指纹 + dismissed 记录做去重——
  这是 app 状态的核心表，不是附属品。
- **归属身份**：MVP 做团队共享列表（建议进公共收件箱，认领制），
  个人化分派后置。

## 6. 落地顺序

1. **MVP：todo list app**（含 read/report 契约的第一次落地、`app_todo` schema、
   建议流水线、report ingest 路径）。← 已完成（PR #35）
1.5. **前端 app 插件区**：把 app 前端从门户核心收进 `web/src/apps/<name>/**`，
   门户核心只认识一个 **app 注册表**（每 app 声明导航名、路由、入口组件），
   `PortalShell` 遍历注册表渲染，不再 import 具体 app。
   仍是单 bundle 单构建（第一方 preset app 同版本 ship 的裁定不变），
   但新增/移除 app 变为"一个目录 + 一行注册"。
   该注册表同时是未来 generated app **通用 manifest 渲染器**的挂载点
   （注册表项既可指向手写组件，也可指向"渲染器 + manifest id"）——
   所以这不是临时方案，是通往目标架构的一步。
   独立多 bundle / 独立域名的更重分离方案已评估并否决：
   在"app 需要独立于门户发版"的真实需求出现之前均属过度设计。
2. **第二个 preset app**：验证 read/report SDK 与契约的复用性。
3. **generated app 的 manifest 托管 runtime** + pattern recognition →
   建议 → 确认 → 生成流水线。
4. **voice/video connector**：独立排期。

每个子项目走各自的 spec → plan → implementation 循环；
下一步是把 MVP 细化为实现计划。
