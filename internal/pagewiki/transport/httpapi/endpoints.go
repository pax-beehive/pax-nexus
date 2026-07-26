package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	api "github.com/pax-beehive/pax-nexus/internal/pagewiki/transport/httpapi/model/pagewiki/api"
)

func (h *Handler) InjectSession(
	ctx context.Context,
	requestContext *app.RequestContext,
	request *api.InjectRequest,
) {
	h.inject(ctx, requestContext, request)
}

func (h *Handler) InjectFile(
	ctx context.Context,
	requestContext *app.RequestContext,
	request *api.InjectRequest,
) {
	h.inject(ctx, requestContext, request)
}

func (h *Handler) inject(
	ctx context.Context,
	requestContext *app.RequestContext,
	request *api.InjectRequest,
) {
	domainRequest, err := injectRequestToDomain(request)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	result, err := h.injector.InjectSession(ctx, domainRequest)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	requestContext.JSON(http.StatusOK, injectResultToAPI(result))
}

func (h *Handler) GetMaintenanceRun(
	ctx context.Context,
	requestContext *app.RequestContext,
	request *api.MaintenanceRunRequest,
) {
	run, err := h.reader.MaintenanceRun(ctx, request.ID)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	requestContext.JSON(http.StatusOK, maintenanceRunToAPI(run))
}

func (h *Handler) GetPage(
	ctx context.Context,
	requestContext *app.RequestContext,
	request *api.PageBySlugRequest,
) {
	page, err := h.reader.PageBySlug(ctx, request.Slug)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	revision, err := h.reader.PageRevision(ctx, page.CurrentRevisionID)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	requestContext.JSON(http.StatusOK, currentPageToAPI(page, revision))
}

func (h *Handler) ListPageRevisions(
	ctx context.Context,
	requestContext *app.RequestContext,
	request *api.PageBySlugRequest,
) {
	page, err := h.reader.PageBySlug(ctx, request.Slug)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	revisions, err := h.reader.PageRevisionHistory(ctx, page.ID)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	requestContext.JSON(http.StatusOK, revisionsToAPI(revisions))
}

func (h *Handler) GetPageRevision(
	ctx context.Context,
	requestContext *app.RequestContext,
	request *api.PageRevisionRequest,
) {
	page, err := h.reader.PageBySlug(ctx, request.Slug)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	revision, err := h.reader.PageRevision(ctx, request.Revision)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	if revision.PageID != page.ID {
		writeError(
			requestContext,
			fmt.Errorf("%w: PageRevision %q", pagewiki.ErrNotFound, request.Revision),
		)
		return
	}
	requestContext.JSON(http.StatusOK, pageRevisionToAPI(revision))
}

func (h *Handler) GetPageBacklinks(
	ctx context.Context,
	requestContext *app.RequestContext,
	request *api.PageBySlugRequest,
) {
	page, err := h.reader.PageBySlug(ctx, request.Slug)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	links, err := h.reader.PageLinks(ctx, page.ID)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	requestContext.JSON(http.StatusOK, pageLinksToAPI(links))
}

func (h *Handler) Search(
	ctx context.Context,
	requestContext *app.RequestContext,
	request *api.SearchRequest,
) {
	results, err := h.reader.Search(ctx, request.Q)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	requestContext.JSON(http.StatusOK, searchResultsToAPI(results))
}

func (h *Handler) GetSourceRevision(
	ctx context.Context,
	requestContext *app.RequestContext,
	request *api.SourceRevisionRequest,
) {
	revision, err := h.reader.SourceRevision(ctx, request.Revision)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	requestContext.JSON(http.StatusOK, sourceRevisionToAPI(revision))
}

func (h *Handler) GetSourceBacklinks(
	ctx context.Context,
	requestContext *app.RequestContext,
	request *api.SourceRevisionRequest,
) {
	backlinks, err := h.reader.SourceBacklinks(ctx, request.Revision)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	requestContext.JSON(http.StatusOK, sourceBacklinksToAPI(backlinks))
}

func (h *Handler) GetNavigation(
	ctx context.Context,
	requestContext *app.RequestContext,
) {
	navigation, err := h.reader.Navigation(ctx)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	requestContext.JSON(http.StatusOK, navigationToAPI(navigation))
}

func writeError(requestContext *app.RequestContext, err error) {
	status := http.StatusUnprocessableEntity
	code := "operation_failed"
	switch {
	case errors.Is(err, pagewiki.ErrUnavailable):
		status = http.StatusServiceUnavailable
		code = "unavailable"
	case errors.Is(err, pagewiki.ErrNotFound):
		status = http.StatusNotFound
		code = "not_found"
	case errors.Is(err, pagewiki.ErrInvalidSearch):
		status = http.StatusBadRequest
		code = "invalid_search"
	case errors.Is(err, pagewiki.ErrInvalidRequest),
		errors.Is(err, pagewiki.ErrInvalidSource),
		errors.Is(err, pagewiki.ErrInvalidPageBrief):
		status = http.StatusBadRequest
		code = "invalid_request"
	case errors.Is(err, pagewiki.ErrRevisionConflict),
		errors.Is(err, pagewiki.ErrImmutableConflict):
		status = http.StatusConflict
		code = "conflict"
	}
	requestContext.JSON(status, map[string]string{
		"code":    code,
		"message": err.Error(),
	})
}

func writeBindingError(requestContext *app.RequestContext, err error) {
	requestContext.JSON(http.StatusBadRequest, map[string]string{
		"code":    "invalid_request",
		"message": err.Error(),
	})
}
