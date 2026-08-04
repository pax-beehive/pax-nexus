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
  DeviceEnrollmentSecret,
  DeviceSummary,
  EnrollmentMetadata,
  EnrollmentSecret,
  HumanMe,
  Invitation,
  Member,
  MembershipStatus,
  Role,
  Team,
} from "./types";
import type { WikiGenerationSettings, WikiIngestionStatus } from "./wiki";
import type { TodoItem } from "./todo";

/** "me" = owner's own-agent endpoints, "admin" = governance endpoints. */
export type AgentScope = "me" | "admin";

// Idempotency keys are opaque strings; the backend stores them as TEXT
// without a format check. All paths below return UUID-shaped keys so keys
// look uniform in logs and in the create-agent form hint.
let actionKeyCounter = 0;

/** Join 32 lowercase hex characters as an 8-4-4-4-12 string. */
function uuidShape(hex: string): string {
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20, 32)}`;
}

/**
 * Fallback for origins where crypto.randomUUID is absent (plain-HTTP,
 * non-localhost deployments): an unguarded call there throws and crashes the
 * whole render. Prefer crypto.getRandomValues; the last resort is
 * Math.random seeded with the current time and a per-session counter, which
 * is enough because only uniqueness per action instance matters.
 */
function fallbackActionKey(): string {
  actionKeyCounter += 1;
  const cryptoObj: Crypto | undefined = globalThis.crypto;
  if (cryptoObj && typeof cryptoObj.getRandomValues === "function") {
    const bytes = new Uint8Array(16);
    cryptoObj.getRandomValues(bytes);
    // Mix in the counter so keys stay unique even behind a broken entropy
    // source, then set the version 4 / variant 1 bits.
    bytes[0] ^= actionKeyCounter & 0xff;
    bytes[1] ^= (actionKeyCounter >> 8) & 0xff;
    bytes[6] = (bytes[6] & 0x0f) | 0x40;
    bytes[8] = (bytes[8] & 0x3f) | 0x80;
    const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
    return uuidShape(hex);
  }
  const time = Date.now().toString(16).padStart(12, "0");
  const counter = (actionKeyCounter % 0xffff).toString(16).padStart(4, "0");
  let random = "";
  for (let i = 0; i < 16; i += 1) {
    random += Math.floor(Math.random() * 16).toString(16);
  }
  return uuidShape(`${time}${counter}${random}`);
}

/** Generate the Idempotency-Key for one user action instance. */
export function beginAction(): string {
  const cryptoObj: Crypto | undefined = globalThis.crypto;
  if (cryptoObj && typeof cryptoObj.randomUUID === "function") {
    return cryptoObj.randomUUID();
  }
  return fallbackActionKey();
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

// ---- Teams (saas profile) ----

/**
 * Create a team; the caller becomes its owner. Works with no existing
 * membership. The slug is derived server-side from the name; a 409
 * team_slug_conflict means the derived slug is taken.
 */
export async function createTeam(name: string, idempotencyKey: string): Promise<Team> {
  const res = await humanFetch<{ team: Team }>("/v1/teams", {
    method: "POST",
    headers: { ...JSON_HEADERS, "Idempotency-Key": idempotencyKey },
    body: JSON.stringify({ name }),
  });
  return res.team;
}

/**
 * Point the caller's session at another team they belong to. Returns the
 * full re-scoped principal (same shape as GET /v1/me); 403 not_team_member
 * when the caller has no active membership in the target team.
 */
export function switchCurrentTeam(teamId: string, idempotencyKey: string): Promise<HumanMe> {
  return humanFetch<HumanMe>("/v1/me/current-team", {
    method: "POST",
    headers: { ...JSON_HEADERS, "Idempotency-Key": idempotencyKey },
    body: JSON.stringify({ team_id: teamId }),
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

export interface CreateDeviceEnrollmentInput {
  device_name: string;
  expires_in_seconds?: number;
  grantable_permissions?: string[];
}

/**
 * Issue a one-time device enrollment token (doc section 5.9). The token is
 * returned exactly once and must be held in memory only. Like agent
 * enrollment creation: no Idempotency-Key, no automatic retry — on timeout
 * the user refreshes the Devices list to check for a produced device.
 */
export function createDeviceEnrollment(
  input: CreateDeviceEnrollmentInput,
): Promise<DeviceEnrollmentSecret> {
  return humanFetch<DeviceEnrollmentSecret>("/v1/me/device-enrollments", {
    method: "POST",
    headers: JSON_HEADERS,
    body: JSON.stringify(input),
  });
}

/**
 * Revoke a Device and cascade-revoke every live agent credential it minted
 * (doc section 5.10). Devices have no optimistic-lock field: this sends an
 * Idempotency-Key only, never resource_version / If-Match.
 */
export async function revokeDevice(
  credentialId: string,
  idempotencyKey: string,
): Promise<DeviceSummary> {
  const res = await humanFetch<{ device: DeviceSummary }>(
    `/v1/admin/devices/${encodeURIComponent(credentialId)}`,
    { method: "DELETE", headers: { "Idempotency-Key": idempotencyKey } },
  );
  return res.device;
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

// ---- Wiki ingestion ----
// WikiIngestionStatus is shared with the read side and lives in ./wiki.

export function setWikiAutoInject(enabled: boolean): Promise<WikiIngestionStatus> {
  return humanFetch<WikiIngestionStatus>("/v1/wiki/ingestion", {
    method: "PUT",
    headers: JSON_HEADERS,
    body: JSON.stringify({ auto_inject: enabled }),
  });
}

export function updateWikiSettings(
  settings: WikiGenerationSettings,
): Promise<WikiGenerationSettings> {
  return humanFetch<WikiGenerationSettings>("/v1/wiki/settings", {
    method: "PUT",
    headers: JSON_HEADERS,
    body: JSON.stringify(settings),
  });
}

export function injectWikiSession(
  sessionId: string,
  idempotencyKey: string,
): Promise<{ processed_streams: number }> {
  return humanFetch<{ processed_streams: number }>(
    `/v1/wiki/sessions/${encodeURIComponent(sessionId)}/inject`,
    {
      method: "POST",
      headers: { "Idempotency-Key": idempotencyKey },
    },
  );
}

export function rebuildWiki(
  idempotencyKey: string,
  since?: string,
): Promise<WikiIngestionStatus> {
  return humanFetch<WikiIngestionStatus>("/v1/wiki/rebuild", {
    method: "POST",
    headers: { ...JSON_HEADERS, "Idempotency-Key": idempotencyKey },
    body: JSON.stringify(since ? { since } : {}),
  });
}

// ---- Todos ----
// Todo/suggestion transitions (create, complete, accept, dismiss) are state
// machines on the backend, but not uniformly idempotent: complete is a
// no-op when repeated, while accept/dismiss reject repeats against an
// already-transitioned record with 409 invalid_transition, which the UI
// surfaces after a refetch. Either way a retry is safe to send as-is, so
// unlike the mutations above, none of these send an Idempotency-Key or
// If-Match.

export function createTodo(title: string, body: string): Promise<TodoItem> {
  return humanFetch<TodoItem>("/v1/todo/todos", {
    method: "POST",
    headers: JSON_HEADERS,
    body: JSON.stringify({ title, body }),
  });
}

export function completeTodo(todoId: string): Promise<TodoItem> {
  return humanFetch<TodoItem>(`/v1/todo/todos/${encodeURIComponent(todoId)}/complete`, {
    method: "POST",
  });
}

export function refreshTodoSuggestions(): Promise<{ created: number }> {
  return humanFetch<{ created: number }>("/v1/todo/suggestions/refresh", { method: "POST" });
}

export function acceptTodoSuggestion(suggestionId: string): Promise<TodoItem> {
  return humanFetch<TodoItem>(
    `/v1/todo/suggestions/${encodeURIComponent(suggestionId)}/accept`,
    { method: "POST" },
  );
}

export function dismissTodoSuggestion(suggestionId: string): Promise<void> {
  return humanFetch<void>(
    `/v1/todo/suggestions/${encodeURIComponent(suggestionId)}/dismiss`,
    { method: "POST" },
  );
}
