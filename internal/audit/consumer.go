package audit

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/session"
)

const (
	ProcessorName    = "session_audit"
	ProcessorVersion = "v1"
)

const defaultScanInterval = 2 * time.Second

// Stream identifies one agent-session evidence stream and its current head.
type Stream struct {
	ScopeID string
	Actor   session.Actor
	Head    int64
}

// Store is the persistence seam for the audit consumer. ApplyBatch commits
// the projection rows and the cursor atomically.
type Store interface {
	PendingStreams(context.Context) ([]Stream, error)
	SessionEvents(context.Context, Stream) ([]session.SessionEvent, error)
	ApplyBatch(context.Context, Batch) error
}

// Controller is the orchestration layer: it scans pending streams on a
// ticker and commits one projected batch per stream. All projection
// decisions live in Project; the controller owns no policy.
type Controller struct {
	store    Store
	logger   *slog.Logger
	interval time.Duration
	mu       sync.Mutex
}

// Start launches the background scan loop. Scans are serialized: a slow scan
// delays the next one instead of overlapping with it.
func (c *Controller) Start(ctx context.Context) {
	go func() {
		c.scan(ctx)
		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.scan(ctx)
			}
		}
	}()
}

func (c *Controller) scan(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	streams, err := c.store.PendingStreams(ctx)
	if err != nil {
		c.logger.ErrorContext(ctx, "session audit scan failed", "error", err)
		return
	}
	for _, stream := range streams {
		if err := c.consume(ctx, stream); err != nil {
			c.logger.ErrorContext(ctx, "session audit projection failed",
				"scope_id", stream.ScopeID,
				"agent_id", stream.Actor.AgentID,
				"session_id", stream.Actor.SessionID,
				"error", err,
			)
		}
	}
}

// consume projects one stream and commits the batch. A stream with no
// readable events still advances its cursor so a missing-events stream is
// not rescanned forever. Errors skip the stream without advancing.
func (c *Controller) consume(ctx context.Context, stream Stream) error {
	events, err := c.store.SessionEvents(ctx, stream)
	if err != nil {
		return fmt.Errorf("read session audit events: %w", err)
	}
	batch := Project(stream, events)
	if err := c.store.ApplyBatch(ctx, batch); err != nil {
		return fmt.Errorf("apply session audit batch: %w", err)
	}
	c.logger.InfoContext(ctx, "session audit batch applied",
		"scope_id", stream.ScopeID,
		"agent_id", stream.Actor.AgentID,
		"session_id", stream.Actor.SessionID,
		"cursor", batch.Cursor,
	)
	return nil
}
