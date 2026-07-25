package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// Device provisioning endpoints (Task 4: IDL + codegen only). These are
// minimal placeholders that keep the generated handler stubs compiling;
// Tasks 5-8 replace these bodies with the real domain-backed
// implementations (device enrollment, device-scoped agent provisioning,
// and admin device management).

func (h *Handler) CreateDeviceEnrollment(_ context.Context, c *app.RequestContext) {
	if !h.requireOnPrem(c) {
		return
	}
	c.String(consts.StatusNotImplemented, "create device enrollment is not implemented")
}

func (h *Handler) ProvisionDeviceAgent(_ context.Context, c *app.RequestContext) {
	if !h.requireOnPrem(c) {
		return
	}
	c.String(consts.StatusNotImplemented, "provision device agent is not implemented")
}

func (h *Handler) ListDeviceProvisions(_ context.Context, c *app.RequestContext) {
	if !h.requireOnPrem(c) {
		return
	}
	c.String(consts.StatusNotImplemented, "list device provisions is not implemented")
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

func (h *Handler) RevokeAdminDevice(_ context.Context, c *app.RequestContext) {
	if !h.requireOnPrem(c) {
		return
	}
	c.String(consts.StatusNotImplemented, "revoke admin device is not implemented")
}
