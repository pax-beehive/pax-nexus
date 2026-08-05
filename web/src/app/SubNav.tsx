import { NavLink } from "react-router-dom";
import type { NavItem } from "./navModel";

/** 二级导航条。分区没有子页面时（Overview）整条不渲染。 */
export function SubNav({ items }: { items: NavItem[] }) {
  if (items.length === 0) return null;
  return (
    <nav className="subnav" aria-label="Section pages">
      {items.map((item) => (
        <NavLink
          key={item.to}
          to={item.to}
          end={item.to.split("/").length <= 2}
          className={({ isActive }) => (isActive ? "active" : "")}
          aria-current={undefined}
        >
          {item.label}
        </NavLink>
      ))}
    </nav>
  );
}
