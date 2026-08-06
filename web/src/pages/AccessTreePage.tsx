import { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import type { AgentProfile, DeviceEnrollmentSecret, DeviceSummary, HumanMe, Member } from "../api/types";
import { can } from "../lib/capabilities";
import { aliveProvisionedAgents } from "../lib/devices";
import { copyTextToClipboard } from "../lib/clipboard";
import { deviceConnectCommand, isSelfDescribingEnrollmentToken } from "../lib/enrollment";
import { Button } from "../components/Button";
import { CreateDeviceEnrollmentModal } from "../components/CreateDeviceEnrollmentModal";
import { SecretCard } from "../components/SecretCard";
import { useToast } from "../components/Toasts";
import { AccessChrome } from "./management/AccessChrome";
import { devicesOf, looseAgentsOf } from "./management/accessTree";
import { CreateAgentModal } from "./management/CreateAgentModal";
import { DeviceAgentsLevel } from "./management/DeviceAgentsLevel";
import { MachinesLevel } from "./management/MachinesLevel";
import { MyAgentsLevel } from "./management/MyAgentsLevel";
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
  if (!can(me.role, "view.members")) return <MyAgentsLevel />;
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
  const toast = useToast();
  const snapshot = useAccessSnapshot();
  // 无条件调用，与 useAccessSnapshot 并列：不能塞进下面任何 if 分支，
  // 否则 hook 调用顺序在渲染间不一致。credentialId 为 undefined 时
  // useDeviceDetail 内部直接落成 "ready"、不发请求。requestedPerson 也提到
  // 这里（快照 ready 判定之前）：下面的一次性密钥/弹窗状态要在任何 early
  // return 之前就能引用它，不能等到 snapshot.snapshot 解出来才算数。
  // `|| undefined` 而不是 `?? undefined`：被截断的 `?person=` 给出的是空串，
  // `??` 留着它，于是它匹配不到任何 member，页面误报「这个人已经不在团队里」。
  // 空串与「没有这个参数」是同一件事。`?machine=` 同理。
  const requestedPerson = params.get("person") || undefined;
  const requestedMachine = params.get("machine") || undefined;
  const deviceDetail = useDeviceDetail(requestedMachine);
  // 第 2 层「本人」头部的两个入口：就地开弹窗，而不是跳到平表页
  // （routes.tsx 的 /management/agents、/management/devices 仍在，但那两页是
  // 团队全览，不是「为自己注册」的入口——注册端点永远只认调用者本人）。
  const [createAgentOpen, setCreateAgentOpen] = useState(false);
  const [connectMachineOpen, setConnectMachineOpen] = useState(false);
  const [enrollmentSecret, setEnrollmentSecret] = useState<DeviceEnrollmentSecret | undefined>();

  // AdminAccessTree 在整段树内导航期间不会重新挂载——下钻/回退只改写
  // ?person=/?machine=，组件实例和它的 state 都留着。这两个弹窗开关与
  // 一次性密钥因此必须绑定在「产生它们的那组树坐标」上，而不是绑在这个
  // 组件的生命周期上：坐标一变（换人、进/出某台机器），上一坐标开着的
  // 弹窗、留着的密钥就都失效，不能带着走到下一张脸上（复审 C1 / I2）。
  useEffect(() => {
    setCreateAgentOpen(false);
    setConnectMachineOpen(false);
    setEnrollmentSecret(undefined);
  }, [requestedPerson, requestedMachine]);

  // 只能出现在产生它的那个面上：本人（isSelf）的第 2 层，且没有 ?machine=
  // ——这与 Connect a machine 按钮本身的可见条件一致，因为唯一能把
  // enrollmentSecret 置为有值的路径就是那个按钮。故意提到任何 snapshot
  // early return 之前算：失败/进行中的 retry() 不该把已经拿到手的一次性
  // 密钥带走（复审 I1）——服务端此时已经创建了这条 enrollment，密钥从
  // state 里消失后唯一的补救是吊销重建。
  const secretCard = enrollmentSecret && !requestedMachine && requestedPerson === me.membership_id && (
    <SecretCard
      title="One-time Device Enrollment token (shown only once)"
      value={enrollmentSecret.token}
      valueLabel=" token"
      expiresAt={enrollmentSecret.expires_at}
      note={
        isSelfDescribingEnrollmentToken(enrollmentSecret.token)
          ? "The token is never written to durable storage, logs, or analytics. If it is lost, you can only revoke the Device and create a new enrollment; the token embeds the connect address — run paxl device connect on the target machine to finish onboarding."
          : "The token is never written to durable storage, logs, or analytics. If it is lost, you can only revoke the Device and create a new enrollment; run paxl device connect on the target machine to finish onboarding."
      }
      extraActions={
        <Button
          size="sm"
          onClick={() => {
            const command = deviceConnectCommand(
              enrollmentSecret.token,
              window.location.origin,
              enrollmentSecret.device_name,
            );
            void copyTextToClipboard(command).then((ok) => {
              if (ok) toast("ok", "Connect command copied");
              else window.prompt("Copy manually:", command);
            });
          }}
        >
          Copy client command
        </Button>
      }
      onClose={() => setEnrollmentSecret(undefined)}
    />
  );

  if (snapshot.status === "loading") {
    return (
      <>
        {secretCard}
        <p className="muted">Loading…</p>
      </>
    );
  }
  if (snapshot.status === "error" || !snapshot.snapshot) {
    return (
      <>
        {secretCard}
        <div className="note bad row between" role="alert">
          <span>Could not load the team’s people.</span>
          <Button size="sm" onClick={snapshot.retry}>
            Retry
          </Button>
        </div>
      </>
    );
  }

  // 提成局部 const：外壳组件与下面几个闭包都要引用它，而 `snapshot.snapshot`
  // 的 narrowing 进不了闭包（原来靠 `snapshot.snapshot!` 的非空断言绕过）。
  const snap = snapshot.snapshot;
  const { members, devices, agents } = snap;
  const person = members.find((member) => member.membership_id === requestedPerson);
  // 链接指向的人已不在快照里：回退到根层并说明，不静默重置。
  const stalePerson = requestedPerson !== undefined && person === undefined;

  const drill = (membershipId: string) => {
    setParams({ person: membershipId });
  };

  const personCrumb = (activePerson: Member) => ({
    label: activePerson.display_name,
    // 与所有同级链接一致地转义。id 是服务端给的不透明串，目前不含需要转义
    // 的字符，所以这不是活 bug；不一致本身才是问题。
    to: `/management?person=${encodeURIComponent(activePerson.membership_id)}`,
  });

  // 非脊柱腿失败时的层内报错：说明 + 单独重试，外壳（面包屑、上层链接）
  // 保留（设计 §6）。
  const legError = (message: string, retry: () => void) => (
    <div className="note bad row between" role="alert">
      <span>{message}</span>
      <Button size="sm" onClick={retry}>
        Retry
      </Button>
    </div>
  );

  // 第 2 层（含第 3 层的回落目标）：某人的机器。抽成局部函数以便第 3 层的
  // stale-machine 回落复用同一段渲染，只多一条说明。
  const renderMachinesLevel = (
    activePerson: Member,
    personDevices: DeviceSummary[],
    /** undefined = 快照 agents 腿失败：只降级「手工注册」那一格。 */
    personAgents: AgentProfile[] | undefined,
    options: { staleMachine: boolean },
  ) => {
    const machines = devicesOf(personDevices, activePerson.membership_id);
    return (
      <AccessChrome
        snapshot={snap}
        crumbs={[{ label: "Everyone", to: "/management" }, { label: activePerson.display_name }]}
        hint={`${machines.length} machines`}
      >
        {options.staleMachine && (
          <div className="note warn small" role="status">
            That machine no longer exists, so we brought you back to {activePerson.display_name}’s
            machines.
          </div>
        )}

        {/* agents 腿失败只让「手工注册」这一格缺席，不该把这一层整个撤掉：
            机器行来自 devices 腿，照常可用。 */}
        {personAgents === undefined &&
          legError(
            `Could not load ${activePerson.display_name}’s hand-registered agents.`,
            snapshot.retry,
          )}

        {secretCard}

        <MachinesLevel
          person={activePerson}
          devices={machines}
          looseAgents={
            personAgents ? looseAgentsOf(personAgents, activePerson.membership_id) : []
          }
          isSelf={activePerson.membership_id === me.membership_id}
          onDrill={(credentialId) =>
            setParams({ person: activePerson.membership_id, machine: credentialId })
          }
          onCreateAgent={() => setCreateAgentOpen(true)}
          onConnectMachine={() => setConnectMachineOpen(true)}
        />

        {createAgentOpen && (
          <CreateAgentModal
            onClose={() => setCreateAgentOpen(false)}
            onCreated={() => {
              setCreateAgentOpen(false);
              snapshot.retry();
            }}
          />
        )}

        {connectMachineOpen && (
          <CreateDeviceEnrollmentModal
            onClose={() => setConnectMachineOpen(false)}
            onCreated={(secret) => {
              setConnectMachineOpen(false);
              setEnrollmentSecret(secret);
              snapshot.retry();
            }}
            onMaybeCreated={() => {
              setConnectMachineOpen(false);
              snapshot.retry();
            }}
          />
        )}
      </AccessChrome>
    );
  };

  // 第 2、3 层都由 devices 腿撑着。这条腿失败时**不能**落到函数末尾的根层：
  // 那样 URL 说第 2/3 层、页面画第 1 层、页内一个字的解释都没有（stalePerson
  // 是 false，人确实在快照里），而且再点一次那个人只会写出等价的 URL，
  // 什么也不会发生——用户被无声地钉死在根层。设计 §6：非脊柱腿失败在对应层
  // 就地报错并可单独重试，回落必须自己说明自己。
  if (person && !devices) {
    return (
      <AccessChrome
        snapshot={snap}
        crumbs={[{ label: "Everyone", to: "/management" }, { label: person.display_name }]}
        hint="— machines"
      >
        {legError(`Could not load ${person.display_name}’s machines.`, snapshot.retry)}
        {secretCard}
      </AccessChrome>
    );
  }

  // 第 3 层：某机器的 Agent。放在第 2 层分支之前——machine 参数存在时，
  // 这一层（或它的 stale 回落）才是该渲染的内容。第 3 层不依赖快照的
  // agents 腿：它展示的 Agent 行来自 deviceDetail 自己的 getDevice 调用，
  // 快照 agents 腿失败是「散装 Agent 分组」这个第 2 层专属特性缺失，不该
  // 把正坐在 ?person=&machine= 的人一路弹回根层（设计：非脊柱腿失败只降级
  // 受影响的格子，不降级整层）。
  if (person && devices && requestedMachine) {
    const device = devicesOf(devices, person.membership_id).find(
      (candidate) => candidate.credential_id === requestedMachine,
    );
    // 机器已删或不属于这个人：回落到这个人的机器层并说明。agents 腿同时
    // 失败也照回落不误，只是「手工注册」那一格换成层内报错——不再连同
    // 这条说明一起消失在根层。
    if (!device) return renderMachinesLevel(person, devices, agents, { staleMachine: true });

    // 第 3 层的 loading / error 同样带外壳：面包屑与上层链接是用户在这两个
    // 状态下唯一的页内退路（设计 §6 末条）。
    const machineCrumbs = [
      { label: "Everyone", to: "/management" },
      personCrumb(person),
      { label: device.device_name },
    ];
    if (deviceDetail.status === "loading") {
      return (
        <AccessChrome snapshot={snap} crumbs={machineCrumbs} hint="— agents">
          {/* 文案不能是光秃秃的 "Loading…"：那是启动占位符的文案，测试
              helper 的 renderApp 等的就是它消失。 */}
          <p className="muted">Loading this machine’s agents…</p>
        </AccessChrome>
      );
    }
    if (deviceDetail.status === "error" || !deviceDetail.detail) {
      return (
        <AccessChrome snapshot={snap} crumbs={machineCrumbs} hint="— agents">
          {legError("Could not load this machine’s agents.", deviceDetail.retry)}
        </AccessChrome>
      );
    }

    const liveAgents = aliveProvisionedAgents(deviceDetail.detail.agents);
    return (
      <AccessChrome snapshot={snap} crumbs={machineCrumbs} hint={`${liveAgents.length} agents`}>
        <DeviceAgentsLevel
          person={person}
          device={device}
          agents={liveAgents}
          onRevoked={() => {
            snapshot.retry();
            setParams({ person: person.membership_id });
          }}
        />
      </AccessChrome>
    );
  }

  // 第 2 层：某人的机器。
  if (person && devices) {
    return renderMachinesLevel(person, devices, agents, { staleMachine: false });
  }

  return (
    <AccessChrome
      snapshot={snap}
      crumbs={[{ label: "Everyone" }]}
      hint={`${members.length} people`}
      lede={
        <p className="muted flush">
          A person joins the team. That person connects a machine. The machine registers the agents
          that run on it — and every key those agents hold traces back up this chain.
        </p>
      }
    >
      {stalePerson && (
        <div className="note warn small" role="status">
          That person is no longer on this team, so we brought you back to everyone.
        </div>
      )}

      <PeopleLevel members={members} devices={devices} agents={agents} onDrill={drill} />
    </AccessChrome>
  );
}
