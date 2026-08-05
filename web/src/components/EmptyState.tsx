import type { ReactNode } from "react";

/** 正向空态：不是错误、不是加载中，而是「这里目前就是空的」。 */
export function EmptyState({
  mark,
  title,
  body,
  action,
}: {
  mark?: string;
  title: string;
  body?: string;
  action?: ReactNode;
}) {
  return (
    <section className="empty-state">
      {mark !== undefined && (
        <span className="empty-mark" aria-hidden="true">
          {mark}
        </span>
      )}
      <h2>{title}</h2>
      {body !== undefined && <p className="muted">{body}</p>}
      {action}
    </section>
  );
}
