package handler

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/deployment/saas"
	api "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/model/teammemory/api"
)

const (
	oidcFlowCookieName     = "tm_oidc_flow"
	humanSessionCookieName = "tm_human_session"
	csrfCookieName         = "tm_csrf"
	csrfHeaderName         = "X-CSRF-Token"
)

func (h *Handler) BeginHumanLogin(_ context.Context, c *app.RequestContext) {
	if !h.requireHumanIdentity(c) {
		return
	}
	flow, err := h.oidc.BeginLogin()
	if err != nil {
		h.writeHumanError(c, "begin human login", err)
		return
	}
	maxAge := int(time.Until(flow.ExpiresAt).Seconds())
	c.SetCookie(oidcFlowCookieName, flow.CookieValue, maxAge, "/v1/auth", "",
		protocol.CookieSameSiteLaxMode, h.cookieSecure, true)
	c.Redirect(consts.StatusFound, []byte(flow.AuthorizationURL))
}

func (h *Handler) CompleteHumanLogin(ctx context.Context, c *app.RequestContext) {
	if !h.requireHumanIdentity(c) {
		return
	}
	var request api.AuthCallbackRequest
	if err := c.BindAndValidate(&request); err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	identity, err := h.oidc.CompleteLogin(
		ctx, request.Code, request.State, string(c.Cookie(oidcFlowCookieName)),
	)
	if err != nil {
		h.writeHumanError(c, "complete human login", err)
		return
	}
	session, err := h.identity.Login(ctx, identity)
	if err != nil {
		h.writeHumanError(c, "persist human login", err)
		return
	}
	maxAge := int(time.Until(session.ExpiresAt).Seconds())
	c.SetCookie(humanSessionCookieName, session.Token, maxAge, "/", "",
		protocol.CookieSameSiteLaxMode, h.cookieSecure, true)
	c.SetCookie(csrfCookieName, csrfToken(session.Token), maxAge, "/", "",
		protocol.CookieSameSiteLaxMode, h.cookieSecure, false)
	c.SetCookie(oidcFlowCookieName, "", -1, "/v1/auth", "",
		protocol.CookieSameSiteLaxMode, h.cookieSecure, true)
	c.Redirect(consts.StatusFound, []byte(h.portalURL))
}

func (h *Handler) LogoutHuman(ctx context.Context, c *app.RequestContext) {
	if !h.requireHumanIdentity(c) || !validateCSRF(c) {
		if h.identity != nil {
			writeHumanAPIError(c, consts.StatusForbidden, "csrf_invalid", "the CSRF token is invalid")
		}
		return
	}
	token := string(c.Cookie(humanSessionCookieName))
	if err := h.identity.Logout(ctx, token); err != nil {
		h.writeHumanError(c, "logout human session", err)
		return
	}
	h.clearHumanCookies(c)
	c.JSON(consts.StatusOK, &api.EmptyResponse{Ok: true})
}

func (h *Handler) ClaimBootstrap(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHuman(ctx, c, true)
	if !ok {
		return
	}
	claimed, err := h.identity.ClaimBootstrap(
		ctx, principal, strings.TrimSpace(string(c.GetHeader("X-PAX-Bootstrap-Secret"))),
	)
	if err != nil {
		h.writeHumanError(c, "claim bootstrap", err)
		return
	}
	c.JSON(consts.StatusOK, humanPrincipalToAPI(claimed))
}

func (h *Handler) GetHumanMe(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHuman(ctx, c, false)
	if !ok {
		return
	}
	response, err := h.humanMeResponse(ctx, principal)
	if err != nil {
		h.writeHumanError(c, "get human me", err)
		return
	}
	c.JSON(consts.StatusOK, response)
}

