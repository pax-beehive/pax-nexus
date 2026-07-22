# On-prem Human Identity and Agent Registry

状态：已接受

日期：2026-07-21

相关文档：

- [单团队本地部署与 PAX Memory 接口边界](./2026-07-21-single-team-on-prem-deployment.md)
- [paxl on-prem Knowledge Capsule Channel](./2026-07-21-paxl-onprem-capsule-channel.md)
- [On-prem Identity 领域语言](../../internal/deployment/onprem/CONTEXT.md)

## 摘要

单团队 on-prem 安装采用人机分离的身份模型：外部身份提供者认证 `User`，本地
`Membership` 决定人的 `owner/admin/member` 角色；`Membership` 拥有一个或多个稳定的
`Agent` Profile；每个 Agent 再持有一个或多个带显式权限的 `Agent Credential`。

部署 Secret 只用于建立第一个 Owner，不作为长期共享的 Admin API Key。管理员通过
Human Invitation 邀请人加入；成员先登记 Agent Profile，再创建短期 Agent Enrollment，
由 paxl 或其他客户端在目标设备上兑换长期 Agent API Key。Human Invitation、Agent
Enrollment 和 Agent Credential 是三种不同的安全对象，不能复用 token、表或生命周期。

本 ADR 细化并取代现有单团队部署 ADR 中“静态 Admin API Key 长期管理
Enrollment”的部分；单 Team、固定内部 scope、Credential-bound Principal 和 Channel
边界保持不变。

## 背景

现有 on-prem v1 直接由静态 Admin API Key 创建 Agent Enrollment。Enrollment 同时携带
`user_id`、`agent_id` 和权限，兑换后直接得到绑定 `user_id + agent_id` 的 API Key。这条
路径适合最初的 headless 部署，但没有以下产品概念：

- 可以登录个人入口的 User；
- User 与当前安装实例之间的 Membership 和角色；
- 邀请、注册、暂停和移除成员；
- 独立于 Credential 的 Agent Profile 与所有权；
- User 自助签发、查看和撤销自己 Agent 的 Credential；
- 可供 paxl 选择接收目标的 Agent Directory；
- 人类管理操作的 Audit Trail。

继续扩展当前 Enrollment 会把“邀请一个人加入”“登记一台 Agent 身份”和“给某个客户端
发一把机器 Key”混成同一个流程。复用 pax-manager 的 User、Node、Friend、Team 控制面
又会破坏一个安装实例就是一个 Team 的既有边界。

## 决策

### 1. 身份由 Human Plane 和 Agent Plane 两层组成

Human Plane 负责人的登录、Membership、角色和 Agent 所有权。Agent Plane 负责机器
身份、Enrollment、Credential 和细粒度权限。

| Plane | Principal | Authentication | Authorization |
| --- | --- | --- | --- |
| Human | User + Membership | OIDC-backed Human Session | `owner/admin/member` role |
| Agent | Agent + Credential | Bearer Agent API Key | explicit Agent permissions |
| Bootstrap | unaffiliated authenticated User | one-time deployment Bootstrap Secret | establish first Owner only |

Human role 不得写进 Agent Credential；Agent permission 也不得被当作 Human role。Agent
Credential 永远不能获得 `owner` 或 `admin`。

产品 Module 只消费认证后的 Principal。User、Membership、Agent Profile、Credential 和
Human Session 都属于 `internal/deployment/onprem` 外层部署上下文；Team Note、Session、
LLM Wiki 和 Recall 不得反向依赖其 Store。

### 2. Bootstrap Secret 只建立第一个 Owner

新安装通过环境或 Secret Manager 注入高熵 Bootstrap Secret。用户先完成 OIDC 登录，得到
一个尚无 Membership 的 Human Session，再携带 Bootstrap Secret 调用 bootstrap claim。
服务端仅在不存在 active Owner 时原子创建第一个 Owner Membership。

成功后：

- bootstrap claim 永久关闭；
- Bootstrap Secret 不再被普通 API 接受；
- 日常管理使用个人 Human Session，而不是共享管理员 Secret；
- 最后一个 active Owner 不能被删除、暂停或降级；
- Owner 可以把另一个 active Member 提升为 Owner 后再转移职责。

