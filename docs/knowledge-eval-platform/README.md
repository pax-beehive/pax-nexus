# Knowledge Eval Platform 交接

更新日期：2026-07-31

## 当前状态

V1 的 Core、Artifact Store、LLM Wiki/PageWiki/Team Note drivers、quality/QA/tester
adapters、paired comparison、deterministic acceptance bundle、Hertz Query/Task
API 和 API-driven Dashboard 已实现。

LoCoMo train `conv-26` 已完成 source-only baseline 与真实 LLM Wiki maintainer
对照。19 个 Sessions、419 个 turns 被导入为 19 个不可变 Sources；maintainer
生成 6 个 Wiki 页面。两个 arm 都运行 artifact-quality 和 Search/Get QA，
并区分 artifact、retrieval 和 reader failure。

Dashboard 不再在构建时 import 单个 `dataset-run.json`。本地 Query API 每次请求
同时扫描 prepared dataset manifest 和结果根目录，提供 Dataset family、Partition、
Group、Run、Solution、Benchmark、结果矩阵、Run 详情和 Artifact View。未运行
Group 来自 prepared input；Run 状态与 Artifact 数量由结果 bundle 覆盖。JSON
snapshot 仍保留为导出与审计格式。

Dashboard 现在包含本地 Experiment Tasks 工作台。用户选择 Dataset、Partition、
Group、baseline/maintainer、模型和问题数后，先调用 preview API；确认后任务进入
单并发持久化队列。任务状态、事件、Run IDs、Artifact IDs 和失败原因每十秒刷新。
source-only baseline 不调用付费 LLM；maintainer 必须由用户显式确认，且
`DEEPSEEK_API_KEY` 只从 API 进程环境读取。LongMemEval-V2 会显示明确的
trajectory adapter blocker，目前不会错误入队。

Dashboard 的 Dataset 区也包含本地安装入口。LoCoMo、LongMemEval-S Cleaned 和
LongMemEval-V2 Small 使用固定 upstream revision 配方，可以独立下载到 API 的
`-dataset-root`，随后执行 checksum、answer-blind、partition/reference 校验并生成
现有实验 runner 直接读取的 `prepared` 目录。安装任务持久化并显示进度、错误与取消
状态；它不实现通用 Dataset marketplace，也不允许网页提交任意服务器路径。

Dashboard 的层级是 `Dataset → Partition → Group`。LoCoMo Group 是
conversation；LongMemEval-S Group 是独立 case haystack；LongMemEval-V2
按共享 trajectory haystack 聚合为 environment，避免为多个问题重复构建相同
Artifact。Group 可进入 `/dataset`：已经运行的 Group 可浏览物化后的不可变
session sources，未运行 Group 会展示 prepared metadata 和明确的未物化状态。

## 关键边界

- `maintainer/ingest.jsonl` 是 Builder 唯一可见的 dataset 输入。
- `reader/query.jsonl` 和 `evaluator/gold.jsonl` 只由 benchmark 侧读取。
- source-only baseline 必须保留为独立 Builder arm。
- 真实 maintainer 必须经过 workspace 的 write sandbox 和 deterministic
  validator；失败时保留审计并停止，不能发布伪 Artifact。
- QA 必须区分 artifact、retrieval 和 reader failure。
- 不要把 `internal/llmwiki/demo/data/*.sqlite` 纳入本任务提交。
- Dashboard 是 `web/llmwiki-benchmark-dashboard` 下的独立 Git 工作树；不要删除
  它的 `.git` 或把它展开成主仓库普通目录。

## 验收命令

运行平台单测：

```bash
GOCACHE=/private/tmp/paxd-go-cache \
  go test ./internal/eval/knowledgeeval/... \
  ./cmd/knowledge-eval-demo \
  ./cmd/knowledge-eval-dataset
```

重建 deterministic V1 bundle：

```bash
GOCACHE=/private/tmp/paxd-go-cache \
  go run ./cmd/knowledge-eval-demo \
  -output web/llmwiki-benchmark-dashboard/public/acceptance
```

运行 LoCoMo `conv-26`：

```bash
set -a
source ./.env.eval-v2

GOCACHE=/private/tmp/paxd-go-cache \
  go run ./cmd/knowledge-eval-dataset \
  -dataset locomo \
  -partition train \
  -case conv-26 \
  -ingest .build/datasets/llmwiki/prepared/train/locomo/maintainer/ingest.jsonl \
  -queries .build/datasets/llmwiki/prepared/train/locomo/reader/query.jsonl \
  -gold .build/datasets/llmwiki/prepared/train/locomo/evaluator/gold.jsonl \
  -limit 5 \
  -output web/llmwiki-benchmark-dashboard/public/acceptance/dataset
```

