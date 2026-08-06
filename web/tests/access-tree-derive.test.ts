import { describe, expect, it } from "vitest";
import {
  agentsOf,
  devicesOf,
  isLooseAgent,
  looseAgentsOf,
  summarizeAgents,
  summarizeMachines,
  summarizePeople,
} from "../src/pages/management/accessTree";
import { makeAgent, makeDevice, makeMember } from "./helpers";

describe("isLooseAgent", () => {
  it("treats a missing provisioned_by as hand-registered", () => {
    expect(isLooseAgent(makeAgent())).toBe(true);
  });

  it("treats an empty-string provisioned_by as device-provisioned", () => {
    // 存在性判断，不是真值判断：后端对人工注册的 Agent 整个省略该字段，
    // 空字符串是「有这个字段」的异常数据，不该被当成散装。
    expect(isLooseAgent(makeAgent({ provisioned_by: "" }))).toBe(false);
  });

  it("treats a device credential id as device-provisioned", () => {
    expect(isLooseAgent(makeAgent({ provisioned_by: "dev_01" }))).toBe(false);
  });
});

describe("devicesOf / agentsOf / looseAgentsOf", () => {
  const devices = [
    makeDevice({ credential_id: "dev_a", created_by_membership_id: "mbr_01" }),
    makeDevice({ credential_id: "dev_b", created_by_membership_id: "mbr_02" }),
  ];
  const agents = [
    makeAgent({ agent_id: "by-hand", owner_membership_id: "mbr_01" }),
    makeAgent({ agent_id: "by-device", owner_membership_id: "mbr_01", provisioned_by: "dev_a" }),
    makeAgent({ agent_id: "other-person", owner_membership_id: "mbr_02" }),
  ];

  it("groups devices by their creating membership", () => {
    expect(devicesOf(devices, "mbr_01").map((d) => d.credential_id)).toEqual(["dev_a"]);
  });

  it("returns every agent owned by the membership", () => {
    expect(agentsOf(agents, "mbr_01").map((a) => a.agent_id)).toEqual(["by-hand", "by-device"]);
  });

  it("returns only the hand-registered agents as loose", () => {
    expect(looseAgentsOf(agents, "mbr_01").map((a) => a.agent_id)).toEqual(["by-hand"]);
  });

  it("ignores agents whose owner_membership_id is absent", () => {
    expect(agentsOf([makeAgent({ owner_membership_id: undefined })], "mbr_01")).toEqual([]);
  });
});

describe("summarizePeople", () => {
  it("counts each role", () => {
    const counts = summarizePeople([
      makeMember({ membership_id: "m1", role: "owner" }),
      makeMember({ membership_id: "m2", role: "admin" }),
      makeMember({ membership_id: "m3", role: "member" }),
      makeMember({ membership_id: "m4", role: "member" }),
    ]);
    expect(counts).toEqual({ total: 4, owners: 1, admins: 1, members: 2 });
  });
});

describe("summarizeMachines", () => {
  it("counts machine states and people with no machine at all", () => {
    const members = [makeMember({ membership_id: "m1" }), makeMember({ membership_id: "m2" })];
    const devices = [
      makeDevice({ credential_id: "d1", created_by_membership_id: "m1", status: "active" }),
      makeDevice({ credential_id: "d2", created_by_membership_id: "m1", status: "revoked" }),
    ];
    expect(summarizeMachines(devices, members)).toEqual({
      total: 2,
      connected: 1,
      revoked: 1,
      peopleWithoutMachine: 1,
    });
  });

  it("counts a person whose only machine is revoked as still having one", () => {
    // 「没有机器」问的是有没有登记过，不是有没有能用的——把吊销过机器的人
    // 混进「一台都没有」会让 admin 以为那个人从没连过。
    const members = [makeMember({ membership_id: "m1" })];
    const devices = [
      makeDevice({ credential_id: "d1", created_by_membership_id: "m1", status: "revoked" }),
    ];
    expect(summarizeMachines(devices, members).peopleWithoutMachine).toBe(0);
  });
});

describe("summarizeAgents", () => {
  it("counts each agent status", () => {
    expect(
      summarizeAgents([
        makeAgent({ agent_id: "a", status: "active" }),
        makeAgent({ agent_id: "b", status: "suspended" }),
        makeAgent({ agent_id: "c", status: "retired" }),
        makeAgent({ agent_id: "d", status: "active" }),
      ]),
    ).toEqual({ total: 4, active: 2, suspended: 1, retired: 1 });
  });
});
