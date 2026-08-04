package handler

// Team control plane endpoints (SaaS profile): /v1/teams and
// /v1/me/current-team. All three authenticate the human session with
// authorizeHuman rather than authorizeHumanMember, because a freshly signed
// up user with no membership anywhere must be able to create their first
// team and list their (empty) team set.

import (
	"context"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	api "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/model/teammemory/api"
)

func (h *Handler) CreateTeam(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHuman(ctx, c, true)
	if !ok || !h.requireTeams(c) {
		return
	}
	var request api.CreateTeamRequest
	if err := c.BindAndValidate(&request); err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	team, err := h.teams.CreateTeam(
		ctx, principal, request.Name, strings.TrimSpace(string(c.GetHeader("Idempotency-Key"))),
	)
	if err != nil {
		h.writeHumanError(c, "create team", err)
		return
	}
	c.JSON(consts.StatusCreated, &api.TeamResponse{Team: teamToAPI(team)})
}

func (h *Handler) ListTeams(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHuman(ctx, c, false)
	if !ok || !h.requireTeams(c) {
		return
	}
	summaries, err := h.teams.ListTeams(ctx, principal)
	if err != nil {
		h.writeHumanError(c, "list teams", err)
		return
	}
	c.JSON(consts.StatusOK, &api.ListTeamsResponse{Teams: teamSummariesToAPI(summaries)})
}

func (h *Handler) SwitchCurrentTeam(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHuman(ctx, c, true)
	if !ok || !h.requireTeams(c) {
		return
	}
	var request api.SwitchTeamRequest
	if err := c.BindAndValidate(&request); err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	updated, err := h.teams.SwitchTeam(ctx, principal, request.TeamID)
	if err != nil {
		h.writeHumanError(c, "switch current team", err)
		return
	}
	response, err := h.humanMeResponse(ctx, updated)
	if err != nil {
		h.writeHumanError(c, "load switched team membership", err)
		return
	}
	c.JSON(consts.StatusOK, response)
}

// requireTeams answers 501 when the SaaS team lifecycle is not wired, which
// is the on-prem profile's shape for these endpoints.
func (h *Handler) requireTeams(c *app.RequestContext) bool {
	if h.teams == nil {
		writeHumanAPIError(c, consts.StatusNotImplemented, "not_configured", "teams are not configured")
		return false
	}
	return true
}