Dashboard：

```bash
# Terminal 1: Query API（默认安装/读取 .build/datasets/llmwiki）
go run ./cmd/knowledge-eval-api -listen 0.0.0.0:58081

# 使用其他本地磁盘；prepared root 默认随之变为 <dataset-root>/prepared
go run ./cmd/knowledge-eval-api \
  -listen 0.0.0.0:58081 \
  -dataset-root /path/to/knowledge-eval-data

# Terminal 2: Dashboard
cd web/llmwiki-benchmark-dashboard
npm test
npm run dev
```

默认地址：

- Query API：`http://localhost:58081`
- Dashboard：使用 `npm run dev` 输出的本地地址
- 可用 `VITE_KNOWLEDGE_EVAL_API_ORIGIN` 覆盖 Dashboard 的 API origin

主要 Dataset API：

- `GET /v1/knowledge-eval/datasets`：Dataset families 与版本、分区、Group 总数。
- `GET /v1/knowledge-eval/dataset-sources`：可安装数据源、固定 revision、本地目录和
  downloaded/prepared 状态。
- `POST /v1/knowledge-eval/dataset-install-tasks`：幂等创建单 Dataset 下载与 prepare
  任务。
- `GET /v1/knowledge-eval/dataset-install-tasks`：读取安装进度、事件和错误；
  `POST .../:task_id/cancel` 可停止 queued/running 任务。
- `GET /v1/knowledge-eval/datasets/:dataset/groups`：按 partition/status
  分页列出所有 Group，包括未运行 Group。
- `GET /v1/knowledge-eval/datasets/:dataset/:partition/:group_id`：Group
  详情、关联 Runs 和 materialized source artifact。
- `POST /v1/knowledge-eval/experiment-tasks/preview`：校验可运行性并预览
  questions、runs、benchmarks 与最大 LLM calls。
- `POST /v1/knowledge-eval/experiment-tasks`：要求 `Idempotency-Key`；付费
  maintainer 还要求 body 中显式 `confirm_paid=true`。
- `GET /v1/knowledge-eval/experiment-tasks`：分页读取本地持久化任务。
- `POST /v1/knowledge-eval/experiment-tasks/:task_id/cancel`：取消 queued/running
  任务。
- `POST /v1/knowledge-eval/cohort-campaigns/preview`：展开选中的 Dataset/Partition，
  返回全部 Groups、questions、覆盖缺口和最大 LLM calls；不会启动任务。
- `POST /v1/knowledge-eval/cohort-campaigns`：要求 `Idempotency-Key`、显式
  `confirm_paid=true`，且 `llm_call_limit` 不低于 preview；Campaign 逐 Group 创建
  child experiment task，并支持持久化恢复。
- `GET /v1/knowledge-eval/cohort-campaigns`：返回精简 Campaign 汇总；
  `GET .../:campaign_id` 按需返回 Group 明细，避免列表接口传输全量 executions。
- `POST /v1/knowledge-eval/cohort-campaigns/:campaign_id/cancel`：只取消该 Campaign
  自己记录的 active child task，不影响其他独立 experiment task。

## 主要入口

- 设计：`docs/knowledge-eval-platform-design.md`
- 实施状态：`docs/knowledge-eval-platform-implementation-plan.md`
- Dataset CLI：`cmd/knowledge-eval-dataset/main.go`
- Query API：`cmd/knowledge-eval-api/main.go`
- Experiment task manager：`internal/eval/knowledgeeval/experimenttask/`
- Cohort campaign manager：`internal/eval/knowledgeeval/cohorttask/`
- Dataset install manager：`internal/eval/knowledgeeval/datasetinstall/`
- Dataset 下载/prepare：`scripts/fetch-llmwiki-session-datasets.sh`、
  `scripts/prepare_llmwiki_session_datasets.py`
- Query API IDL：`idl/knowledge_eval.thrift`
- 文件 Registry：`internal/eval/knowledgeeval/dashboard/`
- LoCoMo orchestration：`internal/eval/knowledgeeval/demo/session_dataset.go`
- LLM Wiki builders/drivers：`internal/eval/knowledgeeval/artifact/llmwiki/`
- 真实 maintainer：`internal/llmwiki/workspace/agent.go`
- Dashboard：`web/llmwiki-benchmark-dashboard/`