// humanMeResponse builds the /v1/me payload. When the SaaS team lifecycle
// is wired it attaches the caller's team list and current team; without it
// (the on-prem profile) the response is exactly what it has always been.
func (h *Handler) humanMeResponse(
	ctx context.Context,
	principal onprem.HumanPrincipal,
) (*api.HumanMeResponse, error) {
	response := humanPrincipalToAPI(principal)
	if h.teams == nil {
		return response, nil
	}
	summaries, err := h.teams.ListTeams(ctx, principal)
	if err != nil {
		return nil, fmt.Errorf("list teams for human me: %w", err)
	}
	response.Teams = teamSummariesToAPI(summaries)
	if principal.ScopeID != "" {
		response.CurrentTeamID = &principal.ScopeID
	}
	return response, nil
}

func (h *Handler) ListMembers(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, false)
	if !ok {
		return
	}
	limit, err := queryLimit(c)
	if err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	members, err := h.identity.ListMembers(ctx, principal, onprem.MemberFilter{
		Role: onprem.Role(c.Query("role")), Status: onprem.MembershipStatus(c.Query("status")),
		Limit: limit, Cursor: c.Query("cursor"),
	})
	if err != nil {
		h.writeHumanError(c, "list members", err)
		return
	}
	c.JSON(consts.StatusOK, memberListToAPI(members, limit))
}

func (h *Handler) GetMember(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, false)
	if !ok {
		return
	}
	member, err := h.identity.GetMember(ctx, principal, c.Param("membership_id"))
	if err != nil {
		h.writeHumanError(c, "get member", err)
		return
	}
	c.JSON(consts.StatusOK, &api.MemberResponse{Member: memberToAPI(member)})
}

func (h *Handler) UpdateMember(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, true)
	if !ok {
		return
	}
	var request api.UpdateMemberRequest
	if err := c.BindAndValidate(&request); err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	resourceVersion, err := mutationResourceVersion(c, request.ResourceVersion)
	if err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	var role *onprem.Role
	if request.Role != nil {
		value := onprem.Role(*request.Role)
		role = &value
	}
	var status *onprem.MembershipStatus
	if request.Status != nil {
		value := onprem.MembershipStatus(*request.Status)
		status = &value
	}
	member, err := h.identity.UpdateMember(ctx, principal, request.MembershipID, onprem.UpdateMemberRequest{
		Role: role, Status: status, ResourceVersion: resourceVersion,
	})
	if err != nil {
		h.writeHumanError(c, "update member", err)
		return
	}
	c.JSON(consts.StatusOK, &api.MemberResponse{Member: memberToAPI(member)})
}

func (h *Handler) ListAuditEvents(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, false)
	if !ok {
		return
	}
	limit, err := queryLimit(c)
	if err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	events, err := h.identity.ListAuditEvents(ctx, principal, onprem.AuditFilter{
		ActorKind: c.Query("actor_kind"), Action: c.Query("action"),
		TargetKind: c.Query("target_kind"), TargetID: c.Query("target_id"),
		Limit: limit, Cursor: c.Query("cursor"),
	})
	if err != nil {
		h.writeHumanError(c, "list audit events", err)
		return
	}
	c.JSON(consts.StatusOK, auditEventListToAPI(events, limit))
}

func (h *Handler) GetAuditEvent(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, false)
	if !ok {
		return
	}
	auditEventID, err := strconv.ParseInt(c.Param("audit_event_id"), 10, 64)
	if err != nil || auditEventID <= 0 {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	event, err := h.identity.GetAuditEvent(ctx, principal, auditEventID)
	if err != nil {
		h.writeHumanError(c, "get audit event", err)
		return
	}
	c.JSON(consts.StatusOK, &api.AuditEventResponse{AuditEvent: auditEventToAPI(event)})
}

func (h *Handler) CreateMembershipInvitation(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHuman(ctx, c, true)
	if !ok {
		return
	}
	var request api.CreateInvitationRequest
	if err := c.BindAndValidate(&request); err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	invitation, err := h.identity.CreateInvitation(ctx, principal, onprem.InvitationRequest{
		TargetEmail: request.TargetEmail, Role: onprem.Role(request.Role),
		ExpiresIn: time.Duration(request.GetExpiresInSeconds()) * time.Second,
	})
	if err != nil {
		h.writeHumanError(c, "create membership invitation", err)
		return
	}
	c.JSON(consts.StatusCreated, invitationToAPI(invitation))
}

