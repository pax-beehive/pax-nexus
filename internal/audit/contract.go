// Package audit owns read-only governance projections over session evidence.
package audit

// Event types carried as free-form session event Type strings on the
// agent-session ingest path. See docs/session-audit-contract.md.
const (
	EventTypeToolCall     = "tool_call"
	EventTypeToolResult   = "tool_result"
	EventTypeToolApproval = "tool_approval"
)

// Metadata keys registered for tool-related session events.
const (
	MetadataToolCallID       = "tool.call_id"
	MetadataToolName         = "tool.name"
	MetadataToolInputSummary = "tool.input_summary"
	MetadataToolStatus       = "tool.status"
	MetadataApprovalDecision = "approval.decision"
	MetadataApprovalActor    = "approval.actor"
)

// Approval decisions a producer may attach to a tool_approval event or
// inline on a tool_call event.
const (
	DecisionApproved = "approved"
	DecisionDenied   = "denied"
	DecisionAuto     = "auto"
)

// Stored approval states. The merge is monotonic: unknown may resolve to
// approved or denied, and a resolved state is never overwritten.
const (
	ApprovalUnknown  = "unknown"
	ApprovalApproved = "approved"
	ApprovalDenied   = "denied"
)

// Finding kinds produced by the projector.
const (
	FindingHighRiskUnapproved = "high_risk_unapproved"
	FindingDeniedToolExecuted = "denied_tool_executed"
	FindingVisibilityUnknown  = "visibility_unknown"
	FindingAttributionMissing = "attribution_missing"
)

// allowedAgentSessionVisibility is the hardcoded first-iteration visibility
// policy for agent-session evidence. Any other value is a finding, never a
// rejection.
var allowedAgentSessionVisibility = map[string]struct{}{
	"":                   {},
	"team":               {},
	"team_note_eligible": {},
	"team_visible":       {},
}