不把 Bootstrap Secret 兼作 break-glass credential。若需要灾难恢复，应使用独立、默认关闭、
有审计的 Recovery 流程，另行设计；不能让长期 root key 绕过所有 User Audit。

### 3. 人类认证委托给 OIDC，不在 v1 保存密码

v1 Human Plane 使用可配置 OIDC Provider。User 的稳定认证键是 `(issuer, subject)`；email
和 display name 只是可更新的 Profile Claim，不能作为主键。

这样避免在 Team Memory 中重新实现密码哈希、MFA、密码重置、邮箱验证、撞库防护和账户
恢复。没有 OIDC 的完全自包含人类认证不属于 v1；未来若增加 Passkey 或 Magic Link，应
实现同一个 Human Identity Provider Interface，不改变 Membership、Agent 和 Credential
模型。

浏览器使用 `Secure`、`HttpOnly`、`SameSite=Lax` 的短期 Human Session cookie。所有修改
状态的 cookie-authenticated 请求必须执行 CSRF 防护。Agent API Key 继续使用
`Authorization: Bearer tm_key_...`，不能建立 Human Session。

完成 OIDC 不等于加入 Team。callback 可以创建或更新 User Profile，但没有 Membership 的
Human Session 只能调用 `/v1/me`、Invitation accept、bootstrap claim 和 logout；其他接口
返回 `403 membership_required`。因此 v1 是 invite-only registration，不提供绕过 Invitation
的 open signup。OIDC email 后续变化只更新 Profile，不改变已授予的 Membership。

### 4. Invitation 创建 Membership，不创建 Agent

Owner/Admin 通过 Human Invitation 邀请用户加入当前隐式 Installation。Invitation 是
一次性、有过期时间、可撤销的 claim：

- 必须绑定目标 OIDC subject，或绑定 OIDC 已验证 email；
- Invitation token 只返回一次，服务端只保存不可逆摘要；
- Invitation 只能授予 `member` 或 `admin`，不能直接授予 `owner`；
- Admin 只能邀请和管理 Member；Owner 才能授予或撤销 Admin/Owner 角色；
- 接受 Invitation 时必须同时验证 Human Session 与目标绑定；
- 接受操作是原子的；同一 Invitation 不能被第二个 User 领取；
- 用户可以成为 Member 而不登记任何 Agent。

Membership 生命周期：

```text
Invitation: pending -> accepted
                    -> revoked
                    -> expired

Membership: active <-> suspended -> removed
```

`removed` 是终态。重新加入需要新 Invitation 和新的 Membership row/audit chain，不能把
旧 Membership 改回 active。暂停或移除 Membership 时，必须在同一事务中停止其所有
Agent 的路由能力、撤销所有 Agent Credential 和 Human Session；历史 Session、Observation、
Team Note、Wiki 和 Envelope 不级联删除。重新加入也不会自动取回旧 Membership 拥有的
Agent；Owner 必须显式执行 ownership transfer。

### 5. User 先登记 Agent，再签发 Credential

Agent 是稳定的 machine identity，不是 Credential。一个 Member 可以拥有多个 Agent；
v1 中一个 Agent 只有一个 owning Membership。

Agent Profile 至少包含：

- `agent_id`：安装实例内唯一、不可变、不可复用的 routing ID；
- `owner_membership_id`：当前 owning Membership；
- `display_name`：面向人的可变名称；
- `description`：该 Agent 的职责与适用场景；
- `agent_type`：例如 `codex`、`claude-code`、`openclaw`；
- `status`：`active`、`suspended` 或 `retired`；
- `directory_visible`：是否允许出现在 Team Agent Directory；
- 创建、更新时间和 retirement 时间。

`agent_id` 是 opaque exact-match identifier：服务端去除首尾空白、拒绝空值、控制字符和
路径分隔符，并施加 128-byte 上限，但不从 display name 或 agent type 推导它。大小写敏感，
创建后不能修改；retired ID 永不释放，防止旧 Envelope、Audit Event 和 Session provenance
被新 Agent 冒领。

