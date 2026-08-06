import { describe, expect, it } from "vitest";
import { resolveAgentAccess, readScopeFor } from "../src/pages/agent/agentScope";
import { makeAgent, makeMe } from "./helpers";

describe("readScopeFor", () => {
  it("admin+ 读 admin scope，member 读 me scope", () => {
    expect(readScopeFor(makeMe({ role: "owner" }))).toBe("admin");
    expect(readScopeFor(makeMe({ role: "admin" }))).toBe("admin");
    expect(readScopeFor(makeMe({ role: "member" }))).toBe("me");
  });
});

describe("resolveAgentAccess", () => {
  it("member 看自己的 Agent：me scope，可编辑可发令牌，不可移交", () => {
    const access = resolveAgentAccess(
      makeMe({ role: "member", membership_id: "mbr_01" }),
      makeAgent({ owner_membership_id: "mbr_01" }),
    );
    expect(access).toMatchObject({
      readScope: "me",
      actScope: "me",
      isSelf: true,
      canEdit: true,
      canSuspend: true,
      canResume: true,
      canRetire: true,
      canIssue: true,
      canRevoke: true,
      canTransfer: false,
    });
  });

  it("admin 看自己的 Agent：读 admin、动作 me，可发令牌", () => {
    // 这是本阶段修的洞：createEnrollment 只有 me scope，合并前 admin+
    // 恒走 admin scope，于是管理员给自己的 Agent 发不了接入令牌。
    const access = resolveAgentAccess(
      makeMe({ role: "admin", membership_id: "mbr_07" }),
      makeAgent({ owner_membership_id: "mbr_07" }),
    );
    expect(access.readScope).toBe("admin");
    expect(access.actScope).toBe("me");
    expect(access.isSelf).toBe(true);
    expect(access.canIssue).toBe(true);
    expect(access.canEdit).toBe(true);
  });

  it("admin 看别人的 Agent：只能暂停与吊销", () => {
    const access = resolveAgentAccess(
      makeMe({ role: "admin", membership_id: "mbr_07" }),
      makeAgent({ owner_membership_id: "mbr_99" }),
    );
    expect(access).toMatchObject({
      readScope: "admin",
      actScope: "admin",
      isSelf: false,
      canSuspend: true,
      canRevoke: true,
      canEdit: false,
      canResume: false,
      canRetire: false,
      canIssue: false,
      canTransfer: false,
    });
  });

  it("owner 看别人的 Agent：治理齐全且可移交，但不可发令牌", () => {
    const access = resolveAgentAccess(
      makeMe({ role: "owner", membership_id: "mbr_01" }),
      makeAgent({ owner_membership_id: "mbr_99" }),
    );
    expect(access).toMatchObject({
      actScope: "admin",
      canEdit: true,
      canResume: true,
      canRetire: true,
      canTransfer: true,
      canIssue: false,
    });
  });

  it("membership_id 或 owner_membership_id 缺失时判为「不是自己的」", () => {
    // 保守：宁可少给权限。后端仍是唯一执法者。
    const noOwner = resolveAgentAccess(
      makeMe({ role: "member", membership_id: "mbr_01" }),
      makeAgent({ owner_membership_id: undefined }),
    );
    expect(noOwner.isSelf).toBe(false);
    expect(noOwner.canIssue).toBe(false);

    const noMembership = resolveAgentAccess(
      makeMe({ role: "member", membership_id: undefined }),
      makeAgent({ owner_membership_id: "mbr_01" }),
    );
    expect(noMembership.isSelf).toBe(false);
    expect(noMembership.canIssue).toBe(false);
  });

  it("退役后一切写动作关闭，读 scope 不变", () => {
    const access = resolveAgentAccess(
      makeMe({ role: "owner", membership_id: "mbr_01" }),
      makeAgent({ owner_membership_id: "mbr_01", status: "retired", retired_at: "2026-08-01T00:00:00Z" }),
    );
    expect(access.retired).toBe(true);
    expect(access.readScope).toBe("admin");
    for (const key of ["canEdit", "canSuspend", "canResume", "canRetire", "canTransfer", "canIssue", "canRevoke"] as const) {
      expect(access[key]).toBe(false);
    }
  });

  it("status 与 retired_at 任一表明退役即视为退役", () => {
    // 后端两个字段都能表达终态；只看其中一个会让某些响应形态漏判。
    const byStatus = resolveAgentAccess(
      makeMe({ role: "owner", membership_id: "mbr_01" }),
      makeAgent({ owner_membership_id: "mbr_01", status: "retired" }),
    );
    const byTimestamp = resolveAgentAccess(
      makeMe({ role: "owner", membership_id: "mbr_01" }),
      makeAgent({ owner_membership_id: "mbr_01", retired_at: "2026-08-01T00:00:00Z" }),
    );
    expect(byStatus.retired).toBe(true);
    expect(byTimestamp.retired).toBe(true);
  });

  it("owner 看自己名下、未退役的 Agent：canTransfer 仍为 true", () => {
    // canTransfer 不应依赖于 isSelf；只要 govern 且 live 就应为 true。
    const access = resolveAgentAccess(
      makeMe({ role: "owner", membership_id: "mbr_01" }),
      makeAgent({ owner_membership_id: "mbr_01" }),
    );
    expect(access.isSelf).toBe(true);
    expect(access.retired).toBe(false);
    expect(access.canTransfer).toBe(true);
  });

  it("status 为 suspended 时 retired 为 false，写动作按 isSelf/govern 正常判定", () => {
    // 暂停和退役是两种不同的终态；暂停的 Agent 应该可以被恢复和编辑。
    const memberSuspended = resolveAgentAccess(
      makeMe({ role: "member", membership_id: "mbr_01" }),
      makeAgent({ owner_membership_id: "mbr_01", status: "suspended" }),
    );
    expect(memberSuspended.retired).toBe(false);
    expect(memberSuspended.canResume).toBe(true);
    expect(memberSuspended.canEdit).toBe(true);

    const adminOtherSuspended = resolveAgentAccess(
      makeMe({ role: "admin", membership_id: "mbr_07" }),
      makeAgent({ owner_membership_id: "mbr_99", status: "suspended" }),
    );
    expect(adminOtherSuspended.retired).toBe(false);
    expect(adminOtherSuspended.canResume).toBe(false);
    expect(adminOtherSuspended.canEdit).toBe(false);
  });
});
