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
        <p className="muted">使用组织 OIDC 账号登录</p>
        {hasInvitation && (
          <div className="note">已保留邀请 continuation（sessionStorage），登录后会回到接受邀请流程。</div>
        )}
        <button
          className="btn primary"
          style={{ width: "100%", justifyContent: "center", padding: 10 }}
          onClick={startOidcLogin}
        >
          Continue with OIDC →
        </button>
        <p className="small faint" style={{ marginTop: 14 }}>
          顶层跳转 <code>GET /v1/auth/login</code>（302 → OIDC Provider），非 fetch 调用
        </p>
        <button className="btn ghost sm" onClick={() => void refresh()}>
          已完成登录？点击重试
        </button>
      </div>
    </div>
  );
}
