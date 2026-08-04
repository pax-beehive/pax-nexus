package httpapi

import (
	"context"
	"errors"
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
	RebuildTopicTree(context.Context) error
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

// Dependencies resolves the Injector and Reader a request should use.
// Wiring decides, per invocation, which scope's service and repository to
// hand back; the handler never caches the result, so every endpoint sees a
// freshly resolved pair for every request. The request is passed through so
// multi-tenant wiring can authenticate it (human session or agent
// credential) and resolve the caller's team; single-scope wiring ignores it.
type Dependencies func(ctx context.Context, request *app.RequestContext) (Injector, Reader, error)

// ErrScopeUnresolved means wiring could not attribute the request to a
// scope (no valid human session or agent credential). The transport maps it
// to 401.
var ErrScopeUnresolved = errors.New("page wiki request scope could not be resolved")

type Handler struct {
	dependencies Dependencies
}

func New(dependencies Dependencies) (*Handler, error) {
	if dependencies == nil {
		return nil, fmt.Errorf("create Page Wiki HTTP handler: dependencies is required")
	}
	return &Handler{dependencies: dependencies}, nil
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
