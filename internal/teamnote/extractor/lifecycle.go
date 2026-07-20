package extractor

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrClosed indicates that an extraction was requested after the extractor
// began shutting down.
var ErrClosed = errors.New("extractor is closed")

// Lifecycle is an optional extension implemented by extractors that own
// asynchronous work.
type Lifecycle interface {
	WaitForBackground(context.Context) error
	Close(context.Context) error
}

// LifecycleStatus exposes bounded coordinator state for health checks and
// leak detection without exposing individual Episode keys.
type LifecycleStatus struct {
	Closed            bool
	ActiveExtractions int
	BackgroundCalls   int
	ActiveEpisodes    int
}

type lifecycleCoordinator struct {
	mu                sync.Mutex
	changed           chan struct{}
	closed            bool
	foreground        int
	background        int
	backgroundErr     error
	backgroundCancels map[uint64]context.CancelFunc
	nextBackgroundID  uint64
}

func newLifecycleCoordinator() lifecycleCoordinator {
	return lifecycleCoordinator{changed: make(chan struct{}), backgroundCancels: make(map[uint64]context.CancelFunc)}
}

func (c *lifecycleCoordinator) beginForeground() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	c.foreground++
	c.signalLocked()
	return nil
}

func (c *lifecycleCoordinator) finishForeground() {
	c.mu.Lock()
	c.foreground--
	c.signalLocked()
	c.mu.Unlock()
}

func (c *lifecycleCoordinator) beginBackground(parent context.Context) (context.Context, func(error)) {
	background, cancel := context.WithCancel(context.WithoutCancel(parent))
	c.mu.Lock()
	c.nextBackgroundID++
	id := c.nextBackgroundID
	c.background++
	if c.closed {
		cancel()
	} else {
		c.backgroundCancels[id] = cancel
	}
	c.signalLocked()
	c.mu.Unlock()
	return background, func(err error) {
		cancel()
		c.finishBackground(id, err)
	}
}

func (c *lifecycleCoordinator) finishBackground(id uint64, err error) {
	c.mu.Lock()
	delete(c.backgroundCancels, id)
	c.background--
	if c.closed && errors.Is(err, context.Canceled) {
		err = nil
	}
	c.backgroundErr = errors.Join(c.backgroundErr, err)
	c.signalLocked()
	c.mu.Unlock()
}

func (c *lifecycleCoordinator) wait(ctx context.Context, closeExtractor bool) error {
	c.mu.Lock()
	if closeExtractor {
		c.closed = true
		for _, cancel := range c.backgroundCancels {
			cancel()
		}
		c.signalLocked()
	}
	for c.foreground > 0 || c.background > 0 {
		changed := c.changed
		c.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return fmt.Errorf("wait for extractor background calls: %w", ctx.Err())
		}
		c.mu.Lock()
	}
	err := c.backgroundErr
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("wait for extractor background calls: %w", err)
	}
	return nil
}

func (c *lifecycleCoordinator) status() LifecycleStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return LifecycleStatus{
		Closed: c.closed, ActiveExtractions: c.foreground, BackgroundCalls: c.background,
	}
}

func (c *lifecycleCoordinator) signalLocked() {
	close(c.changed)
	c.changed = make(chan struct{})
}
