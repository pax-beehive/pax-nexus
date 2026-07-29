# PAX Nexus Application Platform — 架构愿景设计

日期：2026-07-29
状态：愿景已定，子项目 1 待细化为实现计划

## 1. 愿景

把 pax-nexus（Evidence Lake + Team Memory + LLM Wiki）从"一组内置产品"演进为
"一个可在其上搭建 application 的平台层"：

```
输入侧                     pax-nexus layer                    应用层
paxm ─────────┐   ┌──────────────────────────────┐   preset application 1..3
connectors ───┼──▶│ source database (Evidence Lake) │◀── Software 3.0
voice/videos ─┘   │ team memory   ·   LLM Wiki      │   (agent 自动生成的 app)
                  └──────────────────────────────┘
```

下层组件在代码库中已经存在（见 `CONTEXT-MAP.md`）。本设计的核心不是新组件，
而是一次**边界翻转**：今天 teamnote、pagewiki 是仓库内的 bounded context，
目标形态里应用是跑在平台契约之上的消费者。

## 2. 已确认的产品裁定

| 问题 | 裁定 |
|------|------|
| application 的作者与用户 | 第一方开发 preset application，与 pax-nexus 同版本打包 ship 给客户 |
| 自动生成发生在哪里 | 客户部署现场，daily 使用过程中持续做 pattern recognition |
| application 的最终形态 | Web 页面/视图 + 自动化任务（不做对话式 macro） |

推论：

- API 是**内部契约**，与 app 同版本发布，无跨版本对外兼容义务；
  主要兼容压力来自客户升级时"现场已生成的 app"能否存活。
- 客户现场无人审核即生成，安全边界必须靠平台机制而非人工把关。

## 3. 选定路线：App 即 Manifest（路线 A）

application 不是代码，是**一份声明式 manifest，作为数据存在数据库里**，
由平台内置的统一 runtime 执行：

```
Application manifest
├── identity + scopes     # 能读哪些 evidence stream / recall 范围
├── triggers              # cron 或 evidence 事件订阅
├── tasks                 # 每个 trigger 绑定一段 prompt + 工具集（recall/wiki/ingest）
└── views                 # 声明式页面：数据绑定（recall 查询/任务产出）+ 受限组件模板
```

- preset app = 手写 manifest，随版本 ship，加载即生效。
- Software 3.0 的字面含义在此成立：manifest 主体（task 的 prompt、view 的说明）
  就是自然语言；agent 生成一个 app = 生成一份 manifest。
- **沙箱问题几乎消失**：runtime 只执行声明式配置 + 带 scope 的 LLM 调用，
  没有任意代码执行；客户现场自动生成的安全边界靠 scope。
- **升级兼容 = 可再生（regenerable）**：底层 schema 变更后重新生成 manifest，
  不做 manifest 迁移。generated app 被视为可丢弃、可再生的产物，而非需长期维护的资产。

现有挂点：

- evidence lake processor（`docs/evidence-lake-processors.md`）→ 事件订阅扩展点
- `internal/recall` → view/task 的数据绑定查询层
- web 端新增一个通用 app 页面渲染器

### 已否决的路线

- **B：App 即生成代码**（agent 生成真实页面/job 代码，沙箱运行）。
  表达力无上限，但"客户现场无人审核 + 任意代码执行"的组合需要真代码沙箱、
  资源限额、安全审查、升级破损处理，现阶段风险收益不成比例。
- **C：先做完整平台化**（nexus API 抽成对外服务契约，app 全部独立进程接入）。
  边界最干净，但所有 app 均为第一方且同版本 ship，
  跨进程/跨版本契约成本花在没有第三方的地方，属过度设计。

后续若声明式 view 模板表达力不足，可仅在 view 层引入受限组件 DSL 内的
LLM 生成页面（吸收 B 的表达力），不放开任意代码。

## 4. 主要难点（按严重程度排序）

1. **Manifest DSL 表达力是天花板，也是核心设计难题。**
   太窄做不出有用的 app；太宽退化为任意代码（路线 B）。
   验证方式：先用 1 个真实 preset app 场景验算 DSL 能否表达它。
2. **契约冻结时机。** recall 语义仍在 optimization round 演进中；
   manifest 所依赖的查询/schema 需要区分"保证语义"与"尽力语义"，并版本化。
3. **授权与数据边界。** app 级 scope 是 on-prem identity 之上的新增维度；
   自动生成的 app 继承谁的权限必须显式裁定，默认最小权限。
4. **生成容易，生命周期难。** 版本、eval 门禁、安装/停用、schema 变更后的再生成。
   对策即"可再生"取舍（见上）。
5. **反馈污染。** app 产出若回写为 evidence 会形成自我强化回路。
   强制 provenance 标记（human vs app 产生），pattern recognition
   一律排除 app 产生的 evidence。
6. **模式识别冷启动。** 小团队 session 中真实模式稀疏，agent 易从噪声中"发现"模式。
   必须有置信阈值 + 人工确认闸门（"我注意到你每周做 X，要生成一个 app 吗？"），
   冷启动期禁止无确认自动上线。
7. **voice/video 是独立大工程。** 转写、说话人分离、identity 对齐、成本控制；
   仅是 ingest 侧多一路，独立排期，不阻塞平台化。

## 5. 落地顺序（四个可独立交付的子项目）

1. **App manifest + runtime**：scheduler、evidence 事件订阅、通用页面渲染器；
   用 1 个手写 preset app 验证 DSL 表达力。← 地基，先做
2. **再补 2 个 preset app**：用真实需求把 DSL 打磨到"够用"。
3. **Pattern recognition → 建议流水线**：一个 evidence lake 消费者，
   检测重复模式（重复 recall 查询、周期性 session 主题）→ 置信度 →
   用户确认 → agent 生成 manifest → eval → 安装。
4. **voice/video connector**：独立排期。

每个子项目走各自的 spec → plan → implementation 循环；
下一步是把子项目 1 细化为设计与实现计划。
