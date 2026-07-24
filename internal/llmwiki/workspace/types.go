// Package workspace implements the local-first LLM Wiki spike.
package workspace

import (
	"fmt"
	"strings"
	"time"
)

const (
	// PaxmSessionSchema is the native completed-turn export produced by paxm.
	PaxmSessionSchema = "paxm.session-turns.v1"
	manifestSchema    = "pax.llmwiki.workspace.v1"
)

type SessionExport struct {
	SchemaVersion string        `json:"schema_version"`
	Agent         string        `json:"agent"`
	SessionID     string        `json:"session_id"`
	Workspace     string        `json:"workspace"`
	Turns         []SessionTurn `json:"turns"`
}

type SessionTurn struct {
	ID        string    `json:"id"`
	Agent     string    `json:"agent"`
	SessionID string    `json:"session_id"`
	Workspace string    `json:"workspace"`
	User      string    `json:"user"`
	Assistant string    `json:"assistant"`
	CreatedAt time.Time `json:"created_at"`
}

type BuildRequest struct {
	SessionID string
	TurnStart int
	TurnEnd   int
}

type SourceMessage struct {
	Anchor    string
	TurnID    string
	Role      string
	Content   string
	CreatedAt time.Time
}

type SessionPart struct {
	SessionID string
	Agent     string
	Workspace string
	TurnStart int
	TurnEnd   int
	Messages  []SourceMessage
}

type SourceAnchor struct {
	ID        string    `json:"id"`
	TurnID    string    `json:"turn_id"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type SourceRecord struct {
	SessionID string         `json:"session_id"`
	TurnStart int            `json:"turn_start"`
	TurnEnd   int            `json:"turn_end"`
	Path      string         `json:"path"`
	SHA256    string         `json:"sha256"`
	Bytes     int            `json:"bytes"`
	Anchors   []SourceAnchor `json:"anchors"`
}

type Manifest struct {
	SchemaVersion string         `json:"schema_version"`
	Sources       []SourceRecord `json:"sources"`
}

type BuildResult struct {
	Source       SourceRecord `json:"source"`
	TurnCount    int          `json:"turn_count"`
	MessageCount int          `json:"message_count"`
}

type ValidationIssue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type ValidationReport struct {
	Valid         bool              `json:"valid"`
	MarkdownFiles int               `json:"markdown_files"`
	Citations     int               `json:"citations"`
	Errors        []ValidationIssue `json:"errors"`
}

func (r ValidationReport) String() string {
	if r.Valid {
		return fmt.Sprintf(
			"valid: markdown_files=%d citations=%d",
			r.MarkdownFiles,
			r.Citations,
		)
	}
	var result strings.Builder
	for _, issue := range r.Errors {
		if result.Len() > 0 {
			result.WriteByte('\n')
		}
		result.WriteString(issue.Path)
		result.WriteString(": ")
		result.WriteString(issue.Message)
	}
	return result.String()
}
