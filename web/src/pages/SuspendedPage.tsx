import { Kicker } from "../components/Kicker";

/**
 * Suspended membership landing (doc section 4). All human sessions were
 * revoked server-side; even after reactivation old agent credentials stay
 * revoked and new enrollments are required.
 */
export function SuspendedPage() {
  return (
    <main className="entry-screen" aria-label="Suspended">
      <div className="entry-col">
        <div className="entry-brand" aria-hidden="true">
          PAX Nexus
        </div>
        <Kicker>Entry · Suspended</Kicker>
        <h1>账号已被暂停</h1>
        <p className="entry-lede">
          你的 Membership 处于 <code>suspended</code> 状态，所有 Human Session 都已被吊销。
        </p>
        <p className="small entry-foot">
          重新启用账号不会恢复旧的 Agent Credential——你的 Agent 需要在重新启用后重新签发
          Enrollment。如有疑问，请联系你的管理员。
        </p>
      </div>
    </main>
  );
}
