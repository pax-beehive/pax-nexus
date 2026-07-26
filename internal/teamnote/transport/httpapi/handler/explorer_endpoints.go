package handler

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/explorer"
	api "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/model/teammemory/api"
)

func (h *Handler) ListTeamNotes(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeExplorer(ctx, c)
	if !ok {
		return
	}
	limit, err := queryLimit(c)
	if err != nil {
		h.writeExplorerError(c, "list team notes", explorer.ErrInvalidInput)
		return
	}
	filter := explorer.TeamNoteFilter{
		Query: c.Query("q"), Kind: c.Query("kind"), State: c.Query("state"),
		AgentID: c.Query("agent_id"), TaskRef: c.Query("task_ref"),
		ThreadRef: c.Query("thread_ref"), Limit: limit, Cursor: c.Query("cursor"),
	}
	notes, err := h.explorer.ListTeamNotes(ctx, principal, filter)
	if err != nil {
		h.writeExplorerError(c, "list team notes", err)
		return
	}
	c.JSON(consts.StatusOK, teamNoteListToAPI(notes, limit))
}

func (h *Handler) GetTeamNote(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeExplorer(ctx, c)
	if !ok {
		return
	}
	detail, err := h.explorer.GetTeamNote(ctx, principal, c.Param("note_id"))
	if err != nil {
		h.writeExplorerError(c, "get team note", err)
		return
	}
	c.JSON(consts.StatusOK, teamNoteDetailToAPI(detail))
}

func (h *Handler) GetExtractionDiagnostic(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeExplorer(ctx, c)
	if !ok {
		return
	}
	diagnostic, err := h.explorer.GetExtractionDiagnostic(ctx, principal, c.Param("run_id"))
	if err != nil {
		h.writeExplorerError(c, "get extraction diagnostic", err)
		return
	}
	c.JSON(consts.StatusOK, extractionDiagnosticToAPI(diagnostic))
}

func (h *Handler) GetChannelDiagnostic(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeExplorer(ctx, c)
	if !ok {
		return
	}
	diagnostic, err := h.explorer.GetChannelDiagnostic(ctx, principal, c.Param("envelope_id"))
	if err != nil {
		h.writeExplorerError(c, "get channel diagnostic", err)
		return
	}
	c.JSON(consts.StatusOK, channelDiagnosticToAPI(diagnostic))
}

func (h *Handler) authorizeExplorer(
	ctx context.Context,
	c *app.RequestContext,
) (onprem.HumanPrincipal, bool) {
	if h.explorer == nil {
		writeHumanAPIError(c, consts.StatusNotImplemented, "not_configured", "team memory explorer is not configured")
		return onprem.HumanPrincipal{}, false
	}
	principal, ok := h.authorizeHumanMember(ctx, c, false)
	if !ok {
		return onprem.HumanPrincipal{}, false
	}
	if !principal.HasCapability(onprem.CapabilityViewTeamMemory) {
		writeHumanAPIError(c, consts.StatusForbidden, "forbidden", "the operation is not permitted")
		return onprem.HumanPrincipal{}, false
	}
	return principal, true
}

func (h *Handler) writeExplorerError(c *app.RequestContext, operation string, err error) {
	switch {
	case errors.Is(err, onprem.ErrUnauthorized):
		writeHumanAPIError(c, consts.StatusUnauthorized, "unauthorized", "authentication is required")
	case errors.Is(err, onprem.ErrForbidden):
		writeHumanAPIError(c, consts.StatusForbidden, "forbidden", "the operation is not permitted")
	case errors.Is(err, explorer.ErrInvalidInput):
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
	case errors.Is(err, explorer.ErrNotFound):
		writeHumanAPIError(c, consts.StatusNotFound, "explorer_not_found", "the requested resource was not found")
	default:
		h.logger.Error("explorer request failed", "operation", operation, "error", err)
		writeHumanAPIError(c, consts.StatusInternalServerError, "internal_error", "the request could not be completed")
	}
}

func teamNoteListToAPI(notes []explorer.TeamNoteSummary, limit int) *api.ListTeamNotesResponse {
	response := &api.ListTeamNotesResponse{Notes: teamNoteSummariesToAPI(notes)}
	if cursor := explorer.NextTeamNoteCursor(notes, limit); cursor != "" {
		response.NextCursor = &cursor
		response.Notes = response.Notes[:limit]
	}
	return response
}

func teamNoteDetailToAPI(detail explorer.TeamNoteDetail) *api.TeamNoteDetailResponse {
	return &api.TeamNoteDetailResponse{
		Note:               teamNoteToAPI(detail.Note),
		RelatedNotes:       teamNoteSummariesToAPI(detail.RelatedNotes),
		Revisions:          revisionsToAPI(detail.Revisions),
		RecallObservations: recallUsesToAPI(detail.RecallObservations),
	}
}

