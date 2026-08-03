package sessionconsumer

import "context"

// RunQueuedRebuildForTest drives one maybeRebuild pass. External test
// packages (sessionconsumer_test) cannot call unexported methods, and the
// postgres-backed integration suite cannot live in-package because
// platformpostgres imports this package.
func (c *Controller) RunQueuedRebuildForTest(ctx context.Context) {
	c.maybeRebuild(ctx)
}
