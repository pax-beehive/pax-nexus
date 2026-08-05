package operations

import "time"

// SeriesBucket is one time bucket of the Overview throughput series.
//
// Evidence and Recalls are derived from onprem_operation_events, Facts from
// note_revisions. Both sources are scope-isolated: each is filtered by its
// own table's scope_id column, so every field on this struct reflects only
// the caller's own team.
type SeriesBucket struct {
	BucketAt time.Time
	Evidence int64
	Facts    int64
	Recalls  int64
}
