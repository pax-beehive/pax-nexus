import { useNavigate, useSearchParams } from "react-router-dom";
import type { HumanMe } from "../api/types";
import { can } from "../lib/capabilities";
import { Button } from "../components/Button";
import { Crumbs } from "../components/Crumbs";
import { MyAgentsPage } from "./MyAgentsPage";
import { AccessSummary } from "./management/AccessSummary";
import { devicesOf, looseAgentsOf } from "./management/accessTree";
import { MachinesLevel } from "./management/MachinesLevel";
import { PeopleLevel } from "./management/PeopleLevel";
import { useAccessSnapshot } from "./management/useAccessSnapshot";

/**
 * Management 根节点的角色分叉。member 不能读 /v1/admin/*，所以这里不能调用
 * `useAccessSnapshot`（它的 effect 一旦挂载就会无条件发三个 admin 请求）——
 * 分叉必须在任何数据 hook 之前发生，member 分支要落到一个完全不碰
 * useAccessSnapshot 的组件，而不是「调用了但拿到 403」。
 */
export function AccessTreePage({ me }: { me: HumanMe }) {
  // member 分叉在 Task 9 换成 MyAgentsLevel；这里先维持现状。
  if (!can(me.role, "view.members")) return <MyAgentsPage />;
  return <AdminAccessTree me={me} />;
}

/**
 * admin+ 的访问树本体。下钻位置存在 URL 里（?person=&machine=）而不是组件
 * state，所以深链可分享、刷新保位、后退键逐层往上走而不是直接离开页面。
 * `me` 用来判断第 2 层「自己那一行」的 CTA（Connect a machine / + Create
 * Agent）——设备注册端点永远为调用者本人注册，别人的头部不能画这两个按钮。
 */
function AdminAccessTree({ me }: { me: HumanMe }) {
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const snapshot = useAccessSnapshot();

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

  // 第 2 层：某人的机器。devices/agents 缺腿时这一层没有意义，回落到根层。
  if (person && devices && agents) {
    return (
      <>
        <div className="page-head">
          <div>
            <p className="card-kicker">MANAGEMENT · ACCESS TREE</p>
            <h1>Access flows downward</h1>
          </div>
        </div>

        <AccessSummary snapshot={snapshot.snapshot} />

        <div className="at-bar">
          <Crumbs
            items={[
              { label: "Everyone", to: "/management" },
              { label: person.display_name },
            ]}
          />
          <span className="at-bar-hint">
            {devicesOf(devices, person.membership_id).length} machines
          </span>
        </div>

        <MachinesLevel
          person={person}
          devices={devicesOf(devices, person.membership_id)}
          looseAgents={looseAgentsOf(agents, person.membership_id)}
          isSelf={person.membership_id === me.membership_id}
          onDrill={(credentialId) => setParams({ person: person.membership_id, machine: credentialId })}
          onCreateAgent={() => navigate("/management/agents")}
          onConnectMachine={() => navigate("/management/devices")}
        />
      </>
    );
  }

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
