import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { ApiError } from "../api/client";
import { claimBootstrap } from "../api/actions";
import { useAuth } from "../auth/AuthContext";
import { useToast } from "../components/Toasts";

/**
 * First-install claim of the initial Owner (doc section 5.1). The bootstrap
 * secret is sent only in the X-PAX-Bootstrap-Secret header — never in URLs,
 * storage, or logs — and the input is cleared as soon as the request
 * settles. The request is never auto-retried.
 */
export function BootstrapPage() {
  const { refresh } = useAuth();
  const toast = useToast();
  const navigate = useNavigate();
  const [secret, setSecret] = useState("");
  const [busy, setBusy] = useState(false);

  const claim = async () => {
    if (!secret || busy) return;
    setBusy(true);
    try {
      await claimBootstrap(secret);
      toast("ok", "You are now the first Owner; bootstrap is closed");
      await refresh();
      navigate("/agents", { replace: true });
    } catch (err) {
      if (err instanceof ApiError && err.status === 403) {
        toast("bad", "403: incorrect bootstrap secret, or this account already has a Membership");
      } else if (err instanceof ApiError && err.code === "bootstrap_closed") {
        toast("warn", "Bootstrap has already been claimed by someone else or is closed");
        await refresh();
      } else if (err instanceof ApiError && err.status === 401) {
        toast("warn", "Your session has expired; sign in again and retry");
        await refresh();
      } else {
        toast("bad", "Request failed; it will not be retried automatically — verify and retry manually");
      }
    } finally {
      // Clear the secret from component state immediately after the request.
      setSecret("");
      setBusy(false);
    }
  };

  return (
    <div className="center-page">
      <div className="center-box card">
        <h1>Claim the first Owner</h1>
        <p className="muted">Enter the bootstrap secret provided by your operator. The secret never appears in URLs, logs, or persistent storage.</p>
        <label htmlFor="bs-secret">Bootstrap secret</label>
        <input
          id="bs-secret"
          type="password"
          placeholder="operator-provided secret"
          value={secret}
          onChange={(e) => setSecret(e.target.value)}
          autoComplete="off"
        />
        <div style={{ marginTop: 14 }} className="row">
          <button className="btn primary" disabled={!secret || busy} onClick={() => void claim()}>
            {busy ? "Claiming…" : "Claim Owner"}
          </button>
          <button className="btn ghost" onClick={() => navigate("/")}>
            Back
          </button>
        </div>
        <p className="small faint" style={{ marginTop: 12 }}>
          Once claimed, bootstrap is permanently closed and the legacy static Admin key stops working. If multiple
          browsers race to claim, only one succeeds.
        </p>
      </div>
    </div>
  );
}
