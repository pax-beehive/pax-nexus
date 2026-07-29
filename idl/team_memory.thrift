namespace go teammemory.api

struct Actor {
  1: required string user_id (api.body="user_id")
  2: required string agent_id (api.body="agent_id")
  3: required string session_id (api.body="session_id")
}

struct SessionEvent {
  1: required string id (api.body="id")
  2: required Actor actor (api.body="actor")
  3: required i64 sequence (api.body="sequence")
  4: required string type (api.body="type")
  5: required string content (api.body="content")
  6: optional string task_ref (api.body="task_ref")
  7: optional string thread_ref (api.body="thread_ref")
  8: optional string visibility (api.body="visibility")
  9: required string occurred_at (api.body="occurred_at")
  10: optional map<string, string> metadata (api.body="metadata")
}

struct SessionBatch {
  1: required list<SessionEvent> events (api.body="events")
  2: required bool complete (api.body="complete")
}

struct StreamAuthor {
  1: required string kind (api.body="kind")
  2: required string native_id (api.body="native_id")
  3: optional string user_id (api.body="user_id")
}

struct StreamEvent {
  1: required string id (api.body="id")
  2: required string source (api.body="source")
  3: required string stream_id (api.body="stream_id")
  4: required StreamAuthor author (api.body="author")
  5: required string kind (api.body="kind")
  6: required string type (api.body="type")
  7: required string content (api.body="content")
  8: optional string thread_ref (api.body="thread_ref")
  9: required string visibility (api.body="visibility")
  10: required string occurred_at (api.body="occurred_at")
  11: optional map<string, string> metadata (api.body="metadata")
}

struct StreamBatch {
  1: required list<StreamEvent> events (api.body="events")
  2: required bool complete (api.body="complete")
}

struct IngestReceipt {
  1: required i32 accepted
  2: required i32 duplicate
  3: required i64 cursor
  4: optional string run_id
}

struct RecallRequest {
  1: required Actor actor (api.body="actor")
  2: optional string task_ref (api.body="task_ref")
  3: optional string thread_ref (api.body="thread_ref")
  4: required i32 token_budget (api.body="token_budget")
  5: optional string query (api.body="query")
  6: optional i32 max_items (api.body="max_items")
}

struct RecalledNote {
  1: required string note_id
  2: required i32 revision
  3: required string text
  4: required Actor origin
  5: required double relevance
  6: required string certainty
}

struct RecallDecisionSummary {
  1: required bool evidence_sufficient
  2: optional list<string> reason_codes
}

struct NoteEnvelope {
  1: required string revision
  2: required list<string> items
  3: required i32 tokens
  4: optional list<RecalledNote> details
  5: optional RecallDecisionSummary decision
}

struct HealthRequest {}

struct HealthResponse {
  1: required string status
}

struct AgentEnrollmentRequest {
  1: required string user_id (api.body="user_id")
  2: required string agent_id (api.body="agent_id")
  3: optional i64 expires_in_seconds (api.body="expires_in_seconds")
  4: optional list<string> permissions (api.body="permissions")
}

struct AgentEnrollmentResponse {
  1: required string enrollment_id
  2: required string token
  3: required string expires_at
}

struct ExchangeEnrollmentRequest {
  1: required string token (api.body="token")
}

struct AgentCredentialResponse {
  1: required string credential_id
  2: required string api_key
  3: optional string expires_at
  4: optional string kind
  5: required string user_id
  6: required list<string> permissions
}

struct AgentIdentityRequest {}

struct AgentIdentityResponse {
  1: required string user_id
  2: required string agent_id
  3: required string credential_id
  4: required list<string> permissions
}

struct RotateAgentCredentialRequest {}

struct RevokeAgentCredentialRequest {
  1: required string credential_id (api.path="credential_id")
}

struct RevokeAgentCredentialResponse {
  1: required bool revoked
}

struct ObservationEvent {
  1: required string id (api.body="id")
  2: required i64 sequence (api.body="sequence")
  3: required string type (api.body="type")
  4: required string content (api.body="content")
  5: optional string task_ref (api.body="task_ref")
  6: optional string thread_ref (api.body="thread_ref")
  7: optional string visibility (api.body="visibility")
  8: required string occurred_at (api.body="occurred_at")
  9: optional map<string, string> metadata (api.body="metadata")
}

struct ObservationBatch {
  1: required string session_id (api.body="session_id")
  2: required list<ObservationEvent> events (api.body="events")
  3: required bool complete (api.body="complete")
  4: optional string idempotency_key (api.body="idempotency_key")
}

struct ObservationReceipt {
  1: required i32 accepted
  2: required i32 duplicate
  3: required i64 cursor
  4: optional string run_id
  5: optional string idempotency_key
  6: required string status
}

struct MemorySearchRequest {
  1: required string intent (api.body="intent")
  2: optional string source (api.body="source")
  3: required string session_id (api.body="session_id")
  4: optional string task_ref (api.body="task_ref")
  5: optional string thread_ref (api.body="thread_ref")
  6: required string query (api.body="query")
  7: required i32 token_budget (api.body="token_budget")
  8: optional i32 max_items (api.body="max_items")
}

