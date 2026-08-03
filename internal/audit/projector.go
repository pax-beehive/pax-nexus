package audit

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/session"
)

// Batch is the atomic projection unit for one stream scan: derived rows plus
// the cursor they were derived from. The Store applies it in one transaction.
type Batch struct {
	ScopeID         string
	Actor           session.Actor
	Cursor          int64
	ToolCalls       []ToolCall
	ApprovalUpdates []ApprovalUpdate
	Findings        []Finding
	Activity        []ActivityDelta
}

// ToolCall is one normalized tool_call audit row.
type ToolCall struct {
	ScopeID       string
	EventID       string
	UserID        string
	AgentID       string
	SessionID     string
	CallID        string
	ToolName      string
	InputSummary  string
	RiskLevel     Level
	RiskReasons   []Reason
	ApprovalState string
	OccurredAt    time.Time
	CapturedAt    time.Time
}

// ApprovalUpdate carries an approval decision whose tool_call event was not
// part of this batch; the store upgrades a stored row still in unknown state.
type ApprovalUpdate struct {
	CallID string
	State  string
}

// Finding is one persisted policy observation.
type Finding struct {
	FindingID        int64
	ScopeID          string
	UserID           string
	AgentID          string
	SessionID        string
	Kind             string
	Severity         string
	Summary          string
	EvidenceEventIDs []string
	CreatedAt        time.Time
}

// ActivityDelta is the incremental per-day aggregate contributed by one
// batch. SessionIDs carries the distinct sessions seen so the store can
// merge a correct distinct-session count.
type ActivityDelta struct {
	ScopeID       string
	UserID        string
	AgentID       string
	Day           time.Time
	EventCount    int64
	ToolCallCount int64
	HighRiskCount int64
	SessionIDs    []string
	ToolBreakdown map[string]int64
}

// Project derives the audit batch for one stream scan. It is a pure decision
// layer: no I/O, deterministic for a given (stream, events) pair, and
// tolerant of legacy events without tool types or contract metadata.
func Project(stream Stream, events []session.SessionEvent) Batch {
	batch := Batch{
		ScopeID: stream.ScopeID,
		Actor:   stream.Actor,
		Cursor:  stream.Head,
	}
	if len(events) == 0 {
		return batch
	}
	approvals := collectApprovals(events)
	claimed := make(map[string]struct{})
	activity := newActivityAccumulator(stream.ScopeID)
	for _, event := range events {
		activity.observe(event)
		if event.Type != EventTypeToolCall {
			continue
		}
		call := projectToolCall(stream.ScopeID, event, approvals)
		if call.CallID != "" {
			claimed[call.CallID] = struct{}{}
		}
		activity.observeToolCall(call)
		batch.ToolCalls = append(batch.ToolCalls, call)
		batch.Findings = append(batch.Findings, toolCallFindings(call)...)
	}
	batch.ApprovalUpdates = pendingApprovalUpdates(approvals, claimed)
	batch.Findings = append(batch.Findings, complianceFindings(stream.ScopeID, events)...)
	batch.Activity = activity.deltas()
	return batch
}

// collectApprovals indexes approval decisions by call id, whether they ride
// on a tool_approval event or inline on the tool_call event. The first
// decision seen for a call id wins; merges never move a resolved state.
func collectApprovals(events []session.SessionEvent) map[string]string {
	approvals := make(map[string]string)
	for _, event := range events {
		if event.Type != EventTypeToolCall && event.Type != EventTypeToolApproval {
			continue
		}
		state := approvalStateFromDecision(event.Metadata[MetadataApprovalDecision])
		callID := event.Metadata[MetadataToolCallID]
		if state == ApprovalUnknown || callID == "" {
			continue
		}
		if _, ok := approvals[callID]; !ok {
			approvals[callID] = state
		}
	}
	return approvals
}

func approvalStateFromDecision(decision string) string {
	switch decision {
	case DecisionApproved, DecisionAuto:
		return ApprovalApproved
	case DecisionDenied:
		return ApprovalDenied
	default:
		return ApprovalUnknown
	}
}

func projectToolCall(
	scopeID string,
	event session.SessionEvent,
	approvals map[string]string,
) ToolCall {
	callID := event.Metadata[MetadataToolCallID]
	toolName := event.Metadata[MetadataToolName]
	summary := event.Metadata[MetadataToolInputSummary]
	level, reasons := Classify(toolName, summary)
	state := approvals[callID]
	if state == "" {
		state = ApprovalUnknown
	}
	capturedAt := event.CapturedAt
	if capturedAt.IsZero() {
		capturedAt = event.OccurredAt
	}
	return ToolCall{
		ScopeID:       scopeID,
		EventID:       event.ID,
		UserID:        event.Actor.UserID,
		AgentID:       event.Actor.AgentID,
		SessionID:     event.Actor.SessionID,
		CallID:        callID,
		ToolName:      toolName,
		InputSummary:  summary,
		RiskLevel:     level,
		RiskReasons:   reasons,
		ApprovalState: state,
		OccurredAt:    event.OccurredAt,
		CapturedAt:    capturedAt,
	}
}

func toolCallFindings(call ToolCall) []Finding {
	findings := make([]Finding, 0, 2)
	if isHighRisk(call.RiskLevel) && call.ApprovalState == ApprovalUnknown {
		findings = append(findings, toolCallFinding(call, FindingHighRiskUnapproved, string(call.RiskLevel),
			fmt.Sprintf("tool %q with %s risk has no recorded approval decision",
				call.ToolName, call.RiskLevel)))
	}
	if call.ApprovalState == ApprovalDenied {
		findings = append(findings, toolCallFinding(call, FindingDeniedToolExecuted, string(LevelHigh),
			fmt.Sprintf("tool %q executed after the call was denied", call.ToolName)))
	}
	return findings
}