func (h *Handler) ListMembershipInvitations(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, false)
	if !ok {
		return
	}
	limit, err := queryLimit(c)
	if err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	invitations, err := h.identity.ListInvitations(ctx, principal, onprem.InvitationFilter{
		Status: onprem.InvitationStatus(c.Query("status")), Limit: limit, Cursor: c.Query("cursor"),
	})
	if err != nil {
		h.writeHumanError(c, "list membership invitations", err)
		return
	}
	c.JSON(consts.StatusOK, invitationListToAPI(invitations, limit))
}

func (h *Handler) RevokeMembershipInvitation(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, true)
	if !ok {
		return
	}
	invitation, err := h.identity.RevokeInvitation(ctx, principal, c.Param("invitation_id"))
	if err != nil {
		h.writeHumanError(c, "revoke membership invitation", err)
		return
	}
	c.JSON(consts.StatusOK, invitationToAPI(invitation))
}

func (h *Handler) AcceptMembershipInvitation(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHuman(ctx, c, true)
	if !ok {
		return
	}
	var request api.AcceptInvitationRequest
	if err := c.BindAndValidate(&request); err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	accepted, err := h.identity.AcceptInvitation(
		ctx, principal, request.Token, strings.TrimSpace(string(c.GetHeader("Idempotency-Key"))),
	)
	if err != nil {
		h.writeHumanError(c, "accept membership invitation", err)
		return
	}
	c.JSON(consts.StatusOK, humanPrincipalToAPI(accepted))
}

func (h *Handler) ListOwnedAgents(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, false)
	if !ok {
		return
	}
	limit, err := queryLimit(c)
	if err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	agents, err := h.registry.ListOwnedAgents(ctx, principal, onprem.AgentFilter{
		Status: onprem.AgentStatus(c.Query("status")), Limit: limit, Cursor: c.Query("cursor"),
	})
	if err != nil {
		h.writeHumanError(c, "list owned agents", err)
		return
	}
	c.JSON(consts.StatusOK, ownedAgentListToAPI(agents, limit))
}

func (h *Handler) CreateOwnedAgent(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, true)
	if !ok {
		return
	}
	var request api.CreateAgentProfileRequest
	if err := c.BindAndValidate(&request); err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	directoryVisible := true
	if request.IsSetDirectoryVisible() {
		directoryVisible = request.GetDirectoryVisible()
	}
	agent, err := h.registry.CreateAgent(ctx, principal, onprem.CreateAgentRequest{
		AgentID: request.AgentID, DisplayName: request.DisplayName,
		Description: request.GetDescription(), AgentType: request.GetAgentType(),
		DirectoryVisible: directoryVisible,
		IdempotencyKey:   strings.TrimSpace(string(c.GetHeader("Idempotency-Key"))),
	})
	if err != nil {
		h.writeHumanError(c, "create owned agent", err)
		return
	}
	c.JSON(consts.StatusCreated, &api.AgentProfileResponse{Agent: agentProfileToAPI(agent)})
}

func (h *Handler) GetOwnedAgent(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, false)
	if !ok {
		return
	}
	agent, err := h.registry.GetOwnedAgent(ctx, principal, c.Param("agent_id"))
	if err != nil {
		h.writeHumanError(c, "get owned agent", err)
		return
	}
	c.JSON(consts.StatusOK, &api.AgentProfileResponse{Agent: agentProfileToAPI(agent)})
}

