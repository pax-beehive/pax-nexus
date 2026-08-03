package todoapp

import (
	"context"
	"log/slog"
	"time"
)

// StartSuggestionRefresh runs Service.RefreshSuggestions once immediately and
// then on a fixed interval, until the returned stop function is called.
// The shape mirrors startOperationsMaintenance in main.go.
func StartSuggestionRefresh(ctx context.Context, service *Service, scopeID string, interval time.Duration, logger *slog.Logger) func() {
	if interval <= 0 {
		interval = time.Hour
	}
	refreshContext, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		refreshSuggestions(refreshContext, service, scopeID, logger)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-refreshContext.Done():
				return
			case <-ticker.C:
				refreshSuggestions(refreshContext, service, scopeID, logger)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// refreshSuggestions runs a single refresh pass, logging failures without
// propagating them: suggestion refresh is best-effort background work.
func refreshSuggestions(ctx context.Context, service *Service, scopeID string, logger *slog.Logger) {
	created, err := service.RefreshSuggestions(ctx, scopeID)
	if err != nil {
		logger.Warn("todo suggestion refresh failed", "error", err)
		return
	}
	if created > 0 {
		logger.Info("todo suggestions refreshed", "created", created)
	}
}
