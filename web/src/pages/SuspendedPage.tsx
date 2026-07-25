/**
 * Suspended membership landing (doc section 4). All human sessions were
 * revoked server-side; even after reactivation old agent credentials stay
 * revoked and new enrollments are required.
 */
export function SuspendedPage() {
  return (
    <div className="center-page">
      <div className="center-box card" style={{ textAlign: "center" }}>
        <h1>Account Suspended</h1>
        <p className="muted">
          Your Membership is in the <code>suspended</code> state; all Human Sessions have been revoked.
        </p>
        <p className="small muted">
          Reactivating the account does not restore old Agent Credentials — new Enrollments must
          be issued for your Agents after reactivation. Contact your administrator if you have any questions.
        </p>
      </div>
    </div>
  );
}
