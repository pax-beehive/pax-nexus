package operations

import "time"

// SeriesBucket is one time bucket of the Overview throughput series.
//
// Evidence and Recalls are derived from onprem_operation_events, which has no
// scope_id column — see the package's known-defect note. Facts comes from
// note_revisions and IS scope-isolated. When operation-event isolation lands,
// Evidence and Recalls shrink to the caller's own scope.
type SeriesBucket struct {
	BucketAt time.Time
	Evidence int64
	Facts    int64
	Recalls  int64
}
