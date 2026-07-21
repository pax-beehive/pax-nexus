package handler

import (
	"context"
	"errors"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
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
	principal, ok := h.authorize(ctx, c, onprem.PermissionObserve)
	if !ok {
		return
	}
	var request api.ObservationBatch
	if err := c.BindAndValidate(&request); err != nil {
		c.String(consts.StatusBadRequest, "invalid observation batch")
		return
	}
	batch, err := observationBatchToDomain(&request, principal)
	if err != nil {
		c.String(consts.StatusBadRequest, "invalid observation batch")
		return
	}
	receipt, err := h.runtime.ObserveSession(teamnote.WithScope(ctx, principal.ScopeID), batch)
	if err != nil {
		h.logger.ErrorContext(ctx, "observe batch failed", "credential_id", principal.CredentialID, "error", err)
		c.String(consts.StatusUnprocessableEntity, "observe batch")
		return
	}
	c.JSON(consts.StatusOK, observationReceiptToAPI(receipt, request.GetIdempotencyKey()))
}

func (h *Handler) SearchMemory(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorize(ctx, c, onprem.PermissionSearch)
	if !ok {
		return
	}
	var request api.MemorySearchRequest
	if err := c.BindAndValidate(&request); err != nil {
		c.String(consts.StatusBadRequest, "invalid memory search")
		return
	}
	result, err := h.memory.Search(teamnote.WithScope(ctx, principal.ScopeID), memorySearchRequestToDomain(&request, principal))
	if err != nil {
		h.logger.ErrorContext(ctx, "search memory failed", "credential_id", principal.CredentialID, "error", err)
		c.String(consts.StatusUnprocessableEntity, "search memory")
		return
	}
	c.JSON(consts.StatusOK, memorySearchResultToAPI(result))
}

func (h *Handler) GetMemory(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorize(ctx, c, onprem.PermissionGet)
	if !ok {
		return
	}
	var request api.MemoryGetRequest
	if err := c.BindAndValidate(&request); err != nil {
		c.String(consts.StatusBadRequest, "invalid memory get")
		return
	}
	document, err := h.memory.Get(teamnote.WithScope(ctx, principal.ScopeID), recall.GetRequest{
		Actor: teamnote.Actor{UserID: principal.UserID, AgentID: principal.AgentID, SessionID: request.SessionID}, Ref: request.Ref,
	})
	if err != nil {
		h.logger.ErrorContext(ctx, "get memory failed", "credential_id", principal.CredentialID, "error", err)
		c.String(consts.StatusUnprocessableEntity, "get memory")
		return
	}
	c.JSON(consts.StatusOK, memoryDocumentToAPI(document))
}

func (h *Handler) authorize(
	ctx context.Context,
	c *app.RequestContext,
	permission onprem.Permission,
) (onprem.Principal, bool) {
	if !h.requireOnPrem(c) {
		return onprem.Principal{}, false
	}
	key, err := bearerKey(c)
	if err != nil {
		c.String(consts.StatusUnauthorized, "unauthorized")
		return onprem.Principal{}, false
	}
	principal, err := h.credentials.Authenticate(ctx, key)
	if err != nil {
		if errors.Is(err, onprem.ErrUnauthorized) {
			c.String(consts.StatusUnauthorized, "unauthorized")
			return onprem.Principal{}, false
		}
		h.logger.ErrorContext(ctx, "authenticate agent credential failed", "error", err)
		c.String(consts.StatusInternalServerError, "authentication unavailable")
		return onprem.Principal{}, false
	}
	if permission != "" && !principal.HasPermission(permission) {
		c.String(consts.StatusForbidden, "forbidden")
		return onprem.Principal{}, false
	}
	return principal, true
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
	default:
		c.String(consts.StatusUnprocessableEntity, operation)
	}
}
