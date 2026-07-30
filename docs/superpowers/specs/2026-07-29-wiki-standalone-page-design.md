# Wiki 独立展示页 + 主 app 观测页 — 设计

日期:2026-07-29
状态:已与用户确认

## 背景与目标

Wiki 目前作为 `WikiPage` 挂在 `PortalShell` 内(`/wiki` 路由,带侧边导航,靠 `main main-wide` 加宽),UI 空间受限。目标:

1. Wiki 变成独立的全屏展示页面,整个视口交给 wiki 布局,为后续视觉重设计留空间。
2. 主 app 内的 `/wiki` 入口只保留观测能力:ingestion 控制与抽取进度。

本次只做结构拆分与全屏适配,不做视觉重设计。

## 形态决策(已确认)

- 同一 SPA 的全屏路由,不是独立前端应用;复用现有登录态与 API client。
- 侧边栏 `/wiki` 变为状态页,新增后端抽取进度数据。
- 先拆分 + 适配全屏,大的视觉重设计后续单独做。

## 1. 路由架构

- `/wiki/browse` — 新全屏 wiki 路由,注册在 `App.tsx` 中 PortalShell catch-all 之前,仅 `state.kind === "active"` 时可达(复用现有认证守卫)。不渲染 PortalShell。
- `/wiki`(仍在 PortalShell 侧边栏)— 变为 Wiki 状态页:ingestion 控制 + 抽取进度 + 「打开 Wiki」按钮跳转 `/wiki/browse`。
- 深链兼容:`/wiki?page=<slug>` 为现有分享链接格式;状态页检测到 `page` 查询参数时重定向到 `/wiki/browse?page=<slug>`(保留 `revision` 等其余参数),老链接不失效。

## 2. 前端拆分

现有 `web/src/pages/WikiPage.tsx`(约 522 行)一拆为二:

- `WikiStatusPage.tsx`(新,进 PortalShell):
  - 承接 ingestion 区块及其全部状态逻辑(auto-inject 开关、手动注入 session、rebuild)。
  - 新增进度展示:待抽取会话数、最近处理时间;用现有 `usePolling` 轮询。
  - 「打开 Wiki」入口(`navigate("/wiki/browse")`)。
- `WikiBrowsePage.tsx`(由 WikiPage 改名瘦身,全屏):
  - 去掉 ingestion 区块。
  - 保留三栏布局(TopicTree / 正文 / RelationList)、搜索、版本历史。
  - 自带极简 header:← 返回主 app、搜索框;不再依赖 `main main-wide`,新增全视口布局样式(如 `wiki-browse` 类)。

`web/src/components/wiki/` 下的 TopicTree / WikiMarkdown / RelationList 不动。

## 3. 后端进度 API

扩展现有 `GET /v1/wiki/ingestion` 响应(不加新端点):

```json
{
  "auto_inject": true,
  "pending_sessions": 3,
  "last_processed_at": "2026-07-29T08:00:00Z"
}
```

- `pending_sessions`:`PendingStreams()` 返回的积压流数量(有未提交事件的会话)。
- `last_processed_at`:`session_processor_cursors.updated_at` 的最大值(该 scope 下),从未处理过则为 `null`。表已有 `updated_at` 列,无需迁移。
- 实现:`PageWikiConsumerStore` 增加进度查询;`sessionconsumer.Controller.Status()` 组装进 `Status` 结构;handler 层扩展 `WikiIngestionStatusResponse`。
- 前端 `web/src/api/wiki.ts` 的 `WikiIngestionStatus` 类型同步扩展。

## 4. 错误处理

- 进度查询失败不阻塞状态页其余部分:auto-inject 控制仍可用,进度区降级为「进度不可用」提示,沿用现有 error handler 模式。
- `/wiki/browse` 上的数据加载错误处理保持 WikiPage 现有行为不变。

## 5. 测试

- Store:进度查询(pending 计数、max updated_at、空表返回 null)。
- Handler:`wiki_ingestion_endpoints_test.go` 扩展,断言新响应字段。
- 前端:沿用现有页面测试约定;覆盖 `/wiki?page=` → `/wiki/browse?page=` 重定向。

## 范围外(YAGNI)

- 全屏 wiki 的视觉/交互重设计(后续单独立项)。
- 独立部署、子应用、独立域名。
- 抽取任务级别的细粒度进度(百分比/进度条)——后端目前无任务粒度数据。
