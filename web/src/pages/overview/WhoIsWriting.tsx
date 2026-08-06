import type { OperationsAgentStats } from "../../api/types";
import { Card } from "../../components/Card";
import { EmptyState } from "../../components/EmptyState";

export function WhoIsWriting({ agents }: { agents: OperationsAgentStats[] }) {
  const writers = [...agents].sort((a, b) => b.events_written - a.events_written).slice(0, 5);
  if (writers.length === 0) {
    return <EmptyState title="No agent activity yet" body="Writers appear as agents record sessions." />;
  }
  const peak = Math.max(1, ...writers.map((w) => w.events_written));
  return (
    <Card title="Who is writing" meta="evidence events">
      <div className="stack" style={{ gap: 12 }}>
        {writers.map((w) => (
          <div key={w.agent_id}>
            <div className="row" style={{ justifyContent: "space-between", fontSize: 12 }}>
              <span>{w.display_name || w.agent_id}</span>
              <b>{w.events_written}</b>
            </div>
            <div className="ov-writer-track"><div className="ov-writer-bar" style={{ width: `${(w.events_written / peak) * 100}%` }} /></div>
            <div style={{ fontSize: 11, opacity: 0.5 }}>{w.notes_authored} notes authored</div>
          </div>
        ))}
      </div>
    </Card>
  );
}
