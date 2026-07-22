// Role capability matrix (integration doc section 2.2). Pure client-side
// convenience for hiding buttons; the backend enforces per request, so a
// hidden button is never the only line of defense.

import type { Role } from "../api/types";

export type Capability =
  | "invite.member"
  | "invite.admin"
  | "view.members"
  | "view.audit"
  | "view.all-agents"
  | "manage.member-role"
  | "manage.elevated-role"
  | "manage.own-agents"
  | "suspend.any-agent"
  | "govern.any-agent"
  | "claim.bootstrap";

const MATRIX: Record<Capability, ReadonlySet<Role>> = {
  "invite.member": new Set<Role>(["owner", "admin"]),
  "invite.admin": new Set<Role>(["owner"]),
  "view.members": new Set<Role>(["owner", "admin"]),
  "view.audit": new Set<Role>(["owner", "admin"]),
  "view.all-agents": new Set<Role>(["owner", "admin"]),
  "manage.member-role": new Set<Role>(["owner", "admin"]),
  "manage.elevated-role": new Set<Role>(["owner"]),
  "manage.own-agents": new Set<Role>(["owner", "admin", "member"]),
  "suspend.any-agent": new Set<Role>(["owner", "admin"]),
  // Edit profile, resume, retire and transfer are Owner-only.
  "govern.any-agent": new Set<Role>(["owner"]),
  // Only meaningful on first install; a user claiming bootstrap has no
  // membership yet, so the UI never gates the entry on this value.
  "claim.bootstrap": new Set<Role>(["owner"]),
};

export function can(role: Role | undefined, capability: Capability): boolean {
  return role !== undefined && MATRIX[capability].has(role);
}

/** Owner manages everyone; Admin manages only Member-role memberships. */
export function canManageTargetRole(actor: Role, target: Role): boolean {
  return can(actor, target === "member" ? "manage.member-role" : "manage.elevated-role");
}

/** Owner revokes any pending invitation; Admin only member-role ones. */
export function canRevokeInvitation(actor: Role, invitationRole: Role): boolean {
  return canManageTargetRole(actor, invitationRole);
}
