import { useEffect, useState } from "react";
import { NavLink, useLocation } from "react-router-dom";
import type { HumanMe } from "../api/types";
import { hasTeams } from "../lib/teams";
import { TeamSwitcher } from "../components/TeamSwitcher";
import { Button } from "../components/Button";
import { navSections, sectionForPath } from "./navModel";
import { TopBarMenu } from "./TopBarMenu";
import { UserMenu } from "./UserMenu";

const NARROW = "(max-width: 900px)";

/** 视口查询。matchMedia 缺失（老环境或测试未打桩）时按宽屏处理。 */
function useNarrow(): boolean {
  const [narrow, setNarrow] = useState(
    () => typeof matchMedia === "function" && matchMedia(NARROW).matches,
  );
  useEffect(() => {
    if (typeof matchMedia !== "function") return;
    const query = matchMedia(NARROW);
    const onChange = () => setNarrow(query.matches);
    query.addEventListener("change", onChange);
    return () => query.removeEventListener("change", onChange);
  }, []);
  return narrow;
}

export function TopBar({ me, onOpenPalette }: { me: HumanMe; onOpenPalette: () => void }) {
  const location = useLocation();
  const sections = navSections(me);
  const active = sectionForPath(sections, location.pathname);
  const narrow = useNarrow();

  return (
    <div className="topbar">
      <div className="topbar-brand">PAX Nexus</div>
      {hasTeams(me) && (
        <div className="topbar-cell">
          <TeamSwitcher me={me} collapsed={false} />
        </div>
      )}
      {narrow ? (
        <TopBarMenu sections={sections} activeId={active?.id} />
      ) : (
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
      )}
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
