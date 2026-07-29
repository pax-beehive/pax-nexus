// Types mirror idl/team_memory.thrift exactly; field names are the JSON names
// produced by the Hertz Thrift binding.

/** Cursor-paginated list response shared by all list endpoints (doc section 3.4). */
export interface Page<T> {
  items: T[];
  nextCursor?: string;
}

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
  /**
   * Present only when a Device self-registered this agent through
   * POST /v1/device/agent-provisions (doc section 8.1); the value is the
   * provisioning Device credential_id. Human-registered agents omit the
   * field entirely, so consumers must test presence, not truthiness.
   */
  provisioned_by?: string;
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

// ---- Device-scoped agent provisioning (doc sections 8.4-8.6) ----

export type DeviceStatus = "active" | "revoked";

/**
 * POST /v1/me/device-enrollments response: the one-time device enrollment
 * secret, returned exactly once. `grantable_permissions` is the effective
 * ceiling resolved by the backend; the Device Credential itself always has
 * exactly the `agent_provision` permission.
 */
export interface DeviceEnrollmentSecret {
  enrollment_id: string;
  token: string;
  expires_at: string;
  device_name: string;
  grantable_permissions: string[];
}

/** DeviceSummary (doc section 8.5); `status` is server-derived. */
export interface DeviceSummary {
  credential_id: string;
  device_name: string;
  created_by_user_id: string;
  created_by_membership_id: string;
  status: DeviceStatus;
  provisioned_agent_count: number;
  created_at: string;
  revoked_at?: string;
  last_used_at?: string;
  grantable_permissions: string[];
}

/**
 * One credential-history row minted by a Device (doc section 8.6). The same
 * agent_id may appear multiple times (rotation history); `agent_status` is
 * the Agent Profile state, independent of whether this credential row is
 * revoked.
 */
export interface DeviceProvisionedAgent {
  agent_id: string;
  display_name: string;
  agent_type: string;
  agent_status: AgentStatus;
  credential_id: string;
  created_at: string;
  revoked_at?: string;
  last_used_at?: string;
}

/** GET /v1/admin/devices/:credential_id response. */
export interface DeviceDetail {
  device: DeviceSummary;
  agents: DeviceProvisionedAgent[];
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

/** A note summary inside OperationsAgentStats.recent_notes (max 5 per agent). */
export interface OperationsAgentNote {
  note_id: string;
  kind: string;
  subject: string;
  created_at: string;
}

/** Per-agent activity aggregate from GET /v1/admin/operations/agents. */
export interface OperationsAgentStats {
  agent_id: string;
  display_name: string;
  observation_requests: number;
  events_written: number;
  extraction_runs: number;
  extraction_input_tokens: number;
  extraction_output_tokens: number;
  recall_requests: number;
  recall_delivered_items: number;
  recall_empty: number;
  channel_sent: number;
  channel_received_accepted: number;
  notes_authored: number;
  recent_notes: OperationsAgentNote[];
  /** RFC3339; empty string when the agent has no recorded activity. */
  last_active_at: string;
}

export interface OperationsAgentStatsResponse {
  agents: OperationsAgentStats[];
  from_time: string;
  to_time: string;
  generated_at: string;
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

// ---- Owner-only Team Memory Explorer ----

export interface TeamNoteSummary {
  note_id: string;
  kind: string;
  subject: string;
  state: string;
  task_ref?: string;
  thread_ref?: string;
  origin_agent_id: string;
  audience_agent_ids: string[];
  revision: number;
  created_at: string;
  updated_at: string;
  soft_expires_at: string;
  hard_expires_at: string;
}

export interface ExplorerTeamNote {
  summary: TeamNoteSummary;
  body: string;
  origin_user_id: string;
  origin_session_id: string;
  related_subjects: string[];
  valid_at?: string;
  invalid_at?: string;
  source_occurred_at?: string;
}

export interface ExplorerSourceEvent {
  event_id: string;
  user_id: string;
  agent_id: string;
  session_id: string;
  sequence: number;
  type: string;
  content: string;
  task_ref?: string;
  thread_ref?: string;
  visibility: string;
  occurred_at: string;
  captured_at: string;
  extracted_at?: string;
}

export interface ExplorerExtractionRun {
  run_id: string;
  user_id: string;
  agent_id: string;
  session_id: string;
  from_sequence: number;
  to_sequence: number;
  model: string;
  prompt_version: string;
  status: string;
  input_tokens: number;
  output_tokens: number;
  error_code?: string;
  created_at: string;
  completed_at?: string;
}

export interface ExplorerCandidate {
  candidate_id: string;
  action: string;
  kind: string;
  subject: string;
  body: string;
  task_ref?: string;
  thread_ref?: string;
  origin_agent_id: string;
  evidence_event_ids: string[];
  admission_status: string;
  rejection_reason?: string;
  created_at: string;
  resulting_note_id?: string;
}

export interface ExplorerDelivery {
  recipient_user_id: string;
  recipient_agent_id: string;
  recipient_session_id: string;
  delivered_at: string;
  context_tokens: number;
}

export interface ExplorerRevision {
  revision: number;
  candidate_id: string;
  operation: string;
  body: string;
  related_subjects: string[];
  valid_at?: string;
  invalid_at?: string;
  created_at: string;
  expired_at?: string;
  extraction: ExplorerExtractionRun;
  evidence: ExplorerSourceEvent[];
  deliveries: ExplorerDelivery[];
  candidate: ExplorerCandidate;
}

export interface ExplorerRecallUse {
  observation_id: number;
  recipient_agent_id: string;
  recipient_session_id: string;
  occurred_at: string;
  disposition?: string;
  delivered: boolean;
  rejection_reasons: string[];
  budget_drop_reasons: string[];
  hard_gate_failures: string[];
}

export interface TeamNoteDetail {
  note: ExplorerTeamNote;
  related_notes: TeamNoteSummary[];
  revisions: ExplorerRevision[];
  recall_observations: ExplorerRecallUse[];
}

export interface ExtractionDiagnostic {
  run: ExplorerExtractionRun;
  candidates: ExplorerCandidate[];
  source_events: ExplorerSourceEvent[];
  resulting_notes: TeamNoteSummary[];
}

export interface ChannelDiagnostic {
  envelope_id: string;
  from_agent_id: string;
  to_agent_id: string;
  status: string;
  message?: string;
  payload_status: "decoded" | "unavailable" | string;
  created_at: string;
  accepted_at?: string;
  archived_at?: string;
  capsule: {
    capsule_id?: string;
    source_session_id?: string;
    source_agent?: string;
    keyword?: string;
    title?: string;
    summary?: string;
    content?: string;
    status?: string;
    truncated?: boolean;
    route_match_type?: string;
  };
}
