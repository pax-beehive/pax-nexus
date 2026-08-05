import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "../components/Button";
import type { NavSection } from "./navModel";

/** 窄屏下的分区菜单。宽屏由 TopBar 直接渲染横向导航，不挂载本组件。 */
export function TopBarMenu({
  sections,
  activeId,
}: {
  sections: NavSection[];
  activeId?: string;
}) {
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    const onPointerDown = (event: MouseEvent) => {
      if (!wrapRef.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("keydown", onKeyDown);
    document.addEventListener("mousedown", onPointerDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      document.removeEventListener("mousedown", onPointerDown);
    };
  }, [open]);

  return (
    <div className="topbar-cell" ref={wrapRef} style={{ position: "relative" }}>
      <Button
        variant="ghost"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label="Menu"
        onClick={() => setOpen((current) => !current)}
      >
        ☰
      </Button>
      {open && (
        <div className="menu-pop" role="menu" aria-label="Sections">
          {sections.map((section) => (
            <Link
              key={section.id}
              role="menuitem"
              to={section.to}
              aria-current={section.id === activeId ? "page" : undefined}
              onClick={() => setOpen(false)}
            >
              {section.label}
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
