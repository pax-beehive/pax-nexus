package handler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/audit"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	api "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/model/teammemory/api"
)

// auditDayLayout is the accepted format for the from_day/to_day filters and
// the rendered activity day.
const auditDayLayout = "2006-01-02"

func (h *Handler) ListSessionAuditToolCalls(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeSessionAudit(ctx, c)
	if !ok {
		return
	}
	limit, err := queryLimit(c)
	if err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	riskLevel := strings.TrimSpace(c.Query("risk_level"))
	approvalState := strings.TrimSpace(c.Query("approval_state"))
	if !validAuditLevel(riskLevel) || !validAuditApprovalState(approvalState) {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	calls, err := h.sessionAudit.ListToolCalls(ctx, audit.ToolCallFilter{
		ScopeID: principal.ScopeID, UserID: strings.TrimSpace(c.Query("user_id")),
		AgentID: strings.TrimSpace(c.Query("agent_id")), SessionID: strings.TrimSpace(c.Query("session_id")),
		RiskLevel: riskLevel, ApprovalState: approvalState, Limit: limit,
	})
	if err != nil {
		h.writeSessionAuditError(c, "list session audit tool calls", err)
		return
	}
	c.JSON(consts.StatusOK, sessionAuditToolCallsToAPI(calls))
}

func (h *Handler) ListSessionAuditFindings(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeSessionAudit(ctx, c)
	if !ok {
		return
	}
	limit, err := queryLimit(c)
	if err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	kind := strings.TrimSpace(c.Query("kind"))
	severity := strings.TrimSpace(c.Query("severity"))
	if !validAuditFindingKind(kind) || !validAuditLevel(severity) {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	findings, err := h.sessionAudit.ListFindings(ctx, audit.FindingFilter{
		ScopeID: principal.ScopeID, UserID: strings.TrimSpace(c.Query("user_id")),
		AgentID: strings.TrimSpace(c.Query("agent_id")), SessionID: strings.TrimSpace(c.Query("session_id")),
		Kind: kind, Severity: severity, Limit: limit,
	})
	if err != nil {
		h.writeSessionAuditError(c, "list session audit findings", err)
		return
	}
	c.JSON(consts.StatusOK, sessionAuditFindingsToAPI(findings))
}

func (h *Handler) ListSessionAuditActivity(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeSessionAudit(ctx, c)
	if !ok {
		return
	}
	limit, err := queryLimit(c)
	if err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	fromDay, err := parseAuditDay(c.Query("from_day"))
	if err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	toDay, err := parseAuditDay(c.Query("to_day"))
	if err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	days, err := h.sessionAudit.GetActivityDaily(ctx, audit.ActivityFilter{
		ScopeID: principal.ScopeID, UserID: strings.TrimSpace(c.Query("user_id")),
		AgentID: strings.TrimSpace(c.Query("agent_id")),
		FromDay: fromDay, ToDay: toDay, Limit: limit,
	})
	if err != nil {
		h.writeSessionAuditError(c, "list session audit activity", err)
		return
	}
	c.JSON(consts.StatusOK, sessionAuditActivityToAPI(days))
}

// authorizeSessionAudit mirrors the operations capability: a missing
// projection dependency answers 501, otherwise the request needs an active
// human membership like every other admin read. The returned principal
// carries the scope the audit queries filter on: on-prem principals always
// carry the local-team scope, SaaS principals their current team.
func (h *Handler) authorizeSessionAudit(ctx context.Context, c *app.RequestContext) (onprem.HumanPrincipal, bool) {
	if h.sessionAudit == nil {
		writeHumanAPIError(c, consts.StatusNotImplemented, "not_configured", "session audit is not configured")
		return onprem.HumanPrincipal{}, false
	}
	return h.authorizeHumanMember(ctx, c, false)
}

func (h *Handler) writeSessionAuditError(c *app.RequestContext, operation string, err error) {
	h.logger.Error("session audit request failed", "operation", operation, "error", err)
	writeHumanAPIError(c, consts.StatusInternalServerError, "internal_error", "the request could not be completed")
}

// validAuditLevel accepts the audit risk levels; finding severities reuse the
// same level vocabulary. Empty means no filter.
func validAuditLevel(value string) bool {
	switch audit.Level(value) {
	case "", audit.LevelLow, audit.LevelMedium, audit.LevelHigh, audit.LevelCritical:
		return true
	default:
		return false
	}
}

func validAuditApprovalState(value string) bool {
	switch value {
	case "", audit.ApprovalUnknown, audit.ApprovalApproved, audit.ApprovalDenied:
		return true
	default:
		return false
	}
}

func validAuditFindingKind(value string) bool {
	switch value {
	case "", audit.FindingHighRiskUnapproved, audit.FindingDeniedToolExecuted,
		audit.FindingVisibilityUnknown, audit.FindingAttributionMissing:
		return true
	default:
		return false
	}
}

// parseAuditDay parses a YYYY-MM-DD filter bound; empty leaves the range open
// on that side.
func parseAuditDay(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	day, err := time.Parse(auditDayLayout, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse audit day filter %q: %w", raw, err)
	}
	return day, nil
}

func sessionAuditToolCallsToAPI(calls []audit.ToolCall) *api.ListSessionAuditToolCallsResponse {
	result := &api.ListSessionAuditToolCallsResponse{
		ToolCalls: make([]*api.SessionAuditToolCall, 0, len(calls)),
	}
	for _, call := range calls {
		reasons := make([]string, 0, len(call.RiskReasons))
		for _, reason := range call.RiskReasons {
			reasons = append(reasons, string(reason))
		}
		result.ToolCalls = append(result.ToolCalls, &api.SessionAuditToolCall{
			EventID: call.EventID, UserID: call.UserID, AgentID: call.AgentID,
			SessionID: call.SessionID, CallID: call.CallID, ToolName: call.ToolName,
			InputSummary: call.InputSummary, RiskLevel: string(call.RiskLevel),
			RiskReasons: reasons, ApprovalState: call.ApprovalState,
			OccurredAt: call.OccurredAt.UTC().Format(time.RFC3339Nano),
			CapturedAt: call.CapturedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	return result
}

func sessionAuditFindingsToAPI(findings []audit.Finding) *api.ListSessionAuditFindingsResponse {
	result := &api.ListSessionAuditFindingsResponse{
		Findings: make([]*api.SessionAuditFinding, 0, len(findings)),
	}
	for _, finding := range findings {
		result.Findings = append(result.Findings, &api.SessionAuditFinding{
			FindingID: finding.FindingID, UserID: finding.UserID, AgentID: finding.AgentID,
			SessionID: finding.SessionID, Kind: finding.Kind, Severity: finding.Severity,
			Summary: finding.Summary, EvidenceEventIds: nonNilStrings(finding.EvidenceEventIDs),
			CreatedAt: finding.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	return result
}

func sessionAuditActivityToAPI(days []audit.ActivityDaily) *api.ListSessionAuditActivityResponse {
	result := &api.ListSessionAuditActivityResponse{
		Activity: make([]*api.SessionAuditActivityDay, 0, len(days)),
	}
	for _, day := range days {
		breakdown := day.ToolBreakdown
		if breakdown == nil {
			breakdown = map[string]int64{}
		}
		result.Activity = append(result.Activity, &api.SessionAuditActivityDay{
			UserID: day.UserID, AgentID: day.AgentID, Day: day.Day.UTC().Format(auditDayLayout),
			EventCount: day.EventCount, ToolCallCount: day.ToolCallCount,
			HighRiskCount: day.HighRiskCount, SessionCount: day.SessionCount,
			ToolBreakdown: breakdown,
		})
	}
	return result
}
