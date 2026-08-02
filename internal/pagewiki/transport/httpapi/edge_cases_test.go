package httpapi

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	api "github.com/pax-beehive/pax-nexus/internal/pagewiki/transport/httpapi/model/pagewiki/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedHandlersRejectMissingMiddleware(t *testing.T) {
	t.Parallel()
	tests := generatedHandlerTests()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			request := app.NewContext(2)

			tt.handle(context.Background(), request)

			assert.Equal(t, http.StatusInternalServerError, request.Response.StatusCode())
		})
	}
}

func TestGeneratedHandlersRejectMissingRequiredInput(t *testing.T) {
	t.Parallel()
	handler := &Handler{
		injector: failingInjector{err: pagewiki.ErrUnavailable},
		reader:   failingReader{err: pagewiki.ErrUnavailable},
	}
	tests := generatedHandlerTests()

	for _, tt := range tests {
		if !tt.requiresInput {
			continue
		}
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			request := app.NewContext(2)
			request.Set(handlerContextKey, handler)

			tt.handle(context.Background(), request)

			assert.Equal(t, http.StatusBadRequest, request.Response.StatusCode())
		})
	}
}

func TestHandlerMapsReaderFailures(t *testing.T) {
	t.Parallel()
	handler := &Handler{
		injector: failingInjector{err: pagewiki.ErrUnavailable},
		reader:   failingReader{err: pagewiki.ErrUnavailable},
	}
	tests := []struct {
		name   string
		handle func(context.Context, *app.RequestContext)
	}{
		{
			name: "maintenance run",
			handle: func(ctx context.Context, request *app.RequestContext) {
				handler.GetMaintenanceRun(ctx, request, &api.MaintenanceRunRequest{ID: "run"})
			},
		},
		{
			name: "page",
			handle: func(ctx context.Context, request *app.RequestContext) {
				handler.GetPage(ctx, request, &api.PageBySlugRequest{Slug: "page"})
			},
		},
		{
			name: "page revisions",
			handle: func(ctx context.Context, request *app.RequestContext) {
				handler.ListPageRevisions(ctx, request, &api.PageBySlugRequest{Slug: "page"})
			},
		},
		{
			name: "page revision",
			handle: func(ctx context.Context, request *app.RequestContext) {
				handler.GetPageRevision(
					ctx,
					request,
					&api.PageRevisionRequest{Slug: "page", Revision: "revision"},
				)
			},
		},
		{
			name: "page backlinks",
			handle: func(ctx context.Context, request *app.RequestContext) {
				handler.GetPageBacklinks(ctx, request, &api.PageBySlugRequest{Slug: "page"})
			},
		},
		{
			name: "search",
			handle: func(ctx context.Context, request *app.RequestContext) {
				handler.Search(ctx, request, &api.SearchRequest{Q: "query"})
			},
		},
		{
			name: "source revision",
			handle: func(ctx context.Context, request *app.RequestContext) {
				handler.GetSourceRevision(
					ctx,
					request,
					&api.SourceRevisionRequest{Revision: "revision"},
				)
			},
		},
		{
			name: "source backlinks",
			handle: func(ctx context.Context, request *app.RequestContext) {
				handler.GetSourceBacklinks(
					ctx,
					request,
					&api.SourceRevisionRequest{Revision: "revision"},
				)
			},
		},
		{
			name: "navigation",
			handle: func(ctx context.Context, request *app.RequestContext) {
				handler.GetNavigation(ctx, request)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			request := app.NewContext(2)

			tt.handle(context.Background(), request)

			assert.Equal(t, http.StatusServiceUnavailable, request.Response.StatusCode())
		})
	}
}