struct MemoryHit {
  1: optional string ref
  2: required string text
  3: required double score
  4: required i32 tokens
  5: required string disposition
  6: optional map<string, string> metadata
}

struct RecallPathTrace {
  1: required string status
  2: required i64 duration_ms
  3: required i32 candidates
  4: required i32 budget_drops
  5: optional string error
  6: optional string reason
  7: optional list<string> reason_codes
}

struct MemorySearchTrace {
  1: required bool early_return
  2: required RecallPathTrace team_note
  3: required RecallPathTrace wiki_hint
  4: required RecallPathTrace wiki_search
}

struct MemorySearchResponse {
  1: required list<MemoryHit> hits
  2: required bool evidence_sufficient
  3: required MemorySearchTrace trace
}

struct MemoryGetRequest {
  1: required string session_id (api.body="session_id")
  2: required string ref (api.body="ref")
}

struct MemoryDocument {
  1: required string ref
  2: required string text
  3: required i32 tokens
  4: optional map<string, string> provenance
}

struct KnowledgeCapsuleEnvelopeCapsule {
  1: required string capsule_id (api.body="capsule_id")
  2: optional string source_node_id (api.body="source_node_id")
  3: required string source_session_id (api.body="source_session_id")
  4: required string source_agent (api.body="source_agent")
  5: required string keyword (api.body="keyword")
  6: required string title (api.body="title")
  7: required string summary (api.body="summary")
  8: required string content (api.body="content")
  9: required string status (api.body="status")
  10: required bool truncated (api.body="truncated")
  11: required i64 original_estimated_chars (api.body="original_estimated_chars")
  12: optional string created_at (api.body="created_at")
  13: optional string archived_at (api.body="archived_at")
}

struct KnowledgeCapsuleEnvelopeRoute {
  1: required string match_type (api.body="match_type")
  2: optional string match_value (api.body="match_value")
  3: optional string target_agent (api.body="target_agent")
}

struct KnowledgeCapsuleEnvelopePayload {
  1: required string schema_version (api.body="schema_version")
  2: required KnowledgeCapsuleEnvelopeCapsule capsule (api.body="capsule")
  3: optional KnowledgeCapsuleEnvelopeRoute route (api.body="route")
}

struct SendChannelEnvelopeRequest {
  1: required string to_agent_id (api.body="to_agent_id")
  2: required string payload_type (api.body="payload_type")
  3: required KnowledgeCapsuleEnvelopePayload payload_json (api.body="payload_json")
  4: optional string message (api.body="message")
  5: required string idempotency_key (api.body="idempotency_key")
}

struct ListChannelEnvelopesRequest {
  1: optional string status (api.query="status")
  2: optional string direction (api.query="direction")
  3: optional i32 limit (api.query="limit")
  4: optional string cursor (api.query="cursor")
}

struct ChannelEnvelopeByIDRequest {
  1: required string envelope_id (api.path="envelope_id")
}

struct ChannelEnvelope {
  1: required string envelope_id
  2: required string from_user_id
  3: required string from_agent_id
  4: required string to_user_id
  5: required string to_agent_id
  6: required string payload_type
  7: required KnowledgeCapsuleEnvelopePayload payload_json
  8: optional string message
  9: required string idempotency_key
  10: required string status
  11: required string created_at
  12: optional string accepted_at
  13: optional string archived_at
}

struct ChannelEnvelopeResponse {
  1: required ChannelEnvelope envelope
}

struct ListChannelEnvelopesResponse {
  1: required list<ChannelEnvelope> envelopes
}

struct AuthLoginRequest {}

struct AuthCallbackRequest {
  1: required string code (api.query="code")
  2: required string state (api.query="state")
}

struct AuthLogoutRequest {}

struct EmptyResponse {
  1: required bool ok
}

struct ErrorResponse {
  1: required string code
  2: required string message
}

struct BootstrapClaimRequest {}

struct HumanMeRequest {}

struct HumanMeResponse {
  1: required string user_id
  2: optional string email
  3: required bool email_verified
  4: optional string membership_id
  5: optional string role
  6: optional string membership_status
  7: required list<string> capabilities
}

struct WikiIngestionStatusRequest {}

struct UpdateWikiIngestionRequest {
  1: required bool auto_inject (api.body="auto_inject")
}

struct WikiIngestionStatusResponse {
  1: required bool auto_inject
}

struct InjectWikiSessionRequest {
  1: required string session_id (api.path="session_id")
}

struct InjectWikiSessionResponse {
  1: required i32 processed_streams
}

struct RebuildWikiRequest {}

struct RebuildWikiResponse {
  1: required bool auto_inject
}

struct ListMembersRequest {
  1: optional string role (api.query="role")
  2: optional string status (api.query="status")
  3: optional i32 limit (api.query="limit")
  4: optional string cursor (api.query="cursor")
}

struct MemberByIDRequest {
  1: required string membership_id (api.path="membership_id")
}

struct UpdateMemberRequest {
  1: required string membership_id (api.path="membership_id")
  2: optional string role (api.body="role")
  3: optional string status (api.body="status")
  4: required i64 resource_version (api.body="resource_version")
}

