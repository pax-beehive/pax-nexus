/**
 * 501 from GET /v1/me means Human Identity is not configured on this
 * installation (only the legacy TEAM_MEMORY_ADMIN_API_KEY is set). This is an
 * operator problem, not a user permission error (doc sections 1 and 9).
 */
export function NotConfiguredPage() {
  return (
    <div className="center-page">
      <div className="center-box card">
        <h1>Human Identity Not Enabled</h1>
        <p className="muted">
          The server returned <code>501 Not Implemented</code>. This installation only has the
          legacy <code>TEAM_MEMORY_ADMIN_API_KEY</code> configured.
        </p>
        <div className="note warn">
          Operator note: configure <code>TEAM_MEMORY_BOOTSTRAP_SECRET</code>, <code>TEAM_MEMORY_OIDC_*</code>,{" "}
          <code>TEAM_MEMORY_SECRET_PEPPER</code>, and <code>TEAM_MEMORY_PORTAL_URL</code>.
          This is an installation configuration issue, not a user permission error.
        </div>
        <div className="note">
          Local plain-HTTP development also requires explicitly setting{" "}
          <code>TEAM_MEMORY_HUMAN_COOKIE_SECURE=false</code>; otherwise the browser will not send
          the Secure cookie back, causing an infinite login loop.
        </div>
      </div>
    </div>
  );
}
