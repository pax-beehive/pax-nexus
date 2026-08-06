import { MetricTile } from "../../components/MetricTile";
import { summarizeAgents, summarizeMachines, summarizePeople } from "./accessTree";
import type { AccessSnapshot } from "./useAccessSnapshot";

/** 单复数：计数文案里 1 台机器不该写成 "1 machines"。 */
function plural(count: number, one: string, many: string): string {
  return `${count} ${count === 1 ? one : many}`;
}

/**
 * 访问树的三格汇总条。取不到的那条腿显示 —，不是 0：0 会被读成
 * 「确实一个都没有」，而实际是列表没加载出来。
 */
export function AccessSummary({ snapshot }: { snapshot: AccessSnapshot }) {
  const people = summarizePeople(snapshot.members);
  const machines = snapshot.devices && summarizeMachines(snapshot.devices, snapshot.members);
  const agents = snapshot.agents && summarizeAgents(snapshot.agents);

  return (
    <div className="at-summary">
      <MetricTile
        label="People"
        value={people.total}
        note={`${plural(people.owners, "owner", "owners")} · ${plural(people.admins, "admin", "admins")} · ${plural(people.members, "member", "members")}`}
      />
      <MetricTile
        label="Machines"
        value={machines ? machines.total : "—"}
        note={
          machines
            ? `${machines.connected} connected · ${machines.revoked} revoked · ${plural(machines.peopleWithoutMachine, "person has", "people have")} no machine`
            : "Could not be loaded"
        }
      />
      <MetricTile
        label="Agents"
        value={agents ? agents.total : "—"}
        note={
          agents
            ? `${agents.active} active · ${agents.suspended} suspended · ${agents.retired} retired`
            : "Could not be loaded"
        }
      />
    </div>
  );
}
