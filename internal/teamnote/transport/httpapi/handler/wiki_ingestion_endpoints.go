package handler

import (
	"context"
	"errors"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/sessionconsumer"
	api "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/model/teammemory/api"
)

func (h *Handler) GetWikiIngestionStatus(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.authorizeWikiControl(ctx, c, false); !ok {
		return
	}
	status, err := h.wikiControl.Status(ctx, onprem.LocalScopeID)
	if err != nil {
		h.writeWikiControlError(c, "get Wiki ingestion status", err)
		return
	}
	c.JSON(consts.StatusOK, &api.WikiIngestionStatusResponse{AutoInject: status.AutoInject})
}

func (h *Handler) UpdateWikiIngestion(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.authorizeWikiControl(ctx, c, true); !ok {
		return
	}
	var request api.UpdateWikiIngestionRequest
	if err := c.BindAndValidate(&request); err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	status, err := h.wikiControl.SetAutoInject(ctx, onprem.LocalScopeID, request.AutoInject)
	if err != nil {
		h.writeWikiControlError(c, "update Wiki ingestion", err)
		return
	}
	c.JSON(consts.StatusOK, &api.WikiIngestionStatusResponse{AutoInject: status.AutoInject})
}

func (h *Handler) InjectWikiSession(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.authorizeWikiControl(ctx, c, true); !ok {
		return
	}
	sessionID := strings.TrimSpace(c.Param("session_id"))
	if sessionID == "" {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "session ID is required")
		return
	}
	result, err := h.wikiControl.InjectSession(ctx, onprem.LocalScopeID, sessionID)
	if err != nil {
		h.writeWikiControlError(c, "inject Wiki session", err)
		return
	}
	c.JSON(consts.StatusOK, &api.InjectWikiSessionResponse{ProcessedStreams: int32(result.ProcessedStreams)})
}

func (h *Handler) authorizeWikiControl(
	ctx context.Context,
	c *app.RequestContext,
	mutation bool,
) (onprem.HumanPrincipal, bool) {
	if h.wikiControl == nil {
		writeHumanAPIError(c, consts.StatusNotImplemented, "not_configured", "Wiki ingestion is not configured")
		return onprem.HumanPrincipal{}, false
	}
	return h.authorizeHumanMember(ctx, c, mutation)
}

func (h *Handler) writeWikiControlError(c *app.RequestContext, operation string, err error) {
	switch {
	case errors.Is(err, sessionconsumer.ErrSessionNotFound):
		writeHumanAPIError(c, consts.StatusNotFound, "session_not_found", "the requested session was not found")
	default:
		h.logger.Error(operation, "error", err)
		writeHumanAPIError(c, consts.StatusInternalServerError, "internal_error", "the request could not be completed")
	}
}
