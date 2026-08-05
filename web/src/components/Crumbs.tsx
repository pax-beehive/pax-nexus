import { Link } from "react-router-dom";

/** 面包屑：除最后一项外都是链接，最后一项带 aria-current。 */
export function Crumbs({ items }: { items: { label: string; to?: string }[] }) {
  return (
    <nav className="crumbs" aria-label="Breadcrumb">
      {items.map((item, index) => {
        const last = index === items.length - 1;
        return (
          <span key={item.label}>
            {index > 0 && <span className="crumb-sep"> / </span>}
            {last || item.to === undefined ? (
              <span aria-current="page">{item.label}</span>
            ) : (
              <Link to={item.to}>{item.label}</Link>
            )}
          </span>
        );
      })}
    </nav>
  );
}