Owner 可以更新 Profile、暂停或 retire 自己的 Agent。Admin 可以暂停任意 Agent；只有
Owner 角色可以转移 Agent 所有权。转移所有权会撤销该 Agent 的全部 Credential，新 Owner
必须重新 Enrollment，避免旧 Owner 继续以历史 Principal 访问。

Agent 生命周期：

```text
active <-> suspended -> retired
```

`retired` 是终态。没有 Credential 的 active Agent 仍是有效 Profile，但不是 Routable
Agent。

### 6. Personal Portal 创建 Agent Enrollment，而不展示长期 API Key

成员在个人入口为自己已有的 Agent 创建 Agent Enrollment，选择：

- credential label，例如 `macbook-codex`；
- 显式 Agent permissions；
- Enrollment TTL；
- 可选的 Credential expiry policy。

服务端返回一次性 `tm_enroll_...` token。paxl 在目标设备调用现有 exchange API，服务端
原子消费 Enrollment 并只向该设备返回一次 `tm_key_...`。Portal 不生成、保存或再次显示
长期 Agent API Key。

Agent Enrollment 与 Human Invitation 使用不同前缀、表、摘要域和 endpoint。两者状态机
相似但语义不同：

```text
Agent Enrollment: pending -> consumed
                          -> revoked
                          -> expired
```

一个 Agent 可以持有多个 active Credential，支持多设备和无中断轮换。Credential 包含：

- `credential_id`；
- `agent_id`；
- 签发时的 `owner_membership_id + owner_user_id` snapshot；
- label；
- permission set；
- key digest；
- created、expires、revoked、last-used 时间；
- 可选 `rotated_from_credential_id`。

API Key 只存不可逆摘要。列表接口永远不返回 key、摘要或 Enrollment token。Agent 自助轮换
继承原 Credential 的 Agent、权限和 label，并给旧 Key 一个短暂 overlap；Portal 或 Admin
可以立即 revoke 任意 Credential。

### 7. Human role 限制“能管理谁”，Agent permission 限制“机器能做什么”

Human role policy：

| Operation | Owner | Admin | Member |
| --- | --- | --- | --- |
| Invite Member | yes | yes | no |
| Invite/promote Admin | yes | no | no |
| Promote another Owner | yes | no | no |
| Suspend/remove Member | yes | yes | no |
| Suspend/remove Admin | yes | no | no |
| List all Users/Agents | yes | yes | no |
| Create/update own Agent | yes | yes | yes |
| Issue/revoke own Agent Credential | yes | yes | yes |
| Suspend any Agent | yes | yes | no |
| Transfer Agent ownership | yes | no | no |

Agent permissions 保持显式且独立：

```text
observe
search
get
channel_send
channel_receive
```

新 Enrollment 必须显式传 permissions；不能因为字段缺失自动获得默认权限。安装配置提供
`member_grantable_permissions` allowlist，Agent Owner 只能授予 allowlist 内的权限；Admin
也不能把 Human admin 能力写进 Agent Credential。未来增加权限必须保持 deny-by-default。

v1 不增加逐 Agent 或逐 Credential 的人工审批队列。active Member 可以在 allowlist 内
自助创建 Agent 和 Enrollment，Admin 通过 suspend/revoke 进行治理。需要审批流的安装可在
未来增加 policy，不改变核心实体。

### 8. Agent Directory 只暴露可投递的最小 Profile

Channel 发送者需要发现 `to_agent_id`，因此增加 Agent Directory：

```text
GET /v1/channel/agents
GET /v1/channel/agents/:agent_id
```

访问者必须持有 `channel_send`。Directory 只返回 Routable Agent：

- Agent status 为 `active`；
- owning Membership 为 `active`；
- `directory_visible=true`；
- 至少存在一个未过期、未撤销且含 `channel_receive` 的 Credential。

Directory response 只包含 `agent_id`、`display_name`、`description` 和 `agent_type`，不暴露
Owner User/Membership、Credential、permission set 或 last-used。不存在、不可见、暂停或
不可接收的单项查询统一返回 `404`，避免通过 Directory 探测内部身份状态。

`directory_visible` 只控制发现，不是 sender ACL。已经通过其他安全渠道获得 exact
`to_agent_id` 的发送者仍可向 hidden Routable Agent 定向投递；v1 不增加逐发送者 allowlist。
如果未来需要受限收件箱，应增加独立 Inbound Policy，不能把“是否列出”和“谁可发送”复用
为一个布尔值。

