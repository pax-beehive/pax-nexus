// Read-side API wrappers (GET only). All list endpoints treat `cursor` as an
// opaque string passed back verbatim (doc section 3.4).

import { humanFetch } from "./client";
import type {
  AgentProfile,
  AuditEvent,
  CredentialMetadata,
  EnrollmentMetadata,
  HumanMe,
  Invitation,
  Member,
} from "./types";
import type { AgentScope } from "./actions";

export interface Page<T> {
  items: T[];
  nextCursor?: string;
}

export interface ListParams {
  cursor?: string;
  limit?: number;
}

function query(params: Record<string, string | number | undefined>): string {
  const qs = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== "") qs.set(key, String(value));
  }
  const s = qs.toString();
  return s ? `?${s}` : "";
}

function agentBase(scope: AgentScope, agentId: string): string {
  return `/v1/${scope}/agents/${encodeURIComponent(agentId)}`;
}

export function getMe(): Promise<HumanMe> {
  return humanFetch<HumanMe>("/v1/me");
}

// ---- My Agents ----

export async function listMyAgents(
  params: ListParams & { status?: string },
): Promise<Page<AgentProfile>> {
  const res = await humanFetch<{ agents: AgentProfile[]; next_cursor?: string }>(
    `/v1/me/agents${query({ status: params.status, limit: params.limit, cursor: params.cursor })}`,
  );
  return { items: res.agents, nextCursor: res.next_cursor };
}

export async function getOwnAgent(agentId: string): Promise<AgentProfile> {
  const res = await humanFetch<{ agent: AgentProfile }>(agentBase("me", agentId));
  return res.agent;
}

// ---- Enrollments / Credentials (own or admin scope) ----

export async function listEnrollments(
  scope: AgentScope,
  agentId: string,
  params: ListParams & { status?: string },
): Promise<Page<EnrollmentMetadata>> {
  const res = await humanFetch<{ enrollments: EnrollmentMetadata[]; next_cursor?: string }>(
    `${agentBase(scope, agentId)}/enrollments${query({ status: params.status, limit: params.limit, cursor: params.cursor })}`,
  );
  return { items: res.enrollments, nextCursor: res.next_cursor };
}

export async function listCredentials(
  scope: AgentScope,
  agentId: string,
  params: ListParams & { status?: string },
): Promise<Page<CredentialMetadata>> {
  const res = await humanFetch<{ credentials: CredentialMetadata[]; next_cursor?: string }>(
    `${agentBase(scope, agentId)}/credentials${query({ status: params.status, limit: params.limit, cursor: params.cursor })}`,
  );
  return { items: res.credentials, nextCursor: res.next_cursor };
}

// ---- Admin: members / invitations / agents / audit ----

export async function listMembers(
  params: ListParams & { role?: string; status?: string },
): Promise<Page<Member>> {
  const res = await humanFetch<{ members: Member[]; next_cursor?: string }>(
    `/v1/admin/members${query({ role: params.role, status: params.status, limit: params.limit, cursor: params.cursor })}`,
  );
  return { items: res.members, nextCursor: res.next_cursor };
}

export async function listAllMembers(params: Omit<ListParams, "cursor"> = {}): Promise<Member[]> {
  const members: Member[] = [];
  const seenCursors = new Set<string>();
  let cursor: string | undefined;
  do {
    const page = await listMembers({ ...params, cursor });
    members.push(...page.items);
    cursor = page.nextCursor;
    if (cursor && seenCursors.has(cursor)) {
      throw new Error("Member pagination returned a repeated cursor");
    }
    if (cursor) seenCursors.add(cursor);
  } while (cursor);
  return members;
}

export async function getMember(membershipId: string): Promise<Member> {
  const res = await humanFetch<{ member: Member }>(
    `/v1/admin/members/${encodeURIComponent(membershipId)}`,
  );
  return res.member;
}

export async function listInvitations(
  params: ListParams & { status?: string },
): Promise<Page<Invitation>> {
  const res = await humanFetch<{ invitations: Invitation[]; next_cursor?: string }>(
    `/v1/admin/invitations${query({ status: params.status, limit: params.limit, cursor: params.cursor })}`,
  );
  return { items: res.invitations, nextCursor: res.next_cursor };
}