func (h *Handler) UpdateOwnedAgent(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, true)
	if !ok {
		return
	}
	var request api.UpdateAgentProfileRequest
	if err := c.BindAndValidate(&request); err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	resourceVersion, err := mutationResourceVersion(c, request.ResourceVersion)
	if err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	var status *onprem.AgentStatus
	if request.Status != nil {
		value := onprem.AgentStatus(*request.Status)
		status = &value
	}
	agent, err := h.registry.UpdateOwnedAgent(ctx, principal, request.AgentID, onprem.UpdateAgentRequest{
		DisplayName: request.DisplayName, Description: request.Description, AgentType: request.AgentType,
		DirectoryVisible: request.DirectoryVisible, Status: status, ResourceVersion: resourceVersion,
	})
	if err != nil {
		h.writeHumanError(c, "update owned agent", err)
		return
	}
	c.JSON(consts.StatusOK, &api.AgentProfileResponse{Agent: agentProfileToAPI(agent)})
}

func (h *Handler) RetireOwnedAgent(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, true)
	if !ok {
		return
	}
	var request api.RetireAgentProfileRequest
	if err := c.BindAndValidate(&request); err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	resourceVersion, err := mutationResourceVersion(c, request.ResourceVersion)
	if err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	agent, err := h.registry.RetireOwnedAgent(
		ctx, principal, request.AgentID, resourceVersion,
		strings.TrimSpace(string(c.GetHeader("Idempotency-Key"))),
	)
	if err != nil {
		h.writeHumanError(c, "retire owned agent", err)
		return
	}
	c.JSON(consts.StatusOK, &api.AgentProfileResponse{Agent: agentProfileToAPI(agent)})
}

func (h *Handler) CreateOwnedAgentEnrollment(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, true)
	if !ok {
		return
	}
	var request api.CreateOwnedEnrollmentRequest
	if err := c.BindAndValidate(&request); err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	permissions := make([]onprem.Permission, len(request.Permissions))
	for index, permission := range request.Permissions {
		permissions[index] = onprem.Permission(permission)
	}
	var credentialExpiresAt *time.Time
	if request.IsSetCredentialExpiresAt() {
		parsed, err := time.Parse(time.RFC3339Nano, request.GetCredentialExpiresAt())
		if err != nil {
			writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
			return
		}
		credentialExpiresAt = &parsed
	}
	enrollment, err := h.registry.CreateEnrollment(ctx, principal, request.AgentID, onprem.OwnerEnrollmentRequest{
		CredentialLabel: request.CredentialLabel, Permissions: permissions,
		ExpiresIn:           time.Duration(request.GetExpiresInSeconds()) * time.Second,
		CredentialExpiresAt: credentialExpiresAt,
	})
	if err != nil {
		h.writeHumanError(c, "create owned agent enrollment", err)
		return
	}
	c.JSON(consts.StatusCreated, enrollmentToAPI(enrollment))
}

func (h *Handler) ListOwnedAgentEnrollments(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, false)
	if !ok {
		return
	}
	limit, err := queryLimit(c)
	if err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	enrollments, err := h.registry.ListEnrollments(ctx, principal, c.Param("agent_id"), onprem.AgentArtifactFilter{
		Status: c.Query("status"), Limit: limit, Cursor: c.Query("cursor"),
	})
	if err != nil {
		h.writeHumanError(c, "list owned agent enrollments", err)
		return
	}
	c.JSON(consts.StatusOK, enrollmentMetadataListToAPI(enrollments, limit))
}

func (h *Handler) RevokeOwnedAgentEnrollment(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, true)
	if !ok {
		return
	}
	enrollment, err := h.registry.RevokeEnrollment(
		ctx, principal, c.Param("agent_id"), c.Param("enrollment_id"),
		strings.TrimSpace(string(c.GetHeader("Idempotency-Key"))),
	)
	if err != nil {
		h.writeHumanError(c, "revoke owned agent enrollment", err)
		return
	}
	c.JSON(consts.StatusOK, &api.AgentEnrollmentMetadataResponse{Enrollment: enrollmentMetadataToAPI(enrollment)})
}

