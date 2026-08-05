import type { ReactNode } from "react";

export type TagTone = "attention" | "neutral" | "outline";

/** 两色制标签：attention = 需要注意，neutral = 常态，outline = 强调但非告警。 */
export function Tag({
  tone = "neutral",
  title,
  children,
}: {
  tone?: TagTone;
  title?: string;
  children: ReactNode;
}) {
  return (
    <span className={`tag tag-${tone}`} title={title}>
      {children}
    </span>
  );
}
