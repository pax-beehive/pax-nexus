import { isInternalPath, peekPendingInvitation, saveReturnUrl } from "../lib/continuations";
import { useAuth } from "../auth/AuthContext";

/**
 * Login is a top-level navigation to /v1/auth/login (302 to the OIDC
 * provider), never a fetch call. Before leaving, keep the current internal
 * path as return_url; a pending invitation travels on its own channel.
 */
export function LoginPage() {
  const { refresh } = useAuth();
  const hasInvitation = peekPendingInvitation() !== undefined;

  const startOidcLogin = () => {
    const here = window.location.pathname + window.location.search;
    if (here !== "/" && here !== "/login" && !here.startsWith("/join") && isInternalPath(here)) {
      saveReturnUrl(here);
    }
    window.location.assign("/v1/auth/login");
  };

  return (
    <div className="center-page">
      <div className="center-box card" style={{ textAlign: "center" }}>
        <h1>Team Memory Portal</h1>
        <p className="muted">Sign in with your organization OIDC account</p>
        {hasInvitation && (
          <div className="note">Your invitation continuation is preserved (sessionStorage); after signing in you will return to the invitation acceptance flow.</div>
        )}
        <button
          className="btn primary"
          style={{ width: "100%", justifyContent: "center", padding: 10 }}
          onClick={startOidcLogin}
        >
          Continue with OIDC →
        </button>
        <p className="small faint" style={{ marginTop: 14 }}>
          Top-level navigation <code>GET /v1/auth/login</code> (302 → OIDC Provider), not a fetch call
        </p>
        <button className="btn ghost sm" onClick={() => void refresh()}>
          Already signed in? Click to retry
        </button>
      </div>
    </div>
  );
}
