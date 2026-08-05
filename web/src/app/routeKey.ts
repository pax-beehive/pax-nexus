import { matchPath } from "react-router-dom";

/**
 * 把 pathname 折叠成"这属于哪条路由"的稳定身份，供 AppShell 给内容区
 * ErrorBoundary 当 key：同一条路由内换参数是页内导航，不是换了一条路由，
 * 不该触发重挂载。
 *
 * 目前只有 /apps/wiki/:slug 折叠：它是唯一一个"换参数=页内选页"而不是
 * "打开另一条记录"的路由，而且重挂载的代价是重新拉一次导航树（对其它
 * 参数化路由来说重挂载很便宜）。/management/agents/:agentId、
 * /management/devices/:credentialId、/governance/memory/:noteId 故意保持
 * 按 pathname 重挂载——它们都是从列表点进来的，重挂载代价低、状态干净，
 * 而且保留了"记录 A 崩了，打开记录 B 就能恢复"的语义。
 *
 * 权衡（有意选择，不是遗漏）：折叠 wiki 的 key 意味着"换路由自动恢复"这条
 * 崩溃恢复路径在 wiki 内部选页时不再生效——同一 key 下选另一页不会重挂载，
 * 所以不会把还挂着的崩溃边界一起带走。边界自己的 Retry 按钮依旧能用；顶栏
 * 和二级导航本来就在边界外面，也不受影响。真正跨路由（例如从 wiki 切到
 * Management）仍然会换 key、正常重挂载、正常恢复。
 */
export function routeKey(pathname: string): string {
  if (matchPath("/apps/wiki/:slug", pathname)) return "/apps/wiki/:slug";
  return pathname;
}
