package todoapp

import (
	"context"
	"log/slog"
	"sort"
	"time"
)

// StartSuggestionRefresh runs a sweep of Service.RefreshSuggestions over
// every scope reported by scopes once immediately and then on a fixed
// interval, until the returned stop function is called. The shape mirrors
// startOperationsMaintenance in main.go.
func StartSuggestionRefresh(ctx context.Context, service *Service, scopes ScopeLister, interval time.Duration, logger *slog.Logger) func() {
	if interval <= 0 {
		interval = time.Hour
	}
	refreshContext, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		refreshSuggestions(refreshContext, service, scopes, logger)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-refreshContext.Done():
				return
			case <-ticker.C:
				refreshSuggestions(refreshContext, service, scopes, logger)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// refreshSuggestions runs a single sweep pass over every scope reported by
// scopes, in sorted order for determinism. Each scope's refresh failure is
// logged and skipped so one scope's trouble never stops the rest of the
// sweep: suggestion refresh is best-effort background work.
func refreshSuggestions(ctx context.Context, service *Service, scopes ScopeLister, logger *slog.Logger) {
	scopeIDs, err := scopes.ListScopes(ctx)
	if err != nil {
		logger.WarnContext(ctx, "todo suggestion refresh: list scopes failed", "error", err)
		return
	}
	sort.Strings(scopeIDs)
	for _, scopeID := range scopeIDs {
		created, err := service.RefreshSuggestions(ctx, scopeID)
		if err != nil {
			logger.WarnContext(ctx, "todo suggestion refresh failed", "scope_id", scopeID, "error", err)
			continue
		}
		if created > 0 {
			logger.InfoContext(ctx, "todo suggestions refreshed", "scope_id", scopeID, "created", created)
		}
	}
}
