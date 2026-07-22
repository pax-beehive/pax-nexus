package handler

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/operations"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	api "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/model/teammemory/api"
)

func (h *Handler) ObserveSession(ctx context.Context, c *app.RequestContext) {
	startedAt := time.Now()
	var req api.SessionBatch
	if err := c.BindAndValidate(&req); err != nil {
		h.logger.WarnContext(ctx, "session batch rejected", "stage", "bind", "error", err)
		c.String(consts.StatusBadRequest, "invalid session batch")
		return
	}
	scopeID, err := h.resolver.ResolveScope(c)
	if errors.Is(err, ErrUnauthorized) {
		h.logger.WarnContext(ctx, "session batch rejected", "stage", "authorize")
		c.String(consts.StatusUnauthorized, "unauthorized")
		return
	}
	if err != nil {
		h.logger.ErrorContext(ctx, "resolve session scope failed", "error", err)
		c.String(consts.StatusInternalServerError, "resolve scope")
		return
	}
	batch, err := sessionBatchToDomain(&req)
	if err != nil {
		h.logger.WarnContext(ctx, "session batch rejected", "stage", "map", "error", err)
		c.String(consts.StatusBadRequest, "invalid session batch")
		return
	}
	receipt, err := h.runtime.ObserveSession(teamnote.WithScope(ctx, scopeID), batch)
	if err != nil {
		h.logger.ErrorContext(ctx, "observe session failed", "scope_id", scopeID, "error", err)
		c.String(consts.StatusUnprocessableEntity, "observe session")
		return
	}
	actor := batch.Events[0].Actor
	h.logger.InfoContext(ctx, "session batch observed",
		"scope_id", scopeID, "user_id", actor.UserID, "agent_id", actor.AgentID, "session_id", actor.SessionID,
		"events", len(batch.Events), "accepted", receipt.Accepted, "duplicates", receipt.Duplicate,
		"cursor", receipt.Cursor, "run_id", receipt.RunID, "duration_ms", time.Since(startedAt).Milliseconds(),
	)
	c.JSON(consts.StatusOK, ingestReceiptToAPI(receipt))
}

func (h *Handler) RecallNotes(ctx context.Context, c *app.RequestContext) {
	startedAt := time.Now().UTC()
	var principal onprem.Principal
	if h.credentials != nil {
		var ok bool
		var authorizationCode string
		principal, ok, authorizationCode = h.authorizeAgent(ctx, c, onprem.PermissionSearch)
		if !ok {
			h.recordAuthorizationRejection(ctx, principal, operations.KindTeamNoteRecall, startedAt, authorizationCode)
			return
		}
	}
	var req api.RecallRequest
	if err := c.BindAndValidate(&req); err != nil {
		if principal.AgentID != "" {
			h.recordAgentOperation(ctx, principal, operations.Event{
				Kind: operations.KindTeamNoteRecall, Outcome: operations.OutcomeRejected,
				StartedAt: startedAt, ErrorCode: "invalid_request",
			})
		}
		h.logger.WarnContext(ctx, "recall request rejected", "stage", "bind", "error", err)
		c.String(consts.StatusBadRequest, "invalid recall request")
		return
	}
	request, err := recallRequestToDomain(&req)
	if err != nil {
		if principal.AgentID != "" {
			h.recordAgentOperation(ctx, principal, operations.Event{
				Kind: operations.KindTeamNoteRecall, Outcome: operations.OutcomeRejected,
				StartedAt: startedAt, InputItems: 1,
				ErrorCode: "invalid_request",
			})
		}
		h.logger.WarnContext(ctx, "recall request rejected", "stage", "map", "error", err)
		c.String(consts.StatusBadRequest, "invalid recall request")
		return
	}
	scopeID, ok := h.resolveRecallScope(ctx, c, principal, &request)
	if !ok {
		return
	}
	envelope, err := h.runtime.RecallNotes(teamnote.WithScope(ctx, scopeID), request)
	if err != nil {
		if principal.AgentID != "" {
			outcome, errorCode := operationFailure(err)
			h.recordAgentOperation(ctx, principal, operations.Event{
				Kind: operations.KindTeamNoteRecall, Outcome: outcome, StartedAt: startedAt,
				SessionID: request.Actor.SessionID, InputItems: 1, ErrorCode: errorCode,
			})
		}
		h.logger.ErrorContext(ctx, "recall notes failed", "scope_id", scopeID, "user_id", request.Actor.UserID,
			"agent_id", request.Actor.AgentID, "session_id", request.Actor.SessionID, "error", err)
		c.String(consts.StatusUnprocessableEntity, "recall notes")
		return
	}
	if principal.AgentID != "" {
		h.recordAgentOperation(ctx, principal, teamNoteRecallOperation(envelope, request.Actor.SessionID, startedAt))
	}
	h.logger.InfoContext(ctx, "team notes recalled",
		"scope_id", scopeID, "user_id", request.Actor.UserID, "agent_id", request.Actor.AgentID,
		"session_id", request.Actor.SessionID, "notes", len(envelope.Details), "tokens", envelope.Tokens,
		"duration_ms", time.Since(startedAt).Milliseconds(),
	)
	c.JSON(consts.StatusOK, noteEnvelopeToAPI(envelope))
}

func (h *Handler) resolveRecallScope(
	ctx context.Context,
	c *app.RequestContext,
	principal onprem.Principal,
	request *teamnote.RecallRequest,
) (string, bool) {
	if principal.AgentID != "" {
		request.Actor.UserID = principal.UserID
		request.Actor.AgentID = principal.AgentID
		return principal.ScopeID, true
	}
	scopeID, err := h.resolver.ResolveScope(c)
	if errors.Is(err, ErrUnauthorized) {
		h.logger.WarnContext(ctx, "recall request rejected", "stage", "authorize")
		c.String(consts.StatusUnauthorized, "unauthorized")
		return "", false
	}
	if err != nil {
		h.logger.ErrorContext(ctx, "resolve recall scope failed", "error", err)
		c.String(consts.StatusInternalServerError, "resolve scope")
		return "", false
	}
	return scopeID, true
}

func teamNoteRecallOperation(
	envelope teamnote.NoteEnvelope,
	sessionID string,
	startedAt time.Time,
) operations.Event {
	delivered := int64(len(envelope.Details))
	if int64(len(envelope.Items)) > delivered {
		delivered = int64(len(envelope.Items))
	}
	event := operations.Event{
		Kind: operations.KindTeamNoteRecall, Outcome: operations.OutcomeSucceeded,
		StartedAt: startedAt, SessionID: sessionID, InputItems: 1,
		ResultItems: delivered, DeliveredItems: delivered, EvidenceItems: delivered,
	}
	if envelope.ObservationID > 0 {
		event.DetailKind = "recall_observation"
		event.DetailID = strconv.FormatInt(envelope.ObservationID, 10)
	}
	return event
}

func (h *Handler) Health(ctx context.Context, c *app.RequestContext) {
	var req api.HealthRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.String(consts.StatusBadRequest, "invalid health request")
		return
	}
	c.JSON(consts.StatusOK, &api.HealthResponse{Status: "ok"})
}
