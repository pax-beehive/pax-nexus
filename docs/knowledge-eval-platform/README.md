# Knowledge Eval Platform 交接

更新日期：2026-07-30

## 当前状态

V1 的 Core、Artifact Store、LLM Wiki/PageWiki/Team Note drivers、quality/QA/tester
adapters、paired comparison、deterministic acceptance bundle 和 Dashboard 已实现并
通过现有单测。

LoCoMo train `conv-26` 已进入平台，但当前 Dashboard 数据仍是
`source-only-baseline`：19 个 Sessions、419 个 turns 被导入为不可变 Sources，
目录随后被直接封装为 Artifact。这个 arm 是有效的负对照，不是 LLM Wiki
maintainer 的结果。

当前任务是 S16：把 `internal/llmwiki/workspace.RunAgent` 接到 Knowledge Eval
BuilderDriver，用同一份 answer-blind build input 生成真实 Wiki，再与 source-only
arm 运行相同的 artifact-quality 和 Search/Get QA benchmark。

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
  -questions 5 \
  -output web/llmwiki-benchmark-dashboard/public/acceptance/dataset
```

Dashboard：

```bash
cd web/llmwiki-benchmark-dashboard
npm test
npm run dev
```

## 主要入口

- 设计：`docs/knowledge-eval-platform-design.md`
- 实施状态：`docs/knowledge-eval-platform-implementation-plan.md`
- Dataset CLI：`cmd/knowledge-eval-dataset/main.go`
- LoCoMo orchestration：`internal/eval/knowledgeeval/demo/session_dataset.go`
- LLM Wiki builders/drivers：`internal/eval/knowledgeeval/artifact/llmwiki/`
- 真实 maintainer：`internal/llmwiki/workspace/agent.go`
- Dashboard：`web/llmwiki-benchmark-dashboard/`