export async function listAdminAgents(
  params: ListParams & { owner_membership_id?: string; status?: string; q?: string },
): Promise<Page<AgentProfile>> {
  const res = await humanFetch<{ agents: AgentProfile[]; next_cursor?: string }>(
    `/v1/admin/agents${query({
      owner_membership_id: params.owner_membership_id,
      status: params.status,
      q: params.q,
      limit: params.limit,
      cursor: params.cursor,
    })}`,
  );
  return { items: res.agents, nextCursor: res.next_cursor };
}

export async function getAdminAgent(agentId: string): Promise<AgentProfile> {
  const res = await humanFetch<{ agent: AgentProfile }>(agentBase("admin", agentId));
  return res.agent;
}

export async function listAuditEvents(
  params: ListParams & {
    actor_kind?: string;
    action?: string;
    target_kind?: string;
    target_id?: string;
  },
): Promise<Page<AuditEvent>> {
  const res = await humanFetch<{ audit_events: AuditEvent[]; next_cursor?: string }>(
    `/v1/admin/audit-events${query({
      actor_kind: params.actor_kind,
      action: params.action,
      target_kind: params.target_kind,
      target_id: params.target_id,
      limit: params.limit,
      cursor: params.cursor,
    })}`,
  );
  return { items: res.audit_events, nextCursor: res.next_cursor };
}

export async function getAuditEvent(auditEventId: number): Promise<AuditEvent> {
  const res = await humanFetch<{ audit_event: AuditEvent }>(
    `/v1/admin/audit-events/${encodeURIComponent(auditEventId)}`,
  );
  return res.audit_event;
}

// ---- Admin: operations console (read-only, operations doc section 6) ----
// Kept in a separate import so concurrent edits to the sections above never
// conflict with this block.

import type {
  OperationEvent,
  OperationKind,
  OperationOutcome,
  OperationsStorageSnapshot,
  OperationsSummary,
  RecallDiagnostic,
} from "./types";

export interface OperationsTimeFilter {
  from?: string;
  to?: string;
  agent_id?: string;
}

export interface OperationEventFilter extends OperationsTimeFilter {
  operation_kind?: OperationKind;
  outcome?: OperationOutcome;
  limit?: number;
  cursor?: string;
}

export async function getOperationsSummary(
  filter: OperationsTimeFilter,
  signal?: AbortSignal,
): Promise<OperationsSummary> {
  return humanFetch<OperationsSummary>(
    `/v1/admin/operations/summary${query({
      from: filter.from,
      to: filter.to,
      agent_id: filter.agent_id,
    })}`,
    { signal },
  );
}

export async function listOperationEvents(
  filter: OperationEventFilter,
  signal?: AbortSignal,
): Promise<{ items: OperationEvent[]; nextCursor?: string; generatedAt: string }> {
  const res = await humanFetch<{
    events: OperationEvent[];
    next_cursor?: string;
    generated_at: string;
  }>(
    `/v1/admin/operations/events${query({
      from: filter.from,
      to: filter.to,
      agent_id: filter.agent_id,
      operation_kind: filter.operation_kind,
      outcome: filter.outcome,
      limit: filter.limit,
      cursor: filter.cursor,
    })}`,
    { signal },
  );
  return { items: res.events, nextCursor: res.next_cursor, generatedAt: res.generated_at };
}

export async function getRecallDiagnostic(
  observationId: number,
  signal?: AbortSignal,
): Promise<RecallDiagnostic> {
  const res = await humanFetch<{ recall: RecallDiagnostic }>(
    `/v1/admin/operations/recalls/${encodeURIComponent(String(observationId))}`,
    { signal },
  );
  return res.recall;
}

export async function getOperationsStorage(
  signal?: AbortSignal,
): Promise<OperationsStorageSnapshot> {
  const res = await humanFetch<{ storage: OperationsStorageSnapshot }>(
    "/v1/admin/operations/storage",
    { signal },
  );
  return res.storage;
}

export async function listOperationsStorageHistory(
  filter: { from?: string; to?: string; limit?: number; cursor?: string },
  signal?: AbortSignal,
): Promise<{ items: OperationsStorageSnapshot[]; nextCursor?: string }> {
  const res = await humanFetch<{
    snapshots: OperationsStorageSnapshot[];
    next_cursor?: string;
  }>(
    `/v1/admin/operations/storage/history${query({
      from: filter.from,
      to: filter.to,
      limit: filter.limit,
      cursor: filter.cursor,
    })}`,
    { signal },
  );
  return { items: res.snapshots, nextCursor: res.next_cursor };
}
