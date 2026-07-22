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
	"github.com/pax-beehive/pax-nexus/internal/recall"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	api "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/model/teammemory/api"
)

func (h *Handler) CreateAgentEnrollment(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorize(ctx, c, onprem.PermissionAdmin)
	if !ok {
		return
	}
	var request api.AgentEnrollmentRequest
	if err := c.BindAndValidate(&request); err != nil {
		c.String(consts.StatusBadRequest, "invalid enrollment request")
		return
	}
	enrollment, err := h.credentials.CreateEnrollment(ctx, principal, enrollmentRequestToDomain(&request))
	if err != nil {
		h.writeOnPremError(ctx, c, "create agent enrollment", err)
		return
	}
	c.JSON(consts.StatusOK, enrollmentToAPI(enrollment))
}

func (h *Handler) ExchangeAgentEnrollment(ctx context.Context, c *app.RequestContext) {
	if !h.requireOnPrem(c) {
		return
	}
	var request api.ExchangeEnrollmentRequest
	if err := c.BindAndValidate(&request); err != nil {
		c.String(consts.StatusBadRequest, "invalid enrollment exchange")
		return
	}
	credential, err := h.credentials.ExchangeEnrollment(ctx, request.Token)
	if err != nil {
		h.writeOnPremError(ctx, c, "exchange agent enrollment", err)
		return
	}
	c.JSON(consts.StatusOK, credentialToAPI(credential))
}

func (h *Handler) GetAgentIdentity(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorize(ctx, c, "")
	if !ok {
		return
	}
	c.JSON(consts.StatusOK, principalToAPI(principal))
}

func (h *Handler) RotateAgentCredential(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorize(ctx, c, "")
	if !ok {
		return
	}
	credential, err := h.credentials.RotateCredential(ctx, principal)
	if err != nil {
		h.writeOnPremError(ctx, c, "rotate agent credential", err)
		return
	}
	c.JSON(consts.StatusOK, credentialToAPI(credential))
}

func (h *Handler) RevokeAgentCredential(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorize(ctx, c, onprem.PermissionAdmin)
	if !ok {
		return
	}
	var request api.RevokeAgentCredentialRequest
	if err := c.BindAndValidate(&request); err != nil {
		c.String(consts.StatusBadRequest, "invalid credential revocation")
		return
	}
	if err := h.credentials.RevokeCredential(ctx, principal, request.CredentialID); err != nil {
		h.writeOnPremError(ctx, c, "revoke agent credential", err)
		return
	}
	c.JSON(consts.StatusOK, &api.RevokeAgentCredentialResponse{Revoked: true})
}

func (h *Handler) ObserveBatch(ctx context.Context, c *app.RequestContext) {
	startedAt := time.Now().UTC()
	principal, ok, authorizationCode := h.authorizeAgent(ctx, c, onprem.PermissionObserve)
	if !ok {
		h.recordAuthorizationRejection(ctx, principal, operations.KindObservationObserve, startedAt, authorizationCode)
		return
	}
	var request api.ObservationBatch
	if err := c.BindAndValidate(&request); err != nil {
		h.recordAgentOperation(ctx, principal, operations.Event{
			Kind: operations.KindObservationObserve, Outcome: operations.OutcomeRejected,
			StartedAt: startedAt, ErrorCode: "invalid_request",
		})
		c.String(consts.StatusBadRequest, "invalid observation batch")
		return
	}
	batch, err := observationBatchToDomain(&request, principal)
	if err != nil {
		h.recordAgentOperation(ctx, principal, operations.Event{
			Kind: operations.KindObservationObserve, Outcome: operations.OutcomeRejected,
			StartedAt: startedAt, SessionID: request.SessionID, InputItems: int64(len(request.Events)),
			ErrorCode: "invalid_request",
		})
		c.String(consts.StatusBadRequest, "invalid observation batch")
		return
	}
	receipt, err := h.runtime.ObserveSession(teamnote.WithScope(ctx, principal.ScopeID), batch)
	if err != nil {
		outcome, errorCode := operationFailure(err)
		h.recordAgentOperation(ctx, principal, operations.Event{
			Kind: operations.KindObservationObserve, Outcome: outcome, StartedAt: startedAt,
			SessionID: request.SessionID, InputItems: int64(len(request.Events)), ErrorCode: errorCode,
		})
		h.logger.ErrorContext(ctx, "observe batch failed", "credential_id", principal.CredentialID, "error", err)
		c.String(consts.StatusUnprocessableEntity, "observe batch")
		return
	}
	h.recordAgentOperation(ctx, principal, operations.Event{
		Kind: operations.KindObservationObserve, Outcome: operations.OutcomeSucceeded,
		StartedAt: startedAt, SessionID: request.SessionID, InputItems: int64(len(request.Events)),
		AcceptedItems: int64(receipt.Accepted), DuplicateItems: int64(receipt.Duplicate),
	})
	c.JSON(consts.StatusOK, observationReceiptToAPI(receipt, request.GetIdempotencyKey()))
}