`GET /v1/agent-identity` 继续表示“当前 Agent Credential 是谁”；`GET /v1/me/agents` 表示
“当前 User 拥有哪些 Agent”；`GET /v1/admin/agents` 表示管理视图。三者不能合并成一个
带可选参数的模糊接口。

## 数据模型

### 关系

```text
onprem_installation_state (singleton)

onprem_users       1 --- N onprem_human_sessions
onprem_users       1 --- N onprem_memberships
onprem_users       1 --- N onprem_membership_invitations (accepted_by)
onprem_memberships 1 --- N onprem_membership_invitations (created_by)
onprem_memberships 1 --- N onprem_agents
onprem_agents      1 --- N agent_enrollments
onprem_agents      1 --- N agent_credentials

all state changes --- N onprem_audit_events
```

单团队安装不增加 `team_id` 或 `installation_id` 列。数据库实例本身就是 Installation
boundary，内部产品数据继续使用不可由请求选择的 `local-team` scope sentinel。

### `onprem_installation_state`

| Column | Constraint / meaning |
| --- | --- |
| `singleton_id` | fixed primary key `1`; not an external installation ID |
| `bootstrap_claimed_at` | null until the first successful claim |
| `bootstrap_claimed_by_membership_id` | first Owner Membership |
| `created_at`, `updated_at` | audit timestamps |

Bootstrap claim 锁定 singleton row，并同时验证 `bootstrap_claimed_at IS NULL` 和没有 active
Owner。全新安装还要求不存在其他 Membership；升级安装只允许存在 migration 创建的
unclaimed legacy Member。成功创建 first Owner 后写入 claimed state；即使后续数据异常导致
零 Owner，也不能重新开放 bootstrap。Bootstrap Secret 本身不入库。

### `onprem_users`

| Column | Constraint / meaning |
| --- | --- |
| `user_id` | server-generated stable primary key; never email |
| `identity_issuer` | OIDC issuer; nullable only for migrated legacy owner records |
| `identity_subject` | OIDC subject; nullable only before legacy claim |
| `email` | nullable profile claim; never authorization key |
| `email_verified` | copied from trusted provider claim |
| `display_name` | mutable profile field |
| `identity_status` | `unclaimed`, `active`, `disabled` |
| `created_at`, `updated_at`, `last_login_at` | audit timestamps |

建立 partial unique index `(identity_issuer, identity_subject)`，只覆盖非空 identity。User
删除采用 `disabled`，不 hard delete。

### `onprem_human_sessions`

| Column | Constraint / meaning |
| --- | --- |
| `session_id` | public lookup ID and primary key |
| `user_id` | FK to `onprem_users` |
| `secret_digest`, `digest_key_version` | verifier for the opaque cookie secret |
| `created_at`, `expires_at`, `last_seen_at` | bounded session lifetime |
| `revoked_at` | logout, identity disable, or Membership status change |

Human Session 是可撤销的 server-side session，不把 OIDC access/refresh token 放进浏览器。
OIDC callback 完成后销毁 transient state/nonce/PKCE material。服务端可以从 session secret
派生 CSRF token；Membership suspension/removal 与 User disable 必须撤销相关 session。

### `onprem_memberships`

| Column | Constraint / meaning |
| --- | --- |
| `membership_id` | server-generated stable primary key |
| `user_id` | FK to `onprem_users` |
| `role` | `owner`, `admin`, `member` |
| `status` | `active`, `suspended`, `removed` |
| `invited_by_membership_id` | nullable for bootstrap/migration; role-bearing actor |
| `joined_at`, `suspended_at`, `removed_at`, `updated_at` | lifecycle timestamps |

建立 `user_id` 的 partial unique index，只覆盖 `active`、`suspended`，保证一个 User 同时最多
有一个 live Membership，同时保留 removed history 并允许未来通过新 Invitation 重新加入。
所有会减少 active Owner 数量的写入必须串行化并在事务内验证“至少一个 active Owner”。

### `onprem_membership_invitations`

