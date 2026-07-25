package handler

import (
	"context"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	api "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/model/teammemory/api"
)

// Device provisioning endpoints (Task 4: IDL + codegen only). These are
// minimal placeholders that keep the generated handler stubs compiling;
// Task 8 replaces the remaining bodies with the real domain-backed
// implementations (device enrollment and admin device listing/get).
// ProvisionDeviceAgent (Task 5), ListDeviceProvisions (Task 6), and
// RevokeAdminDevice (Task 7) are implemented below.

func (h *Handler) CreateDeviceEnrollment(_ context.Context, c *app.RequestContext) {
	if !h.requireOnPrem(c) {
		return
	}
	c.String(consts.StatusNotImplemented, "create device enrollment is not implemented")
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
	c.JSON(consts.StatusOK, provisionedAgentCredentialToAPI(provisioned))
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

func (h *Handler) ListAdminDevices(_ context.Context, c *app.RequestContext) {
	if !h.requireOnPrem(c) {
		return
	}
	c.String(consts.StatusNotImplemented, "list admin devices is not implemented")
}

func (h *Handler) GetAdminDevice(_ context.Context, c *app.RequestContext) {
	if !h.requireOnPrem(c) {
		return
	}
	c.String(consts.StatusNotImplemented, "get admin device is not implemented")
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
