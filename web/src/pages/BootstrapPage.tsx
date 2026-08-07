import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { apiError } from "../api/client";
import { claimBootstrap } from "../api/actions";
import { useAuth } from "../auth/AuthContext";
import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { Kicker } from "../components/Kicker";
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
      toast("ok", "You're now the first owner; bootstrap is closed");
      await refresh();
      navigate("/agents", { replace: true });
    } catch (err) {
      if (apiError(err, 403)) {
        toast("bad", "403: the bootstrap key is incorrect, or this account already has a membership");
      } else if (apiError(err, undefined, "bootstrap_closed")) {
        toast("warn", "Bootstrap has already been claimed by someone else, or is closed");
        await refresh();
      } else if (apiError(err, 401)) {
        toast("warn", "Your session has expired — sign in again and retry");
        await refresh();
      } else {
        toast("bad", "Request failed; no automatic retry — check and retry manually");
      }
    } finally {
      // Clear the secret from component state immediately after the request.
      setSecret("");
      setBusy(false);
    }
  };

  return (
    <main className="entry-screen" aria-label="Bootstrap">
      <div className="entry-col">
        <div className="entry-brand" aria-hidden="true">
          PAX Nexus
        </div>
        <Kicker>Entry · Bootstrap</Kicker>
        <h1>Claim the first owner</h1>
        <p className="entry-lede">
          Enter the bootstrap key your operator gave you. The key never appears in URLs or
          logs, and is never persisted.
        </p>
        <Card kicker="Onprem · Bootstrap" title="Claim owner">
          <label htmlFor="bs-secret">Bootstrap key</label>
          <input
            id="bs-secret"
            type="password"
            placeholder="Key provided by your operator"
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            autoComplete="off"
          />
          <div className="entry-actions">
            <Button variant="primary" disabled={!secret || busy} onClick={() => void claim()}>
              {busy ? "Claiming…" : "Claim owner"}
            </Button>
            <Button variant="ghost" onClick={() => navigate("/")}>
              Back
            </Button>
          </div>
        </Card>
        <p className="small entry-foot">
          Once claimed, bootstrap closes permanently and the old static admin key stops working.
          If several browsers race to claim, only one succeeds.
        </p>
      </div>
    </main>
  );
}
