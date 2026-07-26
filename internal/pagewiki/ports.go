package pagewiki

import "context"

type PlanInput struct {
	SourceRevision SourceRevision
	PageCatalog    PageCatalog
}

type Planner interface {
	Plan(context.Context, PlanInput) ([]PageBrief, error)
}

type EditInput struct {
	SourceRevision  SourceRevision
	Brief           PageBrief
	CurrentPage     *Page
	CurrentRevision *PageRevision
}

type Editor interface {
	Edit(context.Context, EditInput) (PageDraft, error)
}

type Repository interface {
	SaveSourceRevision(context.Context, SourceRevision) error
	SourceRevision(context.Context, string) (SourceRevision, error)
	PageCatalog(context.Context) (PageCatalog, error)
	PageByID(context.Context, string) (Page, error)
	PageBySlug(context.Context, string) (Page, error)
	PageRevision(context.Context, string) (PageRevision, error)
	PublishPage(context.Context, PagePublication) error
	Navigation(context.Context) (Navigation, error)
	SaveMaintenanceRun(context.Context, MaintenanceRun) error
}
