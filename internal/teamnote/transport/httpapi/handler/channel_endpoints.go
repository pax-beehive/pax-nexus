package handler

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/operations"
	api "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/model/teammemory/api"
)

func (h *Handler) SendChannelEnvelope(ctx context.Context, c *app.RequestContext) {
	startedAt := time.Now().UTC()
	principal, ok, authorizationCode := h.authorizeAgent(ctx, c, onprem.PermissionChannelSend)
	if !ok {
		h.recordAuthorizationRejection(ctx, principal, operations.KindChannelSend, startedAt, authorizationCode)
		return
	}
	var request api.SendChannelEnvelopeRequest
	if err := c.BindAndValidate(&request); err != nil {
		h.recordAgentOperation(ctx, principal, operations.Event{
			Kind: operations.KindChannelSend, Outcome: operations.OutcomeRejected,
			StartedAt: startedAt, InputItems: 1, ErrorCode: "invalid_request",
		})
		c.String(consts.StatusBadRequest, "invalid channel envelope")
		return
	}
	domainRequest, err := channelSendRequestToDomain(&request)
	if err != nil {
		h.recordAgentOperation(ctx, principal, operations.Event{
			Kind: operations.KindChannelSend, Outcome: operations.OutcomeRejected,
			StartedAt: startedAt, InputItems: 1, ErrorCode: "invalid_request",
		})
		c.String(consts.StatusBadRequest, "invalid channel envelope")
		return
	}
	envelope, err := h.channel.Send(ctx, principal, domainRequest)
	if err != nil {
		outcome, errorCode := channelOperationFailure(err)
		h.recordAgentOperation(ctx, principal, operations.Event{
			Kind: operations.KindChannelSend, Outcome: outcome, StartedAt: startedAt,
			InputItems: 1, ErrorCode: errorCode,
		})
		h.writeChannelError(ctx, c, "send channel envelope", err)
		return
	}
	response, err := channelEnvelopeResponseToAPI(envelope)
	if err != nil {
		h.recordAgentOperation(ctx, principal, operations.Event{
			Kind: operations.KindChannelSend, Outcome: operations.OutcomeFailed,
			StartedAt: startedAt, InputItems: 1, ErrorCode: "response_encoding_failed",
		})
		h.writeChannelError(ctx, c, "encode channel envelope", err)
		return
	}
	h.recordAgentOperation(ctx, principal, operations.Event{
		Kind: operations.KindChannelSend, Outcome: operations.OutcomeSucceeded,
		StartedAt: startedAt, InputItems: 1, AcceptedItems: 1,
		DetailKind: "channel_envelope", DetailID: envelope.ID,
	})
	c.JSON(consts.StatusOK, response)
}

func (h *Handler) ListChannelEnvelopes(ctx context.Context, c *app.RequestContext) {
	permission := onprem.PermissionChannelReceive
	direction := strings.TrimSpace(string(c.QueryArgs().Peek("direction")))
	if direction == onprem.EnvelopeDirectionSent {
		permission = onprem.PermissionChannelSend
	}
	principal, ok := h.authorize(ctx, c, permission)
	if !ok {
		return
	}
	limit, err := channelQueryLimit(c)
	if err != nil {
		c.String(consts.StatusBadRequest, "invalid channel envelope limit")
		return
	}
	envelopes, err := h.channel.List(ctx, principal, onprem.ListEnvelopesFilter{
		Status: string(c.QueryArgs().Peek("status")), Direction: direction,
		Limit: limit, Cursor: string(c.QueryArgs().Peek("cursor")),
	})
	if err != nil {
		h.writeChannelError(ctx, c, "list channel envelopes", err)
		return
	}
	response, err := channelEnvelopeListToAPI(envelopes)
	if err != nil {
		h.writeChannelError(ctx, c, "encode channel envelopes", err)
		return
	}
	c.JSON(consts.StatusOK, response)
}

func (h *Handler) GetChannelEnvelope(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorize(ctx, c, onprem.PermissionChannelReceive)
	if !ok {
		return
	}
	envelope, err := h.channel.Get(ctx, principal, c.Param("envelope_id"))
	if err != nil {
		h.writeChannelError(ctx, c, "get channel envelope", err)
		return
	}
	response, err := channelEnvelopeResponseToAPI(envelope)
	if err != nil {
		h.writeChannelError(ctx, c, "encode channel envelope", err)
		return
	}
	c.JSON(consts.StatusOK, response)
}

