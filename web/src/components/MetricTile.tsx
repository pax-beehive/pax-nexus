import type { ReactNode } from "react";

/** Overview 指标格：大数字 + 小单位 + 注解。 */
export function MetricTile({
  label,
  value,
  unit,
  note,
}: {
  label: string;
  value: ReactNode;
  unit?: string;
  note?: ReactNode;
}) {
  return (
    <div className="metric-tile">
      <div className="card-kicker">{label}</div>
      <div className="metric-value">
        <span className="metric-number">{value}</span>
        {unit !== undefined && <span className="metric-unit">{unit}</span>}
      </div>
      {note !== undefined && <div className="small muted">{note}</div>}
    </div>
  );
}
