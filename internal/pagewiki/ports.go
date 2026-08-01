package pagewiki

import "context"

type PlanInput struct {
	SourceRevision SourceRevision
	PageCatalog    PageCatalog
	Directives     GenerationDirectives
}

type Planner interface {
	Plan(context.Context, PlanInput) ([]PageBrief, error)
}

type EditInput struct {
	SourceRevision  SourceRevision
	Brief           PageBrief
	CurrentPage     *Page
	CurrentRevision *PageRevision
	Directives      GenerationDirectives
}

type Editor interface {
	Edit(context.Context, EditInput) (PageDraft, error)
}

type TreeIndexInput struct {
	Catalog    PageCatalog
	Current    TopicTree
	Directives GenerationDirectives
}

type TreeIndexer interface {
	Index(context.Context, TreeIndexInput) (TopicTree, error)
}

type CurationQuote struct {
	ExactText     string
	SourceOrdinal int
}

type CurationPageView struct {
	PageID   string
	Title    string
	Summary  string
	Markdown string
	Quotes   []CurationQuote
}

type PairJudgeInput struct {
	A, B       CurationPageView
	Directives GenerationDirectives
}

type PageJudgeInput struct {
	Page       CurationPageView
	Signals    []string // human-readable reasons the page was selected
	Directives GenerationDirectives
}

type CurationDraft struct {
	Title    string
	Summary  string
	Sections []SectionDraft
}

type PairVerdict struct {
	Verdict   CurationVerdict // merge | conflict | distinct
	Rationale string
	Draft     *CurationDraft // required for merge/conflict
}

type PageVerdict struct {
	Verdict   CurationVerdict // retire | rewrite | keep
	Rationale string
	Draft     *CurationDraft // required for rewrite
}

type VerifyInput struct {
	Action     CurationVerdict
	Rationale  string
	Pages      []CurationPageView
	Directives GenerationDirectives
}

type VerifyVerdict struct {
	Refuted   bool
	Rationale string
}

type Curator interface {
	JudgePair(context.Context, PairJudgeInput) (PairVerdict, error)
	JudgePage(context.Context, PageJudgeInput) (PageVerdict, error)
	Verify(context.Context, VerifyInput) (VerifyVerdict, error)
}

type TextEmbedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type Repository interface {
	SaveSourceRevision(context.Context, SourceRevision) error
	SourceRevision(context.Context, string) (SourceRevision, error)
	PageCatalog(context.Context) (PageCatalog, error)
	PageByID(context.Context, string) (Page, error)
	PageBySlug(context.Context, string) (Page, error)
	PageRevision(context.Context, string) (PageRevision, error)
	PageRevisionHistory(context.Context, string) ([]PageRevision, error)
	PublishPage(context.Context, PagePublication) error
	// PublishPages atomically validates and applies a batch of publications.
	// A Xanadu link may target a Page inside the same batch that does not
	// exist yet; anything else must already exist. The whole batch is
	// rejected if any member fails validation.
	PublishPages(context.Context, []PagePublication) error
	// RetirePage retires a page: CAS against ExpectedBaseRevisionID, then the
	// page stops surfacing in PageCatalog, Navigation, and Search while
	// PageByID/PageBySlug/PageRevisionHistory keep resolving it.
	RetirePage(context.Context, RetireRequest) error
	Navigation(context.Context) (Navigation, error)
	Search(context.Context, string) ([]SearchResult, error)
	PageLinks(context.Context, string) (PageLinkSet, error)
	SourceBacklinks(context.Context, string) ([]SourceBacklink, error)
	RebuildSearchIndex(context.Context) error
	MaintenanceRun(context.Context, string) (MaintenanceRun, error)
	SaveMaintenanceRun(context.Context, MaintenanceRun) error
	TopicTree(context.Context) (TopicTree, error)
	ReplaceTopicTree(context.Context, TopicTree) error
	GenerationSettings(context.Context) (GenerationDirectives, error)
	SetGenerationSettings(context.Context, GenerationDirectives) error
	SaveCurationRun(context.Context, CurationRun) error
	CurationRun(context.Context, string) (CurationRun, error)
	PageEmbeddings(context.Context) ([]PageEmbedding, error)
	SavePageEmbedding(context.Context, PageEmbedding) error
	// SourceRevisionOrdinals returns the 0-based order in which source
	// revisions were saved (postgres hydration replays in created_at order,
	// so the ordinal is a chronology proxy for evidence recency).
	SourceRevisionOrdinals(context.Context) (map[string]int, error)
}
