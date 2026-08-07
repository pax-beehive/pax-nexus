import { useEffect, useState } from "react";
import { NavLink } from "react-router-dom";
import type { HumanMe } from "../api/types";
import { hasTeams } from "../lib/teams";
import { TeamSwitcher } from "../components/TeamSwitcher";
import { Button } from "../components/Button";
import type { NavSection } from "./navModel";
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

/** 分区表由 AppShell 算好传进来（命令面板用的是同一份，见 AppShell）。 */
export function TopBar({
  me,
  sections,
  activeId,
  onOpenPalette,
}: {
  me: HumanMe;
  sections: NavSection[];
  activeId?: string;
  onOpenPalette: () => void;
}) {
  const narrow = useNarrow();

  return (
    <div className="topbar">
      <div className="topbar-brand">PAX Nexus</div>
      {hasTeams(me) && (
        <div className="topbar-cell">
          <TeamSwitcher me={me} />
        </div>
      )}
      {narrow ? (
        <TopBarMenu sections={sections} activeId={activeId} />
      ) : (
        <nav className="topbar-nav" aria-label="Sections">
          {sections.map((section) => (
            <NavLink
              key={section.id}
              to={section.to}
              aria-current={section.id === activeId ? "page" : undefined}
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
