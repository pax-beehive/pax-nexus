/**
 * 501 from GET /v1/me means Human Identity is not configured on this
 * installation (only the legacy TEAM_MEMORY_ADMIN_API_KEY is set). This is an
 * operator problem, not a user permission error (doc sections 1 and 9).
 */
export function NotConfiguredPage() {
  return (
    <div className="center-page">
      <div className="center-box card">
        <h1>Human Identity 未启用</h1>
        <p className="muted">
          服务端返回 <code>501 Not Implemented</code>。当前环境仅配置了旧的{" "}
          <code>TEAM_MEMORY_ADMIN_API_KEY</code>。
        </p>
        <div className="note warn">
          运维提示：需配置 <code>TEAM_MEMORY_BOOTSTRAP_SECRET</code>、<code>TEAM_MEMORY_OIDC_*</code>、
          <code>TEAM_MEMORY_SECRET_PEPPER</code> 与 <code>TEAM_MEMORY_PORTAL_URL</code>
          。这是安装配置问题，不是用户权限错误。
        </div>
        <div className="note">
          本地纯 HTTP 开发还需显式设置 <code>TEAM_MEMORY_HUMAN_COOKIE_SECURE=false</code>
          ，否则浏览器不会回传 Secure Cookie，会造成无限登录循环。
        </div>
      </div>
    </div>
  );
}
