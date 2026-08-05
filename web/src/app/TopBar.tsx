import { NavLink, useLocation } from "react-router-dom";
import type { HumanMe } from "../api/types";
import { hasTeams } from "../lib/teams";
import { TeamSwitcher } from "../components/TeamSwitcher";
import { Button } from "../components/Button";
import { navSections, sectionForPath } from "./navModel";
import { UserMenu } from "./UserMenu";

export function TopBar({ me, onOpenPalette }: { me: HumanMe; onOpenPalette: () => void }) {
  const location = useLocation();
  const sections = navSections(me);
  const active = sectionForPath(sections, location.pathname);

  return (
    <div className="topbar">
      <div className="topbar-brand">PAX Nexus</div>
      {hasTeams(me) && (
        <div className="topbar-cell">
          <TeamSwitcher me={me} collapsed={false} />
        </div>
      )}
      <nav className="topbar-nav" aria-label="Sections">
        {sections.map((section) => (
          <NavLink
            key={section.id}
            to={section.to}
            aria-current={section.id === active?.id ? "page" : undefined}
          >
            {section.label}
          </NavLink>
        ))}
      </nav>
      <Button
        className="topbar-cell topbar-search"
        variant="ghost"
        onClick={onOpenPalette}
      >
        <span>Search agents, notes, actions…</span>
        <span className="topbar-kbd">⌘K</span>
      </Button>
      <UserMenu me={me} />
    </div>
  );
}