struct Member {
  1: required string membership_id
  2: required string user_id
  3: optional string email
  4: required bool email_verified
  5: required string display_name
  6: required string role
  7: required string status
  8: required string joined_at
  9: required string updated_at
  10: required i64 resource_version
}

struct MemberResponse {
  1: required Member member
}

struct ListMembersResponse {
  1: required list<Member> members
  2: optional string next_cursor
}

struct ListAuditEventsRequest {
  1: optional string actor_kind (api.query="actor_kind")
  2: optional string action (api.query="action")
  3: optional string target_kind (api.query="target_kind")
  4: optional string target_id (api.query="target_id")
  5: optional i32 limit (api.query="limit")
  6: optional string cursor (api.query="cursor")
}

struct AuditEventByIDRequest {
  1: required i64 audit_event_id (api.path="audit_event_id")
}

struct AuditEvent {
  1: required i64 audit_event_id
  2: required string actor_kind
  3: optional string actor_user_id
  4: optional string actor_membership_id
  5: optional string actor_agent_id
  6: optional string actor_credential_id
  7: required string action
  8: required string target_kind
  9: required string target_id
  10: required string occurred_at
}

struct AuditEventResponse {
  1: required AuditEvent audit_event
}

struct ListAuditEventsResponse {
  1: required list<AuditEvent> audit_events
  2: optional string next_cursor
}

struct CreateInvitationRequest {
  1: required string target_email (api.body="target_email")
  2: required string role (api.body="role")
  3: optional i64 expires_in_seconds (api.body="expires_in_seconds")
}

struct InvitationResponse {
  1: required string invitation_id
  2: optional string token
  3: required string target_email
  4: required string role
  5: required string status
  6: required string created_at
  7: required string expires_at
}

struct ListInvitationsRequest {
  1: optional string status (api.query="status")
  2: optional i32 limit (api.query="limit")
  3: optional string cursor (api.query="cursor")
}

struct ListInvitationsResponse {
  1: required list<InvitationResponse> invitations
  2: optional string next_cursor
}

struct InvitationByIDRequest {
  1: required string invitation_id (api.path="invitation_id")
}

struct AcceptInvitationRequest {
  1: required string token (api.body="token")
}

struct CreateAgentProfileRequest {
  1: required string agent_id (api.body="agent_id")
  2: required string display_name (api.body="display_name")
  3: optional string description (api.body="description")
  4: optional string agent_type (api.body="agent_type")
  5: optional bool directory_visible (api.body="directory_visible")
}

struct UpdateAgentProfileRequest {
  1: required string agent_id (api.path="agent_id")
  2: optional string display_name (api.body="display_name")
  3: optional string description (api.body="description")
  4: optional string agent_type (api.body="agent_type")
  5: optional bool directory_visible (api.body="directory_visible")
  6: optional string status (api.body="status")
  7: required i64 resource_version (api.body="resource_version")
}

struct AgentProfileByIDRequest {
  1: required string agent_id (api.path="agent_id")
}

struct RetireAgentProfileRequest {
  1: required string agent_id (api.path="agent_id")
  2: required i64 resource_version (api.query="resource_version")
}

struct TransferAgentRequest {
  1: required string agent_id (api.path="agent_id")
  2: required string target_membership_id (api.body="target_membership_id")
  3: required i64 resource_version (api.body="resource_version")
}

struct ListAgentProfilesRequest {
  1: optional string status (api.query="status")
  2: optional i32 limit (api.query="limit")
  3: optional string cursor (api.query="cursor")
}

struct AgentProfile {
  1: required string agent_id
  2: required string display_name
  3: required string description
  4: required string agent_type
  5: required string status
  6: required bool directory_visible
  7: required string created_at
  8: required string updated_at
  9: optional string retired_at
  10: required i64 resource_version
  11: optional string owner_membership_id
  12: optional string owner_user_id
  13: optional string provisioned_by
}

struct AgentProfileResponse {
  1: required AgentProfile agent
}

struct ListAgentProfilesResponse {
  1: required list<AgentProfile> agents
  2: optional string next_cursor
}

struct CreateOwnedEnrollmentRequest {
  1: required string agent_id (api.path="agent_id")
  2: required string credential_label (api.body="credential_label")
  3: required list<string> permissions (api.body="permissions")
  4: optional i64 expires_in_seconds (api.body="expires_in_seconds")
  5: optional string credential_expires_at (api.body="credential_expires_at")
}

struct ListAgentArtifactsRequest {
  1: required string agent_id (api.path="agent_id")
  2: optional string status (api.query="status")
  3: optional i32 limit (api.query="limit")
  4: optional string cursor (api.query="cursor")
}

struct AgentEnrollmentByIDRequest {
  1: required string agent_id (api.path="agent_id")
  2: required string enrollment_id (api.path="enrollment_id")
}

struct AgentCredentialByIDRequest {
  1: required string agent_id (api.path="agent_id")
  2: required string credential_id (api.path="credential_id")
}

