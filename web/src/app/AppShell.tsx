import { useState, type ReactNode } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import type { HumanMe } from "../api/types";
import { ErrorBoundary } from "../components/ErrorBoundary";
import { CommandPalette } from "../components/CommandPalette";
import { navSections, sectionForPath } from "./navModel";
import { SubNav } from "./SubNav";
import { TopBar } from "./TopBar";

/**
 * 外壳：顶栏 + 二级导航 + 内容区。路由本身在 app/routes.tsx，这里只负责框。
 *
 * 内容区的 ErrorBoundary 用 pathname + 当前 team 作 key：一个路由崩了不会带走
 * 顶栏，换路由自动恢复；切换团队时重挂载，让每个视图对新团队重新取数。
 */
export function AppShell({ me, children }: { me: HumanMe; children: ReactNode }) {
  const location = useLocation();
  const navigate = useNavigate();
  const [paletteOpen, setPaletteOpen] = useState(false);
  const sections = navSections(me);
  const active = sectionForPath(sections, location.pathname);

  return (
    <div className="app-shell">
      <TopBar me={me} onOpenPalette={() => setPaletteOpen(true)} />
      <SubNav items={active?.items ?? []} />
      <main className="page">
        <ErrorBoundary
          key={`${location.pathname}:${me.current_team_id ?? ""}`}
          region="route"
          escapeLabel="Back to Management"
          onEscape={() => navigate("/management")}
        >
          {children}
        </ErrorBoundary>
      </main>
      <CommandPalette
        me={me}
        open={paletteOpen}
        onOpenChange={setPaletteOpen}
        sections={sections}
      />
    </div>
  );
}
