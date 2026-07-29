// Channel diagnostic view: one retained capsule envelope. The raw payload
// JSON stays hidden unless the stored payload decoded cleanly.

import type { ChannelDiagnostic } from "../../api/types";
import { formatTime } from "../../lib/format";
import { Stat } from "./SummaryCards";

export function ChannelDiagnosticView({ diagnostic }: { diagnostic: ChannelDiagnostic }) {
  return (
    <>
      <div className="stat-grid">
        <Stat label="status" value={diagnostic.status} />
        <Stat label="payload" value={diagnostic.payload_status} />
        <Stat label="from" value={<code>{diagnostic.from_agent_id}</code>} />
        <Stat label="to" value={<code>{diagnostic.to_agent_id}</code>} />
        <Stat label="created" value={formatTime(diagnostic.created_at)} />
        <Stat label="accepted" value={
          diagnostic.accepted_at ? formatTime(diagnostic.accepted_at) : "—"
        } />
        <Stat label="archived" value={
          diagnostic.archived_at ? formatTime(diagnostic.archived_at) : "—"
        } />
      </div>
      {diagnostic.message && <div className="note">{diagnostic.message}</div>}
      {diagnostic.payload_status !== "decoded" ? (
        <div className="note warn">
          The stored payload schema is unavailable. Raw payload JSON is intentionally hidden.
        </div>
      ) : (
        <>
          <h3>{diagnostic.capsule.title ?? "Knowledge capsule"}</h3>
          {diagnostic.capsule.summary && <p className="muted">{diagnostic.capsule.summary}</p>}
          {diagnostic.capsule.content && (
            <p className="explorer-content">{diagnostic.capsule.content}</p>
          )}
          <div className="chips">
            {diagnostic.capsule.capsule_id && <code>capsule: {diagnostic.capsule.capsule_id}</code>}
            {diagnostic.capsule.source_session_id && (
              <code>session: {diagnostic.capsule.source_session_id}</code>
            )}
            {diagnostic.capsule.keyword && <code>keyword: {diagnostic.capsule.keyword}</code>}
            {diagnostic.capsule.source_agent && <code>source: {diagnostic.capsule.source_agent}</code>}
            {diagnostic.capsule.status && <code>status: {diagnostic.capsule.status}</code>}
            {diagnostic.capsule.capsule_id && (
              <code>truncated: {diagnostic.capsule.truncated ? "yes" : "no"}</code>
            )}
            {diagnostic.capsule.route_match_type && <code>route: {diagnostic.capsule.route_match_type}</code>}
          </div>
        </>
      )}
    </>
  );
}