func extractionDiagnosticToAPI(diagnostic explorer.ExtractionDiagnostic) *api.ExtractionDiagnosticResponse {
	return &api.ExtractionDiagnosticResponse{
		Run:            extractionRunToAPI(diagnostic.Run),
		Candidates:     candidatesToAPI(diagnostic.Candidates),
		SourceEvents:   sourceEventsToAPI(diagnostic.SourceEvents),
		ResultingNotes: teamNoteSummariesToAPI(diagnostic.ResultingNotes),
	}
}

func channelDiagnosticToAPI(diagnostic explorer.ChannelDiagnostic) *api.ChannelDiagnosticResponse {
	response := &api.ChannelDiagnosticResponse{
		EnvelopeID: diagnostic.EnvelopeID, FromAgentID: diagnostic.FromAgentID,
		ToAgentID: diagnostic.ToAgentID, Status: diagnostic.Status,
		PayloadStatus: diagnostic.PayloadStatus,
		CreatedAt:     diagnostic.CreatedAt.UTC().Format(time.RFC3339Nano),
		Capsule:       knowledgeCapsuleToAPI(diagnostic.Capsule),
	}
	response.Message = optionalString(diagnostic.Message)
	response.AcceptedAt = optionalTime(diagnostic.AcceptedAt)
	response.ArchivedAt = optionalTime(diagnostic.ArchivedAt)
	return response
}

func teamNoteToAPI(note explorer.TeamNote) *api.ExplorerTeamNote {
	return &api.ExplorerTeamNote{
		Summary: teamNoteSummaryToAPI(note.TeamNoteSummary), Body: note.Body,
		OriginUserID: note.OriginUserID, OriginSessionID: note.OriginSessionID,
		RelatedSubjects: note.RelatedSubjects, ValidAt: optionalTime(note.ValidAt),
		InvalidAt: optionalTime(note.InvalidAt), SourceOccurredAt: optionalTime(note.SourceOccurredAt),
	}
}

func teamNoteSummariesToAPI(notes []explorer.TeamNoteSummary) []*api.ExplorerTeamNoteSummary {
	result := make([]*api.ExplorerTeamNoteSummary, 0, len(notes))
	for _, note := range notes {
		result = append(result, teamNoteSummaryToAPI(note))
	}
	return result
}

func teamNoteSummaryToAPI(note explorer.TeamNoteSummary) *api.ExplorerTeamNoteSummary {
	return &api.ExplorerTeamNoteSummary{
		NoteID: note.NoteID, Kind: note.Kind, Subject: note.Subject, State: note.State,
		TaskRef: optionalString(note.TaskRef), ThreadRef: optionalString(note.ThreadRef),
		OriginAgentID: note.OriginAgentID, AudienceAgentIds: note.AudienceAgentIDs,
		Revision:      int32(note.Revision),
		CreatedAt:     note.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:     note.UpdatedAt.UTC().Format(time.RFC3339Nano),
		SoftExpiresAt: note.SoftExpiresAt.UTC().Format(time.RFC3339Nano),
		HardExpiresAt: note.HardExpiresAt.UTC().Format(time.RFC3339Nano),
	}
}

func revisionsToAPI(revisions []explorer.Revision) []*api.ExplorerRevision {
	result := make([]*api.ExplorerRevision, 0, len(revisions))
	for _, revision := range revisions {
		result = append(result, &api.ExplorerRevision{
			Revision: int32(revision.Revision), CandidateID: revision.CandidateID,
			Operation: revision.Operation, Body: revision.Body,
			RelatedSubjects: revision.RelatedSubjects,
			ValidAt:         optionalTime(revision.ValidAt), InvalidAt: optionalTime(revision.InvalidAt),
			CreatedAt: revision.CreatedAt.UTC().Format(time.RFC3339Nano),
			ExpiredAt: optionalTime(revision.ExpiredAt), Extraction: extractionRunToAPI(revision.Extraction),
			Evidence: sourceEventsToAPI(revision.Evidence), Deliveries: deliveriesToAPI(revision.Deliveries),
			Candidate: candidateToAPI(revision.Candidate),
		})
	}
	return result
}

