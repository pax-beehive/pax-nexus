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
      toast("ok", "你现在是第一个 Owner，Bootstrap 已经关闭");
      await refresh();
      navigate("/agents", { replace: true });
    } catch (err) {
      if (apiError(err, 403)) {
        toast("bad", "403：Bootstrap 密钥不正确，或者这个账号已经有 Membership");
      } else if (apiError(err, undefined, "bootstrap_closed")) {
        toast("warn", "Bootstrap 已经被别人认领，或者已经关闭");
        await refresh();
      } else if (apiError(err, 401)) {
        toast("warn", "你的会话已过期，请重新登录后再试");
        await refresh();
      } else {
        toast("bad", "请求失败，不会自动重试——请确认后手动重试");
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
        <h1>认领第一个 Owner</h1>
        <p className="entry-lede">
          输入你的操作员提供的 Bootstrap 密钥。这个密钥不会出现在 URL、日志里，也不会被持久化保存。
        </p>
        <Card kicker="Onprem · Bootstrap" title="认领 Owner">
          <label htmlFor="bs-secret">Bootstrap 密钥</label>
          <input
            id="bs-secret"
            type="password"
            placeholder="操作员提供的密钥"
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            autoComplete="off"
          />
          <div className="entry-actions">
            <Button variant="primary" disabled={!secret || busy} onClick={() => void claim()}>
              {busy ? "认领中…" : "认领 Owner"}
            </Button>
            <Button variant="ghost" onClick={() => navigate("/")}>
              返回
            </Button>
          </div>
        </Card>
        <p className="small entry-foot">
          一旦认领成功，Bootstrap 将永久关闭，旧的静态 Admin key 也会随之失效。如果多个浏览器同时抢先认领，只有一个会成功。
        </p>
      </div>
    </main>
  );
}
