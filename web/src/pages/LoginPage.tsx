import { isInternalPath, peekPendingInvitation, saveReturnUrl } from "../lib/continuations";
import { useAuth } from "../auth/AuthContext";
import { Button } from "../components/Button";
import { Kicker } from "../components/Kicker";

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
    <main className="entry-screen" aria-label="Login">
      <div className="entry-col">
        <div className="entry-brand" aria-hidden="true">
          PAX Nexus
        </div>
        <Kicker>Entry · Login</Kicker>
        <h1>Sign in to continue</h1>
        <p className="entry-lede">Sign in with your organization&apos;s identity provider.</p>
        {hasInvitation && (
          <div className="note">
            Your invitation token is saved in this tab; after signing in you&apos;ll be taken
            straight back to accepting the invitation.
          </div>
        )}
        <div className="entry-actions">
          <Button variant="primary" onClick={startOidcLogin}>
            Sign in with OIDC
          </Button>
        </div>
        <p className="small entry-foot">
          You&apos;ll be redirected to your identity provider and brought back here once
          you&apos;re signed in.
        </p>
        <div className="entry-actions">
          <Button variant="ghost" size="sm" onClick={() => void refresh()}>
            Already signed in? Click to retry
          </Button>
        </div>
      </div>
    </main>
  );
}
