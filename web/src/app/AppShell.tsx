import { useEffect, useMemo, useState, type ReactNode } from "react";
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
  //
  // Ctrl-K 在输入框里是 macOS/readline 的「删到行尾」，无条件 preventDefault
  // 会把它从每一个文本框里废掉。所以 ⌘K 始终生效，Ctrl-K 只在焦点不在可编辑
  // 元素上时才接管。
  useEffect(() => {
    const isEditable = (target: EventTarget | null): boolean => {
      if (!(target instanceof HTMLElement)) return false;
      if (target.isContentEditable) return true;
      const tag = target.tagName;
      return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT";
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key.toLowerCase() !== "k") return;
      if (!event.metaKey && !(event.ctrlKey && !isEditable(event.target))) return;
      event.preventDefault();
      setPaletteOpen((current) => !current);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  // 顶栏与命令面板都要这份分区表：算一次往下传，而不是各算各的。每次渲染
  // 重新生成数组还会让 CommandPalette 的 useMemo([sections]) 永远失效。
  const sections = useMemo(() => navSections(me), [me]);
  const active = sectionForPath(sections, location.pathname);

  return (
    <div className="app-shell">
      <TopBar
        me={me}
        sections={sections}
        activeId={active?.id}
        onOpenPalette={() => setPaletteOpen(true)}
      />
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
