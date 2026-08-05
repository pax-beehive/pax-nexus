// 重定向前 IA 的全部路由。表是数据，由 tests/legacy-routes.test.ts 逐条遍历，
// 新增或改动路由时必须同步这里，否则旧书签会 404。

export interface LegacyRoute {
  /** react-router 形式的旧路径，:param 会被原样搬到新路径的同名占位符上。 */
  from: string;
  to: string;
}

export const LEGACY_ROUTES: LegacyRoute[] = [
  { from: "/agents", to: "/management" },
  { from: "/agents/:agentId", to: "/management/agents/:agentId" },
  { from: "/admin/members", to: "/management/members" },
  { from: "/admin/invitations", to: "/management/invitations" },
  { from: "/admin/agents", to: "/management/agents" },
  { from: "/admin/agents/:agentId", to: "/management/agents/:agentId" },
  { from: "/admin/devices", to: "/management/devices" },
  { from: "/admin/devices/:credentialId", to: "/management/devices/:credentialId" },
  { from: "/admin/audit", to: "/governance/audit" },
  { from: "/admin/session-audit", to: "/governance/sessions" },
  { from: "/admin/operations", to: "/governance/pipeline" },
  { from: "/admin/pulse", to: "/overview" },
  { from: "/admin/explorer", to: "/governance/memory" },
  { from: "/admin/explorer/notes/:noteId", to: "/governance/memory/:noteId" },
  { from: "/apps", to: "/apps/wiki" },
  { from: "/wiki/browse", to: "/apps/wiki" },
  { from: "/wiki", to: "/settings/memory" },
  { from: "/todo", to: "/apps/todos" },
  { from: "/team", to: "/settings/team" },
];

/** 把 "/agents/:agentId" 这类模式编译成一个匹配器。 */
function matchPattern(pattern: string, pathname: string): Record<string, string> | undefined {
  const patternParts = pattern.split("/");
  const pathParts = pathname.split("/");
  if (patternParts.length !== pathParts.length) return undefined;
  const params: Record<string, string> = {};
  for (let i = 0; i < patternParts.length; i += 1) {
    const expected = patternParts[i];
    if (expected.startsWith(":")) {
      if (pathParts[i] === "") return undefined;
      params[expected.slice(1)] = decodeURIComponent(pathParts[i]);
      continue;
    }
    if (expected !== pathParts[i]) return undefined;
  }
  return params;
}

function fill(template: string, params: Record<string, string>): string {
  return template
    .split("/")
    .map((part) => (part.startsWith(":") ? encodeURIComponent(params[part.slice(1)]) : part))
    .join("/");
}

/**
 * 旧 wiki 深链把页面 slug 放在 `?page=`，新阅读器把它放进 path。
 * `?revision=` 仍是 query，原样保留。`/wiki?page=…` 是更早的形态，
 * 也要落到阅读器而不是设置页。
 */
function resolveWikiDeepLink(search: string): string | undefined {
  const slug = new URLSearchParams(search).get("page");
  if (!slug) return undefined;
  const revision = new URLSearchParams(search).get("revision");
  const base = `/apps/wiki/${encodeURIComponent(slug)}`;
  return revision ? `${base}?revision=${encodeURIComponent(revision)}` : base;
}

/** 旧路径 → 新路径；不是旧路径则返回 undefined。 */
export function resolveLegacy(pathname: string, search: string): string | undefined {
  if (pathname === "/wiki" || pathname === "/wiki/browse") {
    const deepLink = resolveWikiDeepLink(search);
    if (deepLink) return deepLink;
  }
  for (const route of LEGACY_ROUTES) {
    const params = matchPattern(route.from, pathname);
    if (params) return fill(route.to, params);
  }
  return undefined;
}
