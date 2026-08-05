import type { ReactNode } from "react";

/** Modernist 卡片：kicker / title / meta 三个可选槽 + 主体。 */
export function Card({
  kicker,
  title,
  meta,
  className,
  children,
}: {
  kicker?: ReactNode;
  title?: ReactNode;
  meta?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={className ? `card ${className}` : "card"}>
      {kicker !== undefined && <span className="card-kicker">{kicker}</span>}
      {title !== undefined && <span className="card-title">{title}</span>}
      <div className="card-body">{children}</div>
      {meta !== undefined && <div className="card-meta">{meta}</div>}
    </div>
  );
}
