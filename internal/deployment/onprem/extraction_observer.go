package onprem

import (
	"context"
	"log/slog"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/operations"
	teamruntime "github.com/pax-beehive/pax-nexus/internal/teamnote/runtime"
)

// NewExtractionObserver adapts durable runtime slice outcomes to safe Operation Events.
func NewExtractionObserver(
	recorder operations.Recorder,
	logger *slog.Logger,
) func(context.Context, teamruntime.ExtractionObservation) {
	return func(ctx context.Context, observation teamruntime.ExtractionObservation) {
		attemptID, err := operations.NewAttemptID()
		if err != nil {
			logger.ErrorContext(ctx, "create extraction operation attempt ID failed", "error", err)
			return
		}
		completedAt := observation.CompletedAt.UTC()
		if completedAt.Before(observation.StartedAt) {
			completedAt = observation.StartedAt
		}
		outcome, errorCode := extractionOperationOutcome(observation.Status)
		event := operations.Event{
			AttemptID: attemptID, Kind: operations.KindExtractionRun, Outcome: outcome,
			Actor: operations.Actor{
				Kind: "agent", UserID: observation.Actor.UserID, AgentID: observation.Actor.AgentID,
			},
			SessionID: observation.Actor.SessionID, StartedAt: observation.StartedAt.UTC(), CompletedAt: completedAt,
			DurationMS: completedAt.Sub(observation.StartedAt).Milliseconds(), InputItems: observation.InputEvents,
			ResultItems: observation.Candidates, InputTokens: &observation.InputTokens,
			OutputTokens: &observation.OutputTokens, ErrorCode: errorCode,
		}
		if observation.Status == teamruntime.ExtractionCompleted {
			event.AcceptedItems = observation.Candidates
		}
		if observation.RunID != "" {
			event.DetailKind = "extraction_run"
			event.DetailID = observation.RunID
		}
		recordContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if _, err := recorder.Record(recordContext, event); err != nil {
			logger.ErrorContext(ctx, "record extraction operation event failed", "error", err,
				"dropped_observations", operations.DroppedObservations(recorder))
		}
	}
}

func extractionOperationOutcome(status teamruntime.ExtractionStatus) (operations.Outcome, string) {
	switch status {
	case teamruntime.ExtractionCompleted:
		return operations.OutcomeSucceeded, ""
	case teamruntime.ExtractionQuarantined:
		return operations.OutcomeRejected, "extraction_quarantined"
	case teamruntime.ExtractionTimedOut:
		return operations.OutcomeTimedOut, "deadline_exceeded"
	case teamruntime.ExtractionCancelled:
		return operations.OutcomeCancelled, "cancelled"
	default:
		return operations.OutcomeFailed, "operation_failed"
	}
}
