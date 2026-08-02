# Wiki 主题树增量维护(mdhub 式插入+分裂)设计

日期:2026-08-01
状态:已批准
前置:stacked 于 feat/wiki-reliability-ontology-rollup(PR #63)

## 背景与目标

现状:wiki 主题树由 `internal/pagewiki/llm_tree_indexer.go` 整树重建——每次 catalog
变化后防抖触发,把全部页面清单+当前树快照喂给 LLM 让它"演进"出一棵新树,再经确定性
归一化(同层合并、全局 claim、深度拍平、小主题折叠)整棵覆盖写入。

问题:token 成本随页面数线性增长且每次全量支付;树结构在两次重建间可能大幅漂移;
LLM 输出整棵树,防御面大。

目标:改为 mdhub(`../mdhub/go-backend/classify.go`)验证过的增量模型——

1. **增量插入**:新页面发布时 LLM 从根逐层下钻,把单页归入现有树。
2. **溢出分裂**:节点直接子项超阈值时,LLM 把该节点局部切成 2–6 个语义子组。
3. **全量重建 = 增量重放**:清空树后按发布顺序逐页重新插入。
4. 前端侧边栏改为单层视图+面包屑下钻。

不变量:存储保留 `TopicTree{Topics, Placements}` JSONB 快照;导航 API
(`GET /v1/wiki/navigation`)、策展(Curator/`sameLeafGroups`)、
`stableID("topic", parentID, slug)` 主题 ID 规则均不受影响。
失败保旧树语义不变:任何 LLM 失败只 warn,树保持原状。

## 1. 增量插入(`internal/pagewiki/tree_inserter.go`,新)

入口 `insertPage(pageID)`:

- 加载当前 `TopicTree`,从根开始**迭代**下钻,最多 `MaxDepth` 层
  (沿用 `LLMWIKI_TREE_MAX_DEPTH`,默认 5,根主题算第 1 层)。
- 每层提示词输入:当前主题的直接子主题列表(slug、title、各自子树页数)+
  待插入页面的 title/summary(不取全文)。LLM 三选一:
  - **进入**某个子主题 → 下降一层继续;
  - **停留**当前层 → 落点即当前主题;
  - **新建**子主题(仅未达 MaxDepth 时提供此选项)→ 建新主题并落入。
- 落点确定后追加 `Placement{PageID, TopicID, Rank=该主题现有直挂页面数}`,
  整棵树经 `ReplaceTopicTree` 覆盖写回。
- 页面停留在根 = 不建 placement,自然落入 `Navigation.Pages` 未归类区。
- 幂等:页面已有 placement 时直接跳过。
- 防幻觉:LLM 返回的子主题名不在列表中 → 视为"停留当前层"。

与 mdhub 的刻意差异:mdhub 插入时不允许新建文件夹(只能停留,冷启动靠分裂);
本设计允许下钻到底时新建主题,避免根节点长期堆积后反复分裂。
新主题 ID 沿用 `stableID`,slug 由 LLM 给出、经现有 slug 归一化。

## 2. 溢出分裂

- 插入完成后检查落点主题(含根)的**直接子项数**
  (直挂页面数 + 直接子主题数)> `treeMaxDirectPages`(10),
  且该主题未达 MaxDepth → 入队分裂任务。
- `splitTopic(topicID)`:该主题直挂页面的 slug/title/summary 喂给 LLM,
  要求切成 2–6 个语义子组。校验(照搬 mdhub `validateSplit`):
  - 组数 2–6;组名合法、互不重复、不与现有兄弟子主题重名;
  - 每个 slug 恰好出现一次,无未知 slug;
  - **每组至少 2 页**(取代已删除的小主题折叠规则,从源头避免碎主题)。
- 校验失败:错误原因拼回提示词重试一次;再失败放弃本次分裂,树保持原状。
- 通过后单次 `ReplaceTopicTree` 写回:每组建子主题、迁移对应 placements。
  分裂只重排该节点自身子树,溢出检查不向上传播。
- 新子主题若自身仍超阈值:本次分裂任务结束时复查一次并再入队(MaxDepth 封顶,自然收敛)。

## 3. 调度与全量重建

- **队列**:改造 `service.go` 的 `StartTreeMaintenance`——脏标记
  buffered channel 换成 keyed 去重任务队列
  (key = `"insert:"+pageID` / `"split:"+topicID`),单 goroutine 串行消费,
  失败 warn 后丢弃。队列满丢弃并打日志(照搬 mdhub `keyedJobQueue` 语义)。
- **入队时机**:`InjectSession` 发布/更新页面后为每个新发布页入队 insert;
  删除 catalog 比较+`markTreeDirty` 防抖机制。
- **`FlushTreeReindex`** 语义改为"排空队列后返回",供测试与验收同步等待。
- **全量重建**:新增管理动作 `RebuildTree`(沿用现有 HTTP 管理端点风格):
  清空 TopicTree → 按发布时间升序把全部页面逐个入队 insert。

## 4. 仓储深度校验修复(前置 bug)

`internal/pagewiki/memory/repository.go:1070`(`ReplaceTopicTree`)与
`validateTopics`(行 501-507)硬编码拒绝深度 > 2 的树;`postgres` 版先委托
memory 版校验,故生产同样被拒;而 `reindexTree` 只 warn——现状是 ≥3 层的树被
**静默丢弃**(PR #37 把 MaxDepth 提到 5 时漏改)。

修复:两处校验改为使用注入的 `MaxDepth` 配置;
`repository_test.go` 的 "three levels" 拒绝用例改为"超过 MaxDepth 拒绝"。
此修复是增量插入的前置条件。

## 5. 前端下钻式侧边栏

- `web/src/components/wiki/TopicTree.tsx` 重写为 mdhub `TreeSidebar` 形态:
  - 状态仅 `topicPath: string[]`(提升至 `WikiBrowsePage`);
  - 每次只渲染当前主题层:子主题列表 + 直挂页面列表;
  - 顶部面包屑以「全部」开头回退,当前项 `aria-current="location"`;
  - 子主题按 slug 升序,页面按 Rank 升序。
- 上一页/下一页保留现有深度优先 `collectPages` 全局顺序,不受下钻视图影响。
- 独立阅读器 `internal/pagewiki/transport/httpapi/assets/reader.js`
  同步改造为同一交互。
- 移动端沿用现有 wiki 页响应式处理,不引入 mdhub 的 MobileFolderPicker。

## 6. 错误处理与测试

- LLM JSON 输出沿用 `llm.CompleteJSON` 重试;未知主题名防御见 §1。
- 测试:
  - `tree_inserter_test.go`(fake LLM):下钻路径、停留、新建主题、幂等跳过、
    未知名防御、根停留不建 placement;
  - 分裂端到端:正常分裂、校验失败重试、二次失败放弃保旧树、
    与现有兄弟重名拒绝、复查再入队;
  - `RebuildTree` 重放;
  - 仓储 MaxDepth 校验(≤MaxDepth 通过、超出拒绝);
  - 前端下钻组件测试沿用现有测试形态。

## 删除清单

- `internal/pagewiki/llm_tree_indexer.go` 及其测试
  (含 `pruneDraftTopics` 小主题折叠、`treeMinTopicPages` 常量);
- `markTreeDirty`、防抖常量(静默 5s/上限 60s)与 catalog 比较逻辑;
- 旧整树提示词。

保留常量:`treeMaxDirectPages = 10`(语义从"仅告警"变为分裂阈值)、
`LLMWIKI_TREE_MAX_DEPTH`(默认 5)。