struct AgentEnrollmentMetadata {
  1: required string enrollment_id
  2: required string agent_id
  3: required string credential_label
  4: required list<string> permissions
  5: required string status
  6: required string created_at
  7: required string expires_at
  8: optional string credential_expires_at
}

struct AgentEnrollmentMetadataResponse {
  1: required AgentEnrollmentMetadata enrollment
}

struct ListAgentEnrollmentsResponse {
  1: required list<AgentEnrollmentMetadata> enrollments
  2: optional string next_cursor
}

struct AgentCredentialMetadata {
  1: required string credential_id
  2: required string agent_id
  3: required string label
  4: required list<string> permissions
  5: required string created_at
  6: optional string expires_at
  7: optional string revoked_at
  8: optional string last_used_at
}

struct AgentCredentialMetadataResponse {
  1: required AgentCredentialMetadata credential
}

struct ListAgentCredentialsResponse {
  1: required list<AgentCredentialMetadata> credentials
  2: optional string next_cursor
}

struct ListDirectoryAgentsRequest {
  1: optional string q (api.query="q")
  2: optional i32 limit (api.query="limit")
  3: optional string cursor (api.query="cursor")
}

struct DirectoryAgentByIDRequest {
  1: required string agent_id (api.path="agent_id")
}

struct DirectoryAgent {
  1: required string agent_id
  2: required string display_name
  3: required string description
  4: required string agent_type
}

struct DirectoryAgentResponse {
  1: required DirectoryAgent agent
}

struct ListDirectoryAgentsResponse {
  1: required list<DirectoryAgent> agents
  2: optional string next_cursor
}

struct ListAdminAgentsRequest {
  1: optional string owner_membership_id (api.query="owner_membership_id")
  2: optional string status (api.query="status")
  3: optional string q (api.query="q")
  4: optional i32 limit (api.query="limit")
  5: optional string cursor (api.query="cursor")
}

struct AdminAgentByIDRequest {
  1: required string agent_id (api.path="agent_id")
}

struct CreateDeviceEnrollmentRequest {
  1: required string device_name (api.body="device_name")
  2: optional list<string> grantable_permissions (api.body="grantable_permissions")
  3: optional i64 expires_in_seconds (api.body="expires_in_seconds")
}

struct DeviceEnrollmentResponse {
  1: required string enrollment_id
  2: required string token
  3: required string expires_at
  4: required string device_name
  5: required list<string> grantable_permissions
}

struct ProvisionDeviceAgentRequest {
  1: required string agent_id (api.body="agent_id")
  2: required string display_name (api.body="display_name")
  3: required string agent_type (api.body="agent_type")
  4: optional list<string> permissions (api.body="permissions")
}

struct ProvisionDeviceAgentResponse {
  1: required string credential_id
  2: required string api_key
  3: required string agent_id
  4: required list<string> permissions
  5: required string created_at
  6: optional string expires_at
  7: optional string rotated_from_credential_id
  8: required bool agent_created
  9: required string user_id
}

struct ListDeviceProvisionsRequest {}

struct DeviceProvisionedAgent {
  1: required string agent_id
  2: required string display_name
  3: required string agent_type
  4: required string agent_status
  5: required string credential_id
  6: required string created_at
  7: optional string revoked_at
  8: optional string last_used_at
}

struct ListDeviceProvisionsResponse {
  1: required list<DeviceProvisionedAgent> agents
}

struct ListDevicesRequest {
  1: optional string status (api.query="status")
  2: optional i32 limit (api.query="limit")
  3: optional string cursor (api.query="cursor")
}

struct DeviceSummary {
  1: required string credential_id
  2: required string device_name
  3: required string created_by_user_id
  4: required string created_by_membership_id
  5: required string status
  6: required i64 provisioned_agent_count
  7: required string created_at
  8: optional string revoked_at
  9: optional string last_used_at
  10: required list<string> grantable_permissions
}

struct ListDevicesResponse {
  1: required list<DeviceSummary> devices
  2: optional string next_cursor
}

struct DeviceByIDRequest {
  1: required string credential_id (api.path="credential_id")
}

struct DeviceDetailResponse {
  1: required DeviceSummary device
  2: required list<DeviceProvisionedAgent> agents
}

struct DeviceSummaryResponse {
  1: required DeviceSummary device
}

struct OperationsSummaryRequest {
  1: optional string from_time (api.query="from")
  2: optional string to_time (api.query="to")
  3: optional string agent_id (api.query="agent_id")
}

struct OperationsLatency {
  1: required i64 sample_count
  2: optional i64 p50_ms
  3: optional i64 p95_ms
}

struct ObservationOperationsSummary {
  1: required i64 requests
  2: required i64 succeeded
  3: required i64 input_events
  4: required i64 events_written
  5: required i64 duplicate_events
}

struct ExtractionOperationsSummary {
  1: required i64 runs
  2: required i64 completed
  3: required i64 quarantined
  4: required i64 failed
  5: required i64 admitted_revisions
  6: required i64 unextracted_events
  7: optional string oldest_unextracted_at
}

