package httpapi

import (
	"context"
	"crypto/subtle"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/todoapp"
	api "github.com/pax-beehive/pax-nexus/internal/todoapp/transport/httpapi/model/todoapp/api"
)

const (
	humanSessionCookieName = "tm_human_session"
	csrfCookieName         = "tm_csrf"
	csrfHeaderName         = "X-CSRF-Token"
)

// authorize duplicates the double-submit CSRF check performed by
// internal/teamnote/transport/httpapi/handler/identity_registry_endpoints.go
// (authorizeHuman / validateCSRF, lines ~752-860). That code is unexported
// there, so the logic is reproduced here rather than shared.
//
// Confirmed by reading validateCSRF + csrfToken in that file: the
// X-CSRF-Token header is compared, byte-for-byte (constant time, length
// checked first), against the raw tm_csrf cookie value. It is NOT re-derived
// from the session token on every request -- the derivation
// (base64url(sha256("csrf\x00"+sessionToken))) only happens once, when the
// teamnote handler sets the tm_csrf cookie at login. Matching that exact
// comparison here (rather than re-deriving from the session token) is what
// lets the portal frontend's existing humanFetch flow work against these
// routes unchanged.
func (h *Handler) authorize(
	ctx context.Context,
	c *app.RequestContext,
	mutation bool,
) (onprem.HumanPrincipal, bool) {
	if h.identity == nil {
		writeAPIError(c, consts.StatusNotImplemented, "not_configured", "human identity is not configured")
		return onprem.HumanPrincipal{}, false
	}
	token := string(c.Cookie(humanSessionCookieName))
	if strings.TrimSpace(token) == "" {
		writeAPIError(c, consts.StatusUnauthorized, "unauthenticated", "sign in to the portal first")
		return onprem.HumanPrincipal{}, false
	}
	if mutation && !validateCSRF(c) {
		writeAPIError(c, consts.StatusForbidden, "csrf_invalid", "the CSRF token is invalid")
		return onprem.HumanPrincipal{}, false
	}
	principal, err := h.identity.AuthenticateSession(ctx, token)
	if err != nil {
		writeAPIError(c, consts.StatusUnauthorized, "unauthenticated", "the session is not valid")
		return onprem.HumanPrincipal{}, false
	}
	return principal, true
}

// validateCSRF is a byte-for-byte port of the teamnote handler's
// validateCSRF (identity_registry_endpoints.go:845-852).
func validateCSRF(c *app.RequestContext) bool {
	header := strings.TrimSpace(string(c.GetHeader(csrfHeaderName)))
	cookie := strings.TrimSpace(string(c.Cookie(csrfCookieName)))
	if header == "" || len(header) != len(cookie) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header), []byte(cookie)) == 1
}

func (h *Handler) ListTodos(
	ctx context.Context,
	c *app.RequestContext,
	request *api.ListTodosRequest,
) {
	if _, ok := h.authorize(ctx, c, false); !ok {
		return
	}
	status := todoapp.TodoStatus("")
	if request != nil && request.Status != nil {
		status = todoapp.TodoStatus(*request.Status)
	}
	todos, err := h.service.ListTodos(ctx, status)
	if err != nil {
		writeDomainError(c, err)
		return
	}
	c.JSON(consts.StatusOK, todosToAPI(todos))
}

func (h *Handler) CreateTodo(
	ctx context.Context,
	c *app.RequestContext,
	request *api.CreateTodoRequest,
) {
	principal, ok := h.authorize(ctx, c, true)
	if !ok {
		return
	}
	body := ""
	if request.Body != nil {
		body = *request.Body
	}
	todo, err := h.service.CreateTodo(ctx, principal.UserID, request.Title, body)
	if err != nil {
		writeDomainError(c, err)
		return
	}
	c.JSON(consts.StatusCreated, todoToAPI(todo))
}

func (h *Handler) CompleteTodo(
	ctx context.Context,
	c *app.RequestContext,
	request *api.TodoByIDRequest,
) {
	principal, ok := h.authorize(ctx, c, true)
	if !ok {
		return
	}
	todo, err := h.service.CompleteTodo(ctx, principal.UserID, request.TodoID)
	if err != nil {
		writeDomainError(c, err)
		return
	}
	c.JSON(consts.StatusOK, todoToAPI(todo))
}

func (h *Handler) ListTodoSuggestions(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.authorize(ctx, c, false); !ok {
		return
	}
	suggestions, err := h.service.PendingSuggestions(ctx)
	if err != nil {
		writeDomainError(c, err)
		return
	}
	c.JSON(consts.StatusOK, suggestionsToAPI(suggestions))
}

func (h *Handler) RefreshTodoSuggestions(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.authorize(ctx, c, true); !ok {
		return
	}
	created, err := h.service.RefreshSuggestions(ctx)
	if err != nil {
		writeDomainError(c, err)
		return
	}
	c.JSON(consts.StatusOK, &api.RefreshTodoSuggestionsResponse{Created: int32(created)})
}

func (h *Handler) AcceptTodoSuggestion(
	ctx context.Context,
	c *app.RequestContext,
	request *api.TodoSuggestionByIDRequest,
) {
	principal, ok := h.authorize(ctx, c, true)
	if !ok {
		return
	}
	todo, err := h.service.AcceptSuggestion(ctx, principal.UserID, request.SuggestionID)
	if err != nil {
		writeDomainError(c, err)
		return
	}
	c.JSON(consts.StatusOK, todoToAPI(todo))
}

func (h *Handler) DismissTodoSuggestion(
	ctx context.Context,
	c *app.RequestContext,
	request *api.TodoSuggestionByIDRequest,
) {
	principal, ok := h.authorize(ctx, c, true)
	if !ok {
		return
	}
	if err := h.service.DismissSuggestion(ctx, principal.UserID, request.SuggestionID); err != nil {
		writeDomainError(c, err)
		return
	}
	c.JSON(consts.StatusOK, &api.DismissTodoSuggestionResponse{})
}

func writeAPIError(c *app.RequestContext, status int, code, message string) {
	c.JSON(status, map[string]string{"code": code, "message": message})
}

func writeBindingError(c *app.RequestContext, err error) {
	writeAPIError(c, consts.StatusBadRequest, "invalid_request", err.Error())
}