| Column | Constraint / meaning |
| --- | --- |
| `invitation_id` | primary key |
| `token_digest` | unique irreversible digest |
| `digest_key_version` | identifies the server-side digest key |
| `target_issuer`, `target_subject` | preferred exact identity binding |
| `target_email` | fallback; requires verified OIDC email |
| `role` | `admin` or `member` only |
| `created_by_membership_id` | active Owner/Admin according to role policy |
| `created_at`, `expires_at` | validity window |
| `accepted_at`, `accepted_by_user_id`, `created_membership_id` | atomic claim result |
| `revoked_at` | explicit cancellation |

至少存在 subject binding 或 email binding。pending 查询使用 token digest、expiry、accepted 和
revoked 条件的索引。

### `onprem_agents`

| Column | Constraint / meaning |
| --- | --- |
| `agent_id` | immutable primary key and routing identity |
| `owner_membership_id` | FK to the owning Membership, including historical removed owner |
| `display_name` | required human-readable name |
| `description` | bounded text |
| `agent_type` | bounded adapter/type identifier |
| `status` | `active`, `suspended`, `retired` |
| `directory_visible` | team directory visibility |
| `created_at`, `updated_at`, `retired_at` | lifecycle timestamps |

不对历史 Session、Memory 或 Envelope 建级联 FK。那些记录中的 `user_id/agent_id` 是事件发生
时的 provenance snapshot。

### `agent_enrollments`

演进现有表而不是创建 Human Invitation 的复用表：

| Column | Constraint / meaning |
| --- | --- |
| `enrollment_id` | primary key |
| `token_digest` | unique irreversible digest |
| `digest_key_version` | identifies the server-side digest key |
| `agent_id` | FK to `onprem_agents` |
| `issued_by_membership_id` | Agent Owner or Admin role-bearing actor |
| `credential_label` | identifies target installation/device |
| `permissions` | explicit non-empty permission array |
| `credential_expires_at` | nullable target Credential expiry |
| `created_at`, `expires_at` | Enrollment validity |
| `consumed_at`, `consumed_credential_id` | one-time exchange result |
| `revoked_at` | cancellation before exchange |

### `agent_credentials`

演进现有表：

| Column | Constraint / meaning |
| --- | --- |
| `credential_id` | primary key |
| `key_digest` | unique irreversible digest |
| `digest_key_version` | identifies the server-side digest key |
| `agent_id` | FK to `onprem_agents` |
| `owner_membership_id`, `owner_user_id` | ownership and Principal snapshots at issue time |
| `label` | human-readable device/integration label |
| `permissions` | explicit non-empty permission array |
| `created_at`, `expires_at` | validity window |
| `revoked_at`, `last_used_at` | lifecycle/observability |
| `rotated_from_credential_id` | nullable lineage |

Credential authentication additionally requires
`credential.owner_membership_id = agent.owner_membership_id`，并 joins current Agent、owning
Membership 和 User status；仅凭 Credential row 未过期不足以授权已转移、暂停或移除的
Member/Agent。正常 User 必须为 `active`；migration-only `unclaimed` User 只允许其既有 legacy
Credential，`disabled` User 一律拒绝。

### `onprem_audit_events`

| Column | Constraint / meaning |
| --- | --- |
| `audit_event_id` | monotonic/ordered primary key |
| `actor_kind` | `bootstrap`, `human`, `agent`, `system` |
| `actor_user_id`, `actor_membership_id` | nullable Human actor identity and role context |
| `actor_agent_id`, `actor_credential_id` | nullable Agent actor identity |
| `action` | stable namespaced event type |
| `target_kind`, `target_id` | affected entity |
| `metadata` | bounded JSON without secrets |
| `occurred_at` | server timestamp |

至少记录 bootstrap claim、Invitation create/revoke/accept、role/status change、Agent
create/update/suspend/retire/transfer、Enrollment create/revoke/consume，以及 Credential
issue/rotate/revoke。Token、API Key、digest、OIDC token 和完整 Authorization header 永不写入
日志或 Audit metadata。

### Secret encoding and storage

新 secret 使用带 public lookup ID 的格式：

```text
tm_invite_<invitation_id>.<random-secret>
tm_enroll_<enrollment_id>.<random-secret>
tm_key_<credential_id>.<random-secret>
tm_session_<session_id>.<random-secret>
```

