// Package recall composes independent product recall paths behind one typed
// Agent Memory boundary.
package recall

import (
	"context"

	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

type Intent string

const (
	IntentPassive Intent = "passive"
	IntentActive  Intent = "active"
)

type Source string

const SourceLLMWiki Source = "llm_wiki"

type Disposition string

const (
	DispositionEvidence  Disposition = "evidence"
	DispositionHint      Disposition = "hint"
	DispositionReference Disposition = "reference"
)

type SearchRequest struct {
	Intent      Intent
	Source      Source
	Actor       teamnote.Actor
	TaskRef     string
	ThreadRef   string
	Query       string
	TokenBudget int
	MaxItems    int
}

type GetRequest struct {
	Actor teamnote.Actor
	Ref   string
}

type MemoryHit struct {
	Ref         string
	Text        string
	Score       float64
	Tokens      int
	Disposition Disposition
	Metadata    map[string]string
}

type MemoryDocument struct {
	Ref        string
	Text       string
	Tokens     int
	Provenance map[string]string
}

type PathStatus string

const (
	PathSkipped   PathStatus = "skipped"
	PathCompleted PathStatus = "completed"
	PathCancelled PathStatus = "cancelled"
	PathFailed    PathStatus = "failed"
	PathTimedOut  PathStatus = "timed_out"
)

type PathTrace struct {
	Status      PathStatus
	DurationMS  int64
	Candidates  int
	BudgetDrops int
	Error       string
	Reason      string
	ReasonCodes []string
}

type Trace struct {
	EarlyReturn bool
	TeamNote    PathTrace
	WikiHint    PathTrace
	WikiSearch  PathTrace
}

type SearchResult struct {
	Hits               []MemoryHit
	EvidenceSufficient bool
	Trace              Trace
	ObservationID      int64
}

type TeamNotePath interface {
	RecallNotes(context.Context, teamnote.RecallRequest) (teamnote.NoteEnvelope, error)
}

type WikiPath interface {
	Hint(context.Context, SearchRequest) (MemoryHit, error)
	Search(context.Context, SearchRequest) ([]MemoryHit, error)
	Get(context.Context, GetRequest) (MemoryDocument, error)
}

type Service interface {
	Search(context.Context, SearchRequest) (SearchResult, error)
	Get(context.Context, GetRequest) (MemoryDocument, error)
}
