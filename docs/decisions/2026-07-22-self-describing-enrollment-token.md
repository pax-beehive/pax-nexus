# 自描述 Agent Enrollment Token（内嵌 Origin）

状态：已接受；服务端与 Portal 已实现，paxl 客户端另行发布

日期：2026-07-22

相关文档：

- [On-prem Human Identity and Agent Registry](./2026-07-21-on-prem-identity-and-agent-registry.md)
- [On-prem Identity 前端接入指南](../on-prem-identity-frontend-integration.md)

## 摘要

把部署 origin 编码进 Agent Enrollment token，使 token 成为自描述的客户端接入
凭证：用户只需传递一个字符串，paxl 即可知道向哪个服务端兑换 Credential，不再
需要单独提供 `--url`。内嵌 origin 仅作为客户端提示，服务端授权模型不变。

## 背景

当前 Enrollment token 是不透明字符串 `tm_enroll_<enrollment_id>.<secret>`，服务端
只保存 digest。客户端兑换 API key 需要两个信息：token 本身和兑换端点 origin。因此
Portal 展示的一次性接入命令必须同时携带两者：

```bash
paxl channel connect onprem --url <origin> --enrollment-token <token>
```

当 token 需要经过聊天工具、密码管理器等渠道中转到目标设备时，token 与 origin
容易脱节：用户只复制了 token，或者把 token 给了别人却忘了附上 origin。origin 不是
秘密，把它编进 token 可以把"一个待兑换凭证"收敛成一个可搬运的字符串。

## 决策

### 1. Token 追加第三段：base64url 编码的 origin

```text
tm_enroll_<enrollment_id>.<secret>.<base64url(origin)>
```

- origin 取签发时部署配置的权威值（`TEAM_MEMORY_PORTAL_URL`），不接受请求方
  传入，避免签发侧被诱导生成指向任意外部的 token。
- base64url 编码（无 padding）保证整串仍然是 URL-safe、可整段复制的单 token。
- 旧两段式 `tm_enroll_<id>.<secret>` 必须继续被服务端和 paxl 接受；三段式与两段式
  的 `<id>.<secret>` 部分语义完全一致。

### 2. 服务端保持权威，内嵌 origin 不参与校验

- 服务端 exchange 逻辑不变：按 `<id>.<secret>` 的 digest 查找并原子消费
  Enrollment，忽略第三段。
- 第三段纯粹是给客户端的提示。服务端不校验它与实际请求来源是否一致——origin
  本来就不是访问控制信息。

### 3. paxl 把内嵌 origin 当作 `--url` 默认值，不一致时要求确认

- `paxl channel connect onprem --enrollment-token <token>`：三段式 token 自动解析
  origin；两段式 token 仍要求显式 `--url`。
- 显式 `--url` 优先于内嵌 origin。
- 内嵌 origin 与已有同名 profile 的 origin 不一致时，paxl 必须提示并要求确认，
  防止被篡改的 token 把 secret 送往攻击者控制的服务端（该服务端可拿 secret 向
  真实部署兑换并截获 API key）。
- paxl 不得把内嵌 origin 用于任何授权判断；它只是连接目标。

### 4. Portal 最后简化

服务端与 paxl 先后发布后，Portal 把"复制 token"与"复制客户端命令"收敛为单个
"复制接入凭证"动作；对旧两段式 token 保留现有完整命令展示。

## 影响

- **token 成为完全自包含的可兑换凭证。** 目前泄漏 token 时攻击者还需知道兑换
  端点；内嵌 origin 后这串字符本身就够用。这不改变"token 只展示一次、短时效、
  一次性"的既有约束，但降低了泄漏后的利用门槛，是可接受的折中。
- token 变长（origin 一般增加 30–60 字符），仍在可接受范围。
- 钓鱼向量的主要防线在 paxl 的 profile 不一致确认（决策 3），而非服务端。
- 分段发布无锁步要求：新服务端签发的三段式 token 在旧 paxl 上无法解析 origin，
  旧 paxl 用户可用显式 `--url` 兜底，但理想顺序仍是 paxl 先行。

## 备选方案

- **维持现状**：Portal 已提供含 origin 的完整命令，单次复制即用。否决理由：不
  覆盖 token 单独中转的场景。
- **HMAC 签名第三段**：让服务端能检测 origin 篡改。否决理由：服务端并不消费
  第三段，签名不能阻止客户端被诱导连接伪造服务端，反而增加密钥管理负担；防篡
  改的正确位置是 paxl 的确认交互。
- **改用深链/二维码**：面向 CLI 的目标设备无浏览器交互收益，不解决 ssh 到目标
  设备的场景。
