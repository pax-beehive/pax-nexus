// Action Layer: every mutation in the portal goes through this module.
//
// Idempotency (doc section 3.3):
// - actions that support Idempotency-Key take one generated per user action
//   via beginAction(); network retries of the same action reuse the same key,
//   a re-initiated action gets a new key.
// - invitation/enrollment creation and bootstrap claim return one-time
//   secrets and do NOT support Idempotency-Key: callers must never auto-retry
//   them; on timeout the user refreshes the list to check for a pending record.
//
// Optimistic locking: updates send resource_version in the body AND If-Match.
// On resource_version_conflict the caller refetches; other 409 codes have
// operation-specific recovery and are never silently overwritten.

import { humanFetch } from "./client";
import type {
  AgentProfile,
  CredentialMetadata,
  EnrollmentMetadata,
  EnrollmentSecret,
  HumanMe,
  Invitation,
  Member,
  MembershipStatus,
  Role,
} from "./types";

/** "me" = owner's own-agent endpoints, "admin" = governance endpoints. */
export type AgentScope = "me" | "admin";

/** Generate the Idempotency-Key for one user action instance. */
export function beginAction(): string {
  return crypto.randomUUID();
}

const JSON_HEADERS = { "Content-Type": "application/json" };

/** If-Match with a quoted version; the backend accepts 7, "7" and W/"7". */
export function ifMatch(version: number): string {
  return `"${version}"`;
}

function agentBase(scope: AgentScope, agentId: string): string {
  return `/v1/${scope}/agents/${encodeURIComponent(agentId)}`;
}

// ---- Auth / onboarding ----

export function logout(): Promise<unknown> {
  return humanFetch("/v1/auth/logout", { method: "POST" });
}

/**
 * Claim the first Owner. The secret travels only in this header; callers must
 * clear the input immediately after the request settles and never persist it.
 * No automatic retry.
 */
export function claimBootstrap(secret: string): Promise<HumanMe> {
  return humanFetch<HumanMe>("/v1/bootstrap/claim", {
    method: "POST",
    headers: { "X-PAX-Bootstrap-Secret": secret },
  });
}

export function acceptInvitation(token: string, idempotencyKey: string): Promise<HumanMe> {
  return humanFetch<HumanMe>("/v1/invitations/accept", {
    method: "POST",
    headers: { ...JSON_HEADERS, "Idempotency-Key": idempotencyKey },
    body: JSON.stringify({ token }),
  });
}

// ---- My Agents ----

export interface CreateAgentInput {
  agent_id: string;
  display_name: string;
  description?: string;
  agent_type?: string;
  directory_visible: boolean;
}

export async function createAgent(
  input: CreateAgentInput,
  idempotencyKey: string,
): Promise<AgentProfile> {
  const res = await humanFetch<{ agent: AgentProfile }>("/v1/me/agents", {
    method: "POST",
    headers: { ...JSON_HEADERS, "Idempotency-Key": idempotencyKey },
    body: JSON.stringify(input),
  });
  return res.agent;
}

export interface AgentPatch {
  display_name?: string;
  description?: string;
  agent_type?: string;
  directory_visible?: boolean;
  status?: string;
}

export async function updateAgent(
  scope: AgentScope,
  agentId: string,
  patch: AgentPatch,
  version: number,
): Promise<AgentProfile> {
  const res = await humanFetch<{ agent: AgentProfile }>(agentBase(scope, agentId), {
    method: "PATCH",
    headers: { ...JSON_HEADERS, "If-Match": ifMatch(version) },
    body: JSON.stringify({ ...patch, resource_version: version }),
  });
  return res.agent;
}

/** Retire is terminal; resource_version travels in the query per the IDL. */
export async function retireAgent(
  scope: AgentScope,
  agentId: string,
  version: number,
  idempotencyKey: string,
): Promise<AgentProfile> {
  const res = await humanFetch<{ agent: AgentProfile }>(
    `${agentBase(scope, agentId)}?resource_version=${version}`,
    {
      method: "DELETE",
      headers: { "If-Match": ifMatch(version), "Idempotency-Key": idempotencyKey },
    },
  );
  return res.agent;
}

// ---- Enrollments / Credentials ----

export interface CreateEnrollmentInput {
  credential_label: string;
  permissions: string[];
  expires_in_seconds: number;
  credential_expires_at?: string;
}

/**
 * Issue a one-time enrollment token. The token is returned exactly once and
 * must be held in memory only. No Idempotency-Key, no automatic retry.
 */
export function createEnrollment(
  agentId: string,
  input: CreateEnrollmentInput,
): Promise<EnrollmentSecret> {
  return humanFetch<EnrollmentSecret>(`${agentBase("me", agentId)}/enrollments`, {
    method: "POST",
    headers: JSON_HEADERS,
    body: JSON.stringify(input),
  });
}

/** Revoke sends no JSON body (doc section 6.5). */
export async function revokeEnrollment(
  scope: AgentScope,
  agentId: string,
  enrollmentId: string,
  idempotencyKey: string,
): Promise<EnrollmentMetadata> {
  const res = await humanFetch<{ enrollment: EnrollmentMetadata }>(
    `${agentBase(scope, agentId)}/enrollments/${encodeURIComponent(enrollmentId)}`,
    { method: "DELETE", headers: { "Idempotency-Key": idempotencyKey } },
  );
  return res.enrollment;
}

export async function revokeCredential(
  scope: AgentScope,
  agentId: string,
  credentialId: string,
  idempotencyKey: string,
): Promise<CredentialMetadata> {
  const res = await humanFetch<{ credential: CredentialMetadata }>(
    `${agentBase(scope, agentId)}/credentials/${encodeURIComponent(credentialId)}`,
    { method: "DELETE", headers: { "Idempotency-Key": idempotencyKey } },
  );
  return res.credential;
}

// ---- Admin: invitations / members / agents ----

export interface CreateInvitationInput {
  target_email: string;
  role: Role;
  expires_in_seconds: number;
}

/**
 * Create an invitation. The token is returned exactly once. No
 * Idempotency-Key, no automatic retry (doc section 3.3).
 */
export function createInvitation(input: CreateInvitationInput): Promise<Invitation> {
  return humanFetch<Invitation>("/v1/admin/invitations", {
    method: "POST",
    headers: JSON_HEADERS,
    body: JSON.stringify(input),
  });
}

export function revokeInvitation(invitationId: string): Promise<Invitation> {
  return humanFetch<Invitation>(
    `/v1/admin/invitations/${encodeURIComponent(invitationId)}`,
    { method: "DELETE" },
  );
}

export interface MemberPatch {
  role?: Role;
  status?: MembershipStatus;
}

export async function updateMember(
  membershipId: string,
  patch: MemberPatch,
  version: number,
): Promise<Member> {
  const res = await humanFetch<{ member: Member }>(
    `/v1/admin/members/${encodeURIComponent(membershipId)}`,
    {
      method: "PATCH",
      headers: { ...JSON_HEADERS, "If-Match": ifMatch(version) },
      body: JSON.stringify({ ...patch, resource_version: version }),
    },
  );
  return res.member;
}

export async function transferAgent(
  agentId: string,
  targetMembershipId: string,
  version: number,
): Promise<AgentProfile> {
  const res = await humanFetch<{ agent: AgentProfile }>(
    `${agentBase("admin", agentId)}/transfer`,
    {
      method: "POST",
      headers: { ...JSON_HEADERS, "If-Match": ifMatch(version) },
      body: JSON.stringify({ target_membership_id: targetMembershipId, resource_version: version }),
    },
  );
  return res.agent;
}
