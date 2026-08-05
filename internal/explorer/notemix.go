package explorer

import (
	"context"
	"time"
)

// NoteKindCount is the number of currently live Team Notes of one kind.
// "Live" is the same effective state the storage snapshot uses: a note that is
// neither resolved nor past its hard expiry at the given instant.
type NoteKindCount struct {
	Kind  string
	Count int64
}

// NoteMixReader answers the Overview's "what the team remembers" breakdown.
type NoteMixReader interface {
	NoteMix(ctx context.Context, at time.Time) ([]NoteKindCount, error)
}
