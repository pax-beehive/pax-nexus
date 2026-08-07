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
        <h1>Human Identity is not enabled</h1>
        <p className="entry-lede">
          The server returned <code>501 Not Implemented</code>. This deployment is only
          configured with the legacy <code>TEAM_MEMORY_ADMIN_API_KEY</code>.
        </p>
        <div className="note warn">
          Operator note: configure <code>TEAM_MEMORY_BOOTSTRAP_SECRET</code>,{" "}
          <code>TEAM_MEMORY_OIDC_*</code>, <code>TEAM_MEMORY_SECRET_PEPPER</code> and{" "}
          <code>TEAM_MEMORY_PORTAL_URL</code>. This is an installation configuration issue,
          not a user permission problem.
        </div>
        <div className="note">
          Local plain-HTTP development also needs{" "}
          <code>TEAM_MEMORY_HUMAN_COOKIE_SECURE=false</code> set explicitly; otherwise the
          browser won&apos;t send back the Secure cookie and sign-in loops forever.
        </div>
      </div>
    </main>
  );
}