func (h *Handler) SearchMemory(ctx context.Context, c *app.RequestContext) {
	startedAt := time.Now().UTC()
	principal, ok, authorizationCode := h.authorizeAgent(ctx, c, onprem.PermissionSearch)
	if !ok {
		h.recordAuthorizationRejection(ctx, principal, operations.KindMemorySearch, startedAt, authorizationCode)
		return
	}
	var request api.MemorySearchRequest
	if err := c.BindAndValidate(&request); err != nil {
		h.recordAgentOperation(ctx, principal, operations.Event{
			Kind: operations.KindMemorySearch, Outcome: operations.OutcomeRejected,
			StartedAt: startedAt, ErrorCode: "invalid_request",
		})
		c.String(consts.StatusBadRequest, "invalid memory search")
		return
	}
	result, err := h.memory.Search(teamnote.WithScope(ctx, principal.ScopeID), memorySearchRequestToDomain(&request, principal))
	if err != nil {
		outcome, errorCode := operationFailure(err)
		h.recordAgentOperation(ctx, principal, operations.Event{
			Kind: operations.KindMemorySearch, Outcome: outcome, StartedAt: startedAt,
			SessionID: request.SessionID, InputItems: 1, ErrorCode: errorCode,
		})
		h.logger.ErrorContext(ctx, "search memory failed", "credential_id", principal.CredentialID, "error", err)
		c.String(consts.StatusUnprocessableEntity, "search memory")
		return
	}
	event := memorySearchOperation(result, request.SessionID, startedAt)
	h.recordAgentOperation(ctx, principal, event)
	c.JSON(consts.StatusOK, memorySearchResultToAPI(result))
}

func (h *Handler) GetMemory(ctx context.Context, c *app.RequestContext) {
	startedAt := time.Now().UTC()
	principal, ok, authorizationCode := h.authorizeAgent(ctx, c, onprem.PermissionGet)
	if !ok {
		h.recordAuthorizationRejection(ctx, principal, operations.KindMemoryGet, startedAt, authorizationCode)
		return
	}
	var request api.MemoryGetRequest
	if err := c.BindAndValidate(&request); err != nil {
		h.recordAgentOperation(ctx, principal, operations.Event{
			Kind: operations.KindMemoryGet, Outcome: operations.OutcomeRejected,
			StartedAt: startedAt, ErrorCode: "invalid_request",
		})
		c.String(consts.StatusBadRequest, "invalid memory get")
		return
	}
	document, err := h.memory.Get(teamnote.WithScope(ctx, principal.ScopeID), recall.GetRequest{
		Actor: teamnote.Actor{UserID: principal.UserID, AgentID: principal.AgentID, SessionID: request.SessionID}, Ref: request.Ref,
	})
	if err != nil {
		outcome, errorCode := operationFailure(err)
		h.recordAgentOperation(ctx, principal, operations.Event{
			Kind: operations.KindMemoryGet, Outcome: outcome, StartedAt: startedAt,
			SessionID: request.SessionID, InputItems: 1, ErrorCode: errorCode,
		})
		h.logger.ErrorContext(ctx, "get memory failed", "credential_id", principal.CredentialID, "error", err)
		c.String(consts.StatusUnprocessableEntity, "get memory")
		return
	}
	h.recordAgentOperation(ctx, principal, operations.Event{
		Kind: operations.KindMemoryGet, Outcome: operations.OutcomeSucceeded, StartedAt: startedAt,
		SessionID: request.SessionID, InputItems: 1, ResultItems: 1, ReferenceItems: 1,
	})
	c.JSON(consts.StatusOK, memoryDocumentToAPI(document))
}

func (h *Handler) authorize(
	ctx context.Context,
	c *app.RequestContext,
	permission onprem.Permission,
) (onprem.Principal, bool) {
	principal, ok, _ := h.authorizeAgent(ctx, c, permission)
	return principal, ok
}

