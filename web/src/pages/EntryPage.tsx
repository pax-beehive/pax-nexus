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
        <h1 style={{ textAlign: "center" }}>欢迎{me?.email ? `，${me.email}` : ""}</h1>
        <p className="muted" style={{ textAlign: "center" }}>
          你的账号还没有 Membership。选择一种方式加入：
        </p>
        <div className="card">
          <h3 style={{ marginTop: 0 }}>我有邀请链接 / token</h3>
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
              继续
            </button>
          </div>
        </div>
        <div className="card">
          <h3 style={{ marginTop: 0 }}>首次安装</h3>
          <p className="small muted">部署后第一位用户可 claim 首个 Owner。</p>
          <button className="btn" onClick={() => navigate("/bootstrap")}>
            Claim Bootstrap Owner
          </button>
        </div>
        <p className="small muted" style={{ textAlign: "center" }}>
          都没有？请联系团队的 Owner 获取邀请。
        </p>
      </div>
    </div>
  );
}
