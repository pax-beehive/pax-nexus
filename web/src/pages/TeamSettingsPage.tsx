import { Link, Navigate } from "react-router-dom";
import type { HumanMe } from "../api/types";
import { can } from "../lib/capabilities";
import { currentTeam } from "../lib/teams";
import { RoleBadge } from "../components/Badge";
import { Button } from "../components/Button";

/**
 * Team settings, General panel (design/m3-teams 03). Read-only team info
 * plus entry points to the existing team-scoped Members / Invitations admin
 * pages. There is no rename or delete endpoint on the backend yet, so Rename
 * renders disabled with a "coming soon" hint and the danger zone is skipped
 * entirely.
 *
 * TeamSummary carries no created_at, so the info table omits the Created row
 * from the mockup.
 */
export function TeamSettingsPage({ me }: { me: HumanMe }) {
  const team = currentTeam(me);
  if (!team) return <Navigate to="/agents" replace />;
  const adminLike = can(me.role, "view.members");

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Team settings</h1>
          <p className="muted flush">
            {team.name} · <span className="mono small">{team.slug}</span>
          </p>
        </div>
      </div>

      <div className="card">
        <h2 className="flush">General</h2>
        <table>
          <tbody>
            <tr>
              <th style={{ width: 140 }}>Team name</th>
              <td>{team.name}</td>
            </tr>
            <tr>
              <th>Slug</th>
              <td className="mono small">{team.slug}</td>
            </tr>
            <tr>
              <th>Your role</th>
              <td>
                <RoleBadge role={team.role} />
              </td>
            </tr>
          </tbody>
        </table>
        <div className="row" style={{ marginTop: 12 }}>
          {/* No rename endpoint exists on the backend yet; do not fake one. */}
          <Button disabled>Rename team</Button>
          <span className="small muted">coming soon</span>
        </div>
      </div>

      <div className="card">
        <h2 className="flush">Members &amp; invitations</h2>
        {adminLike ? (
          <>
            <p className="small muted">
              Membership and invitation management live on the team-scoped admin pages:
            </p>
            <div className="row">
              <Link to="/admin/members">Members</Link>
              <Link to="/admin/invitations">Invitations</Link>
            </div>
          </>
        ) : (
          <p className="small muted flush">
            Members and invitations are managed by the team's owners and admins.
          </p>
        )}
      </div>
    </>
  );
}
