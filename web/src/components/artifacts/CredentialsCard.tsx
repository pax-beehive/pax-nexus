// Credentials metadata card: status filter tabs, paged metadata table, and
// the active-row revoke action. Renders metadata only, never API keys.

import type { CredentialMetadata } from "../../api/types";
import { deriveCredentialStatus } from "../../lib/credentials";
import { formatTime } from "../../lib/format";
import type { PagedList } from "../../lib/usePagedList";
import { Badge } from "../Badge";
import { Button } from "../Button";
import { LoadMore } from "./LoadMore";
import { Tabs } from "./Tabs";

const CREDENTIAL_STATUSES = ["all", "active", "expired", "revoked"] as const;

export function CredentialsCard({
  filter,
  onFilterChange,
  credentials,
  onRevoke,
}: {
  filter: string;
  onFilterChange: (v: string) => void;
  credentials: PagedList<CredentialMetadata>;
  onRevoke: (credentialId: string) => void;
}) {
  return (
    <div className="card">
      <h2 style={{ margin: "0 0 8px" }}>Credentials (metadata only, never contains API keys)</h2>
      <Tabs
        label="credential status"
        options={CREDENTIAL_STATUSES}
        value={filter}
        onChange={onFilterChange}
      />
      {credentials.loading ? (
        <p className="muted small">Loading…</p>
      ) : credentials.items.length === 0 ? (
        <p className="muted small">No Credentials yet.</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Label</th>
              <th>Permissions</th>
              <th>Status</th>
              <th>Last used</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {credentials.items.map((c) => {
              const derived = deriveCredentialStatus(c);
              return (
                <tr key={c.credential_id}>
                  <td>{c.label}</td>
                  <td className="small mono">{c.permissions.join(", ")}</td>
                  <td>
                    <Badge status={derived} />
                  </td>
                  <td className="small">{formatTime(c.last_used_at)}</td>
                  <td>
                    {derived === "active" && (
                      <Button variant="danger" size="sm" onClick={() => onRevoke(c.credential_id)}>
                        Revoke
                      </Button>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
      <LoadMore
        nextCursor={credentials.nextCursor}
        loadingMore={credentials.loadingMore}
        onLoadMore={() => void credentials.loadMore()}
      />
    </div>
  );
}
