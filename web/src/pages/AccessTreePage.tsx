import { useSearchParams } from "react-router-dom";
import type { HumanMe } from "../api/types";
import { can } from "../lib/capabilities";
import { Button } from "../components/Button";
import { Crumbs } from "../components/Crumbs";
import { MyAgentsPage } from "./MyAgentsPage";
import { AccessSummary } from "./management/AccessSummary";
import { PeopleLevel } from "./management/PeopleLevel";
import { useAccessSnapshot } from "./management/useAccessSnapshot";

/**
 * Management 的访问树。下钻位置存在 URL 里（?person=&machine=）而不是组件
 * state，所以深链可分享、刷新保位、后退键逐层往上走而不是直接离开页面。
 */
export function AccessTreePage({ me }: { me: HumanMe }) {
  const [params, setParams] = useSearchParams();
  const snapshot = useAccessSnapshot();

  // member 分叉在 Task 9 换成 MyAgentsLevel；这里先维持现状。
  if (!can(me.role, "view.members")) return <MyAgentsPage />;

  if (snapshot.status === "loading") return <p className="muted">Loading…</p>;
  if (snapshot.status === "error" || !snapshot.snapshot) {
    return (
      <div className="note bad row between" role="alert">
        <span>Could not load the team’s people.</span>
        <Button size="sm" onClick={snapshot.retry}>
          Retry
        </Button>
      </div>
    );
  }

  const { members, devices, agents } = snapshot.snapshot;
  const requestedPerson = params.get("person") ?? undefined;
  const person = members.find((member) => member.membership_id === requestedPerson);
  // 链接指向的人已不在快照里：回退到根层并说明，不静默重置。
  const stalePerson = requestedPerson !== undefined && person === undefined;

  const drill = (membershipId: string) => {
    setParams({ person: membershipId });
  };

  return (
    <>
      <div className="page-head">
        <div>
          <p className="card-kicker">MANAGEMENT · ACCESS TREE</p>
          <h1>Access flows downward</h1>
          <p className="muted flush">
            A person joins the team. That person connects a machine. The machine registers the
            agents that run on it — and every key those agents hold traces back up this chain.
          </p>
        </div>
      </div>

      <AccessSummary snapshot={snapshot.snapshot} />

      <div className="at-bar">
        <Crumbs items={[{ label: "Everyone" }]} />
        <span className="at-bar-hint">{members.length} people</span>
      </div>

      {stalePerson && (
        <div className="note warn small" role="status">
          That person is no longer on this team, so we brought you back to everyone.
        </div>
      )}

      <PeopleLevel members={members} devices={devices} agents={agents} onDrill={drill} />
    </>
  );
}
