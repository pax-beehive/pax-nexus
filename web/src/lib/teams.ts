// Team helpers for the saas profile (M3). Profile differences ride runtime
// data (HumanMe.teams / current_team_id), never build flags.

import type { HumanMe, TeamSummary } from "../api/types";

/**
 * Mirror of the server-side slug derivation (lowercase letters, digits and
 * dashes; runs of anything else collapse into one dash). Used only for the
 * onboarding preview — the server derives the authoritative slug.
 */
export function deriveSlug(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

/** True when the principal carries at least one team (saas profile). */
export function hasTeams(me: HumanMe): boolean {
  return (me.teams?.length ?? 0) > 0;
}

/**
 * The team the session is currently scoped to. Falls back to the first
 * listed team when current_team_id is absent (rolling-upgrade safety).
 */
export function currentTeam(me: HumanMe): TeamSummary | undefined {
  const teams = me.teams ?? [];
  return teams.find((team) => team.team_id === me.current_team_id) ?? teams[0];
}

/** Single uppercase letter for the team avatar. */
export function teamInitial(name: string): string {
  const first = name.trim().charAt(0);
  return first ? first.toUpperCase() : "?";
}