`random-secret` 至少 256 bit，使用 CSPRNG 生成并只返回一次。服务端先用 public ID 定位
record，再以带版本的 server-side pepper 计算 domain-separated HMAC digest，并使用
constant-time comparison 校验 secret。四类 secret 使用不同的 HMAC domain；数据库泄露不能
直接把 digest 当作 bearer token。Pepper 由 Secret Manager 提供，不与数据库一起保存。

ID 不是 secret，可以出现在 URL path、trace 和 Audit Event；完整 token、random secret 与
digest 不得出现。legacy token 在兼容窗口继续走旧 digest lookup，所有新签发都使用上述
格式。

## Canonical onboarding flow

```text
Deployment operator -> bootstrap Owner with OIDC session + Bootstrap Secret
Owner/Admin          -> create human Invitation link
Invited human        -> OIDC login -> accept Invitation -> active Membership
Member portal        -> create Agent Profile (agent_id + description + type)
Member portal        -> create one-time Agent Enrollment
paxl target device   -> exchange Enrollment -> store Agent API Key locally
Agent API Key        -> identify self -> list/get routable Agents -> send Capsule
```

Invitation create response 可以返回一次性 `accept_url`，格式为
`https://<installation>/join#invite=tm_invite_...`。token 放在 URL fragment，不进入 HTTP access
log；join page 设置 `Referrer-Policy: no-referrer`，并由前端把 token 放进 accept request。用户
完成 OIDC 后才能接受，拿到链接本身不等于成为 Member。

## HTTP Interface

所有新增 Hertz HTTP Interface 在实现时必须先写入 `idl/team_memory.thrift`，再运行
`make generate`；以下资源和 endpoint 是 IDL 的规范设计输入。

### Resource shapes

```text
Member {
  membership_id, user_id, email, email_verified, display_name,
  role, status, joined_at, updated_at, resource_version
}

AgentProfile {
  agent_id, display_name, description, agent_type,
  status, directory_visible, created_at, updated_at, resource_version
}

AgentEnrollment {
  enrollment_id, agent_id, credential_label, permissions,
  status, created_at, expires_at, credential_expires_at
}

AgentCredentialMetadata {
  credential_id, agent_id, label, permissions,
  created_at, expires_at, revoked_at, last_used_at
}

DirectoryAgent {
  agent_id, display_name, description, agent_type
}
```

Owner/Admin 视图可以在 Member/AgentProfile 外增加 owning Membership 与治理状态；普通
Directory 不复用 Admin DTO。所有 mutation resource 带 `resource_version`，更新 role、status、
Profile 或 ownership 时使用 `If-Match`，版本不匹配返回 `409`，防止 portal 中的并发写覆盖。

### Authentication surface

| Surface | Accepted authenticator |
| --- | --- |
| OIDC login/callback | state + nonce + PKCE protocol state |
| `/v1/me`, invitation accept, `/v1/admin/*` (new) | Human Session cookie |
| `/v1/bootstrap/claim` | Human Session + Bootstrap Secret header |
| enrollment exchange | one-time Agent Enrollment token |
| agent identity/rotation, Memory, Observation, Channel | Agent API Key |
| legacy admin enrollment/revoke during migration | static Admin API Key only |

Human Session 不得调用 Agent self-service endpoint，Agent API Key 不得调用 Human/Admin
endpoint。不能通过同时支持多种认证来让低权限 Principal 意外落入更高权限分支。

### Human authentication and bootstrap

```text
GET  /v1/auth/login
GET  /v1/auth/callback
POST /v1/auth/logout
POST /v1/bootstrap/claim
```

`bootstrap/claim` 同时要求已认证但尚无 Membership 的 Human Session，以及专用
`X-PAX-Bootstrap-Secret` header。该 header 必须被 access log/redaction middleware 视为秘密；
Bootstrap Secret 不放 query 或 JSON body。它只在零 active Owner 时成功。

### Invitation and Membership

