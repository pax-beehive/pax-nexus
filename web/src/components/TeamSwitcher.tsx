import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { apiError } from "../api/client";
import { beginAction, switchCurrentTeam } from "../api/actions";
import type { HumanMe } from "../api/types";
import { useAuth } from "../auth/AuthContext";
import { currentTeam, teamInitial } from "../lib/teams";
import { noticeForError } from "../lib/statusMessage";
import { RoleBadge } from "./Badge";
import { useToast } from "./Toasts";

/**
 * Team switcher (design/m3-teams 02), rendered in PortalShell directly under
 * the .side-top brand block. Visible only when the active principal carries
 * teams (saas profile). Switching POSTs /v1/me/current-team and then
 * refreshes the auth state, re-scoping every view to the new team. The
 * collapsed 54px sidebar reduces the button to the round team avatar; the
 * popover still opens from it.
 */
export function TeamSwitcher({ me, collapsed }: { me: HumanMe; collapsed: boolean }) {
  const { refresh, handleUnauthorized } = useAuth();
  const toast = useToast();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);

  const teams = me.teams ?? [];
  const current = currentTeam(me);

  // Close the popover on outside click and Escape while it is open.
  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(event.target as Node)) setOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("mousedown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open]);

  if (!current) return null;

  const switchTo = async (teamId: string, teamName: string) => {
    setOpen(false);
    if (teamId === current.team_id || busy) return;
    setBusy(true);
    try {
      // The endpoint returns the re-scoped principal; refresh() adopts it
      // into the auth state the same way the invitation-accept flow does.
      await switchCurrentTeam(teamId, beginAction());
      await refresh();
      toast("ok", `Switched to ${teamName}`);
    } catch (err) {
      if (apiError(err, 401)) {
        handleUnauthorized();
      } else {
        const notice = noticeForError(err);
        toast(notice.kind, notice.message);
      }
    } finally {
      setBusy(false);
    }
  };

  const goOnboarding = (mode: "create" | "join") => {
    setOpen(false);
    navigate(mode === "join" ? "/onboarding?mode=join" : "/onboarding");
  };

  return (
    <div className="team-switcher-wrap" ref={wrapRef}>
      <button
        type="button"
        className={collapsed ? "team-switcher collapsed" : "team-switcher"}
        aria-expanded={open}
        aria-haspopup="menu"
        aria-label={collapsed ? `Switch team (current: ${current.name})` : undefined}
        disabled={busy}
        onClick={() => setOpen((value) => !value)}
      >
        <span className={collapsed ? "team-avatar round" : "team-avatar"}>
          {teamInitial(current.name)}
        </span>
        {!collapsed && (
          <>
            <span className="team-name">{current.name}</span>
            <RoleBadge role={current.role} />
            <span className="chevron" aria-hidden="true">
              ▾
            </span>
          </>
        )}
      </button>
      {open && (
        <div className="team-pop" role="menu" aria-label="Switch team">
          {teams.map((team) => {
            const isCurrent = team.team_id === current.team_id;
            return (
              <button
                key={team.team_id}
                type="button"
                className={isCurrent ? "team-pop-row current" : "team-pop-row"}
                role="menuitem"
                onClick={() => void switchTo(team.team_id, team.name)}
              >
                <span className={isCurrent ? "team-avatar" : "team-avatar alt"}>
                  {teamInitial(team.name)}
                </span>
                <span className="row-name">{team.name}</span>
                <RoleBadge role={team.role} />
                {isCurrent && (
                  <span className="check" aria-hidden="true">
                    ✓
                  </span>
                )}
              </button>
            );
          })}
          <div className="team-pop-divider" />
          <button type="button" className="team-pop-row" role="menuitem" onClick={() => goOnboarding("create")}>
            <span className="plus">+</span>
            <span className="row-name">Create a team</span>
          </button>
          <button type="button" className="team-pop-row" role="menuitem" onClick={() => goOnboarding("join")}>
            <span className="plus">→</span>
            <span className="row-name">Join with invitation</span>
          </button>
        </div>
      )}
    </div>
  );
}