func TestHandlerMapsLaterReadFailuresAndRevisionOwnership(t *testing.T) {
	t.Parallel()
	page := pagewiki.Page{
		ID:                "page-id",
		Slug:              "page",
		CurrentRevisionID: "current-revision",
	}
	handler := &Handler{
		injector: failingInjector{err: pagewiki.ErrUnavailable},
		reader: pageStageFailingReader{
			failingReader: failingReader{err: pagewiki.ErrUnavailable},
			page:          page,
		},
	}
	tests := []struct {
		name   string
		handle func(context.Context, *app.RequestContext)
	}{
		{
			name: "current revision",
			handle: func(ctx context.Context, request *app.RequestContext) {
				handler.GetPage(ctx, request, &api.PageBySlugRequest{Slug: page.Slug})
			},
		},
		{
			name: "revision history",
			handle: func(ctx context.Context, request *app.RequestContext) {
				handler.ListPageRevisions(ctx, request, &api.PageBySlugRequest{Slug: page.Slug})
			},
		},
		{
			name: "specific revision",
			handle: func(ctx context.Context, request *app.RequestContext) {
				handler.GetPageRevision(
					ctx,
					request,
					&api.PageRevisionRequest{Slug: page.Slug, Revision: "revision"},
				)
			},
		},
		{
			name: "page links",
			handle: func(ctx context.Context, request *app.RequestContext) {
				handler.GetPageBacklinks(ctx, request, &api.PageBySlugRequest{Slug: page.Slug})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			request := app.NewContext(2)

			tt.handle(context.Background(), request)

			assert.Equal(t, http.StatusServiceUnavailable, request.Response.StatusCode())
		})
	}

	mismatchHandler := &Handler{
		injector: failingInjector{err: pagewiki.ErrUnavailable},
		reader: mismatchReader{
			pageStageFailingReader: pageStageFailingReader{
				failingReader: failingReader{err: pagewiki.ErrUnavailable},
				page:          page,
			},
			revision: pagewiki.PageRevision{
				ID:     "other-revision",
				PageID: "other-page",
			},
		},
	}
	request := app.NewContext(2)

	mismatchHandler.GetPageRevision(
		context.Background(),
		request,
		&api.PageRevisionRequest{Slug: page.Slug, Revision: "other-revision"},
	)

	assert.Equal(t, http.StatusNotFound, request.Response.StatusCode())
}

func TestHandlerMapsInjectionAndDomainErrors(t *testing.T) {
	t.Parallel()
	handler := &Handler{
		injector: failingInjector{err: pagewiki.ErrUnavailable},
		reader:   failingReader{err: pagewiki.ErrUnavailable},
	}
	request := app.NewContext(2)

	handler.InjectSession(context.Background(), request, nil)

	assert.Equal(t, http.StatusUnprocessableEntity, request.Response.StatusCode())

	request = app.NewContext(2)
	handler.InjectFile(
		context.Background(),
		request,
		&api.InjectRequest{Events: []*api.SourceEventInput{nil}},
	)
	assert.Equal(t, http.StatusUnprocessableEntity, request.Response.StatusCode())

	request = app.NewContext(2)
	handler.InjectSession(
		context.Background(),
		request,
		&api.InjectRequest{
			SourceID:       "source",
			IdempotencyKey: "idempotency",
			Raw:            "text",
			Events: []*api.SourceEventInput{{
				ID:        "event",
				StartByte: 0,
				EndByte:   4,
			}},
		},
	)
	assert.Equal(t, http.StatusServiceUnavailable, request.Response.StatusCode())

	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{
			name:       "invalid source",
			err:        pagewiki.ErrInvalidSource,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "revision conflict",
			err:        pagewiki.ErrRevisionConflict,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "unclassified error",
			err:        errors.New("failure"),
			wantStatus: http.StatusUnprocessableEntity,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			response := app.NewContext(0)

			writeError(response, tt.err)

			assert.Equal(t, tt.wantStatus, response.Response.StatusCode())
		})
	}
}

func TestMappingPreservesOutgoingAndResolvedLinks(t *testing.T) {
	t.Parallel()
	link := pagewiki.PageLink{
		ID:             "link",
		PageRevisionID: "source-revision",
		SectionKey:     "links",
		StartByte:      4,
		EndByte:        10,
		ExactText:      "target",
		TargetPageID:   "target-page",
	}

	mapped := linksToAPI([]pagewiki.PageLink{link})
	resolved := resolvedLinksToAPI([]pagewiki.ResolvedPageLink{{
		Link:             link,
		SourcePage:       pagewiki.Page{ID: "source-page", Slug: "source"},
		SourceRevisionID: "source-revision",
		TargetPage:       pagewiki.Page{ID: "target-page", Slug: "target"},
	}})

	require.Len(t, mapped, 1)
	assert.Equal(t, link.TargetPageID, mapped[0].TargetPageID)
	require.Len(t, resolved, 1)
	assert.Equal(t, "source", resolved[0].SourcePage.Slug)
	assert.Equal(t, "target", resolved[0].TargetPage.Slug)
}

