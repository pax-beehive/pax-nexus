package sessionconsumer

import "context"

// RunQueuedRebuildForTest drives queued rebuilds only — no injection — so
// DB-backed tests can assert post-rebuild state without racing a scan.
// External test packages (sessionconsumer_test) cannot call unexported
// methods, and the postgres-backed integration suite cannot live in-package
// because platformpostgres imports this package.
func (c *Controller) RunQueuedRebuildForTest(ctx context.Context) {
	c.maybeRebuild(ctx)
}

// DispatchTickForTest runs one tick (dispatch only; jobs run async).
func (c *Controller) DispatchTickForTest(ctx context.Context) {
	c.tick(ctx)
}

// WaitJobsForTest blocks until every dispatched scope job has finished.
func (c *Controller) WaitJobsForTest() {
	c.jobs.Wait()
}
