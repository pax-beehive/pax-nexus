import type { ReactNode } from "react";

/** 小号大写导语，用在页面标题上方。 */
export function Kicker({ children }: { children: ReactNode }) {
  return <span className="kicker">{children}</span>;
}
