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
        <h1>Account suspended</h1>
        <p className="entry-lede">
          Your membership is <code>suspended</code>, and all human sessions have been revoked.
        </p>
        <p className="small entry-foot">
          Reactivating the account won&apos;t restore old agent credentials — your agents need
          new enrollments issued after reactivation. Contact your admin if you have questions.
        </p>
      </div>
    </main>
  );
}
