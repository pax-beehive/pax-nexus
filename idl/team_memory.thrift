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

service TeamMemoryService {
  IngestReceipt ObserveSession(1: SessionBatch request) (api.post="/v1/session-batches")
  NoteEnvelope RecallNotes(1: RecallRequest request) (api.post="/v1/notes/recall")
  HealthResponse Health(1: HealthRequest request) (api.get="/healthz")
  AgentEnrollmentResponse CreateAgentEnrollment(1: AgentEnrollmentRequest request) (api.post="/v1/admin/agent-enrollments")
  AgentCredentialResponse ExchangeAgentEnrollment(1: ExchangeEnrollmentRequest request) (api.post="/v1/agent-enrollments/exchange")
  AgentIdentityResponse GetAgentIdentity(1: AgentIdentityRequest request) (api.get="/v1/agent-identity")
  AgentCredentialResponse RotateAgentCredential(1: RotateAgentCredentialRequest request) (api.post="/v1/agent-credentials/rotate")
  RevokeAgentCredentialResponse RevokeAgentCredential(1: RevokeAgentCredentialRequest request) (api.delete="/v1/admin/agent-credentials/:credential_id")
  ObservationReceipt ObserveBatch(1: ObservationBatch request) (api.post="/v1/observations")
  MemorySearchResponse SearchMemory(1: MemorySearchRequest request) (api.post="/v1/memory/search")
  MemoryDocument GetMemory(1: MemoryGetRequest request) (api.post="/v1/memory/get")
  ChannelEnvelopeResponse SendChannelEnvelope(1: SendChannelEnvelopeRequest request) (api.post="/v1/channel/envelopes")
  ListChannelEnvelopesResponse ListChannelEnvelopes(1: ListChannelEnvelopesRequest request) (api.get="/v1/channel/envelopes")
  ChannelEnvelopeResponse GetChannelEnvelope(1: ChannelEnvelopeByIDRequest request) (api.get="/v1/channel/envelopes/:envelope_id")
  ChannelEnvelopeResponse AcceptChannelEnvelope(1: ChannelEnvelopeByIDRequest request) (api.post="/v1/channel/envelopes/:envelope_id/accept")
  ChannelEnvelopeResponse ArchiveChannelEnvelope(1: ChannelEnvelopeByIDRequest request) (api.post="/v1/channel/envelopes/:envelope_id/archive")
}
