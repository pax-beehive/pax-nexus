import { useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { apiError } from "../api/client";
import { acceptInvitation, beginAction } from "../api/actions";
import { useAuth } from "../auth/AuthContext";
import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { Kicker } from "../components/Kicker";
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
      toast("ok", "已加入团队");
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
        toast("warn", "此账号已经有 Membership，邀请无法覆盖你现有的角色");
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
        <p className="entry-lede">邀请链接似乎不完整。</p>
        <div className="note warn">
          没有可用的邀请令牌。请使用管理员发给你的完整邀请链接打开这个页面。
        </div>
        <div className="entry-actions">
          <Button variant="ghost" onClick={() => navigate("/")}>
            返回首页
          </Button>
        </div>
      </>
    );
  } else if (invalid) {
    body = (
      <>
        <div className="note bad">
          这条邀请已失效（过期 / 已撤销 / 已使用 / 邮箱不匹配）。为避免泄露邀请详情，所有失败情形都显示同一个状态。如需新的邀请，请联系你的管理员。
        </div>
        <div className="entry-actions">
          <Button variant="ghost" onClick={() => navigate("/")}>
            返回首页
          </Button>
        </div>
      </>
    );
  } else if (state.kind === "loading") {
    body = <p className="entry-lede">加载中…</p>;
  } else if (state.kind === "unauthenticated") {
    body = (
      <>
        <p className="entry-lede">登录以接受邀请。邀请状态已经保存在这个标签页里。</p>
        <div className="entry-actions">
          <Button variant="primary" onClick={startOidcLogin}>
            登录并继续 →
          </Button>
          <Button variant="ghost" onClick={cancel}>
            取消并清除本地令牌
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
        <div className="note warn">此账号已经有 Membership，邀请无法覆盖你现有的角色。</div>
        <div className="entry-actions">
          <Button variant="primary" onClick={() => navigate("/agents")}>
            进入 Portal
          </Button>
        </div>
      </>
    );
  } else {
    const email =
      state.kind === "no-membership" || state.kind === "active" ? state.me.email : undefined;
    body = (
      <>
        <p className="entry-lede">
          以 <code>{email ?? "当前账号"}</code> 的身份加入团队。
        </p>
        <Card title="确认邀请令牌">
          <div className="secret-val">{token}</div>
        </Card>
        <div className="entry-actions">
          <Button variant="primary" disabled={busy} onClick={() => void accept()}>
            {busy ? "接受中…" : "接受邀请"}
          </Button>
          <Button variant="ghost" onClick={cancel}>
            取消并清除本地令牌
          </Button>
        </div>
      </>
    );
  }

  return (
    <main className="entry-screen" aria-label="Join">
      <div className="entry-col">
        <div className="entry-brand" aria-hidden="true">
          PAX Nexus
        </div>
        <Kicker>Entry · Join</Kicker>
        <h1>接受邀请</h1>
        {body}
      </div>
    </main>
  );
}
