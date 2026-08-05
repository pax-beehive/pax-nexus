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
 */
export function routeKey(pathname: string): string {
  if (matchPath("/apps/wiki/:slug", pathname)) return "/apps/wiki/:slug";
  return pathname;
}
