// Per-day activity chart for /governance/sessions (design phase 5 §2.2).
// Replaces the original table view with pure-CSS bars: bar height is
// data-driven sizing (event_count relative to the day's max), not a spacing
// value, hence the inline style rather than a utility class.
import type { SessionAuditActivityDay } from "../../api/types";

function dayKey(day: SessionAuditActivityDay): string {
  return `${day.user_id}:${day.agent_id}:${day.day}`;
}

export function SessionDaysChart({ days }: { days: SessionAuditActivityDay[] }) {
  if (days.length === 0) {
    return (
      <div className="card">
        <p className="muted small">No recorded activity.</p>
      </div>
    );
  }

  // maxCount is 0 only when every day's event_count is 0 — guard the divide
  // so that case renders flat 0%-tall bars instead of NaN.
  const maxCount = Math.max(...days.map((d) => d.event_count));
  const gridStyle = { gridTemplateColumns: `repeat(${days.length}, 1fr)` };

  return (
    <div className="card">
      <div className="gv-days" style={gridStyle}>
        {days.map((d) => {
          const pct = maxCount > 0 ? (d.event_count / maxCount) * 100 : 0;
          return (
            <div className="gv-day-col" key={dayKey(d)}>
              <span className="gv-day-count">{d.event_count}</span>
              <div className="gv-day-bar" style={{ height: `${pct}%` }} />
            </div>
          );
        })}
      </div>
      <div className="gv-day-labels" style={gridStyle}>
        {days.map((d) => (
          <div key={dayKey(d)}>
            <div className="gv-day-date">{d.day}</div>
            <div className="small">
              {d.session_count} sessions · {d.tool_call_count} calls
            </div>
            {d.high_risk_count > 0 && (
              <div className="gv-day-high">{d.high_risk_count} high risk</div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