func (h *Handler) ListOwnedAgentCredentials(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, false)
	if !ok {
		return
	}
	limit, err := queryLimit(c)
	if err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	credentials, err := h.registry.ListCredentials(ctx, principal, c.Param("agent_id"), onprem.AgentArtifactFilter{
		Status: c.Query("status"), Limit: limit, Cursor: c.Query("cursor"),
	})
	if err != nil {
		h.writeHumanError(c, "list owned agent credentials", err)
		return
	}
	c.JSON(consts.StatusOK, credentialMetadataListToAPI(credentials, limit))
}

func (h *Handler) RevokeOwnedAgentCredential(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, true)
	if !ok {
		return
	}
	credential, err := h.registry.RevokeOwnedCredential(
		ctx, principal, c.Param("agent_id"), c.Param("credential_id"),
		strings.TrimSpace(string(c.GetHeader("Idempotency-Key"))),
	)
	if err != nil {
		h.writeHumanError(c, "revoke owned agent credential", err)
		return
	}
	c.JSON(consts.StatusOK, &api.AgentCredentialMetadataResponse{Credential: credentialMetadataToAPI(credential)})
}

func (h *Handler) ListDirectoryAgents(ctx context.Context, c *app.RequestContext) {
	if h.registry == nil {
		c.String(consts.StatusNotImplemented, "agent registry is not configured")
		return
	}
	principal, ok := h.authorize(ctx, c, onprem.PermissionChannelSend)
	if !ok {
		return
	}
	limit, err := queryLimit(c)
	if err != nil {
		c.String(consts.StatusBadRequest, "invalid directory limit")
		return
	}
	agents, err := h.registry.ListDirectoryAgents(ctx, principal, onprem.AgentFilter{
		Query: c.Query("q"), Limit: limit, Cursor: c.Query("cursor"),
	})
	if err != nil {
		h.writeChannelError(ctx, c, "list directory agents", err)
		return
	}
	c.JSON(consts.StatusOK, directoryAgentListToAPI(agents, limit))
}

func (h *Handler) GetDirectoryAgent(ctx context.Context, c *app.RequestContext) {
	if h.registry == nil {
		c.String(consts.StatusNotImplemented, "agent registry is not configured")
		return
	}
	principal, ok := h.authorize(ctx, c, onprem.PermissionChannelSend)
	if !ok {
		return
	}
	agent, err := h.registry.GetDirectoryAgent(ctx, principal, c.Param("agent_id"))
	if err != nil {
		h.writeChannelError(ctx, c, "get directory agent", err)
		return
	}
	c.JSON(consts.StatusOK, &api.DirectoryAgentResponse{Agent: directoryAgentToAPI(agent)})
}

func (h *Handler) ListAdminAgents(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, false)
	if !ok {
		return
	}
	limit, err := queryLimit(c)
	if err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	agents, err := h.registry.ListAdminAgents(ctx, principal, onprem.AgentFilter{
		OwnerMembershipID: c.Query("owner_membership_id"), Status: onprem.AgentStatus(c.Query("status")),
		Query: c.Query("q"), Limit: limit, Cursor: c.Query("cursor"),
	})
	if err != nil {
		h.writeHumanError(c, "list admin agents", err)
		return
	}
	c.JSON(consts.StatusOK, ownedAgentListToAPI(agents, limit))
}

func (h *Handler) GetAdminAgent(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, false)
	if !ok {
		return
	}
	agent, err := h.registry.GetAdminAgent(ctx, principal, c.Param("agent_id"))
	if err != nil {
		h.writeHumanError(c, "get admin agent", err)
		return
	}
	c.JSON(consts.StatusOK, &api.AgentProfileResponse{Agent: agentProfileToAPI(agent)})
}

