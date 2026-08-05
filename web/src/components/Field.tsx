import type { ReactNode } from "react";

/** 表单字段包装：标签、可选提示、可选错误。控件由调用方传入并自带 id。 */
export function Field({
  label,
  htmlFor,
  hint,
  error,
  children,
}: {
  label: string;
  htmlFor: string;
  hint?: string;
  error?: string;
  children: ReactNode;
}) {
  return (
    <div className="field">
      <label htmlFor={htmlFor}>{label}</label>
      {children}
      {hint !== undefined && <p className="small muted flush">{hint}</p>}
      {error !== undefined && <p className="note bad flush">{error}</p>}
    </div>
  );
}
