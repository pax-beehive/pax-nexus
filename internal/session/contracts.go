// Package session owns shared agent-session identity and immutable event types.
package session

import "time"

// Actor identifies one stable agent execution identity and its current session.
type Actor struct {
	UserID    string `json:"user_id"`
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
}

// SessionEvent is immutable source evidence received from paxm.
type SessionEvent struct {
	ID         string            `json:"id"`
	Actor      Actor             `json:"actor"`
	Sequence   int64             `json:"sequence"`
	Type       string            `json:"type"`
	Content    string            `json:"content"`
	TaskRef    string            `json:"task_ref,omitempty"`
	ThreadRef  string            `json:"thread_ref,omitempty"`
	Visibility string            `json:"visibility,omitempty"`
	OccurredAt time.Time         `json:"occurred_at"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// SessionBatch is the durable ingestion unit for a session stream.
type SessionBatch struct {
	Events   []SessionEvent `json:"events"`
	Complete bool           `json:"complete"`
}

// IngestReceipt identifies what the session lake durably accepted.
type IngestReceipt struct {
	Accepted  int    `json:"accepted"`
	Duplicate int    `json:"duplicate"`
	Cursor    int64  `json:"cursor"`
	RunID     string `json:"run_id,omitempty"`
}