func (h *Handler) UpdateAdminAgent(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, true)
	if !ok {
		return
	}
	var request api.UpdateAgentProfileRequest
	if err := c.BindAndValidate(&request); err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	resourceVersion, err := mutationResourceVersion(c, request.ResourceVersion)
	if err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	var status *onprem.AgentStatus
	if request.Status != nil {
		value := onprem.AgentStatus(*request.Status)
		status = &value
	}
	agent, err := h.registry.UpdateAdminAgent(ctx, principal, request.AgentID, onprem.UpdateAgentRequest{
		DisplayName: request.DisplayName, Description: request.Description, AgentType: request.AgentType,
		DirectoryVisible: request.DirectoryVisible, Status: status, ResourceVersion: resourceVersion,
	})
	if err != nil {
		h.writeHumanError(c, "update admin agent", err)
		return
	}
	c.JSON(consts.StatusOK, &api.AgentProfileResponse{Agent: agentProfileToAPI(agent)})
}

func (h *Handler) RetireAdminAgent(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, true)
	if !ok {
		return
	}
	var request api.RetireAgentProfileRequest
	if err := c.BindAndValidate(&request); err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	resourceVersion, err := mutationResourceVersion(c, request.ResourceVersion)
	if err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	agent, err := h.registry.RetireAdminAgent(
		ctx, principal, request.AgentID, resourceVersion,
		strings.TrimSpace(string(c.GetHeader("Idempotency-Key"))),
	)
	if err != nil {
		h.writeHumanError(c, "retire admin agent", err)
		return
	}
	c.JSON(consts.StatusOK, &api.AgentProfileResponse{Agent: agentProfileToAPI(agent)})
}

func (h *Handler) TransferAdminAgent(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, true)
	if !ok {
		return
	}
	var request api.TransferAgentRequest
	if err := c.BindAndValidate(&request); err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	resourceVersion, err := mutationResourceVersion(c, request.ResourceVersion)
	if err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	agent, err := h.registry.TransferAgent(ctx, principal, request.AgentID, onprem.TransferAgentRequest{
		TargetMembershipID: request.TargetMembershipID, ResourceVersion: resourceVersion,
	})
	if err != nil {
		h.writeHumanError(c, "transfer admin agent", err)
		return
	}
	c.JSON(consts.StatusOK, &api.AgentProfileResponse{Agent: agentProfileToAPI(agent)})
}

func (h *Handler) ListAdminAgentEnrollments(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, false)
	if !ok {
		return
	}
	limit, err := queryLimit(c)
	if err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	enrollments, err := h.registry.ListAdminEnrollments(
		ctx, principal, c.Param("agent_id"), onprem.AgentArtifactFilter{
			Status: c.Query("status"), Limit: limit, Cursor: c.Query("cursor"),
		},
	)
	if err != nil {
		h.writeHumanError(c, "list admin agent enrollments", err)
		return
	}
	c.JSON(consts.StatusOK, enrollmentMetadataListToAPI(enrollments, limit))
}

func (h *Handler) RevokeAdminAgentEnrollment(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, true)
	if !ok {
		return
	}
	enrollment, err := h.registry.RevokeAdminEnrollment(
		ctx, principal, c.Param("agent_id"), c.Param("enrollment_id"),
		strings.TrimSpace(string(c.GetHeader("Idempotency-Key"))),
	)
	if err != nil {
		h.writeHumanError(c, "revoke admin agent enrollment", err)
		return
	}
	c.JSON(consts.StatusOK, &api.AgentEnrollmentMetadataResponse{Enrollment: enrollmentMetadataToAPI(enrollment)})
}

func (h *Handler) ListAdminAgentCredentials(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, false)
	if !ok {
		return
	}
	limit, err := queryLimit(c)
	if err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	credentials, err := h.registry.ListAdminCredentials(
		ctx, principal, c.Param("agent_id"), onprem.AgentArtifactFilter{
			Status: c.Query("status"), Limit: limit, Cursor: c.Query("cursor"),
		},
	)
	if err != nil {
		h.writeHumanError(c, "list admin agent credentials", err)
		return
	}
	c.JSON(consts.StatusOK, credentialMetadataListToAPI(credentials, limit))
}

