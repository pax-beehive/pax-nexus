package handler

import (
	"fmt"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	api "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/model/teammemory/api"
)

func sessionBatchToDomain(request *api.SessionBatch) (teamnote.SessionBatch, error) {
	if request == nil {
		return teamnote.SessionBatch{}, fmt.Errorf("map session batch: request is nil")
	}
	events := make([]teamnote.SessionEvent, 0, len(request.Events))
	for _, event := range request.Events {
		mapped, err := sessionEventToDomain(event)
		if err != nil {
			return teamnote.SessionBatch{}, err
		}
		events = append(events, mapped)
	}
	return teamnote.SessionBatch{Events: events, Complete: request.Complete}, nil
}

func sessionEventToDomain(event *api.SessionEvent) (teamnote.SessionEvent, error) {
	if event == nil || event.Actor == nil {
		return teamnote.SessionEvent{}, fmt.Errorf("map session event: event and actor are required")
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, event.OccurredAt)
	if err != nil {
		return teamnote.SessionEvent{}, fmt.Errorf("map event %q occurred_at: %w", event.ID, err)
	}
	return teamnote.SessionEvent{
		ID: event.ID, Actor: actorToDomain(event.Actor), Sequence: event.Sequence,
		Type: event.Type, Content: event.Content, TaskRef: event.GetTaskRef(),
		ThreadRef: event.GetThreadRef(), Visibility: event.GetVisibility(), OccurredAt: occurredAt,
		Metadata: event.Metadata,
	}, nil
}

func recallRequestToDomain(request *api.RecallRequest) (teamnote.RecallRequest, error) {
	if request == nil || request.Actor == nil {
		return teamnote.RecallRequest{}, fmt.Errorf("map recall request: request and actor are required")
	}
	return teamnote.RecallRequest{
		Actor: actorToDomain(request.Actor), TaskRef: request.GetTaskRef(),
		ThreadRef: request.GetThreadRef(), TokenBudget: int(request.TokenBudget), Query: request.GetQuery(),
		MaxItems: int(request.GetMaxItems()),
	}, nil
}

func actorToDomain(actor *api.Actor) teamnote.Actor {
	return teamnote.Actor{UserID: actor.UserID, AgentID: actor.AgentID, SessionID: actor.SessionID}
}

func ingestReceiptToAPI(receipt teamnote.IngestReceipt) *api.IngestReceipt {
	result := &api.IngestReceipt{
		Accepted: int32(receipt.Accepted), Duplicate: int32(receipt.Duplicate), Cursor: receipt.Cursor,
	}
	if receipt.RunID != "" {
		result.RunID = &receipt.RunID
	}
	return result
}

func noteEnvelopeToAPI(envelope teamnote.NoteEnvelope) *api.NoteEnvelope {
	details := make([]*api.RecalledNote, len(envelope.Details))
	for index, detail := range envelope.Details {
		details[index] = &api.RecalledNote{
			NoteID: detail.NoteID, Revision: int32(detail.Revision), Text: detail.Text, Origin: actorToAPI(detail.Origin),
			Relevance: detail.Relevance, Certainty: string(detail.Certainty),
		}
	}
	reasonCodes := make([]string, len(envelope.Decision.ReasonCodes))
	for index, reason := range envelope.Decision.ReasonCodes {
		reasonCodes[index] = string(reason)
	}
	return &api.NoteEnvelope{
		Revision: envelope.Revision, Items: envelope.Items, Tokens: int32(envelope.Tokens), Details: details,
		Decision: &api.RecallDecisionSummary{
			EvidenceSufficient: envelope.Decision.EvidenceSufficient, ReasonCodes: reasonCodes,
		},
	}
}

func actorToAPI(actor teamnote.Actor) *api.Actor {
	return &api.Actor{UserID: actor.UserID, AgentID: actor.AgentID, SessionID: actor.SessionID}
}
