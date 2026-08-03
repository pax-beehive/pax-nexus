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

// Stream identifies one agent-session evidence stream, its current head, and
// the sequence already committed for this processor. Committed is zero for a
// stream this processor has never applied.
type Stream struct {
	ScopeID   string
	Actor     session.Actor
	Head      int64
	Committed int64
}

// Store is the persistence seam for the audit consumer. SessionEvents returns
// only the uncommitted window (Committed, Head] so every event is projected
// exactly once; ApplyBatch commits the projection rows and the cursor
// atomically.
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
	cancel   context.CancelFunc
	done     chan struct{}
}

// Start launches the background scan loop. Scans are serialized: a slow scan
// delays the next one instead of overlapping with it. The loop stops when
// ctx is cancelled or Stop is called.
func (c *Controller) Start(ctx context.Context) {
	ctx, c.cancel = context.WithCancel(ctx)
	c.done = make(chan struct{})
	go func(done chan struct{}) {
		defer close(done)
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
	}(c.done)
}

// Stop cancels the scan loop and waits for the background goroutine to
// exit, bounded by ctx. Stopping a controller that was never started is a
// no-op.
func (c *Controller) Stop(ctx context.Context) error {
	if c.cancel == nil {
		return nil
	}
	c.cancel()
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop session audit consumer: %w", ctx.Err())
	}
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
