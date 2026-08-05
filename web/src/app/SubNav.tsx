import { NavLink } from "react-router-dom";
import type { NavItem } from "./navModel";

/**
 * 二级导航条。分区没有子页面时（Overview）整条不渲染。
 *
 * 高亮完全靠 NavLink 默认写上的 aria-current="page"，样式见 layout.css 的
 * `.subnav a[aria-current="page"]`。不要再补 className/aria-current：没有
 * `.subnav a.active` 这条规则，而 aria-current={undefined} 是空操作
 * （react-router 用的是默认参数），加了只会让人以为高亮另有来源。
 */
export function SubNav({ items }: { items: NavItem[] }) {
  if (items.length === 0) return null;
  return (
    <nav className="subnav" aria-label="Section pages">
      {items.map((item) => (
        <NavLink key={item.to} to={item.to} end={item.to.split("/").length <= 2}>
          {item.label}
        </NavLink>
      ))}
    </nav>
  );
}