struct RecallOperationsSummary {
  1: required i64 requests
  2: required i64 succeeded
  3: required i64 with_evidence
  4: required i64 empty
  5: required i64 memory_hits
  6: required i64 team_notes_delivered
  7: required i64 memory_search_requests
  8: required i64 memory_get_requests
  9: required i64 team_note_recall_requests
  10: required i64 evidence_hits
  11: required i64 hint_hits
  12: required i64 reference_hits
}

struct OperationsSummaryResponse {
  1: required string from_time
  2: required string to_time
  3: required string generated_at
  4: required ObservationOperationsSummary observations
  5: required ExtractionOperationsSummary extraction
  6: required RecallOperationsSummary recalls
  7: required OperationsLatency latency
  8: required i64 errors
}

struct ListOperationEventsRequest {
  1: optional string operation_kind (api.query="operation_kind")
  2: optional string outcome (api.query="outcome")
  3: optional string agent_id (api.query="agent_id")
  4: optional string from_time (api.query="from")
  5: optional string to_time (api.query="to")
  6: optional i32 limit (api.query="limit")
  7: optional string cursor (api.query="cursor")
}

struct OperationEvent {
  1: required i64 operation_event_id
  2: required string attempt_id
  3: required string operation_kind
  4: required string outcome
  5: optional string actor_user_id
  6: optional string actor_membership_id
  7: optional string actor_agent_id
  8: optional string session_id
  9: required string started_at
  10: required string completed_at
  11: required i64 duration_ms
  12: required i64 input_items
  13: required i64 accepted_items
  14: required i64 duplicate_items
  15: required i64 result_items
  16: required i64 delivered_items
  17: required i64 evidence_items
  18: required i64 hint_items
  19: required i64 reference_items
  20: optional i64 input_tokens
  21: optional i64 output_tokens
  22: optional string detail_kind
  23: optional string detail_id
  24: optional string error_code
}

struct ListOperationEventsResponse {
  1: required list<OperationEvent> events
  2: optional string next_cursor
  3: required string generated_at
}

struct RecallDiagnosticByIDRequest {
  1: required i64 observation_id (api.path="observation_id")
}

struct RecallDiagnostic {
  1: required i64 observation_id
  2: required string occurred_at
  3: required string agent_id
  4: required string session_id
  5: required i64 duration_ms
  6: required i64 token_budget
  7: required i64 max_items
  8: required bool evidence_sufficient
  9: required list<string> reason_codes
  10: required list<string> lanes_executed
  11: required i64 candidates
  12: required i64 fusion_kept
  13: required i64 planned_notes
  14: required i64 planned_tokens
  15: required i64 delivered_items
  16: required map<string, i64> disposition_counts
  17: required map<string, i64> rejection_counts
  18: required map<string, i64> budget_drop_counts
  19: required map<string, i64> hard_gate_failure_counts
}

struct RecallDiagnosticResponse {
  1: required RecallDiagnostic recall
}

struct OperationsStorageRequest {}

struct ListOperationsStorageHistoryRequest {
  1: optional string from_time (api.query="from")
  2: optional string to_time (api.query="to")
  3: optional i32 limit (api.query="limit")
  4: optional string cursor (api.query="cursor")
}

struct StorageComponent {
  1: required string component
  2: required map<string, i64> counts
  3: required i64 logical_bytes
  4: required i64 physical_bytes
  5: optional i64 estimated_reclaimable_bytes
  6: optional string oldest_at
  7: optional string newest_at
}

struct OperationsStorageSnapshot {
  1: required i64 snapshot_id
  2: required i32 schema_version
  3: required string captured_at
  4: required string status
  5: required list<string> warning_codes
  6: required i64 database_physical_bytes
  7: required i64 other_physical_bytes
  8: required list<StorageComponent> components
}

struct OperationsStorageResponse {
  1: required OperationsStorageSnapshot storage
}

struct ListOperationsStorageHistoryResponse {
  1: required list<OperationsStorageSnapshot> snapshots
  2: optional string next_cursor
}

struct OperationsAgentStatsRequest {
  1: optional string from_time (api.query="from")
  2: optional string to_time (api.query="to")
}

struct OperationsAgentRecentNote {
  1: required string note_id
  2: required string kind
  3: required string subject
  4: required string created_at
}

struct OperationsAgentStats {
  1: required string agent_id
  2: required string display_name
  3: required i64 observation_requests
  4: required i64 events_written
  5: required i64 extraction_runs
  6: required i64 extraction_input_tokens
  7: required i64 extraction_output_tokens
  8: required i64 recall_requests
  9: required i64 recall_delivered_items
  10: required i64 recall_empty
  11: required i64 channel_sent
  12: required i64 channel_received_accepted
  13: required i64 notes_authored
  14: required list<OperationsAgentRecentNote> recent_notes
  15: optional string last_active_at
}

struct OperationsAgentStatsResponse {
  1: required list<OperationsAgentStats> agents
  2: required string from_time
  3: required string to_time
  4: required string generated_at
}

struct ListTeamNotesRequest {
  1: optional string q (api.query="q")
  2: optional string kind (api.query="kind")
  3: optional string state (api.query="state")
  4: optional string agent_id (api.query="agent_id")
  5: optional string task_ref (api.query="task_ref")
  6: optional string thread_ref (api.query="thread_ref")
  7: optional i32 limit (api.query="limit")
  8: optional string cursor (api.query="cursor")
}

