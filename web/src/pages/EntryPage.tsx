import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { savePendingInvitation } from "../lib/continuations";
import { useAuth } from "../auth/AuthContext";

/**
 * Landing page for an authenticated user without a Membership (doc section
 * 4): paste an invitation token, claim bootstrap on first install, or ask an
 * Owner for an invitation.
 */
export function EntryPage() {
  const { state } = useAuth();
  const me = state.kind === "no-membership" ? state.me : undefined;
  const navigate = useNavigate();
  const [token, setToken] = useState("");

  const useToken = () => {
    const trimmed = token.trim();
    if (!trimmed) return;
    savePendingInvitation(trimmed);
    navigate("/join");
  };

  return (
    <div className="center-page">
      <div className="center-box">
        <h1 style={{ textAlign: "center" }}>Welcome{me?.email ? `, ${me.email}` : ""}</h1>
        <p className="muted" style={{ textAlign: "center" }}>
          Your account has no Membership yet. Choose a way to join:
        </p>
        <div className="card">
          <h3 style={{ marginTop: 0 }}>I have an invitation link / token</h3>
          <label htmlFor="entry-token">Invitation token</label>
          <input
            id="entry-token"
            type="text"
            placeholder="tm_invite_inv_01.xxxxxxxx"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            autoComplete="off"
          />
          <div style={{ marginTop: 10 }}>
            <button className="btn primary" disabled={!token.trim()} onClick={useToken}>
              Continue
            </button>
          </div>
        </div>
        <div className="card">
          <h3 style={{ marginTop: 0 }}>First-time install</h3>
          <p className="small muted">The first user after deployment can claim the initial Owner.</p>
          <button className="btn" onClick={() => navigate("/bootstrap")}>
            Claim Bootstrap Owner
          </button>
        </div>
        <p className="small muted" style={{ textAlign: "center" }}>
          Neither applies? Contact your team's Owner for an invitation.
        </p>
      </div>
    </div>
  );
}