func (h *Handler) authorizeAgent(
	ctx context.Context,
	c *app.RequestContext,
	permission onprem.Permission,
) (onprem.Principal, bool, string) {
	if !h.requireOnPrem(c) {
		return onprem.Principal{}, false, "not_configured"
	}
	key, err := bearerKey(c)
	if err != nil {
		c.String(consts.StatusUnauthorized, "unauthorized")
		return onprem.Principal{}, false, "unauthorized"
	}
	principal, err := h.credentials.Authenticate(ctx, key)
	if err != nil {
		if errors.Is(err, onprem.ErrUnauthorized) {
			c.String(consts.StatusUnauthorized, "unauthorized")
			return onprem.Principal{}, false, "unauthorized"
		}
		h.logger.ErrorContext(ctx, "authenticate agent credential failed", "error", err)
		c.String(consts.StatusInternalServerError, "authentication unavailable")
		return onprem.Principal{}, false, "authentication_unavailable"
	}
	if permission != "" && !principal.HasPermission(permission) {
		c.String(consts.StatusForbidden, "forbidden")
		return principal, false, "forbidden"
	}
	return principal, true, ""
}

func (h *Handler) recordAuthorizationRejection(
	ctx context.Context,
	principal onprem.Principal,
	kind operations.Kind,
	startedAt time.Time,
	errorCode string,
) {
	if principal.AgentID == "" {
		return
	}
	h.recordAgentOperation(ctx, principal, operations.Event{
		Kind: kind, Outcome: operations.OutcomeRejected, StartedAt: startedAt, ErrorCode: errorCode,
	})
}

func (h *Handler) recordAgentOperation(
	ctx context.Context,
	principal onprem.Principal,
	event operations.Event,
) {
	if h.recorder == nil {
		return
	}
	attemptID, err := operations.NewAttemptID()
	if err != nil {
		h.logger.ErrorContext(ctx, "create operation attempt ID failed", "error", err)
		return
	}
	completedAt := time.Now().UTC()
	if completedAt.Before(event.StartedAt) {
		completedAt = event.StartedAt
	}
	event.AttemptID = attemptID
	event.Actor = operations.Actor{
		Kind: "agent", UserID: principal.UserID, MembershipID: principal.MembershipID,
		AgentID: principal.AgentID, CredentialID: principal.CredentialID,
	}
	event.CompletedAt = completedAt
	event.DurationMS = completedAt.Sub(event.StartedAt).Milliseconds()
	recordContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if _, err := h.recorder.Record(recordContext, event); err != nil {
		h.logger.ErrorContext(ctx, "record operation event failed", "operation_kind", event.Kind, "error", err,
			"dropped_observations", operations.DroppedObservations(h.recorder))
	}
}

func memorySearchOperation(result recall.SearchResult, sessionID string, startedAt time.Time) operations.Event {
	event := operations.Event{
		Kind: operations.KindMemorySearch, Outcome: operations.OutcomeSucceeded, StartedAt: startedAt,
		SessionID: sessionID, InputItems: 1, ResultItems: int64(len(result.Hits)),
	}
	for _, hit := range result.Hits {
		switch hit.Disposition {
		case recall.DispositionEvidence:
			event.EvidenceItems++
		case recall.DispositionHint:
			event.HintItems++
		case recall.DispositionReference:
			event.ReferenceItems++
		}
	}
	event.DeliveredItems = event.EvidenceItems
	if result.ObservationID > 0 {
		event.DetailKind = "recall_observation"
		event.DetailID = strconv.FormatInt(result.ObservationID, 10)
	}
	return event
}

func operationFailure(err error) (operations.Outcome, string) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return operations.OutcomeTimedOut, "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return operations.OutcomeCancelled, "cancelled"
	default:
		return operations.OutcomeFailed, "operation_failed"
	}
}

func (h *Handler) requireOnPrem(c *app.RequestContext) bool {
	if h.credentials == nil || h.memory == nil {
		c.String(consts.StatusNotImplemented, "on-prem API is not configured")
		return false
	}
	return true
}

func (h *Handler) writeOnPremError(ctx context.Context, c *app.RequestContext, operation string, err error) {
	h.logger.ErrorContext(ctx, operation+" failed", "error", err)
	switch {
	case errors.Is(err, onprem.ErrUnauthorized), errors.Is(err, onprem.ErrEnrollmentInvalid):
		c.String(consts.StatusUnauthorized, operation)
	case errors.Is(err, onprem.ErrForbidden):
		c.String(consts.StatusForbidden, operation)
	case errors.Is(err, onprem.ErrCredentialNotFound):
		c.String(consts.StatusNotFound, operation)
	case errors.Is(err, onprem.ErrAgentIdentityConflict):
		c.String(consts.StatusConflict, operation)
	default:
		c.String(consts.StatusUnprocessableEntity, operation)
	}
}
