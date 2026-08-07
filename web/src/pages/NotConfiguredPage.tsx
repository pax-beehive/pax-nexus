import { Kicker } from "../components/Kicker";

/**
 * 501 from GET /v1/me means Human Identity is not configured on this
 * installation (only the legacy TEAM_MEMORY_ADMIN_API_KEY is set). This is an
 * operator problem, not a user permission error (doc sections 1 and 9).
 */
export function NotConfiguredPage() {
  return (
    <main className="entry-screen" aria-label="Not Configured">
      <div className="entry-col">
        <div className="entry-brand" aria-hidden="true">
          PAX Nexus
        </div>
        <Kicker>Entry · Not Configured</Kicker>
        <h1>尚未启用 Human Identity</h1>
        <p className="entry-lede">
          服务器返回了 <code>501 Not Implemented</code>。这套部署目前只配置了旧版的{" "}
          <code>TEAM_MEMORY_ADMIN_API_KEY</code>。
        </p>
        <div className="note warn">
          操作员提示：请配置 <code>TEAM_MEMORY_BOOTSTRAP_SECRET</code>、
          <code>TEAM_MEMORY_OIDC_*</code>、<code>TEAM_MEMORY_SECRET_PEPPER</code> 以及{" "}
          <code>TEAM_MEMORY_PORTAL_URL</code>。这是安装配置问题，不是用户权限问题。
        </div>
        <div className="note">
          本地纯 HTTP 开发还需要显式设置 <code>TEAM_MEMORY_HUMAN_COOKIE_SECURE=false</code>；
          否则浏览器不会带回 Secure cookie，会导致登录无限循环。
        </div>
      </div>
    </main>
  );
}
