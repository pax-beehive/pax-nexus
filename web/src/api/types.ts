// Types mirror idl/team_memory.thrift exactly; field names are the JSON names
// produced by the Hertz Thrift binding.

export type Role = "owner" | "admin" | "member";
export type MembershipStatus = "active" | "suspended" | "removed";
export type AgentStatus = "active" | "suspended" | "retired";
export type InvitationStatus = "pending" | "accepted" | "revoked" | "expired";
export type EnrollmentStatus = "pending" | "consumed" | "revoked" | "expired";

/** Permissions grantable through enrollment issuance (doc section 5.5). */
export const GRANTABLE_PERMISSIONS = [
  "observe",
  "search",
  "get",
  "channel_send",
  "channel_receive",
] as const;
export type Permission = (typeof GRANTABLE_PERMISSIONS)[number];

/** GET /v1/me response (also bootstrap claim and invitation accept). */
export interface HumanMe {
  user_id: string;
  email?: string;
  email_verified: boolean;
  membership_id?: string;
  role?: Role;
  membership_status?: MembershipStatus;
}

export interface Member {
  membership_id: string;
  user_id: string;
  email?: string;
  email_verified: boolean;
  display_name: string;
  role: Role;
  status: MembershipStatus;
  joined_at: string;
  updated_at: string;
  resource_version: number;
}

/** InvitationResponse: `token` is present only in the create response. */
export interface Invitation {
  invitation_id: string;
  token?: string;
  target_email: string;
  role: Role;
  status: InvitationStatus;
  created_at: string;
  expires_at: string;
}

export interface AgentProfile {
  agent_id: string;
  display_name: string;
  description: string;
  agent_type: string;
  status: AgentStatus;
  directory_visible: boolean;
  created_at: string;
  updated_at: string;
  retired_at?: string;
  resource_version: number;
  owner_membership_id?: string;
  owner_user_id?: string;
}

export interface EnrollmentMetadata {
  enrollment_id: string;
  agent_id: string;
  credential_label: string;
  permissions: string[];
  status: EnrollmentStatus;
  created_at: string;
  expires_at: string;
  credential_expires_at?: string;
}

/** AgentEnrollmentResponse: the one-time enrollment secret, returned once. */
export interface EnrollmentSecret {
  enrollment_id: string;
  token: string;
  expires_at: string;
}

export interface CredentialMetadata {
  credential_id: string;
  agent_id: string;
  label: string;
  permissions: string[];
  created_at: string;
  expires_at?: string;
  revoked_at?: string;
  last_used_at?: string;
}

export interface AuditEvent {
  audit_event_id: number;
  actor_kind: string;
  actor_user_id?: string;
  actor_membership_id?: string;
  actor_agent_id?: string;
  actor_credential_id?: string;
  action: string;
  target_kind: string;
  target_id: string;
  occurred_at: string;
}
