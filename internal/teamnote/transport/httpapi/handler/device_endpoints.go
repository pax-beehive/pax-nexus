package handler

import (
	"context"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	api "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/model/teammemory/api"
)

// Device provisioning endpoints. ProvisionDeviceAgent (Task 5),
// ListDeviceProvisions (Task 6), RevokeAdminDevice (Task 7),
// ListAdminDevices/GetAdminDevice (Task 8), and CreateDeviceEnrollment
// (Task 8.5) are implemented below.

// CreateDeviceEnrollment creates a one-time device enrollment token (Task
// 8.5's device provisioning portal endpoint). It is a human-session admin
// mutation, so it mirrors this file's own ListAdminDevices/RevokeAdminDevice
// and identity_registry_endpoints.go's CreateOwnedAgentEnrollment exactly:
// authorizeHumanMember(ctx, c, true) handles session auth + CSRF (mutation
// requests require CSRF; RegistryService.CreateDeviceEnrollment itself
// re-checks Owner/Admin via authorizeHumanAdmin, so the member-level
// authorize here is defense in depth, not the only gate). Unlike
// ProvisionDeviceAgent/ExchangeAgentEnrollment (device-token flows), this
// endpoint does not call requireOnPrem: none of its human-session siblings in
// this file do, since that guard checks h.credentials/h.memory, not
// h.registry.
func (h *Handler) CreateDeviceEnrollment(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, true)
	if !ok {
		return
	}
	var request api.CreateDeviceEnrollmentRequest
	if err := c.BindAndValidate(&request); err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	deviceName := strings.TrimSpace(request.DeviceName)
	permissions := make([]onprem.Permission, len(request.GrantablePermissions))
	for index, permission := range request.GrantablePermissions {
		permissions[index] = onprem.Permission(permission)
	}
	enrollment, grantablePermissions, err := h.registry.CreateDeviceEnrollment(ctx, principal, onprem.DeviceEnrollmentRequest{
		DeviceName: deviceName, GrantablePermissions: permissions,
		ExpiresIn: time.Duration(request.GetExpiresInSeconds()) * time.Second,
	})
	if err != nil {
		h.writeHumanError(c, "create device enrollment", err)
		return
	}
	c.JSON(consts.StatusCreated, deviceEnrollmentToAPI(enrollment, deviceName, grantablePermissions))
}

// ProvisionDeviceAgent creates or rotates the credential for an agent that a
// device credential provisions. The service re-checks that the
// authenticated principal is device-kind and carries PermissionAgentProvision,
// so an agent credential that somehow carries the permission is still
// rejected with 403.
func (h *Handler) ProvisionDeviceAgent(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorize(ctx, c, onprem.PermissionAgentProvision)
	if !ok {
		return
	}
	var request api.ProvisionDeviceAgentRequest
	if err := c.BindAndValidate(&request); err != nil {
		c.String(consts.StatusBadRequest, "invalid device agent provisioning request")
		return
	}
	provisioned, err := h.credentials.ProvisionDeviceAgent(ctx, principal, deviceProvisionRequestToDomain(&request))
	if err != nil {
		h.writeOnPremError(ctx, c, "provision device agent", err)
		return
	}
	c.JSON(consts.StatusOK, provisionedAgentCredentialToAPI(provisioned, principal))
}

// ListDeviceProvisions returns every credential the calling device
// credential has provisioned, including revoked history. The service
// re-checks that the authenticated principal is device-kind and carries
// PermissionAgentProvision, so an agent credential is rejected with 403.
func (h *Handler) ListDeviceProvisions(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorize(ctx, c, onprem.PermissionAgentProvision)
	if !ok {
		return
	}
	agents, err := h.credentials.ListDeviceProvisionedAgents(ctx, principal)
	if err != nil {
		h.writeOnPremError(ctx, c, "list device provisions", err)
		return
	}
	c.JSON(consts.StatusOK, deviceProvisionedAgentsToAPI(agents))
}

// ListAdminDevices returns the admin device-management listing (device
// credentials only). It mirrors ListAdminAgents's human-session handling
// exactly: a GET, so no CSRF is required.
func (h *Handler) ListAdminDevices(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, false)
	if !ok {
		return
	}
	limit, err := queryLimit(c)
	if err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	devices, err := h.registry.ListDevices(ctx, principal, onprem.DeviceFilter{
		Status: c.Query("status"), Limit: limit, Cursor: c.Query("cursor"),
	})
	if err != nil {
		h.writeHumanError(c, "list admin devices", err)
		return
	}
	c.JSON(consts.StatusOK, deviceListToAPI(devices, limit))
}

// GetAdminDevice returns a device credential's summary plus every credential
// row it has provisioned (including revoked history). It mirrors
// GetAdminAgent's human-session handling exactly: a GET, so no CSRF is
// required.
func (h *Handler) GetAdminDevice(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, false)
	if !ok {
		return
	}
	detail, err := h.registry.GetDevice(ctx, principal, c.Param("credential_id"))
	if err != nil {
		h.writeHumanError(c, "get admin device", err)
		return
	}
	c.JSON(consts.StatusOK, deviceDetailToAPI(detail))
}

// RevokeAdminDevice revokes a device credential and cascades to every agent
// credential it provisioned (Task 7). It mirrors RevokeAdminAgentCredential's
// human-session + CSRF + Idempotency-Key handling exactly.
func (h *Handler) RevokeAdminDevice(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeHumanMember(ctx, c, true)
	if !ok {
		return
	}
	summary, err := h.registry.RevokeDevice(
		ctx, principal, c.Param("credential_id"),
		strings.TrimSpace(string(c.GetHeader("Idempotency-Key"))),
	)
	if err != nil {
		h.writeHumanError(c, "revoke admin device", err)
		return
	}
	c.JSON(consts.StatusOK, &api.DeviceSummaryResponse{Device: deviceSummaryToAPI(summary)})
}
