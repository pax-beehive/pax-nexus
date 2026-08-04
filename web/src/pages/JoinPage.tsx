import { useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { apiError } from "../api/client";
import { acceptInvitation, beginAction } from "../api/actions";
import { useAuth } from "../auth/AuthContext";
import { Button } from "../components/Button";
import { useToast } from "../components/Toasts";
import {
  clearPendingInvitation,
  peekPendingInvitation,
  savePendingInvitation,
} from "../lib/continuations";
import { noticeForError } from "../lib/statusMessage";

/**
 * Invitation acceptance (doc section 5.3).
 *
 * Token hygiene: the token arrives in the URL fragment (#invite=...), is
 * moved to tab-scoped sessionStorage and the address bar is erased
 * immediately — before first render — so it never reaches access logs,
 * Referer headers, or analytics.
 *
 * The accept call uses one Idempotency-Key per page instance, so retrying
 * after a network failure replays safely. All token failures render a single
 * uniform 410 state to avoid leaking invitation details.
 */
export function JoinPage() {
  const { state, refresh, handleUnauthorized } = useAuth();
  const toast = useToast();
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const [invalid, setInvalid] = useState(false);
  // One Idempotency-Key per accept action instance; reused across retries.
  const actionKeyRef = useRef<string | undefined>(undefined);

  const [token] = useState<string | undefined>(() => {
    const fromHash = new URLSearchParams(window.location.hash.slice(1)).get("invite");
    if (fromHash) {
      savePendingInvitation(fromHash);
      window.history.replaceState(null, "", window.location.pathname + window.location.search);
      return fromHash;
    }
    return peekPendingInvitation();
  });

  const accept = async () => {
    if (!token || busy) return;
    if (!actionKeyRef.current) actionKeyRef.current = beginAction();
    setBusy(true);
    try {
      await acceptInvitation(token, actionKeyRef.current);
      clearPendingInvitation();
      toast("ok", "Joined the team");
      await refresh();
      navigate("/agents", { replace: true });
    } catch (err) {
      if (apiError(err, 410)) {
        // Expired / revoked / used / malformed / email mismatch: one uniform
        // invalid state. If another tab already accepted, the refresh below
        // reclassifies us as active and the guards take over.
        clearPendingInvitation();
        setInvalid(true);
        await refresh();
      } else if (apiError(err, undefined, "membership_conflict")) {
        toast("warn", "This account already has a Membership; the invitation cannot override your existing role");
        await refresh();
      } else if (apiError(err, 401)) {
        handleUnauthorized();
      } else {
        const notice = noticeForError(err);
        toast(notice.kind, notice.message);
      }
    } finally {
      setBusy(false);
    }
  };

  const cancel = () => {
    clearPendingInvitation();
    navigate("/", { replace: true });
  };

  const startOidcLogin = () => {
    // The invitation continuation stays in sessionStorage; no return_url is
    // needed because the post-login redirect checks pending_invitation first.
    window.location.assign("/v1/auth/login");
  };

  let body: JSX.Element;
  if (!token) {
    body = (
      <>
        <h1>Accept invitation</h1>
        <div className="note warn">No invitation token is available. Open this page with the full invitation link sent by your administrator.</div>
        <Button variant="ghost" onClick={() => navigate("/")}>
          Back to home
        </Button>
      </>
    );
  } else if (invalid) {
    body = (
      <>
        <h1>Accept invitation</h1>
        <div className="note bad">
          This invitation is invalid (expired / revoked / already used / email mismatch). All failures show this same
          state to avoid leaking invitation details. Contact your administrator for a new invitation.
        </div>
        <Button variant="ghost" onClick={() => navigate("/")}>
          Back to home
        </Button>
      </>
    );
  } else if (state.kind === "loading") {
    body = <p className="muted">Loading…</p>;
  } else if (state.kind === "unauthenticated") {
    body = (
      <>
        <h1>Accept invitation</h1>
        <p className="muted">Sign in to accept the invitation. The invitation continuation is preserved in this tab.</p>
        <Button variant="primary" onClick={startOidcLogin}>
          Sign in and continue →
        </Button>
        <div style={{ marginTop: 10 }}>
          <Button variant="ghost" onClick={cancel}>
            Cancel and clear the local token
          </Button>
        </div>
      </>
    );
  } else if (
    state.kind === "suspended" ||
    (state.kind === "active" && (state.me.teams?.length ?? 0) === 0)
  ) {
    // On-prem single-membership rule: an active account cannot accept another
    // invitation. In saas a user may belong to multiple teams, so the active
    // state falls through to the accept form below.
    body = (
      <>
        <h1>Accept invitation</h1>
        <div className="note warn">This account already has a Membership; the invitation cannot override your existing role.</div>
        <Button variant="primary" onClick={() => navigate("/agents")}>
          Enter the Portal
        </Button>
      </>
    );
  } else {
    const email =
      state.kind === "no-membership" || state.kind === "active" ? state.me.email : undefined;
    body = (
      <>
        <h1>Accept invitation</h1>
        <p className="muted">
          Join the team as <code>{email ?? "the current account"}</code>.
        </p>
        <div className="secret-val">{token}</div>
        <Button variant="primary" disabled={busy} onClick={() => void accept()}>
          {busy ? "Accepting…" : "Accept invitation"}
        </Button>
        <div style={{ marginTop: 10 }}>
          <Button variant="ghost" onClick={cancel}>
            Cancel and clear the local token
          </Button>
        </div>
      </>
    );
  }

  return (
    <div className="center-page">
      <div className="center-box card">{body}</div>
    </div>
  );
}
