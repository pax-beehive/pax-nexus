import { useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ApiError } from "../api/client";
import { acceptInvitation, beginAction } from "../api/actions";
import { useAuth } from "../auth/AuthContext";
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
      toast("ok", "已加入 Team");
      await refresh();
      navigate("/agents", { replace: true });
    } catch (err) {
      if (err instanceof ApiError && err.status === 410) {
        // Expired / revoked / used / malformed / email mismatch: one uniform
        // invalid state. If another tab already accepted, the refresh below
        // reclassifies us as active and the guards take over.
        clearPendingInvitation();
        setInvalid(true);
        await refresh();
      } else if (err instanceof ApiError && err.code === "membership_conflict") {
        toast("warn", "当前账号已有 Membership，邀请不能覆盖现有角色");
        await refresh();
      } else if (err instanceof ApiError && err.status === 401) {
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
        <h1>接受邀请</h1>
        <div className="note warn">没有可用的邀请 token。请使用管理员发送的完整邀请链接打开本页。</div>
        <button className="btn ghost" onClick={() => navigate("/")}>
          返回首页
        </button>
      </>
    );
  } else if (invalid) {
    body = (
      <>
        <h1>接受邀请</h1>
        <div className="note bad">
          邀请无效（已过期 / 已撤销 / 已使用 / 邮箱不匹配）。为避免泄漏邀请详情，所有失败统一显示此状态。请联系管理员重新邀请。
        </div>
        <button className="btn ghost" onClick={() => navigate("/")}>
          返回首页
        </button>
      </>
    );
  } else if (state.kind === "loading") {
    body = <p className="muted">加载中…</p>;
  } else if (state.kind === "unauthenticated") {
    body = (
      <>
        <h1>接受邀请</h1>
        <p className="muted">登录后即可接受邀请。邀请 continuation 已保留在当前标签页。</p>
        <button className="btn primary" onClick={startOidcLogin}>
          登录并继续 →
        </button>
        <div style={{ marginTop: 10 }}>
          <button className="btn ghost" onClick={cancel}>
            取消并清除本地 token
          </button>
        </div>
      </>
    );
  } else if (state.kind === "active" || state.kind === "suspended") {
    body = (
      <>
        <h1>接受邀请</h1>
        <div className="note warn">当前账号已有 Membership，邀请不能用于覆盖现有角色。</div>
        <button className="btn primary" onClick={() => navigate("/agents")}>
          进入 Portal
        </button>
      </>
    );
  } else {
    const email = state.kind === "no-membership" ? state.me.email : undefined;
    body = (
      <>
        <h1>接受邀请</h1>
        <p className="muted">
          以 <code>{email ?? "当前账号"}</code> 的身份加入 Team。
        </p>
        <div className="secret-val">{token}</div>
        <button className="btn primary" disabled={busy} onClick={() => void accept()}>
          {busy ? "接受中…" : "接受邀请"}
        </button>
        <div style={{ marginTop: 10 }}>
          <button className="btn ghost" onClick={cancel}>
            取消并清除本地 token
          </button>
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
