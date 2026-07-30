# LLM Wiki 树深度上限可配置 — 设计

日期:2026-07-30
状态:已与用户确认

## 背景与目标

LLM tree indexer 目前把主题树深度硬编码为 2 层:提示词写死 "at most two levels deep",且 `normalizeTree` 结构上只处理 root topic + 一层 child(更深的节点被 `collectNodePages` 拍平)。用户反馈 2 层太浅。目标:深度上限通过环境变量可配置,默认 5 层。

前端 `Topic` 组件递归渲染、存储为 parent_id 结构、`stableID("topic", parentID, slug)` 父链式——均无深度假设,只需改 indexer 与配置链。

## 1. 配置链

- 新环境变量 `LLMWIKI_TREE_MAX_DEPTH`:默认 5,仅 LLM organizer 模式(`openai`/`harness`)下被读取。
- 值语义:主题层数上限(root topic 为第 1 层)。合法值为正整数;设置了但解析失败或 < 1 时,`buildPageWikiMaintainers` 返回启动错误(与 `LLMWIKI_ORGANIZER_MODE` 非法值处理一致)。空/未设置 → 默认 5。
- `applicationConfig` 增加 `llmwikiTreeMaxDepth string`(原始值,解析在 `buildPageWikiMaintainers`);`LLMTreeIndexerConfig` 增加 `MaxDepth int`,0 值在 `NewLLMTreeIndexer` 内落到默认常量 `treeDefaultMaxDepth = 5`。

## 2. indexer 重构(internal/pagewiki/llm_tree_indexer.go)

- `normalizeTree` 从两层循环改为递归构建 `draftTopic` 树,深度受 `MaxDepth` 约束:第 `MaxDepth` 层的节点若还有 children,用现有 `collectNodePages` 把其整棵子树的页面拍平进该节点(现行为在 2 层时的推广)。
- 剪枝规则递归化、自底向上:任一子主题直属+子树页面数 < `treeMinTopicPages`(3)时并入父级直属页;root 级主题不足 3 页时页面回到 root_pages 预算(现有行为保持)。
- `treeMaxDirectPages`(10)密度警告在每一层照打。
- 重复 slug 合并、`claim` 去重、`topicSlug` 归一化行为不变。
- 提示词从 const 改为按深度生成(`treeIndexerPrompt(maxDepth int) string`),"at most two levels deep" 处改为 "at most N levels of topics deep",其余措辞不变。

## 3. 部署配套

`compose.yaml` 的 team-memory 服务增加全部 LLMWIKI 变量透传(带空默认,不改变现有行为):
`LLMWIKI_ORGANIZER_MODE`、`LLMWIKI_LLM_BASE_URL`、`LLMWIKI_LLM_API_KEY`、`LLMWIKI_LLM_MODEL`、`LLMWIKI_TREE_MAX_DEPTH`。
(此前这些变量完全没有透传,容器部署里 LLM organizer 实际不可启用——本次一并补上。)

## 4. 测试

- indexer 单测(现有 `llm_tree_indexer_test.go` 套件模式):深 3+ 层的 LLM 响应在 MaxDepth≥3 时逐层保留(stableID 父链、placements 正确);超过 MaxDepth 的层被拍平到上限层;<3 页子主题递归并入父级;`MaxDepth` 缺省(0)= 5 的默认行为;提示词包含配置的层数措辞。
- `main.go` 配置解析:非法 `LLMWIKI_TREE_MAX_DEPTH` 启动报错、空值走默认(在 `buildPageWikiMaintainers` 层面可测则测,否则由构造函数测试覆盖)。
- 现有 `TestBuildsTwoLevelTreeWithStableIDs` 等用例在默认深度 5 下必须继续通过(2 层响应不受影响)。

## 范围外(YAGNI)

- UI 深层缩进的视觉优化(现有 `wiki-topic-children` 递归缩进可用)。
- 按层差异化的 min-pages/max-direct-pages 规则。
- workstation/demo overlay 的专门配置(基础 compose 透传即可覆盖)。
