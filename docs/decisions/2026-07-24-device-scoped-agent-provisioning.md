# 设备级 Agent 供应（Device-Scoped Agent Provisioning）

状态：已接受

日期：2026-07-24

相关文档：

- [On-prem Human Identity and Agent Registry](./2026-07-21-on-prem-identity-and-agent-registry.md)
- [paxl on-prem Knowledge Capsule Channel](./2026-07-21-paxl-onprem-capsule-channel.md)
- [自描述 Enrollment Token](./2026-07-22-self-describing-enrollment-token.md)

## 背景

当前接入模型以单个 Agent 为粒度：每个 Agent 需要人在 Portal 创建 Agent Profile、
创建一次性 Enrollment、把 token 拷到目标机器，再分别完成 paxl（`channel connect`）
和 paxm（provider env 配置）两处接入。一台机器上运行多个 Agent CLI（codex、
claude、pi 等）时，每个 Agent 重复完整流程，产生两类摩擦：

1. 机器是人的天然心智单位（"把我这台电脑接上"），但 token 按 Agent 下发，
   一台机器需要 N 次 Portal 操作和 N 次 token 拷贝；
2. 同一个 Agent 的凭证要在 paxl 和 paxm 两个工具里各配置一遍，机器上同一份
   秘密存在多处，接入步骤翻倍。

Agent 维度的归因（`team_notes.origin_agent_id`、per-agent operations、recall 审计）
是系统核心价值，不能为了简化接入而合并 Agent 身份。要改的是凭证的**发放与配置
方式**，不是身份模型。

## 决策

### 1. 新增 Device 凭证类型

`agent_credentials` 增加 `kind` 维度：`agent`（现状，默认）与 `device`。

Device 凭证：

- 由一次性 Device Enrollment 交换而来，绑定创建它的 Human（`user_id`）和一个人类
  可读的设备名（如 `todd-macbook-air`）；
- 权限只有一个：`agent_provision`。它**不能** observe、search、recall 或走
  Channel——Device 不是知识生产/消费主体，任何知识行为仍然必须由 Agent 凭证完成；
- 有独立的生命周期：可吊销；吊销 Device 级联吊销由它铸发的全部 Agent 凭证。

### 2. Device 凭证可自助注册 Agent 并铸发 Agent 凭证

新增 Device 认证的供应端点：

```text
POST /v1/device/agent-provisions
```

请求：`agent_id`、`display_name`、`agent_type`（codex/claude/pi/...）、可选
`permissions`（不得超过 Device 创建时被授予的可授权集合）。响应：一次性返回
`tm_key_...` Agent 凭证。

- Agent Profile 不存在时由该端点按请求自描述创建，归因
  `provisioned_by = <device credential id>`；`agent_id` 冲突且归属同一 Device
  时返回既有有效凭证的轮换结果（新凭证 + 旧凭证吊销），归属其他 Device 或 Human
  时返回 409；
- 每个 Device 的活跃 Agent 数设上限（默认 16，可配置），铸发行为写入
  `onprem_audit_events`；
- 铸发的 Agent 凭证与现有凭证完全同构：同样的 `tm_key_` 格式、权限校验、
  observe/search/recall/channel 行为与归因路径不变。

### 3. 机器上单一凭证持有点，paxl/paxm 自助接入

paxl 与 paxm 的身份体系按三层对齐，on-prem 路径下 user 与 agent 两层本就同构，
需要统一的是存储与发现；device 层以本 ADR 的 Device 凭证为准：

| 层 | paxl（cloud manager） | paxl / paxm（on-prem） |
| --- | --- | --- |
| user | pax-manager 用户 + user API key | team-memory Human `usr_...`（两边相同） |
| device | pax-manager `NodeID` | 本 ADR 的 Device 凭证（设备名即本机节点身份） |
| agent | 无 | team-memory Agent 凭证 `tm_key_...`（两边相同） |

cloud 控制面（manager 用户/Node）与 on-prem 控制面（Human/Device/Agent）不做跨面
统一；统一发生在 on-prem 面内：

- paxm 的 team provider 不再要求手填 `TEAM_MEMORY_API_KEY` / `PAXM_USER_ID`，
  改为从 paxl 的 Device 凭证自助供应（见下）；`paxm setup` 的 `user-id` 默认值
  取自 paxl profile 的 team-memory `user_id`；
- zep/mem0 等 paxm 私有 provider 的自由文本 user-id 属于本地记忆分桶，不在
  兼容范围内。

目标接入流程（一台机器、任意多个 Agent）：

```bash
# 一次：人（Owner/Admin）在 Portal 创建 Device Enrollment，token 拷到机器
paxl device connect onprem \
  --url https://memory.company.internal \
  --device-name todd-macbook-air \
  --enrollment-token tm_enroll_xxx

# 之后：每个工具/Agent 从本机 paxl 的 Device 凭证自助供应，无需再回 Portal
paxl channel connect onprem --agent personal-codex   # paxl 自己铸发并保存
paxm setup --integration team-memory                  # paxm 发现 paxl Device 凭证，
                                                       # 为自己铸发 Agent 凭证
```

- paxl 在本机保存 Device 凭证（与 channel profile 相同的 sqlite 凭证区，文件权限
  0600），作为机器上唯一的长期秘密；
- paxm 的 team provider 增加凭证发现：本机存在 paxl Device 凭证时，调用供应端点
  为自己（按 `PAXM_AGENT_ID`）铸发/轮换 Agent 凭证并缓存；发现失败时回退到现有
  `TEAM_MEMORY_API_KEY` 显式配置，行为完全兼容；
- 非 paxl/paxm 的第三方 Agent 可直接用 HTTP 端点完成同样流程。

### 4. Portal 与审计

- Portal 增加 Device 视图：Device 列表（名称、创建者、铸发的 Agent 数、最近活动）、
  吊销操作（级联预览）；Device 详情页列出它铸发的 Agent；
- Agent 列表/详情展示 `provisioned_by`，区分"人工注册"与"设备自描述注册"；
- Device Enrollment 的创建沿用现有 Enrollment UI，仅增加 `device` 类型选择；
  默认权限模板不变，Device 类型强制 `agent_provision` 单项权限。

### 5. 安全边界

- Device Enrollment 与 Agent Enrollment 一样：一次性、短过期（15 分钟）、
  交换后即失效；
- Device 凭证泄漏的爆炸半径 = 可铸发新 Agent（受上限与审计约束）+ 已铸发 Agent
  的全部能力；吊销 Device 一键回收。它不扩大既有 Agent 凭证的任何权限；
- 供应端点不接受客户端指定的 scope、user_id 或跨用户 agent_id；
- 保留现有 Agent Enrollment 全流程不变，Device 流程是纯增量；两种流程产出的
  Agent 凭证在系统中不可区分（除 `provisioned_by` 归因）。

## 不做的事（v1）

- 不做 Device 自身的知识行为（observe/recall/channel）；
- 不做跨机器的 Device 共享或机器间 federation；
- 不做 Device 凭证的自动轮换提醒；v1 过期/泄漏走吊销重建；
- paxm 之外的第三方客户端 SDK 化留给后续。

## 结果

- 一台机器一次 Portal 操作、一次 token 拷贝、一条命令完成接入；后续 Agent 按需
  自助供应，paxl/paxm 不再重复配置；
- Agent 归因、权限模型、审计全部保留；
- 既有部署与凭证不受影响，可渐进采用。
