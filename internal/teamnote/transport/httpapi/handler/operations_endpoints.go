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

func (h *Handler) GetOperationsSummary(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeOperations(ctx, c)
	if !ok {
		return
	}
	filter, err := operationsTimeFilter(c.Query("from"), c.Query("to"), c.Query("agent_id"))
	if err != nil {
		h.writeOperationsError(c, "get operations summary", err)
		return
	}
	summary, err := h.operations.Summary(ctx, principal, filter)
	if err != nil {
		h.writeOperationsError(c, "get operations summary", err)
		return
	}
	c.JSON(consts.StatusOK, operationsSummaryToAPI(summary))
}

func (h *Handler) ListOperationEvents(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeOperations(ctx, c)
	if !ok {
		return
	}
	filter, err := operationEventFilterFromRequest(c)
	if err != nil {
		h.writeOperationsError(c, "list operation events", err)
		return
	}
	events, err := h.operations.ListEvents(ctx, principal, filter)
	if err != nil {
		h.writeOperationsError(c, "list operation events", err)
		return
	}
	c.JSON(consts.StatusOK, operationEventsToAPI(events, filter.Limit, time.Now().UTC()))
}

func (h *Handler) GetRecallDiagnostic(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeOperations(ctx, c)
	if !ok {
		return
	}
	observationID, err := strconv.ParseInt(strings.TrimSpace(c.Param("observation_id")), 10, 64)
	if err != nil || observationID <= 0 {
		h.writeOperationsError(c, "get recall diagnostic", operations.ErrInvalidInput)
		return
	}
	diagnostic, err := h.operations.GetRecallDiagnostic(ctx, principal, observationID)
	if err != nil {
		h.writeOperationsError(c, "get recall diagnostic", err)
		return
	}
	c.JSON(consts.StatusOK, &api.RecallDiagnosticResponse{Recall: recallDiagnosticToAPI(diagnostic)})
}

func (h *Handler) GetOperationsStorage(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeOperations(ctx, c)
	if !ok {
		return
	}
	snapshot, err := h.operations.LatestStorage(ctx, principal)
	if err != nil {
		h.writeOperationsError(c, "get operations storage", err)
		return
	}
	c.JSON(consts.StatusOK, &api.OperationsStorageResponse{Storage: storageSnapshotToAPI(snapshot)})
}

func (h *Handler) ListOperationsStorageHistory(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeOperations(ctx, c)
	if !ok {
		return
	}
	filter, err := storageFilterFromRequest(c)
	if err != nil {
		h.writeOperationsError(c, "list operations storage history", err)
		return
	}
	snapshots, err := h.operations.ListStorage(ctx, principal, filter)
	if err != nil {
		h.writeOperationsError(c, "list operations storage history", err)
		return
	}
	c.JSON(consts.StatusOK, storageSnapshotsToAPI(snapshots, filter.Limit))
}

func (h *Handler) authorizeOperations(
	ctx context.Context,
	c *app.RequestContext,
) (onprem.HumanPrincipal, bool) {
	if h.operations == nil {
		writeHumanAPIError(c, consts.StatusNotImplemented, "not_configured", "operations monitoring is not configured")
		return onprem.HumanPrincipal{}, false
	}
	return h.authorizeHumanMember(ctx, c, false)
}

func (h *Handler) writeOperationsError(c *app.RequestContext, operation string, err error) {
	switch {
	case errors.Is(err, onprem.ErrUnauthorized):
		writeHumanAPIError(c, consts.StatusUnauthorized, "unauthorized", "authentication is required")
	case errors.Is(err, onprem.ErrForbidden):
		writeHumanAPIError(c, consts.StatusForbidden, "forbidden", "the operation is not permitted")
	case errors.Is(err, operations.ErrInvalidInput):
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
	case errors.Is(err, operations.ErrRecallNotFound):
		writeHumanAPIError(c, consts.StatusNotFound, "recall_diagnostic_not_found", "the requested resource was not found")
	case errors.Is(err, operations.ErrRecallExpired):
		writeHumanAPIError(c, consts.StatusGone, "diagnostic_expired", "the requested diagnostic has expired")
	case errors.Is(err, operations.ErrStorageNotAvailable):
		writeHumanAPIError(c, consts.StatusServiceUnavailable, "storage_not_available", "a storage snapshot is not available")
	default:
		h.logger.Error("operations request failed", "operation", operation, "error", err)
		writeHumanAPIError(c, consts.StatusInternalServerError, "internal_error", "the request could not be completed")
	}
}