func (h *Handler) RevokeAdminAgentCredential(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, true)
	if !ok {
		return
	}
	credential, err := h.registry.RevokeAdminCredential(
		ctx, principal, c.Param("agent_id"), c.Param("credential_id"),
		strings.TrimSpace(string(c.GetHeader("Idempotency-Key"))),
	)
	if err != nil {
		h.writeHumanError(c, "revoke admin agent credential", err)
		return
	}
	c.JSON(consts.StatusOK, &api.AgentCredentialMetadataResponse{Credential: credentialMetadataToAPI(credential)})
}

func (h *Handler) authorizeHuman(
	ctx context.Context,
	c *app.RequestContext,
	mutation bool,
) (onprem.HumanPrincipal, bool) {
	if !h.requireHumanIdentity(c) {
		return onprem.HumanPrincipal{}, false
	}
	if mutation && !validateCSRF(c) {
		writeHumanAPIError(c, consts.StatusForbidden, "csrf_invalid", "the CSRF token is invalid")
		return onprem.HumanPrincipal{}, false
	}
	principal, err := h.identity.AuthenticateSession(ctx, string(c.Cookie(humanSessionCookieName)))
	if err != nil {
		h.writeHumanError(c, "authenticate human session", err)
		return onprem.HumanPrincipal{}, false
	}
	return principal, true
}

func (h *Handler) authorizeHumanMember(
	ctx context.Context,
	c *app.RequestContext,
	mutation bool,
) (onprem.HumanPrincipal, bool) {
	principal, ok := h.authorizeHuman(ctx, c, mutation)
	if !ok {
		return onprem.HumanPrincipal{}, false
	}
	if principal.MembershipID == "" || principal.MembershipStatus != onprem.MembershipStatusActive {
		writeHumanAPIError(c, consts.StatusForbidden, "membership_required", "an active membership is required")
		return onprem.HumanPrincipal{}, false
	}
	return principal, true
}

func (h *Handler) requireHumanIdentity(c *app.RequestContext) bool {
	if h.identity == nil || h.oidc == nil {
		writeHumanAPIError(c, consts.StatusNotImplemented, "not_configured", "human identity is not configured")
		return false
	}
	return true
}

func (h *Handler) clearHumanCookies(c *app.RequestContext) {
	c.SetCookie(humanSessionCookieName, "", -1, "/", "", protocol.CookieSameSiteLaxMode, h.cookieSecure, true)
	c.SetCookie(csrfCookieName, "", -1, "/", "", protocol.CookieSameSiteLaxMode, h.cookieSecure, false)
}

