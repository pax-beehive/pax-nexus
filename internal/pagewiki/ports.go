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
}
