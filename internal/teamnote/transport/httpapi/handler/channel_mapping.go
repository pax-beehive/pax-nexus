package handler

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	api "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/model/teammemory/api"
)

func channelSendRequestToDomain(request *api.SendChannelEnvelopeRequest) (onprem.SendEnvelopeRequest, error) {
	if request == nil || request.PayloadJSON == nil {
		return onprem.SendEnvelopeRequest{}, fmt.Errorf("map channel send request: payload_json is required")
	}
	payload, err := json.Marshal(request.PayloadJSON)
	if err != nil {
		return onprem.SendEnvelopeRequest{}, fmt.Errorf("map channel payload: %w", err)
	}
	return onprem.SendEnvelopeRequest{
		ToAgentID: request.ToAgentID, PayloadType: request.PayloadType, PayloadJSON: payload,
		Message: request.GetMessage(), IdempotencyKey: request.IdempotencyKey,
	}, nil
}

func channelEnvelopeResponseToAPI(envelope onprem.ChannelEnvelope) (*api.ChannelEnvelopeResponse, error) {
	mapped, err := channelEnvelopeToAPI(envelope)
	if err != nil {
		return nil, err
	}
	return &api.ChannelEnvelopeResponse{Envelope: mapped}, nil
}

func channelEnvelopeListToAPI(
	envelopes []onprem.ChannelEnvelope,
) (*api.ListChannelEnvelopesResponse, error) {
	mapped := make([]*api.ChannelEnvelope, 0, len(envelopes))
	for _, envelope := range envelopes {
		current, err := channelEnvelopeToAPI(envelope)
		if err != nil {
			return nil, err
		}
		mapped = append(mapped, current)
	}
	return &api.ListChannelEnvelopesResponse{Envelopes: mapped}, nil
}

func channelEnvelopeToAPI(envelope onprem.ChannelEnvelope) (*api.ChannelEnvelope, error) {
	var payload api.KnowledgeCapsuleEnvelopePayload
	if err := json.Unmarshal(envelope.PayloadJSON, &payload); err != nil {
		return nil, fmt.Errorf("decode stored channel payload: %w", err)
	}
	mapped := &api.ChannelEnvelope{
		EnvelopeID: envelope.ID, FromUserID: envelope.FromUserID, FromAgentID: envelope.FromAgentID,
		ToUserID: envelope.ToUserID, ToAgentID: envelope.ToAgentID, PayloadType: envelope.PayloadType,
		PayloadJSON: &payload, IdempotencyKey: envelope.IdempotencyKey, Status: envelope.Status,
		CreatedAt: envelope.CreatedAt.Format(time.RFC3339Nano),
	}
	if envelope.Message != "" {
		mapped.Message = &envelope.Message
	}
	if envelope.AcceptedAt != nil {
		value := envelope.AcceptedAt.Format(time.RFC3339Nano)
		mapped.AcceptedAt = &value
	}
	if envelope.ArchivedAt != nil {
		value := envelope.ArchivedAt.Format(time.RFC3339Nano)
		mapped.ArchivedAt = &value
	}
	return mapped, nil
}
