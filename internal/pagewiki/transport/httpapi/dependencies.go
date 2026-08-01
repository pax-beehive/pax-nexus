package httpapi

import (
	"context"
	"fmt"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
)

const handlerContextKey = "page-wiki.http-handler"

type Injector interface {
	InjectSession(
		context.Context,
		pagewiki.InjectSessionRequest,
	) (pagewiki.InjectResult, error)
}

type Reader interface {
	SourceRevision(context.Context, string) (pagewiki.SourceRevision, error)
	PageByID(context.Context, string) (pagewiki.Page, error)
	PageBySlug(context.Context, string) (pagewiki.Page, error)
	PageRevision(context.Context, string) (pagewiki.PageRevision, error)
	PageRevisionHistory(context.Context, string) ([]pagewiki.PageRevision, error)
	Navigation(context.Context) (pagewiki.Navigation, error)
	Search(context.Context, string) ([]pagewiki.SearchResult, error)
	PageLinks(context.Context, string) (pagewiki.PageLinkSet, error)
	SourceBacklinks(context.Context, string) ([]pagewiki.SourceBacklink, error)
	MaintenanceRun(context.Context, string) (pagewiki.MaintenanceRun, error)
}

type Handler struct {
	injector Injector
	reader   Reader
}

func New(injector Injector, reader Reader) (*Handler, error) {
	if injector == nil {
		return nil, fmt.Errorf("create Page Wiki HTTP handler: injector is required")
	}
	if reader == nil {
		return nil, fmt.Errorf("create Page Wiki HTTP handler: reader is required")
	}
	return &Handler{injector: injector, reader: reader}, nil
}

func InstanceMiddleware(handler *Handler) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		request.Set(handlerContextKey, handler)
		request.Next(ctx)
	}
}

func handlerFromRequest(request *app.RequestContext) (*Handler, bool) {
	configured, found := request.Get(handlerContextKey)
	if !found {
		return nil, false
	}
	handler, ok := configured.(*Handler)
	return handler, ok && handler != nil
}