type generatedHandlerTest struct {
	name          string
	requiresInput bool
	handle        func(context.Context, *app.RequestContext)
}

func generatedHandlerTests() []generatedHandlerTest {
	return []generatedHandlerTest{
		{name: "inject session", requiresInput: true, handle: InjectSession},
		{name: "inject file", requiresInput: true, handle: InjectFile},
		{name: "maintenance run", requiresInput: true, handle: GetMaintenanceRun},
		{name: "page", requiresInput: true, handle: GetPage},
		{name: "page revisions", requiresInput: true, handle: ListPageRevisions},
		{name: "page revision", requiresInput: true, handle: GetPageRevision},
		{name: "page backlinks", requiresInput: true, handle: GetPageBacklinks},
		{name: "search", requiresInput: true, handle: Search},
		{name: "source revision", requiresInput: true, handle: GetSourceRevision},
		{name: "source backlinks", requiresInput: true, handle: GetSourceBacklinks},
		{name: "navigation", handle: GetNavigation},
		{name: "reader", handle: GetReader},
		{name: "reader asset", requiresInput: true, handle: GetReaderAsset},
	}
}

type failingInjector struct {
	err error
}

func (i failingInjector) InjectSession(
	context.Context,
	pagewiki.InjectSessionRequest,
) (pagewiki.InjectResult, error) {
	return pagewiki.InjectResult{}, i.err
}

func (i failingInjector) RebuildTopicTree(context.Context) error {
	return i.err
}

type failingReader struct {
	err error
}

func (r failingReader) SourceRevision(
	context.Context,
	string,
) (pagewiki.SourceRevision, error) {
	return pagewiki.SourceRevision{}, r.err
}

func (r failingReader) PageByID(context.Context, string) (pagewiki.Page, error) {
	return pagewiki.Page{}, r.err
}

func (r failingReader) PageBySlug(context.Context, string) (pagewiki.Page, error) {
	return pagewiki.Page{}, r.err
}

func (r failingReader) PageRevision(
	context.Context,
	string,
) (pagewiki.PageRevision, error) {
	return pagewiki.PageRevision{}, r.err
}

func (r failingReader) PageRevisionHistory(
	context.Context,
	string,
) ([]pagewiki.PageRevision, error) {
	return nil, r.err
}

func (r failingReader) Navigation(context.Context) (pagewiki.Navigation, error) {
	return pagewiki.Navigation{}, r.err
}

func (r failingReader) Search(
	context.Context,
	string,
) ([]pagewiki.SearchResult, error) {
	return nil, r.err
}

func (r failingReader) PageLinks(
	context.Context,
	string,
) (pagewiki.PageLinkSet, error) {
	return pagewiki.PageLinkSet{}, r.err
}

func (r failingReader) SourceBacklinks(
	context.Context,
	string,
) ([]pagewiki.SourceBacklink, error) {
	return nil, r.err
}

func (r failingReader) MaintenanceRun(
	context.Context,
	string,
) (pagewiki.MaintenanceRun, error) {
	return pagewiki.MaintenanceRun{}, r.err
}

type pageStageFailingReader struct {
	failingReader
	page pagewiki.Page
}

func (r pageStageFailingReader) PageBySlug(
	context.Context,
	string,
) (pagewiki.Page, error) {
	return r.page, nil
}

type mismatchReader struct {
	pageStageFailingReader
	revision pagewiki.PageRevision
}

func (r mismatchReader) PageRevision(
	context.Context,
	string,
) (pagewiki.PageRevision, error) {
	return r.revision, nil
}

func TestGeneratedHandlerFixturesAreComplete(t *testing.T) {
	t.Parallel()
	require.Len(t, generatedHandlerTests(), 13)
}
