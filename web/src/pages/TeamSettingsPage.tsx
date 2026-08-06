import { Link, Navigate } from "react-router-dom";
import type { HumanMe } from "../api/types";
import { can } from "../lib/capabilities";
import { currentTeam } from "../lib/teams";
import { RoleBadge } from "../components/Badge";
import { Button } from "../components/Button";
import { Kicker } from "../components/Kicker";

/**
 * Team settings, General panel (design/m3-teams 03), Modernist repaint
 * (phase 6 task 4). Data comes straight from `me.teams`, already resolved
 * at boot by AuthContext — this page makes no request of its own and has no
 * failure mode (spec §4 corrected during review: was listed as "whole page,
 * retryable error", actually "no data fetch"; see task-4-report.md).
 *
 * The design mockup's info bar shows four cells (address / deployment form
 * / member count / created date). Only "deployment form" survives contact
 * with the data this page can actually reach: TeamSummary carries no
 * address and no created_at (confirmed server-side — even the full Team
 * record has no address field), and member count would need the
 * admin-only members list, which non-admins can't fetch. The two remaining
 * cells (slug, your role) are real fields already on TeamSummary, promoted
 * from the old General table into this bar instead of being invented.
 */
export function TeamSettingsPage({ me }: { me: HumanMe }) {
  const team = currentTeam(me);
  if (!team) return <Navigate to="/agents" replace />;
  const adminLike = can(me.role, "view.members");

  return (
    <>
      <div className="page-head">
        <div>
          <Kicker>Settings · team</Kicker>
          <h1>{team.name}</h1>
          <p className="muted flush">
            Multi-tenant SaaS workspace — isolated from every other team on this control plane.
          </p>
        </div>
      </div>

      <div className="team-facts">
        <div className="team-fact">
          <span className="card-kicker">Slug</span>
          <span className="mono">{team.slug}</span>
        </div>
        <div className="team-fact">
          <span className="card-kicker">Your role</span>
          <RoleBadge role={team.role} />
        </div>
        <div className="team-fact">
          <span className="card-kicker">Deployment</span>
          <span>Multi-tenant SaaS</span>
        </div>
      </div>

      <div className="card">
        <h2 className="flush">General</h2>
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
