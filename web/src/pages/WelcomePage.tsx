import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useAuth, type DeploymentProfile } from "../auth/AuthContext";
import { savePendingInvitation } from "../lib/continuations";
import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { Kicker } from "../components/Kicker";

/** Accept a raw token or a full join link (`.../join#invite=<token>`). */
function parseInvitationInput(raw: string): string {
  const trimmed = raw.trim();
  const match = trimmed.match(/#invite=([^\s&]+)/);
  return match ? match[1] : trimmed;
}

export interface WelcomeScreenProps {
  profile: DeploymentProfile;
  email?: string;
  /**
   * Onprem only: whether the bootstrap-claim entry should be offered.
   *
   * There is no backend signal for this today — GET /v1/bootstrap/* does not
   * exist, only POST /v1/bootstrap/claim, and this task is frontend-only
   * (no backend changes). Production always renders with the default (every
   * onprem no-membership session sees the claim entry, matching the previous
   * always-on behaviour and the teams-onprem.dom.test.tsx regression test).
   * The prop exists so the "bootstrap already closed" branch is real, tested
   * code — reachable today only by rendering WelcomeScreen directly (see
   * welcome.dom.test.tsx) — instead of dead code that would bit-rot until a
   * real signal is wired up.
   */
  canBootstrap?: boolean;
}

/** Presentational half of the entry screen: no context reads, easy to unit test. */
export function WelcomeScreen({ profile, email, canBootstrap = true }: WelcomeScreenProps) {
  const navigate = useNavigate();
  const [token, setToken] = useState("");

  const useToken = () => {
    const parsed = parseInvitationInput(token);
    if (!parsed) return;
    savePendingInvitation(parsed);
    navigate("/join");
  };

  const showBootstrap = profile === "onprem" && canBootstrap;

  return (
    <main className="entry-screen" aria-label="Welcome">
      <div className="entry-col">
        <div className="entry-brand" aria-hidden="true">
          PAX Nexus
        </div>
        <Kicker>Entry · Welcome</Kicker>
        <h1>{showBootstrap ? "认领这套部署的第一个 Owner" : "你还不属于任何团队"}</h1>
        <p className="entry-lede">
          {showBootstrap
            ? "你是第一个登录这套部署的账号，可以认领 Owner 身份完成初始化；也可以用邀请令牌直接加入已有团队。"
            : "等待团队 Owner 给你发邀请，或者直接粘贴邀请令牌 / 链接加入。"}
        </p>

        {showBootstrap && (
          <Card kicker="Onprem · Bootstrap" title="认领初始 Owner">
            <p>部署完成后的第一个用户可以认领 Owner 身份；这个操作不可逆，且只能成功一次。</p>
            <div className="entry-actions">
              <Button variant="primary" onClick={() => navigate("/bootstrap")}>
                前往认领 Owner
              </Button>
            </div>
          </Card>
        )}

        <Card kicker="Entry · Join" title="用邀请令牌加入">
          <label htmlFor="welcome-invite-token">邀请令牌或链接</label>
          <input
            id="welcome-invite-token"
            type="text"
            placeholder="tm_invite_inv_01.xxxxxxxx"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            autoComplete="off"
          />
          <div className="entry-actions">
            <Button variant="primary" disabled={!token.trim()} onClick={useToken}>
              继续
            </Button>
          </div>
        </Card>

        {!showBootstrap && <p className="small entry-foot">没有邀请？联系你团队的 Owner。</p>}
        {email && <p className="small entry-foot">已登录为 {email}</p>}
      </div>
    </main>
  );
}

/**
 * `/welcome` route element (doc section 4): the single landing screen for an
 * authenticated user without a Membership, replacing the previous split
 * between a dedicated onprem page and a dedicated saas page. Reads the
 * deployment profile from AuthContext and hands a plain-props view down to
 * WelcomeScreen.
 */
export function WelcomePage() {
  const { state } = useAuth();
  const profile: DeploymentProfile = state.kind === "no-membership" ? state.profile : "saas";
  const email = state.kind === "no-membership" ? state.me.email : undefined;
  return <WelcomeScreen profile={profile} email={email} />;
}
