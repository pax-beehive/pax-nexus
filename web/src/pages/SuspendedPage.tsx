/**
 * Suspended membership landing (doc section 4). All human sessions were
 * revoked server-side; even after reactivation old agent credentials stay
 * revoked and new enrollments are required.
 */
export function SuspendedPage() {
  return (
    <div className="center-page">
      <div className="center-box card" style={{ textAlign: "center" }}>
        <h1>账号已被暂停</h1>
        <p className="muted">
          你的 Membership 处于 <code>suspended</code> 状态，所有 Human Session 已被撤销。
        </p>
        <p className="small muted">
          恢复账号不会还原旧 Agent Credential——恢复后需为 Agent 重新签发 Enrollment。如有疑问请联系管理员。
        </p>
      </div>
    </div>
  );
}