func (h *Handler) writeHumanError(c *app.RequestContext, operation string, err error) {
	if writeSaaSError(c, err) {
		return
	}
	switch {
	case errors.Is(err, onprem.ErrUnauthorized):
		writeHumanAPIError(c, consts.StatusUnauthorized, "unauthorized", "authentication is required")
	case errors.Is(err, onprem.ErrForbidden):
		writeHumanAPIError(c, consts.StatusForbidden, "forbidden", "the operation is not permitted")
	case errors.Is(err, onprem.ErrAgentNotFound):
		writeHumanAPIError(c, consts.StatusNotFound, "agent_not_found", "the requested resource was not found")
	case errors.Is(err, onprem.ErrAuditEventNotFound):
		writeHumanAPIError(c, consts.StatusNotFound, "audit_event_not_found", "the requested resource was not found")
	case errors.Is(err, onprem.ErrCredentialNotFound):
		writeHumanAPIError(c, consts.StatusNotFound, "credential_not_found", "the requested resource was not found")
	case errors.Is(err, onprem.ErrLastActiveOwner):
		writeHumanAPIError(c, consts.StatusConflict, "last_active_owner", "the requested change conflicts with current state")
	case errors.Is(err, onprem.ErrResourceVersionConflict):
		writeHumanAPIError(c, consts.StatusConflict, "resource_version_conflict", "the requested change conflicts with current state")
	case errors.Is(err, onprem.ErrAgentIDConflict):
		writeHumanAPIError(c, consts.StatusConflict, "agent_id_conflict", "the requested change conflicts with current state")
	case errors.Is(err, onprem.ErrIdempotencyConflict):
		writeHumanAPIError(c, consts.StatusConflict, "idempotency_conflict", "the requested change conflicts with current state")
	case errors.Is(err, onprem.ErrInvalidStateTransition):
		writeHumanAPIError(c, consts.StatusConflict, "invalid_state_transition", "the requested change conflicts with current state")
	case errors.Is(err, onprem.ErrBootstrapClosed):
		writeHumanAPIError(c, consts.StatusConflict, "bootstrap_closed", "the requested change conflicts with current state")
	case errors.Is(err, onprem.ErrMembershipConflict):
		writeHumanAPIError(c, consts.StatusConflict, "membership_conflict", "the requested change conflicts with current state")
	case errors.Is(err, onprem.ErrAgentConflict):
		writeHumanAPIError(c, consts.StatusConflict, "agent_conflict", "the requested change conflicts with current state")
	case errors.Is(err, onprem.ErrInvitationInvalid):
		writeHumanAPIError(c, consts.StatusGone, "invitation_gone", "the requested credential is no longer valid")
	case errors.Is(err, onprem.ErrEnrollmentInvalid):
		writeHumanAPIError(c, consts.StatusGone, "enrollment_gone", "the requested credential is no longer valid")
	case errors.Is(err, onprem.ErrInvalidIdentityInput):
		writeHumanAPIError(c, consts.StatusUnprocessableEntity, "invalid_input", "the request input is invalid")
	default:
		h.logger.Error("human identity request failed", "operation", operation, "error", err)
		writeHumanAPIError(c, consts.StatusInternalServerError, "internal_error", "the request could not be completed")
	}
}

// writeSaaSError maps the saas control plane sentinels to stable API error
// codes and reports whether the error matched one of them.
func writeSaaSError(c *app.RequestContext, err error) bool {
	switch {
	case errors.Is(err, saas.ErrTeamSlugConflict):
		writeHumanAPIError(c, consts.StatusConflict, "team_slug_conflict", "the requested change conflicts with current state")
	case errors.Is(err, saas.ErrNotTeamMember):
		writeHumanAPIError(c, consts.StatusForbidden, "not_team_member", "the operation is not permitted")
	case errors.Is(err, saas.ErrUnsupportedInSaaS):
		writeHumanAPIError(c, consts.StatusNotImplemented, "unsupported_profile", "the operation is not supported in this deployment")
	default:
		return false
	}
	return true
}

func writeHumanAPIError(c *app.RequestContext, status int, code, message string) {
	c.JSON(status, &api.ErrorResponse{Code: code, Message: message})
}

func validateCSRF(c *app.RequestContext) bool {
	header := strings.TrimSpace(string(c.GetHeader(csrfHeaderName)))
	cookie := strings.TrimSpace(string(c.Cookie(csrfCookieName)))
	if header == "" || len(header) != len(cookie) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header), []byte(cookie)) == 1
}

func csrfToken(sessionToken string) string {
	digest := sha256.Sum256([]byte("csrf\x00" + sessionToken))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func queryLimit(c *app.RequestContext) (int, error) {
	raw := strings.TrimSpace(c.Query("limit"))
	if raw == "" {
		return 50, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 || limit > 100 {
		return 0, errors.New("limit must be between 1 and 100")
	}
	return limit, nil
}

func mutationResourceVersion(c *app.RequestContext, bodyVersion int64) (int64, error) {
	raw := strings.TrimSpace(string(c.GetHeader("If-Match")))
	if raw == "" {
		if bodyVersion <= 0 {
			return 0, errors.New("resource version is required")
		}
		return bodyVersion, nil
	}
	raw = strings.TrimPrefix(raw, "W/")
	raw = strings.Trim(raw, `"`)
	version, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || version <= 0 || (bodyVersion > 0 && version != bodyVersion) {
		return 0, errors.New("If-Match does not match the resource version")
	}
	return version, nil
}