```text
POST   /v1/admin/invitations
GET    /v1/admin/invitations?status=&limit=&cursor=
DELETE /v1/admin/invitations/:invitation_id
POST   /v1/invitations/accept

GET    /v1/me
GET    /v1/admin/members?status=&role=&limit=&cursor=
GET    /v1/admin/members/:membership_id
PATCH  /v1/admin/members/:membership_id
```

Invitation accept 使用 `Idempotency-Key`。`PATCH members` 只能改变 role/status，不能改变
OIDC identity。Invitation token 只出现在 create response/accept URL 和 accept request。对
已有 active/suspended Membership 的 User 接受新 Invitation 返回 `409`。

Secret-bearing create 不承诺重放明文 token：若 Invitation create response 丢失，管理员从
list 找到该 pending record，revoke 后重新创建。这样不用为了网络重试可逆保存 secret。

### Agent Profile and owner-side Credential management

```text
GET    /v1/me/agents?status=&limit=&cursor=
POST   /v1/me/agents
GET    /v1/me/agents/:agent_id
PATCH  /v1/me/agents/:agent_id
DELETE /v1/me/agents/:agent_id

POST   /v1/me/agents/:agent_id/enrollments
GET    /v1/me/agents/:agent_id/enrollments?status=&limit=&cursor=
DELETE /v1/me/agents/:agent_id/enrollments/:enrollment_id

GET    /v1/me/agents/:agent_id/credentials
DELETE /v1/me/agents/:agent_id/credentials/:credential_id
```

`POST /v1/me/agents` 接收 `agent_id`、`display_name`、`description`、`agent_type` 和
`directory_visible`。Enrollment create 接收 `credential_label`、非空 `permissions`、
`expires_in_seconds` 和可选 `credential_expires_at`；response 在 metadata 外只返回一次
`enrollment_token`。

`DELETE agent` 表示 retire，不 hard delete。Credential list 返回 metadata，不返回秘密。
Agent create 和 revoke 接受 `Idempotency-Key`；secret-bearing Enrollment create 与 Invitation
相同，response 丢失时 revoke 后重建，不提供 token read-back。

### Agent self-service and Channel directory

```text
POST /v1/agent-enrollments/exchange
GET  /v1/agent-identity
POST /v1/agent-credentials/rotate

GET  /v1/channel/agents?q=&limit=&cursor=
GET  /v1/channel/agents/:agent_id
```

保留现有 Agent endpoint，避免 paxl 配置和 Credential rotation 断裂。Directory 使用 Agent
Credential，而不是 Human Session。

### Admin Agent management

```text
GET   /v1/admin/agents?owner_membership_id=&status=&q=&limit=&cursor=
GET   /v1/admin/agents/:agent_id
PATCH /v1/admin/agents/:agent_id
POST  /v1/admin/agents/:agent_id/transfer
GET   /v1/admin/agents/:agent_id/enrollments
DELETE /v1/admin/agents/:agent_id/enrollments/:enrollment_id
GET   /v1/admin/agents/:agent_id/credentials
DELETE /v1/admin/agents/:agent_id/credentials/:credential_id

GET   /v1/admin/audit-events?actor_kind=&action=&target_kind=&target_id=&limit=&cursor=
GET   /v1/admin/audit-events/:audit_event_id
```

Admin list/get 可返回 owner、Agent 状态和 Credential metadata，但仍不得返回 Key 或 digest。
transfer 仅允许 Owner role，并在同一事务撤销旧 Credential。Audit read 允许 Owner/Admin，
但 response 继续执行 secret redaction 和 metadata size limit。

### Response and error conventions

- 所有 list 使用 opaque cursor；默认 `limit=50`，最大 `100`；
- create response 返回 server-generated ID、状态和时间戳；
- `401`：缺少或无效认证；
- `403`：Principal 有效但 role/permission 不足；
- `404`：资源不存在或调用方不可见；
- `409`：Agent ID 冲突、最后 Owner invariant、重复 claim 或非法状态转换；
- `410`：调用方已持有的 Invitation/Enrollment token 已过期、消费或撤销；
- `422`：结构合法但违反字段/permission policy；
- `429`：login、Invitation accept、Enrollment exchange 或 Key authentication 限流。

Human mutation endpoint 必须执行 CSRF 防护。Invitation accept、Enrollment exchange 和
Credential authentication 按来源与 token/key digest 双维度限流。Human cookie endpoint 只
允许配置的 portal origin，不允许 credentialed wildcard CORS。

