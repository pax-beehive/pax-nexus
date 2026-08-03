package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/operations"
)

type operationsMaintenanceStore interface {
	operations.Recorder
	CaptureStorage(context.Context, time.Time) (operations.StorageSnapshot, error)
	DeleteBefore(context.Context, time.Time, time.Time) (int64, int64, error)
}

func startOperationsMaintenance(
	ctx context.Context,
	store operationsMaintenanceStore,
	recorder operations.Recorder,
	config applicationConfig,
	logger *slog.Logger,
) func() {
	maintenanceContext, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		maintainOperations(maintenanceContext, store, recorder, config, logger, time.Now().UTC())
		ticker := time.NewTicker(config.operationsSnapshotInterval)
		defer ticker.Stop()
		for {
			select {
			case <-maintenanceContext.Done():
				return
			case now := <-ticker.C:
				maintainOperations(maintenanceContext, store, recorder, config, logger, now.UTC())
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func maintainOperations(
	ctx context.Context,
	store operationsMaintenanceStore,
	recorder operations.Recorder,
	config applicationConfig,
	logger *slog.Logger,
	now time.Time,
) {
	captureContext, cancelCapture := context.WithTimeout(ctx, config.operationsMaintenanceTimeout)
	_, captureErr := store.CaptureStorage(captureContext, now)
	cancelCapture()
	if captureErr != nil {
		logger.ErrorContext(ctx, "capture operations storage snapshot failed", "error", captureErr)
	}
	retentionContext, cancelRetention := context.WithTimeout(ctx, config.operationsMaintenanceTimeout)
	defer cancelRetention()
	deletedEvents, deletedSnapshots, err := store.DeleteBefore(
		retentionContext,
		now.Add(-config.operationsEventRetention),
		now.Add(-config.operationsStorageRetention),
	)
	if err != nil {
		logger.ErrorContext(ctx, "delete expired operations data failed", "error", err)
		return
	}
	if deletedEvents+deletedSnapshots == 0 {
		return
	}
	attemptID, err := operations.NewAttemptID()
	if err != nil {
		logger.ErrorContext(ctx, "create operations retention attempt ID failed", "error", err)
		return
	}
	completedAt := time.Now().UTC()
	if completedAt.Before(now) {
		completedAt = now
	}
	_, err = recorder.Record(retentionContext, operations.Event{
		AttemptID: attemptID, Kind: operations.KindSystemRetention, Outcome: operations.OutcomeSucceeded,
		Actor: operations.Actor{Kind: "system"}, StartedAt: now, CompletedAt: completedAt,
		DurationMS: completedAt.Sub(now).Milliseconds(),
		InputItems: deletedEvents + deletedSnapshots, AcceptedItems: deletedEvents + deletedSnapshots,
	})
	if err != nil {
		logger.ErrorContext(ctx, "record operations retention event failed", "error", err,
			"dropped_observations", operations.DroppedObservations(recorder))
	}
}
