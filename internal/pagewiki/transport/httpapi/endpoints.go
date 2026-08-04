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
	injector, _, err := h.dependencies(ctx, requestContext)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	result, err := injector.InjectSession(ctx, domainRequest)
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
	_, reader, err := h.dependencies(ctx, requestContext)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	run, err := reader.MaintenanceRun(ctx, request.ID)
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
	_, reader, err := h.dependencies(ctx, requestContext)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	page, err := reader.PageBySlug(ctx, request.Slug)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	revision, err := reader.PageRevision(ctx, page.CurrentRevisionID)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	var successorSlug string
	if page.Retired() && page.SuccessorPageID != "" {
		successor, err := reader.PageByID(ctx, page.SuccessorPageID)
		switch {
		case err == nil:
			successorSlug = successor.Slug
		case errors.Is(err, pagewiki.ErrNotFound):
			// Successor no longer resolvable; surface the retirement without a link.
		default:
			writeError(requestContext, err)
			return
		}
	}
	requestContext.JSON(http.StatusOK, currentPageToAPI(page, revision, successorSlug))
}

func (h *Handler) ListPageRevisions(
	ctx context.Context,
	requestContext *app.RequestContext,
	request *api.PageBySlugRequest,
) {
	_, reader, err := h.dependencies(ctx, requestContext)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	page, err := reader.PageBySlug(ctx, request.Slug)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	revisions, err := reader.PageRevisionHistory(ctx, page.ID)
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
	_, reader, err := h.dependencies(ctx, requestContext)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	page, err := reader.PageBySlug(ctx, request.Slug)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	revision, err := reader.PageRevision(ctx, request.Revision)
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
	_, reader, err := h.dependencies(ctx, requestContext)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	page, err := reader.PageBySlug(ctx, request.Slug)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	links, err := reader.PageLinks(ctx, page.ID)
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
	_, reader, err := h.dependencies(ctx, requestContext)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	results, err := reader.Search(ctx, request.Q)
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
	_, reader, err := h.dependencies(ctx, requestContext)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	revision, err := reader.SourceRevision(ctx, request.Revision)
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
	_, reader, err := h.dependencies(ctx, requestContext)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	backlinks, err := reader.SourceBacklinks(ctx, request.Revision)
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
	_, reader, err := h.dependencies(ctx, requestContext)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	navigation, err := reader.Navigation(ctx)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	requestContext.JSON(http.StatusOK, navigationToAPI(navigation))
}

// RebuildTopicTree discards the stored topic tree and re-places every active
// page from scratch. It blocks until the rebuild is complete. A 200 response
// means the replay has been submitted and the queue drained; if the background
// worker is running concurrently, a small number of trailing tasks may still be
// finishing, so the tree is eventually consistent.
func (h *Handler) RebuildTopicTree(
	ctx context.Context,
	requestContext *app.RequestContext,
	request *api.RebuildTopicTreeRequest,
) {
	injector, _, err := h.dependencies(ctx, requestContext)
	if err != nil {
		writeError(requestContext, err)
		return
	}
	if err := injector.RebuildTopicTree(ctx); err != nil {
		writeError(requestContext, err)
		return
	}
	requestContext.JSON(http.StatusOK, api.RebuildTopicTreeResponse{})
}

func writeError(requestContext *app.RequestContext, err error) {
	status := http.StatusUnprocessableEntity
	code := "operation_failed"
	switch {
	case errors.Is(err, ErrScopeUnresolved):
		status = http.StatusUnauthorized
		code = "unauthorized"
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
