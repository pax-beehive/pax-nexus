import type { ReactNode } from "react";

export type TagTone = "attention" | "neutral" | "outline";

/** 两色制标签：attention = 需要注意，neutral = 常态，outline = 强调但非告警。 */
export function Tag({
  tone = "neutral",
  title,
  className,
  children,
}: {
  tone?: TagTone;
  title?: string;
  /** Extra class(es) appended after the tone class, for callers that need
      one more visual tweak (e.g. text-transform) without re-implementing
      the tag chrome. */
  className?: string;
  children: ReactNode;
}) {
  return (
    <span className={className ? `tag tag-${tone} ${className}` : `tag tag-${tone}`} title={title}>
      {children}
    </span>
  );
}
