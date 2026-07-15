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
}

struct RecalledNote {
  1: required string note_id
  2: required i32 revision
  3: required string text
  4: required Actor origin
}

struct NoteEnvelope {
  1: required string revision
  2: required list<string> items
  3: required i32 tokens
  4: optional list<RecalledNote> details
}

struct HealthRequest {}

struct HealthResponse {
  1: required string status
}

service TeamMemoryService {
  IngestReceipt ObserveSession(1: SessionBatch request) (api.post="/v1/session-batches")
  NoteEnvelope RecallNotes(1: RecallRequest request) (api.post="/v1/notes/recall")
  HealthResponse Health(1: HealthRequest request) (api.get="/healthz")
}