func (h *Handler) AcceptChannelEnvelope(ctx context.Context, c *app.RequestContext) {
	h.updateChannelEnvelope(ctx, c, "accept", operations.KindChannelAccept, func(
		ctx context.Context,
		principal onprem.Principal,
		envelopeID string,
	) (onprem.ChannelEnvelope, error) {
		return h.channel.Accept(ctx, principal, envelopeID)
	})
}

func (h *Handler) ArchiveChannelEnvelope(ctx context.Context, c *app.RequestContext) {
	h.updateChannelEnvelope(ctx, c, "archive", operations.KindChannelArchive, func(
		ctx context.Context,
		principal onprem.Principal,
		envelopeID string,
	) (onprem.ChannelEnvelope, error) {
		return h.channel.Archive(ctx, principal, envelopeID)
	})
}

func (h *Handler) updateChannelEnvelope(
	ctx context.Context,
	c *app.RequestContext,
	action string,
	kind operations.Kind,
	update func(context.Context, onprem.Principal, string) (onprem.ChannelEnvelope, error),
) {
	startedAt := time.Now().UTC()
	principal, ok, authorizationCode := h.authorizeAgent(ctx, c, onprem.PermissionChannelReceive)
	if !ok {
		h.recordAuthorizationRejection(ctx, principal, kind, startedAt, authorizationCode)
		return
	}
	envelope, err := update(ctx, principal, c.Param("envelope_id"))
	if err != nil {
		outcome, errorCode := channelOperationFailure(err)
		h.recordAgentOperation(ctx, principal, operations.Event{
			Kind: kind, Outcome: outcome, StartedAt: startedAt, InputItems: 1, ErrorCode: errorCode,
		})
		h.writeChannelError(ctx, c, action+" channel envelope", err)
		return
	}
	response, err := channelEnvelopeResponseToAPI(envelope)
	if err != nil {
		h.recordAgentOperation(ctx, principal, operations.Event{
			Kind: kind, Outcome: operations.OutcomeFailed, StartedAt: startedAt,
			InputItems: 1, ErrorCode: "response_encoding_failed",
		})
		h.writeChannelError(ctx, c, "encode channel envelope", err)
		return
	}
	h.recordAgentOperation(ctx, principal, operations.Event{
		Kind: kind, Outcome: operations.OutcomeSucceeded, StartedAt: startedAt,
		InputItems: 1, AcceptedItems: 1, DetailKind: "channel_envelope", DetailID: envelope.ID,
	})
	c.JSON(consts.StatusOK, response)
}

func channelOperationFailure(err error) (operations.Outcome, string) {
	switch {
	case errors.Is(err, onprem.ErrInvalidChannelRequest):
		return operations.OutcomeRejected, "invalid_request"
	case errors.Is(err, onprem.ErrEnvelopeState), errors.Is(err, onprem.ErrIdempotencyConflict),
		errors.Is(err, onprem.ErrAgentIdentityConflict):
		return operations.OutcomeRejected, "conflict"
	case errors.Is(err, onprem.ErrTargetAgentNotFound), errors.Is(err, onprem.ErrEnvelopeNotFound):
		return operations.OutcomeRejected, "not_found"
	default:
		return operationFailure(err)
	}
}

func channelQueryLimit(c *app.RequestContext) (int, error) {
	raw := strings.TrimSpace(string(c.QueryArgs().Peek("limit")))
	if raw == "" {
		return 0, nil
	}
	return strconv.Atoi(raw)
}

func (h *Handler) writeChannelError(ctx context.Context, c *app.RequestContext, operation string, err error) {
	h.logger.ErrorContext(ctx, operation+" failed", "error", err)
	switch {
	case errors.Is(err, onprem.ErrUnauthorized):
		c.String(consts.StatusUnauthorized, operation)
	case errors.Is(err, onprem.ErrForbidden):
		c.String(consts.StatusForbidden, operation)
	case errors.Is(err, onprem.ErrTargetAgentNotFound), errors.Is(err, onprem.ErrEnvelopeNotFound):
		c.String(consts.StatusNotFound, operation)
	case errors.Is(err, onprem.ErrAgentIdentityConflict), errors.Is(err, onprem.ErrEnvelopeState),
		errors.Is(err, onprem.ErrIdempotencyConflict):
		c.String(consts.StatusConflict, operation)
	case errors.Is(err, onprem.ErrInvalidChannelRequest):
		c.String(consts.StatusBadRequest, operation)
	default:
		c.String(consts.StatusInternalServerError, operation)
	}
}
