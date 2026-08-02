# GroupMemBench → Per-User Agent 工作 Session 数据集(Finance 域)设计

日期:2026-08-01
状态:待评审
范围:Finance 域先行;管线设计对四域通用,其余三域为后续复跑

## 1. 背景与目标

GroupMemBench(arXiv:2605.14498,UCSB/Microsoft)提供四域合成的多人群聊语料
(Technology / Finance / Healthcare / Manufacturing),每域含 channel 化的
posts/replies、per-user persona、决策反转元数据,以及六类绑定提问者的评测题。
数据源:`github.com/UCSB-NLP-Chang/GroupMemBench`(数据镜像在 HF
`kimperyang/GroupMemBench`)。

本设计把 Finance 域按 user 重组,为每个 user 模拟其个人 agent 的"工作 session"
(agent 阅读该 user 可见的群消息并产出结构化动作轨迹),形成:

1. **主要用途**:灌入 team-memory / llm-wiki 的 ingest 通道,用原题集评测群体
   记忆效果;
2. **副产品**:富格式数据集上传 Hugging Face。

仓库现状:`internal/eval/groupmembench/files.go` 已能把原始群聊消息直接转成
`[]session.SessionBatch`(`type: "message"`)。本设计是其升级形态——per-user
agent 轨迹 session,复用同一 ingest 契约。

## 2. 数据源要点(实测)

- Finance 域:6 channel、12 user、~6.7k posts + ~19.7k replies,时间跨度仅
  ~19 天,消息间隔中位数 129s——**按空闲间隔切 session 不可行**;
- channel 内消息**未按时间排序**,reply 因果需校验;
- 发布的问题集(214 题 = 185 非拒答 + 29 拒答,六类)每题只有
  `{id, question, answer, asking_user_id}`,**无 gold 证据指针**;
- `is_decision_point` 覆盖 ~73% 消息,只有决策反转子集可作强触发信号;
- 用户活跃度极度倾斜(单 channel 内 10~1273 条不等),as-is 保留。

## 3. 总体数据流

```
Finance 原始 JSON
  → 排序/校验(时间排序;reply 不得早于父消息,违例记日志并按父消息时间夹逼)
  → anchor 恢复(214 题 answer ↔ 证据消息,BM25 反查 + 人工核对尾部)
  → 可见性解析(user → 其发过言的 channel 全集)
  → session 切窗(user × 自然日,observation 上限 60,超量按时间拆段)
  → 规则骨架(确定性生成动作列表,证据消息强制入轨)
  → deepseek-v4-flash 填内容(每 session 一次调用,含重试/兜底)
  → 输出:HF 富格式 JSONL + SessionBatch 转换器 + 问题集增强版
```

## 4. 可见性与切窗

- **可见性**:agent 可见 = 该 user 发过 ≥1 条消息的 channel 里的全部消息。
  潜水不可观测,发言是唯一可靠的成员信号。
- **切窗**:`user × 自然日` 为基本单位;observation >60 时按时间顺序拆段
  (`.../s1`、`.../s2`)。估计全域 3000-4000 个 session,其中有动作、需要
  LLM 调用的约 3000 个。
- 规则层无跨窗状态,任意单窗可独立重跑。

## 5. 动作骨架规则(确定性)

每 session 动作上限 8 个,优先级从高到低:

| 优先级 | 触发 | 动作 |
|---|---|---|
| 1 | anchor 证据消息(见 §7) | `memory_write`(强制) |
| 2 | 决策反转/修订消息(`decision_change_metadata` 非空) | `memory_write` |
| 3 | @提及该 user 或 reply 该 user 的消息 | `draft_reply` |
| 4 | 含截止日期/日程的消息(正则:日期、"by/deadline/due") | `todo` |
| 5 | 剩余配额 ~15% | LLM 自由补充(标 `freeform: true`) |

同一消息命中多规则时取最高优先级一个动作;`is_noise: true` 的消息不触发
1-4(但保留在 observations 里,它们本来就是干扰项)。

## 6. LLM 内容填充与韧性

- 模型:**deepseek-v4-flash**(官方价 $0.14/M 输入 miss、$0.0028/M 输入
  hit、$0.28/M 输出);
- 每 session 一次调用:system prompt 注入该 user 的
  role/tone/style/expertise(12 种变体,吃 DeepSeek 前缀缓存),user prompt
  为骨架动作列表 + 涉及消息原文,输出 JSON;
