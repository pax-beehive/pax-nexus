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

const SourcePageWiki Source = "pagewiki"

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
	PageWiki    *PageWikiContext
}

type MemoryDocument struct {
	Ref        string
	Text       string
	Tokens     int
	Provenance map[string]string
	PageWiki   *PageWikiContext
}

type PageWikiContext struct {
	PageID     string
	Slug       string
	Title      string
	RevisionID string
	SectionKey string
	Citations  []PageWikiCitation
	Links      []PageWikiLink
}

type PageWikiCitation struct {
	CitationID    string
	SectionKey    string
	StartByte     int
	EndByte       int
	ExactText     string
	SourceAnchors []PageWikiSourceAnchor
}

type PageWikiSourceAnchor struct {
	SourceRevisionID string
	EventID          string
	StartByte        int
	EndByte          int
	ExactQuote       string
}

type PageWikiLink struct {
	Direction    string
	SectionKey   string
	ExactText    string
	SourcePageID string
	TargetPageID string
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
