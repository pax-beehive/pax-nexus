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
        <h1>登录以继续</h1>
        <p className="entry-lede">使用你所在组织的 OIDC 账号登录。</p>
        {hasInvitation && (
          <div className="note">
            你的邀请令牌已经保存在这个标签页里；登录后会自动回到接受邀请的流程。
          </div>
        )}
        <div className="entry-actions">
          <Button variant="primary" onClick={startOidcLogin}>
            使用 OIDC 登录 →
          </Button>
        </div>
        <p className="small entry-foot">
          顶层跳转到 <code>GET /v1/auth/login</code>（302 → OIDC Provider），不是一次 fetch 请求
        </p>
        <div className="entry-actions">
          <Button variant="ghost" size="sm" onClick={() => void refresh()}>
            已经登录过？点击重试
          </Button>
        </div>
      </div>
    </main>
  );
}
