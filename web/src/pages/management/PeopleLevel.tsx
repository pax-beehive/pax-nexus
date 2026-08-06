import type { AgentProfile, DeviceSummary, Member } from "../../api/types";
import { agentsOf, devicesOf } from "./accessTree";

/** 取不到的那条腿显示 —，与汇总条同一约定。 */
function count(items: unknown[] | undefined): string {
  return items === undefined ? "—" : String(items.length);
}

export function PeopleLevel({
  members,
  devices,
  agents,
  onDrill,
}: {
  members: Member[];
  devices?: DeviceSummary[];
  agents?: AgentProfile[];
  onDrill: (membershipId: string) => void;
}) {
  return (
    <div>
      {members.map((member) => (
        <button
          key={member.membership_id}
          type="button"
          className="at-row at-people"
          onClick={() => onDrill(member.membership_id)}
        >
          <span className="row">
            <span className="at-row-name">{member.display_name}</span>
            <span className="card-kicker">{member.role}</span>
          </span>
          <span className="small mono muted">{member.email}</span>
          <span className="small">{count(devices && devicesOf(devices, member.membership_id))}</span>
          <span className="small">{count(agents && agentsOf(agents, member.membership_id))}</span>
          <span className="at-row-go" aria-hidden="true">
            →
          </span>
        </button>
      ))}
    </div>
  );
}