func toolCallFinding(call ToolCall, kind string, severity string, summary string) Finding {
	return Finding{
		ScopeID:          call.ScopeID,
		UserID:           call.UserID,
		AgentID:          call.AgentID,
		SessionID:        call.SessionID,
		Kind:             kind,
		Severity:         severity,
		Summary:          summary,
		EvidenceEventIDs: []string{call.EventID},
	}
}

// pendingApprovalUpdates emits store-side state upgrades for approvals whose
// tool_call event was not part of this batch.
func pendingApprovalUpdates(approvals map[string]string, claimed map[string]struct{}) []ApprovalUpdate {
	updates := make([]ApprovalUpdate, 0, len(approvals))
	for callID, state := range approvals {
		if _, ok := claimed[callID]; ok {
			continue
		}
		updates = append(updates, ApprovalUpdate{CallID: callID, State: state})
	}
	sort.Slice(updates, func(i, j int) bool { return updates[i].CallID < updates[j].CallID })
	return updates
}

// complianceFindings checks the hardcoded visibility policy and user
// attribution for every event, tool-related or not. Findings aggregate per
// batch (per visibility value, per missing-attribution set) instead of one
// row per event.
func complianceFindings(scopeID string, events []session.SessionEvent) []Finding {
	unknown := make(map[string][]string)
	unknownActor := make(map[string]session.Actor)
	missing := make([]string, 0)
	var missingActor session.Actor
	for _, event := range events {
		if _, ok := allowedAgentSessionVisibility[event.Visibility]; !ok {
			if _, seen := unknown[event.Visibility]; !seen {
				unknownActor[event.Visibility] = event.Actor
			}
			unknown[event.Visibility] = append(unknown[event.Visibility], event.ID)
		}
		if strings.TrimSpace(event.Actor.UserID) == "" {
			if len(missing) == 0 {
				missingActor = event.Actor
			}
			missing = append(missing, event.ID)
		}
	}
	findings := make([]Finding, 0, len(unknown)+1)
	for _, visibility := range sortedKeys(unknown) {
		ids := unknown[visibility]
		actor := unknownActor[visibility]
		findings = append(findings, Finding{
			ScopeID:          scopeID,
			UserID:           actor.UserID,
			AgentID:          actor.AgentID,
			SessionID:        actor.SessionID,
			Kind:             FindingVisibilityUnknown,
			Severity:         string(LevelMedium),
			Summary:          fmt.Sprintf("%d event(s) carry unregistered visibility %q", len(ids), visibility),
			EvidenceEventIDs: ids,
		})
	}
	if len(missing) > 0 {
		findings = append(findings, Finding{
			ScopeID:          scopeID,
			UserID:           missingActor.UserID,
			AgentID:          missingActor.AgentID,
			SessionID:        missingActor.SessionID,
			Kind:             FindingAttributionMissing,
			Severity:         string(LevelLow),
			Summary:          fmt.Sprintf("%d event(s) have no user attribution", len(missing)),
			EvidenceEventIDs: missing,
		})
	}
	return findings
}

func sortedKeys(values map[string][]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type activityKey struct {
	userID  string
	agentID string
	day     time.Time
}

type activityAccumulator struct {
	scopeID string
	byKey   map[activityKey]*ActivityDelta
}

func newActivityAccumulator(scopeID string) *activityAccumulator {
	return &activityAccumulator{scopeID: scopeID, byKey: make(map[activityKey]*ActivityDelta)}
}

func (a *activityAccumulator) observe(event session.SessionEvent) {
	delta := a.deltaFor(event.Actor, event.OccurredAt)
	delta.EventCount++
	if !containsString(delta.SessionIDs, event.Actor.SessionID) {
		delta.SessionIDs = append(delta.SessionIDs, event.Actor.SessionID)
	}
}

func (a *activityAccumulator) observeToolCall(call ToolCall) {
	delta := a.deltaFor(session.Actor{
		UserID:    call.UserID,
		AgentID:   call.AgentID,
		SessionID: call.SessionID,
	}, call.OccurredAt)
	delta.ToolCallCount++
	if delta.ToolBreakdown == nil {
		delta.ToolBreakdown = make(map[string]int64)
	}
	delta.ToolBreakdown[call.ToolName]++
	if isHighRisk(call.RiskLevel) {
		delta.HighRiskCount++
	}
}

func (a *activityAccumulator) deltaFor(actor session.Actor, occurredAt time.Time) *ActivityDelta {
	key := activityKey{
		userID:  actor.UserID,
		agentID: actor.AgentID,
		day:     occurredAt.UTC().Truncate(24 * time.Hour),
	}
	delta, ok := a.byKey[key]
	if !ok {
		delta = &ActivityDelta{
			ScopeID: a.scopeID,
			UserID:  actor.UserID,
			AgentID: actor.AgentID,
			Day:     key.day,
		}
		a.byKey[key] = delta
	}
	return delta
}

// deltas returns the accumulated deltas in a deterministic order with
// sorted session id sets.
func (a *activityAccumulator) deltas() []ActivityDelta {
	keys := make([]activityKey, 0, len(a.byKey))
	for key := range a.byKey {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].userID != keys[j].userID {
			return keys[i].userID < keys[j].userID
		}
		if keys[i].agentID != keys[j].agentID {
			return keys[i].agentID < keys[j].agentID
		}
		return keys[i].day.Before(keys[j].day)
	})
	deltas := make([]ActivityDelta, 0, len(keys))
	for _, key := range keys {
		delta := a.byKey[key]
		sort.Strings(delta.SessionIDs)
		deltas = append(deltas, *delta)
	}
	return deltas
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
