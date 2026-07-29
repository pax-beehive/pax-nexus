// Shared label/value cell used across the operations views (summary cards
// and the diagnostic drawers/views that show similar stat grids).

import type { ReactNode } from "react";

export function Stat({ label, value, title }: { label: string; value: ReactNode; title?: string }) {
  return (
    <div className="stat" title={title}>
      <div className="stat-value">{value}</div>
      <div className="stat-label">{label}</div>
    </div>
  );
}