- **并发**:24-32 个 worker(信号量限流),尊重渠道 rate limit;
- **重试逻辑**(用户明确要求做好):
  - 传输/服务错误(timeout、5xx、429、连接重置):指数退避 + 全抖动
    (base 1s,cap 60s),最多 5 次;429 若带 `Retry-After` 优先遵循;
  - 空响应/截断/JSON 解析失败(沿用 PR #57 的事件经验):原 prompt 附解析
    错误重问 1 次;仍失败则**模板兜底**(动作内容直接引用源消息关键句),
    session 标 `fallback: true`,管线永不因单窗卡死;
  - **断点续跑**:每次成功响应按 `(session_id, prompt_hash)` 落盘缓存,
    重跑只补缺口;
  - **熔断**:滑动窗口内失败率 >20% 时暂停 5 分钟再半开恢复,防止渠道故障
    时烧完重试预算;
  - 结束时输出重试/兜底统计报告,`fallback` session 数超过 2% 视为红灯。

## 7. Anchor 恢复

- 用 BM25 以 `answer + question` 为查询在全域消息上反查证据消息,取 top-5
  候选,再用 deepseek-v4-flash 逐题判定(214 次短调用,~$0.06;拒答题
  预期无证据,判定为"确无匹配"即通过);
- 机器判定不确定的尾部(预计 10-30 题)人工核对;
- 产出 `evidence_msg_ids`(可多条,多跳题)写回问题集,并进一步映射
  `evidence_session_ids`——这补上了原数据集缺失的 gold 指针,随 HF 一起发布。

## 8. 输出格式

### 8.1 HF 富格式 `sessions/finance.jsonl`

```json
{
  "session_id": "User_1/2025-07-19/s1",
  "agent": {"user_id": "User_1", "role": "Business Analyst",
             "tone": "anxious", "style": "anecdotal", "expertise": "expert"},
  "window": {"start": "2025-07-19T00:00:00", "end": "2025-07-19T23:59:59"},
  "observations": [
    {"channel": "AML (Anti-Money Laundering) Project", "msg_node": "Msg_447",
     "author": "User_1", "timestamp": "2025-07-19T00:14:03"}
  ],
  "trajectory": [
    {"type": "memory_write", "source_msgs": ["Msg_447"], "freeform": false,
     "fallback": false,
     "content": "User_7 owns the reg assessment, due 2025-07-16 ..."}
  ]
}
```

observations 只存引用(channel + msg_node + 少量元数据),消息原文单独放
`messages/finance.jsonl`,避免富格式里全量复制 19 万次。

### 8.2 问题集增强版 `questions/finance/*.jsonl`

原四字段 + `evidence_msg_ids` + `evidence_session_ids`。

### 8.3 SessionBatch 转换器

富格式 → `[]session.SessionBatch`(`internal/session/contracts.go` 契约):
- actor = `{user_id: "User_1", agent_id: "groupmembench-agent",
  session_id: "User_1/2025-07-19/s1"}`;
- observation → `type: "observation"` 事件(content 为消息文本行);
- 轨迹动作 → `type: "memory_write" / "todo" / "draft_reply"` 事件;
- `occurred_at` 取窗内时间,`sequence` 按序递增;
- 输出可直接被 `cmd/eval-v2-memory -session-batches-file` 消费,或 POST
  `/v1/session-batches`。

### 8.4 补充 split:`opencode-replay`(小规模真 agent 轨迹)

- 挑 1-2 个 user × 若干天(50-100 个 session),复用 `evals/opencode/` 的
  docker 基建:每窗把该 user 可见消息写成文件,给 opencode 一个固定任务
  prompt("阅读并整理要点/待办/需回复项"),真跑 agent loop;
- 产出经 paxm 捕获路径进 ingest(capture → `/v1/session-batches`),同时
  作为生产链路的端到端冒烟;
- **不参与 214 题主评测**(非确定性、无证据覆盖保证),在 HF 上作为独立
  split 发布,标注"真实 agent 轨迹风味样本";
- 成本增量:每 session 约 10-30 次调用,总量 ~1-3k 次,仍在 $5 量级。

## 9. 验证

- 规则确定性 + LLM 缓存 → 管线可复现;
- **覆盖率硬检查**(拒答题除外,其 evidence 预期为空):每题的
  `evidence_msg_ids` 必须出现在提问者至少一个
  session 的 observations 中,且有对应 memory_write;违例题输出例外清单
  (可能暴露"提问者不可见证据"的原数据问题,本身是有价值的发现);
- 抽样 30 个 session 人工质检轨迹语言质量;
- 单元测试:排序/校验、切窗、骨架规则、转换器、重试状态机(mock 渠道)。

## 10. 成本与速度预算

| 阶段 | 量 | 成本 | 耗时 |
|---|---|---|---|
| 非 LLM 处理 | 3 万消息 | — | 分钟级 |
| LLM 填内容 | ~3000 调用,~9M in / ~1.8M out | ~$2(上限 $5) | 并发 24-32 下 20-40 min |
| anchor 判定 | 214 短调用 | ~$0.06 | 分钟级 |
| 人工 | anchor 尾部核对 + 30 session 抽检 | — | 一次性 2-3 h |
| 缓存重跑 | — | $0 | 分钟级 |

## 11. 实现放置

- `cmd/groupmembench-sessions/`(Go),复用 `internal/platform/llm` 渠道与
  `internal/eval/groupmembench` 已有加载代码;
- HF 上传用 huggingface-cli,dataset card 注明来源、引用原论文、标明合成
  数据与本管线的加工方式。

## 12. 非目标(本期不做)

- 评测执行端(把 214 题跑进 team-memory 打分)——交付到"数据集 + 转换器
  可被现有 eval 工具消费"为止;
- 其余三域的复跑(管线通用,数据就绪后重复执行即可);
- 带真实 tool-call 结构的 trajectory adapter(ingest 契约为纯文本事件,
  `sessiondataset/dataset.go:88` 显式拒绝 trajectory 数据集,本期不碰);
- 时间轴拉伸重映射(会破坏 temporal 题的日期答案,明确否决)。
