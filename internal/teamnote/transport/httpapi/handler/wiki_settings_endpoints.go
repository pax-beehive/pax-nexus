package handler

import (
	"context"
	"errors"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	api "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/model/teammemory/api"
)

func (h *Handler) GetWikiGenerationSettings(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeWikiSettings(ctx, c, false)
	if !ok {
		return
	}
	directives, err := h.wikiSettings.GenerationSettings(ctx, principal.ScopeID)
	if err != nil {
		h.logger.Error("get Wiki generation settings", "error", err)
		writeHumanAPIError(c, consts.StatusInternalServerError, "internal_error", "the request could not be completed")
		return
	}
	c.JSON(consts.StatusOK, &api.WikiGenerationSettingsResponse{
		Language: directives.Language, CustomInstructions: directives.CustomInstructions,
	})
}

func (h *Handler) UpdateWikiGenerationSettings(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeWikiSettings(ctx, c, true)
	if !ok {
		return
	}
	var request api.UpdateWikiGenerationSettingsRequest
	if err := c.BindAndValidate(&request); err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	stored, err := h.wikiSettings.SetGenerationSettings(ctx, principal.ScopeID, pagewiki.GenerationDirectives{
		Language: request.Language, CustomInstructions: request.CustomInstructions,
	})
	switch {
	case errors.Is(err, pagewiki.ErrInvalidGenerationSettings):
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", err.Error())
		return
	case err != nil:
		h.logger.Error("update Wiki generation settings", "error", err)
		writeHumanAPIError(c, consts.StatusInternalServerError, "internal_error", "the request could not be completed")
		return
	}
	c.JSON(consts.StatusOK, &api.WikiGenerationSettingsResponse{
		Language: stored.Language, CustomInstructions: stored.CustomInstructions,
	})
}

func (h *Handler) authorizeWikiSettings(
	ctx context.Context,
	c *app.RequestContext,
	mutation bool,
) (onprem.HumanPrincipal, bool) {
	if h.wikiSettings == nil {
		writeHumanAPIError(c, consts.StatusNotImplemented, "not_configured", "Wiki settings are not configured")
		return onprem.HumanPrincipal{}, false
	}
	return h.authorizeHumanMember(ctx, c, mutation)
}
