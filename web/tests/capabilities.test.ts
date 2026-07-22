import { describe, expect, it } from "vitest";
import { can, canManageTargetRole, canRevokeInvitation, type Capability } from "../src/lib/capabilities";
import type { Role } from "../src/api/types";

// Expectations transcribed from the capability matrix (doc section 2.2).
const MATRIX: Record<Capability, Record<Role, boolean>> = {
  "invite.member": { owner: true, admin: true, member: false },
  "invite.admin": { owner: true, admin: false, member: false },
  "view.members": { owner: true, admin: true, member: false },
  "view.audit": { owner: true, admin: true, member: false },
  "view.all-agents": { owner: true, admin: true, member: false },
  "manage.member-role": { owner: true, admin: true, member: false },
  "manage.elevated-role": { owner: true, admin: false, member: false },
  "manage.own-agents": { owner: true, admin: true, member: true },
  "suspend.any-agent": { owner: true, admin: true, member: false },
  "govern.any-agent": { owner: true, admin: false, member: false },
  "claim.bootstrap": { owner: true, admin: false, member: false },
};

describe("can(role, capability)", () => {
  for (const [cap, roles] of Object.entries(MATRIX) as [Capability, Record<Role, boolean>][]) {
    for (const [role, expected] of Object.entries(roles) as [Role, boolean][]) {
      it(`${role} ${expected ? "can" : "cannot"} ${cap}`, () => {
        expect(can(role, cap)).toBe(expected);
      });
    }
  }

  it("denies everything when the role is unknown", () => {
    for (const cap of Object.keys(MATRIX) as Capability[]) {
      expect(can(undefined, cap)).toBe(false);
    }
  });
});

describe("target-aware helpers", () => {
  it("admin manages members but not owner/admin", () => {
    expect(canManageTargetRole("admin", "member")).toBe(true);
    expect(canManageTargetRole("admin", "admin")).toBe(false);
    expect(canManageTargetRole("admin", "owner")).toBe(false);
  });

  it("owner manages every role", () => {
    expect(canManageTargetRole("owner", "owner")).toBe(true);
    expect(canManageTargetRole("owner", "admin")).toBe(true);
    expect(canManageTargetRole("owner", "member")).toBe(true);
  });

  it("member manages nothing", () => {
    expect(canManageTargetRole("member", "member")).toBe(false);
  });

  it("admin revokes only member-role invitations; owner revokes any", () => {
    expect(canRevokeInvitation("admin", "member")).toBe(true);
    expect(canRevokeInvitation("admin", "admin")).toBe(false);
    expect(canRevokeInvitation("owner", "admin")).toBe(true);
  });
});