func extractionRunToAPI(run explorer.ExtractionRun) *api.ExplorerExtractionRun {
	return &api.ExplorerExtractionRun{
		RunID: run.RunID, UserID: run.UserID, AgentID: run.AgentID, SessionID: run.SessionID,
		FromSequence: run.FromSequence, ToSequence: run.ToSequence,
		Model: run.Model, PromptVersion: run.PromptVersion, Status: run.Status,
		InputTokens: run.InputTokens, OutputTokens: run.OutputTokens,
		ErrorCode:   optionalString(run.ErrorCode),
		CreatedAt:   run.CreatedAt.UTC().Format(time.RFC3339Nano),
		CompletedAt: optionalTime(run.CompletedAt),
	}
}

func candidatesToAPI(candidates []explorer.Candidate) []*api.ExplorerCandidate {
	result := make([]*api.ExplorerCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		result = append(result, candidateToAPI(candidate))
	}
	return result
}

func candidateToAPI(candidate explorer.Candidate) *api.ExplorerCandidate {
	return &api.ExplorerCandidate{
		CandidateID: candidate.CandidateID, Action: candidate.Action, Kind: candidate.Kind,
		Subject: candidate.Subject, Body: candidate.Body,
		TaskRef: optionalString(candidate.TaskRef), ThreadRef: optionalString(candidate.ThreadRef),
		OriginAgentID: candidate.OriginAgentID, EvidenceEventIds: candidate.EvidenceEventIDs,
		AdmissionStatus: candidate.AdmissionStatus,
		RejectionReason: optionalString(candidate.RejectionReason),
		CreatedAt:       candidate.CreatedAt.UTC().Format(time.RFC3339Nano),
		ResultingNoteID: optionalString(candidate.ResultingNoteID),
	}
}

func sourceEventsToAPI(events []explorer.SourceEvent) []*api.ExplorerSourceEvent {
	result := make([]*api.ExplorerSourceEvent, 0, len(events))
	for _, event := range events {
		result = append(result, &api.ExplorerSourceEvent{
			EventID: event.EventID, UserID: event.UserID, AgentID: event.AgentID,
			SessionID: event.SessionID, Sequence: event.Sequence, Type: event.Type,
			Content: event.Content, TaskRef: optionalString(event.TaskRef),
			ThreadRef: optionalString(event.ThreadRef), Visibility: event.Visibility,
			OccurredAt:  event.OccurredAt.UTC().Format(time.RFC3339Nano),
			CapturedAt:  event.CapturedAt.UTC().Format(time.RFC3339Nano),
			ExtractedAt: optionalTime(event.ExtractedAt),
		})
	}
	return result
}

func deliveriesToAPI(deliveries []explorer.Delivery) []*api.ExplorerDelivery {
	result := make([]*api.ExplorerDelivery, 0, len(deliveries))
	for _, delivery := range deliveries {
		result = append(result, &api.ExplorerDelivery{
			RecipientUserID: delivery.RecipientUserID, RecipientAgentID: delivery.RecipientAgentID,
			RecipientSessionID: delivery.RecipientSessionID,
			DeliveredAt:        delivery.DeliveredAt.UTC().Format(time.RFC3339Nano),
			ContextTokens:      int32(delivery.ContextTokens),
		})
	}
	return result
}

func recallUsesToAPI(uses []explorer.RecallUse) []*api.ExplorerRecallUse {
	result := make([]*api.ExplorerRecallUse, 0, len(uses))
	for _, use := range uses {
		result = append(result, &api.ExplorerRecallUse{
			ObservationID: use.ObservationID, RecipientAgentID: use.RecipientAgentID,
			RecipientSessionID: use.RecipientSessionID,
			OccurredAt:         use.OccurredAt.UTC().Format(time.RFC3339Nano),
			Disposition:        optionalString(use.Disposition), Delivered: use.Delivered,
			RejectionReasons: use.RejectionReasons, BudgetDropReasons: use.BudgetDropReasons,
			HardGateFailures: use.HardGateFailures,
		})
	}
	return result
}

func knowledgeCapsuleToAPI(capsule explorer.KnowledgeCapsule) *api.ExplorerKnowledgeCapsule {
	return &api.ExplorerKnowledgeCapsule{
		CapsuleID: optionalString(capsule.CapsuleID), SourceSessionID: optionalString(capsule.SourceSessionID),
		SourceAgent: optionalString(capsule.SourceAgent), Keyword: optionalString(capsule.Keyword),
		Title: optionalString(capsule.Title), Summary: optionalString(capsule.Summary),
		Content: optionalString(capsule.Content), Status: optionalString(capsule.Status),
		Truncated:      optionalBool(capsule.Truncated, capsule.CapsuleID != ""),
		RouteMatchType: optionalString(capsule.RouteMatchType),
	}
}

func optionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func optionalTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
}

func optionalBool(value bool, set bool) *bool {
	if !set {
		return nil
	}
	return &value
}
