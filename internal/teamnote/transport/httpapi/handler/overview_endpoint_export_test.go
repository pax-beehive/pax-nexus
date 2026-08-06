package handler

// OverviewSourceTimeoutForTest exposes overviewSourceTimeout to the external
// handler_test package so tests can shrink it (e.g. to exercise the
// per-source timeout path) without widening the production API surface —
// overviewSourceTimeout itself stays unexported.
var OverviewSourceTimeoutForTest = &overviewSourceTimeout
