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
  // Server-issued capabilities (operations doc section 2.1); rolling upgrades
  // may omit the field, consumers must treat that as an empty list.
  capabilities: string[];
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

// ---- Operations console DTOs (operations doc section 5) ----
// Field names match the Hertz JSON output exactly; Thrift `optional` fields
// may be absent entirely, not just null.

export type OperationKind =
  | "observation.observe"
  | "memory.search"
  | "memory.get"
  | "team_note.recall"
  | "extraction.run"
  | "channel.send"
  | "channel.accept"
  | "channel.archive"
  | "system.retention";

export type OperationOutcome =
  | "succeeded"
  | "rejected"
  | "failed"
  | "timed_out"
  | "cancelled";

export interface OperationsSummary {
  from_time: string;
  to_time: string;
  generated_at: string;
  observations: {
    requests: number;
    succeeded: number;
    input_events: number;
    events_written: number;
    duplicate_events: number;
  };
  extraction: {
    runs: number;
    completed: number;
    quarantined: number;
    failed: number;
    admitted_revisions: number;
    unextracted_events: number;
    oldest_unextracted_at?: string;
  };
  recalls: {
    requests: number;
    succeeded: number;
    with_evidence: number;
    empty: number;
    memory_hits: number;
    team_notes_delivered: number;
    memory_search_requests: number;
    memory_get_requests: number;
    team_note_recall_requests: number;
    evidence_hits: number;
    hint_hits: number;
    reference_hits: number;
  };
  latency: {
    sample_count: number;
    p50_ms?: number;
    p95_ms?: number;
  };
  errors: number;
}

export interface OperationEvent {
  operation_event_id: number;
  attempt_id: string;
  operation_kind: OperationKind;
  outcome: OperationOutcome;
  actor_user_id?: string;
  actor_membership_id?: string;
  actor_agent_id?: string;
  session_id?: string;
  started_at: string;
  completed_at: string;
  duration_ms: number;
  input_items: number;
  accepted_items: number;
  duplicate_items: number;
  result_items: number;
  delivered_items: number;
  evidence_items: number;
  hint_items: number;
  reference_items: number;
  input_tokens?: number;
  output_tokens?: number;
  detail_kind?: string;
  detail_id?: string;
  error_code?: string;
}

export interface RecallDiagnostic {
  observation_id: number;
  occurred_at: string;
  agent_id: string;
  session_id: string;
  duration_ms: number;
  token_budget: number;
  max_items: number;
  evidence_sufficient: boolean;
  reason_codes: string[];
  lanes_executed: string[];
  candidates: number;
  fusion_kept: number;
  planned_notes: number;
  planned_tokens: number;
  delivered_items: number;
  disposition_counts: Record<string, number>;
  rejection_counts: Record<string, number>;
  budget_drop_counts: Record<string, number>;
  hard_gate_failure_counts: Record<string, number>;
}

export interface StorageComponent {
  component: string;
  counts: Record<string, number>;
  logical_bytes: number;
  physical_bytes: number;
  estimated_reclaimable_bytes?: number;
  oldest_at?: string;
  newest_at?: string;
}

export interface OperationsStorageSnapshot {
  snapshot_id: number;
  schema_version: number;
  captured_at: string;
  status: "complete" | "partial" | string;
  warning_codes: string[];
  database_physical_bytes: number;
  other_physical_bytes: number;
  components: StorageComponent[];
}