func operationEventFilterFromRequest(c *app.RequestContext) (operations.EventFilter, error) {
	timeFilter, err := operationsTimeFilter(c.Query("from"), c.Query("to"), c.Query("agent_id"))
	if err != nil {
		return operations.EventFilter{}, err
	}
	kind, err := operations.ParseKind(c.Query("operation_kind"))
	if err != nil {
		return operations.EventFilter{}, err
	}
	outcome, err := operations.ParseOutcome(c.Query("outcome"))
	if err != nil {
		return operations.EventFilter{}, err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return operations.EventFilter{}, operations.ErrInvalidInput
	}
	return operations.EventFilter{
		TimeFilter: timeFilter,
		Kind:       kind,
		Outcome:    outcome,
		Limit:      limit,
		Cursor:     strings.TrimSpace(c.Query("cursor")),
	}, nil
}

func storageFilterFromRequest(c *app.RequestContext) (operations.StorageFilter, error) {
	timeFilter, err := operationsTimeFilter(c.Query("from"), c.Query("to"), "")
	if err != nil {
		return operations.StorageFilter{}, err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return operations.StorageFilter{}, operations.ErrInvalidInput
	}
	return operations.StorageFilter{
		From: timeFilter.From, To: timeFilter.To, Limit: limit, Cursor: strings.TrimSpace(c.Query("cursor")),
	}, nil
}

func operationsTimeFilter(fromRaw, toRaw, agentID string) (operations.TimeFilter, error) {
	from, err := optionalOperationsTime(fromRaw)
	if err != nil {
		return operations.TimeFilter{}, err
	}
	to, err := optionalOperationsTime(toRaw)
	if err != nil {
		return operations.TimeFilter{}, err
	}
	return operations.TimeFilter{From: from, To: to, AgentID: strings.TrimSpace(agentID)}, nil
}

func optionalOperationsTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, operations.ErrInvalidInput
	}
	return parsed.UTC(), nil
}

func operationsSummaryToAPI(summary operations.Summary) *api.OperationsSummaryResponse {
	return &api.OperationsSummaryResponse{
		FromTime: summary.From.Format(time.RFC3339Nano), ToTime: summary.To.Format(time.RFC3339Nano),
		GeneratedAt: summary.GeneratedAt.Format(time.RFC3339Nano),
		Observations: &api.ObservationOperationsSummary{
			Requests: summary.Observations.Requests, Succeeded: summary.Observations.Succeeded,
			InputEvents: summary.Observations.InputEvents, EventsWritten: summary.Observations.EventsWritten,
			DuplicateEvents: summary.Observations.DuplicateEvents,
		},
		Extraction: &api.ExtractionOperationsSummary{
			Runs: summary.Extraction.Runs, Completed: summary.Extraction.Completed,
			Quarantined: summary.Extraction.Quarantined, Failed: summary.Extraction.Failed,
			AdmittedRevisions:   summary.Extraction.AdmittedRevisions,
			UnextractedEvents:   summary.Extraction.UnextractedEvents,
			OldestUnextractedAt: optionalOperationsTimeString(summary.Extraction.OldestUnextractedAt),
		},
		Recalls: &api.RecallOperationsSummary{
			Requests: summary.Recalls.Requests, Succeeded: summary.Recalls.Succeeded,
			WithEvidence: summary.Recalls.WithEvidence, Empty: summary.Recalls.Empty,
			MemoryHits: summary.Recalls.MemoryHits, TeamNotesDelivered: summary.Recalls.TeamNotesDelivered,
			MemorySearchRequests:   summary.Recalls.MemorySearchRequests,
			MemoryGetRequests:      summary.Recalls.MemoryGetRequests,
			TeamNoteRecallRequests: summary.Recalls.TeamNoteRecallRequests,
			EvidenceHits:           summary.Recalls.EvidenceHits, HintHits: summary.Recalls.HintHits,
			ReferenceHits: summary.Recalls.ReferenceHits,
		},
		Latency: &api.OperationsLatency{
			SampleCount: summary.Latency.SampleCount, P50Ms: summary.Latency.P50MS, P95Ms: summary.Latency.P95MS,
		},
		Errors: summary.Errors,
	}
}

func operationEventsToAPI(
	events []operations.Event,
	limit int,
	generatedAt time.Time,
) *api.ListOperationEventsResponse {
	cursor := operations.NextEventCursor(events, limit)
	if len(events) > limit {
		events = events[:limit]
	}
	response := &api.ListOperationEventsResponse{
		Events:      make([]*api.OperationEvent, 0, len(events)),
		GeneratedAt: generatedAt.UTC().Format(time.RFC3339Nano),
	}
	for _, event := range events {
		response.Events = append(response.Events, operationEventToAPI(event))
	}
	if cursor != "" {
		response.NextCursor = &cursor
	}
	return response
}

