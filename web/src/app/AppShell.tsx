import { useEffect, useState, type ReactNode } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import type { HumanMe } from "../api/types";
import { ErrorBoundary } from "../components/ErrorBoundary";
import { CommandPalette } from "../components/CommandPalette";
import { navSections, sectionForPath } from "./navModel";
import { routeKey } from "./routeKey";
import { SubNav } from "./SubNav";
import { TopBar } from "./TopBar";

/**
 * 外壳：顶栏 + 二级导航 + 内容区。路由本身在 app/routes.tsx，这里只负责框。
 *
 * 内容区的 ErrorBoundary 用路由身份（routeKey，见该文件）+ 当前 team 作
 * key：一个路由崩了不会带走顶栏，换路由自动恢复；切换团队时重挂载，让每个
 * 视图对新团队重新取数。key 用的是"路由"而不是原始 pathname——同一条路由
 * 内换参数（例如 /apps/wiki/:slug 换选中的页面）是页内导航，不该触发重挂
 * 载；routeKey 里对 /apps/wiki/:slug 这一条路由做了折叠，其余路由仍按
 * pathname 重挂载。
 */
export function AppShell({ me, children }: { me: HumanMe; children: ReactNode }) {
  const location = useLocation();
  const navigate = useNavigate();
  const [paletteOpen, setPaletteOpen] = useState(false);

  // ⌘K / Ctrl-K 全局开关；Escape 由面板自己处理。
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setPaletteOpen((current) => !current);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);
  const sections = navSections(me);
  const active = sectionForPath(sections, location.pathname);

  return (
    <div className="app-shell">
      <TopBar me={me} onOpenPalette={() => setPaletteOpen(true)} />
      <SubNav items={active?.items ?? []} />
      <main className="page">
        <ErrorBoundary
          key={`${routeKey(location.pathname)}:${me.current_team_id ?? ""}`}
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