struct TeamNoteByIDRequest {
  1: required string note_id (api.path="note_id")
}

struct ExtractionDiagnosticByIDRequest {
  1: required string run_id (api.path="run_id")
}

struct ChannelDiagnosticByIDRequest {
  1: required string envelope_id (api.path="envelope_id")
}

struct ExplorerTeamNoteSummary {
  1: required string note_id
  2: required string kind
  3: required string subject
  4: required string state
  5: optional string task_ref
  6: optional string thread_ref
  7: required string origin_agent_id
  8: required list<string> audience_agent_ids
  9: required i32 revision
  10: required string created_at
  11: required string updated_at
  12: required string soft_expires_at
  13: required string hard_expires_at
}

struct ExplorerTeamNote {
  1: required ExplorerTeamNoteSummary summary
  2: required string body
  3: required string origin_user_id
  4: required string origin_session_id
  5: required list<string> related_subjects
  6: optional string valid_at
  7: optional string invalid_at
  8: optional string source_occurred_at
}

struct ExplorerSourceEvent {
  1: required string event_id
  2: required string user_id
  3: required string agent_id
  4: required string session_id
  5: required i64 sequence
  6: required string type
  7: required string content
  8: optional string task_ref
  9: optional string thread_ref
  10: required string visibility
  11: required string occurred_at
  12: required string captured_at
  13: optional string extracted_at
}

struct ExplorerExtractionRun {
  1: required string run_id
  2: required string user_id
  3: required string agent_id
  4: required string session_id
  5: required i64 from_sequence
  6: required i64 to_sequence
  7: required string model
  8: required string prompt_version
  9: required string status
  10: required i64 input_tokens
  11: required i64 output_tokens
  12: optional string error_code
  13: required string created_at
  14: optional string completed_at
}

struct ExplorerCandidate {
  1: required string candidate_id
  2: required string action
  3: required string kind
  4: required string subject
  5: required string body
  6: optional string task_ref
  7: optional string thread_ref
  8: required string origin_agent_id
  9: required list<string> evidence_event_ids
  10: required string admission_status
  11: optional string rejection_reason
  12: required string created_at
  13: optional string resulting_note_id
}

struct ExplorerDelivery {
  1: required string recipient_user_id
  2: required string recipient_agent_id
  3: required string recipient_session_id
  4: required string delivered_at
  5: required i32 context_tokens
}

struct ExplorerRevision {
  1: required i32 revision
  2: required string candidate_id
  3: required string operation
  4: required string body
  5: required list<string> related_subjects
  6: optional string valid_at
  7: optional string invalid_at
  8: required string created_at
  9: optional string expired_at
  10: required ExplorerExtractionRun extraction
  11: required list<ExplorerSourceEvent> evidence
  12: required list<ExplorerDelivery> deliveries
  13: required ExplorerCandidate candidate
}

struct ExplorerRecallUse {
  1: required i64 observation_id
  2: required string recipient_agent_id
  3: required string recipient_session_id
  4: required string occurred_at
  5: optional string disposition
  6: required bool delivered
  7: required list<string> rejection_reasons
  8: required list<string> budget_drop_reasons
  9: required list<string> hard_gate_failures
}

struct ListTeamNotesResponse {
  1: required list<ExplorerTeamNoteSummary> notes
  2: optional string next_cursor
}

struct TeamNoteDetailResponse {
  1: required ExplorerTeamNote note
  2: required list<ExplorerTeamNoteSummary> related_notes
  3: required list<ExplorerRevision> revisions
  4: required list<ExplorerRecallUse> recall_observations
}

struct ExtractionDiagnosticResponse {
  1: required ExplorerExtractionRun run
  2: required list<ExplorerCandidate> candidates
  3: required list<ExplorerSourceEvent> source_events
  4: required list<ExplorerTeamNoteSummary> resulting_notes
}

struct ExplorerKnowledgeCapsule {
  1: optional string capsule_id
  2: optional string source_session_id
  3: optional string source_agent
  4: optional string keyword
  5: optional string title
  6: optional string summary
  7: optional string content
  8: optional string status
  9: optional bool truncated
  10: optional string route_match_type
}

struct ChannelDiagnosticResponse {
  1: required string envelope_id
  2: required string from_agent_id
  3: required string to_agent_id
  4: required string status
  5: optional string message
  6: required string payload_status
  7: required string created_at
  8: optional string accepted_at
  9: optional string archived_at
  10: required ExplorerKnowledgeCapsule capsule
}

