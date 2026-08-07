import { MetricTile } from "../../components/MetricTile";
import { plural } from "../../lib/format";
import { summarizeAgents, summarizeMachines, summarizePeople } from "./accessTree";
import type { AccessSnapshot } from "./useAccessSnapshot";

/**
 * 访问树的三格汇总条。取不到的那条腿显示 —，不是 0：0 会被读成
 * 「确实一个都没有」，而实际是列表没加载出来。
 *
 * snapshot 整个缺席 = 快照本身还在取或取不到（外壳此时仍要画，见
 * AccessChrome）：三格一起退成 —，注解由调用方给（"Loading…" / 默认的
 * "Could not be loaded"），同样不能写成 0。
 */
export function AccessSummary({
  snapshot,
  unavailableNote = "Could not be loaded",
}: {
  snapshot?: AccessSnapshot;
  unavailableNote?: string;
}) {
  const people = snapshot && summarizePeople(snapshot.members);
  const machines = snapshot?.devices && summarizeMachines(snapshot.devices, snapshot.members);
  const agents = snapshot?.agents && summarizeAgents(snapshot.agents);

  return (
    <div className="at-summary">
      <MetricTile
        label="People"
        value={people ? people.total : "—"}
        note={
          people
            ? `${plural(people.owners, "owner", "owners")} · ${plural(people.admins, "admin", "admins")} · ${plural(people.members, "member", "members")}`
            : unavailableNote
        }
      />
      <MetricTile
        label="Machines"
        value={machines ? machines.total : "—"}
        note={
          machines
            ? `${machines.connected} connected · ${machines.revoked} revoked · ${plural(machines.peopleWithoutMachine, "person has", "people have")} no machine`
            : unavailableNote
        }
      />
      <MetricTile
        label="Agents"
        value={agents ? agents.total : "—"}
        note={
          agents
            ? `${agents.active} active · ${agents.suspended} suspended · ${agents.retired} retired`
            : unavailableNote
        }
      />
    </div>
  );
}
