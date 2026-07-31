# Page Wiki 生成设置(语言 + 自定义指令)设计

日期:2026-07-30
状态:设计已定,待写实现计划

## 1. 目标

让团队在运行时配置 wiki 生成产物的两项属性,无需改环境变量或重启:

- **生成语言**:页面正文、页面标题、topic 标题统一使用的语言;
- **自定义指令**:一段自由文本的团队风格指引,注入到生成提示词。

## 2. 已裁定的决定

| 决定 | 裁定 |
| --- | --- |
| 第一批配置项 | 仅 language + custom_instructions 两项 |
| 生效语义 | 只影响之后的生成运行;旧页面不动,全量切换用已有 Rebuild |
| 权限 | 沿用 WikiStatusPage 上 auto-inject 开关的同一 capability |
| 粒度 | per-scope(与 `pagewiki_ingestion_settings` 同粒度);wiki 是团队共享产物,语言团队一致 |
| 默认值 | 两字段皆空 = 跟随证据原文,升级后行为零变化 |
| 存储位置 | 新表,不复用 ingestion settings(ingestion 控制"要不要吃",generation 控制"怎么写",边界分开) |
| 注入层 | pagewiki 领域层,不做 platform/llm 通用 directives(todoapp 等无此需求,YAGNI) |

## 3. 数据模型

新迁移 `internal/platform/postgres/migrations/024_pagewiki_generation_settings.sql`
(024 为 main 当前最大迁移号 023 之后的下一个;实现时以届时实际最大号为准):

```sql
CREATE TABLE pagewiki_generation_settings (
    scope_id            TEXT PRIMARY KEY,
    language            TEXT NOT NULL DEFAULT '',
    custom_instructions TEXT NOT NULL DEFAULT '',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

无行 = 默认(两字段空)。写入用 upsert。

## 4. 领域层

### 4.1 类型与端口

- `pagewiki` 新增值类型 `GenerationDirectives{Language, CustomInstructions string}`。
- repository 端口新增:
  - `GenerationSettings(ctx, scopeID) (GenerationDirectives, error)`(无行返回零值,不报错)
  - `SetGenerationSettings(ctx, scopeID, GenerationDirectives) error`

### 4.2 注入路径

service 在每次生成运行(inject、update、tree reindex)**开始时读取一次**设置,整个 run 使用同一份 directives——运行中途改设置不影响进行中的 run,避免同一批产物混用新旧语言。

directives 传给全部三个 LLM maintainer:

- **session planner**、**session editor**:页面正文与标题;
- **tree indexer**:topic 标题。topic 标题也是生成物,只改页面不改树会出现中文页面挂在英文 topic 下的割裂。

### 4.3 提示词组装

统一一个纯函数 `generationDirectivesPrompt(d GenerationDirectives) string`,三个 maintainer 的 system prompt 末尾追加其返回值:

- `Language` 非空 → `Write all generated prose, page titles, and topic titles in <language>.`
- `CustomInstructions` 非空 → 以明确分隔符包裹的段落,标注为 team style guidance,并声明其优先级低于结构契约(JSON 输出格式、slug 规则等不受其覆盖)。
- 两者皆空 → 返回空串,提示词与现状逐字节一致。

### 4.4 校验

PUT 侧统一校验(领域层函数,transport 复用):

- `Language`:trim 后 ≤ 64 字符;
- `CustomInstructions`:trim 后 ≤ 2000 字符;
- 超限返回校验错误(HTTP 400)。

自由文本不做枚举白名单——前端下拉只是输入糖,存储层永远是文本。

## 5. HTTP API

挂在 `internal/pagewiki/transport/httpapi`:

- `GET /v1/wiki/settings` → `{"language": "...", "custom_instructions": "..."}`;未设置时返回空字段;权限与现有 wiki status 读取端点一致(能看状态页即可读)。
- `PUT /v1/wiki/settings`,body 同上;权限与 `SetAutoInject` 端点相同的 capability。

## 6. 前端

`WikiStatusPage` 新增 "Generation" 卡片(与现有 ingestion 卡片并列):

- 语言下拉:跟随原文(空)/ 简体中文 / English / 自定义(显示文本输入);
- 自定义指令 textarea(显示剩余字数,上限 2000);
- 保存按钮,成功后 toast;
- 说明文案:"仅影响之后的生成;想全量切换语言请使用 Rebuild。"

下拉选项与存储解耦:读取时若值不在预设列表则落入"自定义"并回填文本。

## 7. 错误处理

- **生成 run 读设置失败:中断该 run 并报错**,不降级为无 directives——静默丢设置会产出错语言的页面,比失败更糟(fail loud)。
- PUT 校验失败 → 400 带字段级错误信息;repository 写失败 → 500。
- GET 在表不存在行时返回默认值,不报错。

## 8. 测试

- **repository**:设置读写 round-trip、无行返回零值、upsert 覆盖(postgres 测试,沿用现有 DB 测试基建)。
- **service**:三条生成路径各断言 system prompt 含语言指令与自定义指令(scripted/fake ChatClient);两字段皆空时 prompt 与现状一致;读设置失败时 run 报错。
- **httpapi**:GET 默认空、PUT 校验(超长 400)、权限拒绝路径。
- **前端 dom 测试**:卡片渲染默认态、保存流、预设外值落入自定义。

## 9. 非目标

- 树深度(`LLMWIKI_TREE_MAX_DEPTH`)收进 DB——留环境变量,需求出现再迁;
- per-user 设置;
- 已有页面自动翻译或改设置自动 rebuild;
- platform/llm 层通用 directives 注入;
- todoapp rewriter 等其他 LLM 消费方的语言设置。
