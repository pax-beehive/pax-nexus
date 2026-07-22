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
      toast("ok", "已成为首个 Owner，bootstrap 已关闭");
      await refresh();
      navigate("/agents", { replace: true });
    } catch (err) {
      if (err instanceof ApiError && err.status === 403) {
        toast("bad", "403：bootstrap secret 错误，或当前账号已有 Membership");
      } else if (err instanceof ApiError && err.code === "bootstrap_closed") {
        toast("warn", "bootstrap 已被其他人抢先 claim 或已关闭");
        await refresh();
      } else if (err instanceof ApiError && err.status === 401) {
        toast("warn", "登录状态已失效，请重新登录后再试");
        await refresh();
      } else {
        toast("bad", "请求失败；不会自动重试，请确认后手工重试");
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
        <h1>Claim 首个 Owner</h1>
        <p className="muted">输入运维提供的 bootstrap secret。secret 不会进入 URL、日志或持久存储。</p>
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
            {busy ? "提交中…" : "Claim Owner"}
          </button>
          <button className="btn ghost" onClick={() => navigate("/")}>
            返回
          </button>
        </div>
        <p className="small faint" style={{ marginTop: 12 }}>
          bootstrap 一旦成功将永久关闭，旧 static Admin key 同时失效；多个浏览器同时 claim 时只有一个成功。
        </p>
      </div>
    </div>
  );
}
