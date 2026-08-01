// Enrollments metadata card: status filter tabs, paged metadata table, and
// the pending-row revoke action. One-time secrets never appear here.

import type { EnrollmentMetadata } from "../../api/types";
import { formatTime } from "../../lib/format";
import type { PagedList } from "../../lib/usePagedList";
import { Badge } from "../Badge";
import { Button } from "../Button";
import { Countdown } from "../Countdown";
import { LoadMore } from "./LoadMore";
import { Tabs } from "./Tabs";

const ENROLLMENT_STATUSES = ["all", "pending", "consumed", "revoked", "expired"] as const;

export function EnrollmentsCard({
  agentStatus,
  canIssue,
  onIssue,
  filter,
  onFilterChange,
  enrollments,
  onRevoke,
}: {
  agentStatus: string;
  canIssue: boolean;
  onIssue: () => void;
  filter: string;
  onFilterChange: (v: string) => void;
  enrollments: PagedList<EnrollmentMetadata>;
  onRevoke: (enrollmentId: string) => void;
}) {
  return (
    <div className="card">
      <div className="row between">
        <h2 className="flush">Enrollments</h2>
        {canIssue && (
          <Button variant="primary" size="sm" onClick={onIssue}>
            + Issue one-time Enrollment
          </Button>
        )}
      </div>
      {agentStatus !== "active" && (
        <div className="note warn small">
          Agent is not active: suspend / retire immediately revokes all Credentials and pending Enrollments, and returning to active does not restore old keys.
        </div>
      )}
      <Tabs
        label="enrollment status"
        options={ENROLLMENT_STATUSES}
        value={filter}
        onChange={onFilterChange}
      />
      {enrollments.loading ? (
        <p className="muted small">Loading…</p>
      ) : enrollments.items.length === 0 ? (
        <p className="muted small">No Enrollments yet.</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Label</th>
              <th>Permissions</th>
              <th>Status</th>
              <th>Expires/Created</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {enrollments.items.map((e) => (
              <tr key={e.enrollment_id}>
                <td>{e.credential_label}</td>
                <td className="small mono">{e.permissions.join(", ")}</td>
                <td>
                  <Badge status={e.status} />
                </td>
                <td className="small">
                  {e.status === "pending" ? <Countdown to={e.expires_at} /> : formatTime(e.created_at)}
                </td>
                <td>
                  {e.status === "pending" && (
                    <Button variant="danger" size="sm" onClick={() => onRevoke(e.enrollment_id)}>
                      Revoke
                    </Button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <LoadMore
        nextCursor={enrollments.nextCursor}
        loadingMore={enrollments.loadingMore}
        onLoadMore={() => void enrollments.loadMore()}
      />
    </div>
  );
}
