import { useCallback, useEffect, useState } from "react";
import { Link, Navigate } from "react-router-dom";
import { listTeams } from "../api/queries";
import type { HumanMe, TeamSummary } from "../api/types";
import { can } from "../lib/capabilities";
import { isAbortError } from "../lib/usePolling";
import { RoleBadge } from "../components/Badge";
import { Button } from "../components/Button";
import { Kicker } from "../components/Kicker";
import { RegionError } from "../components/RegionError";

type TeamsRegion =
  | { status: "loading" }
  | { status: "error"; error: unknown }
  | { status: "ready"; teams: TeamSummary[] };

/**
 * Team settings, General panel (design/m3-teams 03), Modernist repaint
 * (phase 6 task 4). Unlike the previous cut, this page owns its GET
 * /v1/teams call instead of trusting the `me` prop's cached `teams` array
 * verbatim — that is what gives it a real loading/error lifecycle, which is
 * what the page's retryable-error requirement needs (spec §4: "Team: whole
 * page, retryable error"). Same endpoint AuthContext already calls during
 * the on-prem/saas profile probe; no new backend surface.
 *
 * The design mockup's info bar shows four cells (address / deployment form
 * / member count / created date). Only "deployment form" survives contact
 * with the data this page can actually reach without a new request:
 * TeamSummary carries no address and no created_at (confirmed server-side —
 * even the full Team record has no address field), and member count would
 * need the admin-only members list, which non-admins can't fetch and which
 * this task is not adding as a new request. The two remaining cells (slug,
 * your role) are real fields already on TeamSummary, promoted from the old
 * General table into this bar instead of being invented.
 */
export function TeamSettingsPage({ me }: { me: HumanMe }) {
  const [region, setRegion] = useState<TeamsRegion>({ status: "loading" });
  const [epoch, setEpoch] = useState(0);

  const retry = useCallback(() => setEpoch((value) => value + 1), []);

  useEffect(() => {
    const controller = new AbortController();
    setRegion({ status: "loading" });
    listTeams()
      .then((teams) => {
        if (controller.signal.aborted) return;
        setRegion({ status: "ready", teams });
      })
      .catch((error: unknown) => {
        if (isAbortError(error) || controller.signal.aborted) return;
        setRegion({ status: "error", error });
      });
    return () => controller.abort();
  }, [epoch]);

  if (region.status === "loading") {
    return (
      <div className="page-head">
        <div>
          <Kicker>Settings · team</Kicker>
          <p className="muted flush">Loading team…</p>
        </div>
      </div>
    );
  }

  if (region.status === "error") {
    return (
      <>
        <div className="page-head">
          <div>
            <Kicker>Settings · team</Kicker>
            <h1>Team settings</h1>
          </div>
        </div>
        <RegionError error={region.error} onRetry={retry} />
      </>
    );
  }

  const team = region.teams.find((candidate) => candidate.team_id === me.current_team_id) ?? region.teams[0];
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