service TeamMemoryService {
  IngestReceipt ObserveSession(1: SessionBatch request) (api.post="/v1/session-batches")
  IngestReceipt ObserveStream(1: StreamBatch request) (api.post="/v1/stream-batches")
  NoteEnvelope RecallNotes(1: RecallRequest request) (api.post="/v1/notes/recall")
  HealthResponse Health(1: HealthRequest request) (api.get="/healthz")
  AgentEnrollmentResponse CreateAgentEnrollment(1: AgentEnrollmentRequest request) (api.post="/v1/admin/agent-enrollments")
  AgentCredentialResponse ExchangeAgentEnrollment(1: ExchangeEnrollmentRequest request) (api.post="/v1/agent-enrollments/exchange")
  AgentIdentityResponse GetAgentIdentity(1: AgentIdentityRequest request) (api.get="/v1/agent-identity")
  AgentCredentialResponse RotateAgentCredential(1: RotateAgentCredentialRequest request) (api.post="/v1/agent-credentials/rotate")
  RevokeAgentCredentialResponse RevokeAgentCredential(1: RevokeAgentCredentialRequest request) (api.delete="/v1/admin/agent-credentials/:credential_id")
  DeviceEnrollmentResponse CreateDeviceEnrollment(1: CreateDeviceEnrollmentRequest request) (api.post="/v1/me/device-enrollments")
  ProvisionDeviceAgentResponse ProvisionDeviceAgent(1: ProvisionDeviceAgentRequest request) (api.post="/v1/device/agent-provisions")
  ListDeviceProvisionsResponse ListDeviceProvisions(1: ListDeviceProvisionsRequest request) (api.get="/v1/device/agent-provisions")
  ListDevicesResponse ListAdminDevices(1: ListDevicesRequest request) (api.get="/v1/admin/devices")
  DeviceDetailResponse GetAdminDevice(1: DeviceByIDRequest request) (api.get="/v1/admin/devices/:credential_id")
  DeviceSummaryResponse RevokeAdminDevice(1: DeviceByIDRequest request) (api.delete="/v1/admin/devices/:credential_id")
  ObservationReceipt ObserveBatch(1: ObservationBatch request) (api.post="/v1/observations")
  MemorySearchResponse SearchMemory(1: MemorySearchRequest request) (api.post="/v1/memory/search")
  MemoryDocument GetMemory(1: MemoryGetRequest request) (api.post="/v1/memory/get")
  ChannelEnvelopeResponse SendChannelEnvelope(1: SendChannelEnvelopeRequest request) (api.post="/v1/channel/envelopes")
  ListChannelEnvelopesResponse ListChannelEnvelopes(1: ListChannelEnvelopesRequest request) (api.get="/v1/channel/envelopes")
  ChannelEnvelopeResponse GetChannelEnvelope(1: ChannelEnvelopeByIDRequest request) (api.get="/v1/channel/envelopes/:envelope_id")
  ChannelEnvelopeResponse AcceptChannelEnvelope(1: ChannelEnvelopeByIDRequest request) (api.post="/v1/channel/envelopes/:envelope_id/accept")
  ChannelEnvelopeResponse ArchiveChannelEnvelope(1: ChannelEnvelopeByIDRequest request) (api.post="/v1/channel/envelopes/:envelope_id/archive")
  EmptyResponse BeginHumanLogin(1: AuthLoginRequest request) (api.get="/v1/auth/login")
  EmptyResponse CompleteHumanLogin(1: AuthCallbackRequest request) (api.get="/v1/auth/callback")
  EmptyResponse LogoutHuman(1: AuthLogoutRequest request) (api.post="/v1/auth/logout")
  HumanMeResponse ClaimBootstrap(1: BootstrapClaimRequest request) (api.post="/v1/bootstrap/claim")
  HumanMeResponse GetHumanMe(1: HumanMeRequest request) (api.get="/v1/me")
  WikiIngestionStatusResponse GetWikiIngestionStatus(1: WikiIngestionStatusRequest request) (api.get="/v1/wiki/ingestion")
  WikiIngestionStatusResponse UpdateWikiIngestion(1: UpdateWikiIngestionRequest request) (api.put="/v1/wiki/ingestion")
  InjectWikiSessionResponse InjectWikiSession(1: InjectWikiSessionRequest request) (api.post="/v1/wiki/sessions/:session_id/inject")
  RebuildWikiResponse RebuildWiki(1: RebuildWikiRequest request) (api.post="/v1/wiki/rebuild")
  InvitationResponse CreateMembershipInvitation(1: CreateInvitationRequest request) (api.post="/v1/admin/invitations")
  ListInvitationsResponse ListMembershipInvitations(1: ListInvitationsRequest request) (api.get="/v1/admin/invitations")
  InvitationResponse RevokeMembershipInvitation(1: InvitationByIDRequest request) (api.delete="/v1/admin/invitations/:invitation_id")
  HumanMeResponse AcceptMembershipInvitation(1: AcceptInvitationRequest request) (api.post="/v1/invitations/accept")
  ListAgentProfilesResponse ListOwnedAgents(1: ListAgentProfilesRequest request) (api.get="/v1/me/agents")
  AgentProfileResponse CreateOwnedAgent(1: CreateAgentProfileRequest request) (api.post="/v1/me/agents")
  AgentProfileResponse GetOwnedAgent(1: AgentProfileByIDRequest request) (api.get="/v1/me/agents/:agent_id")
  AgentProfileResponse UpdateOwnedAgent(1: UpdateAgentProfileRequest request) (api.patch="/v1/me/agents/:agent_id")
  AgentProfileResponse RetireOwnedAgent(1: RetireAgentProfileRequest request) (api.delete="/v1/me/agents/:agent_id")
  AgentEnrollmentResponse CreateOwnedAgentEnrollment(1: CreateOwnedEnrollmentRequest request) (api.post="/v1/me/agents/:agent_id/enrollments")
  ListAgentEnrollmentsResponse ListOwnedAgentEnrollments(1: ListAgentArtifactsRequest request) (api.get="/v1/me/agents/:agent_id/enrollments")
  AgentEnrollmentMetadataResponse RevokeOwnedAgentEnrollment(1: AgentEnrollmentByIDRequest request) (api.delete="/v1/me/agents/:agent_id/enrollments/:enrollment_id")
  ListAgentCredentialsResponse ListOwnedAgentCredentials(1: ListAgentArtifactsRequest request) (api.get="/v1/me/agents/:agent_id/credentials")
  AgentCredentialMetadataResponse RevokeOwnedAgentCredential(1: AgentCredentialByIDRequest request) (api.delete="/v1/me/agents/:agent_id/credentials/:credential_id")
  ListDirectoryAgentsResponse ListDirectoryAgents(1: ListDirectoryAgentsRequest request) (api.get="/v1/channel/agents")
  DirectoryAgentResponse GetDirectoryAgent(1: DirectoryAgentByIDRequest request) (api.get="/v1/channel/agents/:agent_id")
  ListAgentProfilesResponse ListAdminAgents(1: ListAdminAgentsRequest request) (api.get="/v1/admin/agents")
  AgentProfileResponse GetAdminAgent(1: AdminAgentByIDRequest request) (api.get="/v1/admin/agents/:agent_id")
  AgentProfileResponse UpdateAdminAgent(1: UpdateAgentProfileRequest request) (api.patch="/v1/admin/agents/:agent_id")
  AgentProfileResponse RetireAdminAgent(1: RetireAgentProfileRequest request) (api.delete="/v1/admin/agents/:agent_id")
  AgentProfileResponse TransferAdminAgent(1: TransferAgentRequest request) (api.post="/v1/admin/agents/:agent_id/transfer")
  ListAgentEnrollmentsResponse ListAdminAgentEnrollments(1: ListAgentArtifactsRequest request) (api.get="/v1/admin/agents/:agent_id/enrollments")
  AgentEnrollmentMetadataResponse RevokeAdminAgentEnrollment(1: AgentEnrollmentByIDRequest request) (api.delete="/v1/admin/agents/:agent_id/enrollments/:enrollment_id")
  ListAgentCredentialsResponse ListAdminAgentCredentials(1: ListAgentArtifactsRequest request) (api.get="/v1/admin/agents/:agent_id/credentials")
  AgentCredentialMetadataResponse RevokeAdminAgentCredential(1: AgentCredentialByIDRequest request) (api.delete="/v1/admin/agents/:agent_id/credentials/:credential_id")
  ListMembersResponse ListMembers(1: ListMembersRequest request) (api.get="/v1/admin/members")
  MemberResponse GetMember(1: MemberByIDRequest request) (api.get="/v1/admin/members/:membership_id")
  MemberResponse UpdateMember(1: UpdateMemberRequest request) (api.patch="/v1/admin/members/:membership_id")
  ListAuditEventsResponse ListAuditEvents(1: ListAuditEventsRequest request) (api.get="/v1/admin/audit-events")
  AuditEventResponse GetAuditEvent(1: AuditEventByIDRequest request) (api.get="/v1/admin/audit-events/:audit_event_id")
  OperationsSummaryResponse GetOperationsSummary(1: OperationsSummaryRequest request) (api.get="/v1/admin/operations/summary")
  ListOperationEventsResponse ListOperationEvents(1: ListOperationEventsRequest request) (api.get="/v1/admin/operations/events")
  RecallDiagnosticResponse GetRecallDiagnostic(1: RecallDiagnosticByIDRequest request) (api.get="/v1/admin/operations/recalls/:observation_id")
  OperationsStorageResponse GetOperationsStorage(1: OperationsStorageRequest request) (api.get="/v1/admin/operations/storage")
  ListOperationsStorageHistoryResponse ListOperationsStorageHistory(1: ListOperationsStorageHistoryRequest request) (api.get="/v1/admin/operations/storage/history")
  OperationsAgentStatsResponse ListOperationsAgentStats(1: OperationsAgentStatsRequest request) (api.get="/v1/admin/operations/agents")
  ListTeamNotesResponse ListTeamNotes(1: ListTeamNotesRequest request) (api.get="/v1/admin/team-notes")
  TeamNoteDetailResponse GetTeamNote(1: TeamNoteByIDRequest request) (api.get="/v1/admin/team-notes/:note_id")
  ExtractionDiagnosticResponse GetExtractionDiagnostic(1: ExtractionDiagnosticByIDRequest request) (api.get="/v1/admin/diagnostics/extractions/:run_id")
  ChannelDiagnosticResponse GetChannelDiagnostic(1: ChannelDiagnosticByIDRequest request) (api.get="/v1/admin/diagnostics/channels/:envelope_id")
}