## 兼容与迁移

采用渐进迁移，不使已部署 Agent API Key 失效：

1. 创建 Installation State、User、Human Session、Membership、Invitation、Agent Profile 和
   Audit 表；
2. 为现有 `agent_credentials.user_id` 去重创建 `unclaimed` User，以及带独立
   `membership_id` 的 active Member 记录；
3. 从 `onprem_agent_identities` 和现有 Credential 回填 Agent Profile，默认
   `display_name=agent_id`、`status=active`、`directory_visible=true`；
4. 保留现有 Credential digest、ID、权限和时间戳，并补 owner Membership/User snapshot、
   label；
5. 若同一 `agent_id` 已关联多个 `user_id`，migration 必须失败并要求管理员先处理，不能
   静默选择 Owner；
6. 现有 `onprem_agent_identities` 在所有读路径切换到 `onprem_agents` 后删除；
7. 旧 `POST /v1/admin/agent-enrollments` 在兼容窗口内保留，但只能为已存在 Agent 创建
   Enrollment，传入 `user_id` 必须匹配 owning Membership 的 User；为兼容旧客户端，该
   legacy endpoint 在 permissions 缺失时暂时保留原默认值并输出 deprecation audit，新
   `/v1/me/.../enrollments` 必须显式传 permissions；
8. 当前静态 Admin API Key 在升级窗口内仅支持 legacy admin endpoint，并输出 deprecation
   audit；建立第一个 Owner 后默认关闭。需要延长时必须使用显式配置，不能永久默认开启；
9. unclaimed legacy User 的 Agent/Credential 保持运行，但该 User 不能登录个人入口。真人
   通过正常 Invitation 获得自己的新 Membership；Owner 再显式 transfer 对应 legacy Agent，
   transfer 会撤销旧 Credential，成员通过新 Enrollment 重新接入。迁移本身不自动转移
   ownership，也不自动使现有 Key 失效。

产品历史数据中的 `user_id/agent_id` 不迁移、不重写，也不因 User/Agent 状态变化而删除。

## 被拒绝的方案

### 继续让 Admin Key 直接签发任意 Agent

实现简单，但没有 Human identity、所有权、自助管理和逐人审计；共享 root secret 无法区分
谁创建或撤销了 Agent，不作为长期产品模型。

### Invitation 直接创建 Agent Credential

这会把 Human Invitation 与机器 Enrollment 混为 bearer token，并迫使一个人加入时就必须
拥有 Agent。拒绝。

### Portal 直接生成并展示长期 API Key

长期 Key 会经过浏览器、剪贴板和 Human Session。选择 Portal 创建短期 Enrollment、目标
设备兑换 Key，使长期秘密只落在实际 Agent 设备。

### 在本服务保存密码

需要承担密码恢复、MFA、邮件验证和攻击防护，且与 Agent Memory 无关。v1 委托 OIDC。

### 复用 pax-manager User/Node/Team/Friend 模型

会把多租户控制面和云端关系模型带进单 Team on-prem。仅复用 paxl 的 Agent/Capsule 语义，
不复用 pax-manager ownership topology。

### 每次 Agent 或 Credential 创建都要求 Admin 审批

能提供更强控制，但会破坏本地团队的自助接入。v1 采用 role + grantable permission policy
和事后 suspend/revoke；未来可在不改变实体模型的情况下增加审批 Policy。

## 结果

- 增加 Human auth、Invitation、Membership、Agent Registry、Personal Portal 和 Audit 能力；
- 现有 Agent Credential、Memory Principal 和 Capsule Channel Contract 保持兼容；
- paxl 继续使用一次性 Agent Enrollment 配置目标设备，无需接触 Human Session；
- User removal、Agent suspension 和 ownership transfer 将成为影响所有 Credential 与路由的
  强授权边界；
- Team Note、Session、LLM Wiki 和 Recall 继续只接收 Principal 与 provenance，不拥有身份
  数据；
- v1 仍是单 Installation、单 Team，不增加 Team CRUD、外部 Friend、跨实例 federation、
  本地密码数据库或多 Owner Agent。
