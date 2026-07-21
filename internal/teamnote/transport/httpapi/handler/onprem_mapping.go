package handler

import (
	"fmt"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/recall"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	api "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/model/teammemory/api"
)

func enrollmentRequestToDomain(request *api.AgentEnrollmentRequest) onprem.EnrollmentRequest {
	permissions := make([]onprem.Permission, len(request.Permissions))
	for index, permission := range request.Permissions {
		permissions[index] = onprem.Permission(permission)
	}
	return onprem.EnrollmentRequest{
		UserID: request.UserID, AgentID: request.AgentID,
		ExpiresIn: time.Duration(request.GetExpiresInSeconds()) * time.Second, Permissions: permissions,
	}
}

func enrollmentToAPI(enrollment onprem.Enrollment) *api.AgentEnrollmentResponse {
	return &api.AgentEnrollmentResponse{
		EnrollmentID: enrollment.ID, Token: enrollment.Token, ExpiresAt: enrollment.ExpiresAt.Format(time.RFC3339Nano),
	}
}

func credentialToAPI(credential onprem.IssuedCredential) *api.AgentCredentialResponse {
	result := &api.AgentCredentialResponse{CredentialID: credential.CredentialID, APIKey: credential.APIKey}
	if credential.ExpiresAt != nil {
		value := credential.ExpiresAt.Format(time.RFC3339Nano)
		result.ExpiresAt = &value
	}
	return result
}

func principalToAPI(principal onprem.Principal) *api.AgentIdentityResponse {
	permissions := make([]string, len(principal.Permissions))
	for index, permission := range principal.Permissions {
		permissions[index] = string(permission)
	}
	return &api.AgentIdentityResponse{
		UserID: principal.UserID, AgentID: principal.AgentID,
		CredentialID: principal.CredentialID, Permissions: permissions,
	}
}

func observationBatchToDomain(
	request *api.ObservationBatch,
	principal onprem.Principal,
) (teamnote.SessionBatch, error) {
	if request == nil || request.SessionID == "" || len(request.Events) == 0 {
		return teamnote.SessionBatch{}, fmt.Errorf("map observation batch: session_id and events are required")
	}
	actor := teamnote.Actor{UserID: principal.UserID, AgentID: principal.AgentID, SessionID: request.SessionID}
	events := make([]teamnote.SessionEvent, 0, len(request.Events))
	for _, event := range request.Events {
		if event == nil {
			return teamnote.SessionBatch{}, fmt.Errorf("map observation batch: event is required")
		}
		occurredAt, err := time.Parse(time.RFC3339Nano, event.OccurredAt)
		if err != nil {
			return teamnote.SessionBatch{}, fmt.Errorf("map observation event %q occurred_at: %w", event.ID, err)
		}
		events = append(events, teamnote.SessionEvent{
			ID: event.ID, Actor: actor, Sequence: event.Sequence, Type: event.Type, Content: event.Content,
			TaskRef: event.GetTaskRef(), ThreadRef: event.GetThreadRef(), Visibility: event.GetVisibility(),
			OccurredAt: occurredAt, Metadata: event.Metadata,
		})
	}
	return teamnote.SessionBatch{Events: events, Complete: request.Complete}, nil
}

func observationReceiptToAPI(receipt teamnote.IngestReceipt, idempotencyKey string) *api.ObservationReceipt {
	result := &api.ObservationReceipt{
		Accepted: int32(receipt.Accepted), Duplicate: int32(receipt.Duplicate), Cursor: receipt.Cursor, Status: "accepted",
	}
	if receipt.RunID != "" {
		result.RunID = &receipt.RunID
		result.Status = "processing"
	}
	if idempotencyKey != "" {
		result.IdempotencyKey = &idempotencyKey
	}
	return result
}

func memorySearchRequestToDomain(request *api.MemorySearchRequest, principal onprem.Principal) recall.SearchRequest {
	return recall.SearchRequest{
		Intent: recall.Intent(request.Intent), Source: recall.Source(request.GetSource()),
		Actor:   teamnote.Actor{UserID: principal.UserID, AgentID: principal.AgentID, SessionID: request.SessionID},
		TaskRef: request.GetTaskRef(), ThreadRef: request.GetThreadRef(), Query: request.Query,
		TokenBudget: int(request.TokenBudget), MaxItems: int(request.GetMaxItems()),
	}
}

func memorySearchResultToAPI(result recall.SearchResult) *api.MemorySearchResponse {
	hits := make([]*api.MemoryHit, len(result.Hits))
	for index, hit := range result.Hits {
		current := &api.MemoryHit{
			Text: hit.Text, Score: hit.Score, Tokens: int32(hit.Tokens),
			Disposition: string(hit.Disposition), Metadata: hit.Metadata,
		}
		if hit.Ref != "" {
			current.Ref = &hit.Ref
		}
		hits[index] = current
	}
	return &api.MemorySearchResponse{
		Hits: hits, EvidenceSufficient: result.EvidenceSufficient,
		Trace: &api.MemorySearchTrace{
			EarlyReturn: result.Trace.EarlyReturn, TeamNote: pathTraceToAPI(result.Trace.TeamNote),
			WikiHint: pathTraceToAPI(result.Trace.WikiHint), WikiSearch: pathTraceToAPI(result.Trace.WikiSearch),
		},
	}
}

func pathTraceToAPI(trace recall.PathTrace) *api.RecallPathTrace {
	result := &api.RecallPathTrace{
		Status: string(trace.Status), DurationMs: trace.DurationMS,
		Candidates: int32(trace.Candidates), BudgetDrops: int32(trace.BudgetDrops),
	}
	if trace.Error != "" {
		result.Error = &trace.Error
	}
	if trace.Reason != "" {
		result.Reason = &trace.Reason
	}
	return result
}

func memoryDocumentToAPI(document recall.MemoryDocument) *api.MemoryDocument {
	return &api.MemoryDocument{
		Ref: document.Ref, Text: document.Text, Tokens: int32(document.Tokens), Provenance: document.Provenance,
	}
}
