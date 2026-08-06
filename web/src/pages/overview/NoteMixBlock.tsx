import type { OverviewNoteMixEntry } from "../../api/types";
import { Card } from "../../components/Card";
import { EmptyState } from "../../components/EmptyState";

const KIND_TOKENS = [
  "var(--color-accent)",
  "var(--color-neutral-700)",
  "var(--color-accent-2-500)",
  "var(--color-neutral-400)",
] as const;

export function NoteMixBlock({ mix }: { mix: OverviewNoteMixEntry[] }) {
  if (mix.length === 0) {
    return <EmptyState title="No live notes yet" body="The mix fills in as agents write team notes." />;
  }
  const top = mix.slice(0, KIND_TOKENS.length - 1);
  const rest = mix.slice(KIND_TOKENS.length - 1);
  const entries = rest.length > 0
    ? [...top, { kind: "other", count: rest.reduce((n, e) => n + e.count, 0), pct: rest.reduce((n, e) => n + e.pct, 0) }]
    : mix;
  let acc = 0;
  const stops = entries.map((entry, i) => {
    const from = acc;
    acc += entry.pct;
    return `${KIND_TOKENS[i]} ${from}% ${acc}%`;
  });
  const total = mix.reduce((n, e) => n + e.count, 0);
  return (
    <Card title="What the team remembers" meta={`${total} live facts`}>
      <div className="row" style={{ alignItems: "center", gap: 22 }}>
        <div className="ov-donut" style={{ background: `conic-gradient(${stops.join(", ")})` }} role="img" aria-label="Live note mix by kind" />
        <div className="stack" style={{ gap: 9, flex: 1 }}>
          {entries.map((entry, i) => (
            <div key={entry.kind} className="row" style={{ gap: 8, fontSize: 12 }}>
              <span className="ov-chart-swatch" style={{ background: KIND_TOKENS[i] }} />
              <span style={{ flex: 1 }}>{entry.kind}</span>
              <b>{entry.count}</b>
              <span style={{ opacity: 0.5 }}>{Math.round(entry.pct)}%</span>
            </div>
          ))}
        </div>
      </div>
    </Card>
  );
}
