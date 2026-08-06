import { useNavigate, useSearchParams } from "react-router-dom";
import type { AgentProfile, DeviceSummary, HumanMe, Member } from "../api/types";
import { can } from "../lib/capabilities";
import { aliveProvisionedAgents } from "../lib/devices";
import { Button } from "../components/Button";
import { Crumbs } from "../components/Crumbs";
import { MyAgentsPage } from "./MyAgentsPage";
import { AccessSummary } from "./management/AccessSummary";
import { devicesOf, looseAgentsOf } from "./management/accessTree";
import { DeviceAgentsLevel } from "./management/DeviceAgentsLevel";
import { MachinesLevel } from "./management/MachinesLevel";
import { PeopleLevel } from "./management/PeopleLevel";
import { useAccessSnapshot } from "./management/useAccessSnapshot";
import { useDeviceDetail } from "./management/useDeviceDetail";

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
  // 无条件调用，与 useAccessSnapshot 并列：不能塞进下面任何 if 分支，
  // 否则 hook 调用顺序在渲染间不一致。credentialId 为 undefined 时
  // useDeviceDetail 内部直接落成 "ready"、不发请求。
  const requestedMachine = params.get("machine") ?? undefined;
  const deviceDetail = useDeviceDetail(requestedMachine);

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

  // 第 2 层（含第 3 层的回落目标）：某人的机器。抽成局部函数以便第 3 层的
  // stale-machine 回落复用同一段渲染，只多一条说明。
  const renderMachinesLevel = (
    activePerson: Member,
    personDevices: DeviceSummary[],
    personAgents: AgentProfile[],
    options: { staleMachine: boolean },
  ) => (
    <>
      <div className="page-head">
        <div>
          <p className="card-kicker">MANAGEMENT · ACCESS TREE</p>
          <h1>Access flows downward</h1>
        </div>
      </div>

      <AccessSummary snapshot={snapshot.snapshot!} />

      <div className="at-bar">
        <Crumbs
          items={[
            { label: "Everyone", to: "/management" },
            { label: activePerson.display_name },
          ]}
        />
        <span className="at-bar-hint">
          {devicesOf(personDevices, activePerson.membership_id).length} machines
        </span>
      </div>

      {options.staleMachine && (
        <div className="note warn small" role="status">
          That machine no longer exists, so we brought you back to {activePerson.display_name}’s
          machines.
        </div>
      )}

      <MachinesLevel
        person={activePerson}
        devices={devicesOf(personDevices, activePerson.membership_id)}
        looseAgents={looseAgentsOf(personAgents, activePerson.membership_id)}
        isSelf={activePerson.membership_id === me.membership_id}
        onDrill={(credentialId) =>
          setParams({ person: activePerson.membership_id, machine: credentialId })
        }
        onCreateAgent={() => navigate("/management/agents")}
        onConnectMachine={() => navigate("/management/devices")}
      />
    </>
  );

  // 第 3 层：某机器的 Agent。放在第 2 层分支之前——machine 参数存在时，
  // 这一层（或它的 stale 回落）才是该渲染的内容。第 3 层不依赖快照的
  // agents 腿：它展示的 Agent 行来自 deviceDetail 自己的 getDevice 调用，
  // 快照 agents 腿失败是「散装 Agent 分组」这个第 2 层专属特性缺失，不该
  // 把正坐在 ?person=&machine= 的人一路弹回根层（设计：非脊柱腿失败只降级
  // 受影响的格子，不降级整层）。只有 stale-machine 回落要渲染第 2 层的
  // MachinesLevel，才需要 agents；agents 腿也失败时那条回落没有东西可画，
  // 落到根层。
  if (person && devices && requestedMachine) {
    const device = devicesOf(devices, person.membership_id).find(
      (candidate) => candidate.credential_id === requestedMachine,
    );
    if (!device) {
      // 机器已删或不属于这个人：回落到这个人的机器层并说明。这条回落要
      // 画 MachinesLevel（含散装 Agent 分组），才真的需要 agents；agents
      // 腿也失败时这条回落没有东西可画，落到函数末尾的根层。
      if (agents) return renderMachinesLevel(person, devices, agents, { staleMachine: true });
    } else if (deviceDetail.status === "loading") {
      return <p className="muted">Loading…</p>;
    } else if (deviceDetail.status === "error" || !deviceDetail.detail) {
      return (
        <div className="note bad row between" role="alert">
          <span>Could not load this machine’s agents.</span>
          <Button size="sm" onClick={deviceDetail.retry}>
            Retry
          </Button>
        </div>
      );
    } else {
      const liveAgents = aliveProvisionedAgents(deviceDetail.detail.agents);
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
                { label: person.display_name, to: `/management?person=${person.membership_id}` },
                { label: device.device_name },
              ]}
            />
            <span className="at-bar-hint">{liveAgents.length} agents</span>
          </div>

          <DeviceAgentsLevel
            person={person}
            device={device}
            agents={liveAgents}
            onRevoked={() => {
              snapshot.retry();
              setParams({ person: person.membership_id });
            }}
          />
        </>
      );
    }
  }

  // 第 2 层：某人的机器。devices/agents 缺腿时这一层没有意义，回落到根层。
  if (person && devices && agents) {
    return renderMachinesLevel(person, devices, agents, { staleMachine: false });
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