func operationEventToAPI(event operations.Event) *api.OperationEvent {
	return &api.OperationEvent{
		OperationEventID: event.OperationEventID, AttemptID: event.AttemptID,
		OperationKind: string(event.Kind), Outcome: string(event.Outcome),
		ActorUserID:       optionalOperationsString(event.Actor.UserID),
		ActorMembershipID: optionalOperationsString(event.Actor.MembershipID),
		ActorAgentID:      optionalOperationsString(event.Actor.AgentID), SessionID: optionalOperationsString(event.SessionID),
		StartedAt: event.StartedAt.Format(time.RFC3339Nano), CompletedAt: event.CompletedAt.Format(time.RFC3339Nano),
		DurationMs: event.DurationMS, InputItems: event.InputItems, AcceptedItems: event.AcceptedItems,
		DuplicateItems: event.DuplicateItems, ResultItems: event.ResultItems, DeliveredItems: event.DeliveredItems,
		EvidenceItems: event.EvidenceItems, HintItems: event.HintItems, ReferenceItems: event.ReferenceItems,
		InputTokens: event.InputTokens, OutputTokens: event.OutputTokens,
		DetailKind: optionalOperationsString(event.DetailKind), DetailID: optionalOperationsString(event.DetailID),
		ErrorCode: optionalOperationsString(event.ErrorCode),
	}
}

func recallDiagnosticToAPI(diagnostic operations.RecallDiagnostic) *api.RecallDiagnostic {
	return &api.RecallDiagnostic{
		ObservationID: diagnostic.ObservationID, OccurredAt: diagnostic.OccurredAt.Format(time.RFC3339Nano),
		AgentID: diagnostic.AgentID, SessionID: diagnostic.SessionID, DurationMs: diagnostic.DurationMS,
		TokenBudget: diagnostic.TokenBudget, MaxItems: diagnostic.MaxItems,
		EvidenceSufficient: diagnostic.EvidenceSufficient, ReasonCodes: diagnostic.ReasonCodes,
		LanesExecuted: diagnostic.LanesExecuted, Candidates: diagnostic.Candidates, FusionKept: diagnostic.FusionKept,
		PlannedNotes: diagnostic.PlannedNotes, PlannedTokens: diagnostic.PlannedTokens,
		DeliveredItems: diagnostic.DeliveredItems, DispositionCounts: nonNilOperationsMap(diagnostic.DispositionCounts),
		RejectionCounts:       nonNilOperationsMap(diagnostic.RejectionCounts),
		BudgetDropCounts:      nonNilOperationsMap(diagnostic.BudgetDropCounts),
		HardGateFailureCounts: nonNilOperationsMap(diagnostic.HardGateFailureCounts),
	}
}

func storageSnapshotsToAPI(
	snapshots []operations.StorageSnapshot,
	limit int,
) *api.ListOperationsStorageHistoryResponse {
	cursor := operations.NextStorageCursor(snapshots, limit)
	if len(snapshots) > limit {
		snapshots = snapshots[:limit]
	}
	response := &api.ListOperationsStorageHistoryResponse{
		Snapshots: make([]*api.OperationsStorageSnapshot, 0, len(snapshots)),
	}
	for _, snapshot := range snapshots {
		response.Snapshots = append(response.Snapshots, storageSnapshotToAPI(snapshot))
	}
	if cursor != "" {
		response.NextCursor = &cursor
	}
	return response
}

func storageSnapshotToAPI(snapshot operations.StorageSnapshot) *api.OperationsStorageSnapshot {
	components := make([]*api.StorageComponent, 0, len(snapshot.Components))
	for _, component := range snapshot.Components {
		components = append(components, &api.StorageComponent{
			Component: component.Component, Counts: nonNilOperationsMap(component.Counts),
			LogicalBytes: component.LogicalBytes, PhysicalBytes: component.PhysicalBytes,
			EstimatedReclaimableBytes: component.EstimatedReclaimableBytes,
			OldestAt:                  optionalOperationsTimeString(component.OldestAt), NewestAt: optionalOperationsTimeString(component.NewestAt),
		})
	}
	return &api.OperationsStorageSnapshot{
		SnapshotID: snapshot.SnapshotID, SchemaVersion: snapshot.SchemaVersion,
		CapturedAt: snapshot.CapturedAt.Format(time.RFC3339Nano), Status: snapshot.Status,
		WarningCodes: snapshot.WarningCodes, DatabasePhysicalBytes: snapshot.DatabasePhysicalBytes,
		OtherPhysicalBytes: snapshot.OtherPhysicalBytes, Components: components,
	}
}

func optionalOperationsString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func optionalOperationsTimeString(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
}

func nonNilOperationsMap(value map[string]int64) map[string]int64 {
	if value == nil {
		return map[string]int64{}
	}
	return value
}
