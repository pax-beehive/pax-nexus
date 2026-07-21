# paxl on-prem Knowledge Capsule Channel

状态：已接受

日期：2026-07-21

相关文档：

- [单团队本地部署与 PAX Memory 接口边界](./2026-07-21-single-team-on-prem-deployment.md)

## 背景

`paxl capsule send` 当前把 Knowledge Capsule 包装成 envelope，并固定通过
pax-manager 的用户 API 发送。该路径同时依赖 pax-manager 的 device login、User、
Node、Friend 和 Team Agent 模型。单团队 on-prem Team Memory 只有 enrollment、
credential-bound Agent identity、Observation 和 Memory search/get；它不应为了传递
Capsule 而复制云端 Friend、Team 或多租户控制面。

接收侧已经具有需要的本地能力：paxl 可以把远端 envelope 幂等地物化为本地 Capsule，
再根据 `any`、`project` 或 `keyword` route 创建 pending hook injection。新通道应复用
这套 payload 和本地注入语义，而不是引入第二种 Capsule 格式。

## 决策

### 1. 将远端投递抽象为 Envelope Channel

paxl 在 `EnvelopeFacade` 之后增加一个小型 `EnvelopeChannel` Interface：

```go
type EnvelopeChannel interface {
    Send(context.Context, SendEnvelopeRequest) (Envelope, error)
    List(context.Context, ListEnvelopeRequest) ([]Envelope, error)
    Get(context.Context, string) (Envelope, error)
    Accept(context.Context, string) (Envelope, error)
    Archive(context.Context, string) (Envelope, error)
}
```

`EnvelopeFacade` 继续拥有 Capsule payload 编码、本地物化、route injection 和幂等协调；
Channel 只负责认证后的远端传输与状态变更。

提供两个实现：

- `manager`：包装当前 pax-manager envelope API，保持现有行为和默认值；
- `onprem`：调用单团队 Team Memory 的 `/v1/channel/envelopes` API。

不能通过把 on-prem URL 写入现有 `manager_url` 来复用 manager client。两种服务的认证、
响应 envelope 和身份模型不同；伪装兼容会把 User、Node 和 device login 泄漏进本地部署。

### 2. 保持 Knowledge Capsule payload v2

两个 Channel 使用同一个 payload：

```text
paxl.envelope_payload.knowledge_capsule.v2
```

payload 保留 Capsule 内容与可选 route：

- `match_type`: `any`、`project` 或 `keyword`；
- `match_value`: project basename 或 prompt keyword；
- `target_agent`: 接收端本地 Agent adapter 过滤条件。

on-prem 服务验证 schema、大小和 route 结构，但不把 Capsule 自动写入 Team Note，也不把
它当作 Observation。只有接收端 paxl 成功写入本地 Capsule 和 injection 后，才确认远端
envelope。

### 3. v1 使用定向 Agent 收件箱

发送请求必须指定 `to_agent_id`。服务端从 Bearer credential 取得
`from_user_id + from_agent_id`，请求体不得提供或覆盖发送方身份。

v1 不提供 Team 广播。广播需要为每个接收 Agent 保存独立 delivery/ack 状态，不能复用
一个全局 `accepted` 状态；该能力留给后续独立设计。

Agent 必须已经完成 enrollment exchange 并持有有效 credential，才能成为发送目标。
在单个 on-prem 安装实例内，`agent_id` 被视为唯一的可路由身份。

### 4. Channel 配置与 CLI

paxl 把 Channel credential 与现有 manager credential 分开保存，允许 cloud manager 和
一个或多个 on-prem 实例同时存在。建议的 CLI 为：

```bash
paxl channel connect onprem \
  --url https://memory.company.internal \
  --enrollment-token tm_enroll_xxx

paxl capsule send <capsule-id> \
  --channel onprem \
  --to-agent-id agent-reviewer \
  --match project \
  --project team-memory \
  --agent codex

paxl inbox list --channel onprem
```

未指定 `--channel` 时继续使用 `manager`，保证现有脚本兼容。Agent hook 在 user-prompt
阶段轮询所有启用且允许自动接收的 Channel；单个 Channel 故障只记录诊断，不阻止其他
Channel 的本地 injection claim。

### 5. 一致性和安全边界

- 发送请求携带稳定 `idempotency_key`；同一发送 Agent 下重复 key 必须返回同一 envelope，
  不同 payload 使用同一 key 必须报冲突。
- payload 上限保持 128 KiB，message 上限保持 1000 字符。
- `channel_send` 和 `channel_receive` 是独立、显式 enrollment 权限；省略 permissions 时的
  既有默认权限集不自动获得 Channel 能力。
- inbox/get/accept/archive 只允许 credential-bound 接收 Agent；outbox 只允许发送 Agent。
- paxl 先提交本地 Capsule/injection，再调用远端 accept。accept 请求丢失时，remote
  envelope ID 仍可让本地重放保持幂等。
- Channel API 不提供 Friend、Team CRUD、租户选择或客户端指定 scope。

## Team Memory API Contract

```text
POST /v1/channel/envelopes
GET  /v1/channel/envelopes
GET  /v1/channel/envelopes/:envelope_id
POST /v1/channel/envelopes/:envelope_id/accept
POST /v1/channel/envelopes/:envelope_id/archive
```

创建请求包含 `to_agent_id`、`payload_type`、`payload_json`、可选 `message` 和必填
`idempotency_key`。列表支持 `status`、`direction`、`limit` 和 cursor。所有响应返回稳定
envelope ID、发送/接收 Principal、payload、状态和时间戳。

## 结果

- pax-manager 无需为 on-prem Channel 改动。
- Team Memory 新增外层部署能力，但 Team Note、LLM Wiki 和 Recall Router 不依赖 Channel。
- paxl 的本地 Capsule 和 hook route 行为在 cloud/on-prem 之间保持一致。
- v1 明确排除广播、服务端自动记忆化和跨实例 federation。
