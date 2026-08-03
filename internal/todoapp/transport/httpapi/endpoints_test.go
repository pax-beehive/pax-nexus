package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	todoapprouter "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/router/todoapp/api"
	"github.com/pax-beehive/pax-nexus/internal/todoapp"
	"github.com/pax-beehive/pax-nexus/internal/todoapp/transport/httpapi"
	"github.com/stretchr/testify/require"
)

const (
	sessionToken = "session-token"
	csrfValue    = "csrf-token"
)

func newTestServer(t *testing.T, service httpapi.Service, identity httpapi.HumanAuthenticator) *server.Hertz {
	t.Helper()
	handler, err := httpapi.New(service, identity)
	require.NoError(t, err)
	hertz := server.New()
	hertz.Use(httpapi.InstanceMiddleware(handler))
	todoapprouter.Register(hertz)
	return hertz
}

func performJSON(hertz *server.Hertz, method, path, body string, headers ...ut.Header) *ut.ResponseRecorder {
	var requestBody *ut.Body
	if body != "" {
		requestBody = &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)}
	}
	allHeaders := append([]ut.Header{{Key: "Content-Type", Value: "application/json"}}, headers...)
	return ut.PerformRequest(hertz.Engine, method, path, requestBody, allHeaders...)
}

func sessionCookieHeader() ut.Header {
	return ut.Header{Key: "Cookie", Value: "tm_human_session=" + sessionToken + "; tm_csrf=" + csrfValue}
}

func TestGivenNoSessionCookieWhenListTodosThenUnauthorized(t *testing.T) {
	t.Parallel()
	hertz := newTestServer(t, &fakeService{}, fakeAuthenticator{principal: onprem.HumanPrincipal{UserID: "user-1"}})

	response := performJSON(hertz, http.MethodGet, "/v1/todo/todos", "")

	require.Equal(t, http.StatusUnauthorized, response.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, "unauthenticated", body["code"])
}

func TestGivenMismatchedCSRFWhenCreateTodoThenForbidden(t *testing.T) {
	t.Parallel()
	hertz := newTestServer(t, &fakeService{}, fakeAuthenticator{principal: onprem.HumanPrincipal{UserID: "user-1"}})

	response := performJSON(
		hertz, http.MethodPost, "/v1/todo/todos", `{"title":"Buy milk"}`,
		sessionCookieHeader(),
		ut.Header{Key: "X-CSRF-Token", Value: "wrong-token"},
	)

	require.Equal(t, http.StatusForbidden, response.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, "csrf_invalid", body["code"])
}

func TestGivenAuthenticatedRequestWhenListTodosThenTodosAreMapped(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	fake := &fakeService{
		todos: []todoapp.Todo{
			{
				ID:        "todo-1",
				Title:     "Buy milk",
				Body:      "2%",
				Status:    todoapp.TodoOpen,
				Source:    todoapp.TodoSourceManual,
				CreatedBy: "user-1",
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}
	hertz := newTestServer(t, fake, fakeAuthenticator{principal: onprem.HumanPrincipal{UserID: "user-1"}})

	response := performJSON(hertz, http.MethodGet, "/v1/todo/todos?status=open", "", sessionCookieHeader())

	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, todoapp.TodoOpen, fake.listedStatus)
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	todos, ok := body["todos"].([]any)
	require.True(t, ok)
	require.Len(t, todos, 1)
	item, ok := todos[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "todo-1", item["todo_id"])
	require.Equal(t, "Buy milk", item["title"])
	require.Equal(t, "open", item["status"])
	require.Equal(t, "manual", item["source"])
	require.Equal(t, "user-1", item["created_by"])
	require.Equal(t, now.Format(time.RFC3339), item["created_at"])
	require.Equal(t, now.Format(time.RFC3339), item["updated_at"])
}

func TestGivenAuthenticatedRequestWhenCreateTodoThenPrincipalIsCreator(t *testing.T) {
	t.Parallel()
	fake := &fakeService{}
	hertz := newTestServer(t, fake, fakeAuthenticator{principal: onprem.HumanPrincipal{UserID: "user-42"}})

	response := performJSON(
		hertz, http.MethodPost, "/v1/todo/todos", `{"title":"Buy milk","body":"2%"}`,
		sessionCookieHeader(),
		ut.Header{Key: "X-CSRF-Token", Value: csrfValue},
	)

	require.Equal(t, http.StatusCreated, response.Code)
	require.Equal(t, "user-42", fake.createdByUserID)
	require.Equal(t, "Buy milk", fake.createdTitle)
	require.Equal(t, "2%", fake.createdBody)
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, "user-42", body["created_by"])
}

func TestGivenSuggestionErrorsWhenAcceptSuggestionThenErrorsAreMapped(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "unknown suggestion", err: todoapp.ErrNotFound, wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "already resolved", err: todoapp.ErrInvalidTransition, wantStatus: http.StatusConflict, wantCode: "invalid_transition"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeService{acceptErr: tt.err}
			hertz := newTestServer(t, fake, fakeAuthenticator{principal: onprem.HumanPrincipal{UserID: "user-1"}})

			response := performJSON(
				hertz, http.MethodPost, "/v1/todo/suggestions/missing/accept", "",
				sessionCookieHeader(),
				ut.Header{Key: "X-CSRF-Token", Value: csrfValue},
			)

			require.Equal(t, tt.wantStatus, response.Code)
			var body map[string]any
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			require.Equal(t, tt.wantCode, body["code"])
		})
	}
}

func TestGivenUnconfiguredIdentityWhenListTodosThenNotImplemented(t *testing.T) {
	t.Parallel()
	hertz := newTestServer(t, &fakeService{}, nil)

	response := performJSON(hertz, http.MethodGet, "/v1/todo/todos", "")

	require.Equal(t, http.StatusNotImplemented, response.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, "not_configured", body["code"])
}

func TestGivenInvalidSessionWhenListTodosThenUnauthorized(t *testing.T) {
	t.Parallel()
	hertz := newTestServer(t, &fakeService{}, fakeAuthenticator{err: onprem.ErrUnauthorized})

	response := performJSON(hertz, http.MethodGet, "/v1/todo/todos", "", sessionCookieHeader())

	require.Equal(t, http.StatusUnauthorized, response.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, "unauthenticated", body["code"])
}

func TestGivenMalformedBodyWhenCreateTodoThenBindingErrorIsReturned(t *testing.T) {
	t.Parallel()
	hertz := newTestServer(t, &fakeService{}, fakeAuthenticator{principal: onprem.HumanPrincipal{UserID: "user-1"}})

	response := performJSON(
		hertz, http.MethodPost, "/v1/todo/todos", `{`,
		sessionCookieHeader(),
		ut.Header{Key: "X-CSRF-Token", Value: csrfValue},
	)

	require.Equal(t, http.StatusBadRequest, response.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, "invalid_request", body["code"])
}

func TestGivenMalformedBodyWhenMutationRoutesAreCalledThenBindingErrorIsReturned(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		path string
	}{
		{name: "complete todo", path: "/v1/todo/todos/todo-1/complete"},
		{name: "accept suggestion", path: "/v1/todo/suggestions/suggestion-1/accept"},
		{name: "dismiss suggestion", path: "/v1/todo/suggestions/suggestion-1/dismiss"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			hertz := newTestServer(t, &fakeService{}, fakeAuthenticator{principal: onprem.HumanPrincipal{UserID: "user-1"}})

			response := performJSON(
				hertz, http.MethodPost, tt.path, `{`,
				sessionCookieHeader(),
				ut.Header{Key: "X-CSRF-Token", Value: csrfValue},
			)

			require.Equal(t, http.StatusBadRequest, response.Code)
			var body map[string]any
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			require.Equal(t, "invalid_request", body["code"])
		})
	}
}

func TestGivenAuthenticatedRequestWhenCompleteTodoThenTodoIsCompleted(t *testing.T) {
	t.Parallel()
	fake := &fakeService{}
	hertz := newTestServer(t, fake, fakeAuthenticator{principal: onprem.HumanPrincipal{UserID: "user-1"}})

	response := performJSON(
		hertz, http.MethodPost, "/v1/todo/todos/todo-1/complete", "",
		sessionCookieHeader(),
		ut.Header{Key: "X-CSRF-Token", Value: csrfValue},
	)

	require.Equal(t, http.StatusOK, response.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, "todo-1", body["todo_id"])
	require.Equal(t, "done", body["status"])
	require.Equal(t, "note-1", body["note_id"])
}

func TestGivenUnknownTodoWhenCompleteTodoThenNotFound(t *testing.T) {
	t.Parallel()
	fake := &fakeService{completeErr: todoapp.ErrNotFound}
	hertz := newTestServer(t, fake, fakeAuthenticator{principal: onprem.HumanPrincipal{UserID: "user-1"}})

	response := performJSON(
		hertz, http.MethodPost, "/v1/todo/todos/missing/complete", "",
		sessionCookieHeader(),
		ut.Header{Key: "X-CSRF-Token", Value: csrfValue},
	)

	require.Equal(t, http.StatusNotFound, response.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, "not_found", body["code"])
}

func TestGivenAuthenticatedRequestWhenListTodoSuggestionsThenSuggestionsAreMapped(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	fake := &fakeService{
		suggestions: []todoapp.Suggestion{
			{
				ID:        "suggestion-1",
				NoteID:    "note-1",
				Kind:      "action_item",
				Title:     "Follow up",
				Body:      "Ping the customer",
				Status:    todoapp.SuggestionPending,
				CreatedAt: now,
			},
		},
	}
	hertz := newTestServer(t, fake, fakeAuthenticator{principal: onprem.HumanPrincipal{UserID: "user-1"}})

	response := performJSON(hertz, http.MethodGet, "/v1/todo/suggestions", "", sessionCookieHeader())

	require.Equal(t, http.StatusOK, response.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	suggestions, ok := body["suggestions"].([]any)
	require.True(t, ok)
	require.Len(t, suggestions, 1)
	item, ok := suggestions[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "suggestion-1", item["suggestion_id"])
	require.Equal(t, "note-1", item["note_id"])
	require.Equal(t, "action_item", item["kind"])
	require.Equal(t, "Follow up", item["title"])
	require.Equal(t, "Ping the customer", item["body"])
	require.Equal(t, "pending", item["status"])
	require.Equal(t, now.Format(time.RFC3339), item["created_at"])
}

func TestGivenAuthenticatedRequestWhenRefreshTodoSuggestionsThenCreatedCountIsReturned(t *testing.T) {
	t.Parallel()
	fake := &fakeService{refreshCreated: 3}
	hertz := newTestServer(t, fake, fakeAuthenticator{principal: onprem.HumanPrincipal{UserID: "user-1"}})

	response := performJSON(
		hertz, http.MethodPost, "/v1/todo/suggestions/refresh", "",
		sessionCookieHeader(),
		ut.Header{Key: "X-CSRF-Token", Value: csrfValue},
	)

	require.Equal(t, http.StatusOK, response.Code)
	var body struct {
		Created int `json:"created"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, 3, body.Created)
}

func TestGivenAuthenticatedRequestWhenDismissTodoSuggestionThenSuccess(t *testing.T) {
	t.Parallel()
	fake := &fakeService{}
	hertz := newTestServer(t, fake, fakeAuthenticator{principal: onprem.HumanPrincipal{UserID: "user-1"}})

	response := performJSON(
		hertz, http.MethodPost, "/v1/todo/suggestions/suggestion-1/dismiss", "",
		sessionCookieHeader(),
		ut.Header{Key: "X-CSRF-Token", Value: csrfValue},
	)

	require.Equal(t, http.StatusOK, response.Code)
}

func TestGivenInvalidTransitionWhenDismissTodoSuggestionThenConflict(t *testing.T) {
	t.Parallel()
	fake := &fakeService{dismissErr: todoapp.ErrInvalidTransition}
	hertz := newTestServer(t, fake, fakeAuthenticator{principal: onprem.HumanPrincipal{UserID: "user-1"}})

	response := performJSON(
		hertz, http.MethodPost, "/v1/todo/suggestions/suggestion-1/dismiss", "",
		sessionCookieHeader(),
		ut.Header{Key: "X-CSRF-Token", Value: csrfValue},
	)

	require.Equal(t, http.StatusConflict, response.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, "invalid_transition", body["code"])
}

// TestGivenUnknownServiceErrorThenDetailIsLoggedNotExposed pins the info
// disclosure fix: an unmapped error (e.g. raw Postgres text) must reach the
// handler's logger but never the browser, mirroring the teamnote handler.
func TestGivenUnknownServiceErrorThenDetailIsLoggedNotExposed(t *testing.T) {
	t.Parallel()
	fake := &fakeService{listErr: errors.New(`pq: password authentication failed for user "todo"`)}
	// A nil logger option input keeps the default and must not panic.
	nilLoggerHandler, err := httpapi.New(fake,
		fakeAuthenticator{principal: onprem.HumanPrincipal{UserID: "user-1"}}, httpapi.WithLogger(nil))
	require.NoError(t, err)
	require.NotNil(t, nilLoggerHandler)

	var logs bytes.Buffer
	handler, err := httpapi.New(fake, fakeAuthenticator{principal: onprem.HumanPrincipal{UserID: "user-1"}},
		httpapi.WithLogger(slog.New(slog.NewJSONHandler(&logs, nil))))
	require.NoError(t, err)
	hertz := server.New()
	hertz.Use(httpapi.InstanceMiddleware(handler))
	todoapprouter.Register(hertz)

	response := performJSON(hertz, http.MethodGet, "/v1/todo/todos", "", sessionCookieHeader())

	require.Equal(t, http.StatusInternalServerError, response.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, "internal_error", body["code"])
	require.Equal(t, "the request could not be completed", body["message"])
	require.NotContains(t, response.Body.String(), "password")
	require.Contains(t, logs.String(), "password authentication failed")
}

// TestGivenRefreshInProgressThenConflictIsReturned maps the domain's
// single-flight refresh outcome onto the wire: 409 with a stable code the
// portal can turn into "refresh already running".
func TestGivenRefreshInProgressThenConflictIsReturned(t *testing.T) {
	t.Parallel()
	fake := &fakeService{refreshErr: fmt.Errorf("refresh suggestions for scope s: %w", todoapp.ErrRefreshInProgress)}
	hertz := newTestServer(t, fake, fakeAuthenticator{principal: onprem.HumanPrincipal{UserID: "user-1"}})

	response := performJSON(
		hertz, http.MethodPost, "/v1/todo/suggestions/refresh", "",
		sessionCookieHeader(),
		ut.Header{Key: "X-CSRF-Token", Value: csrfValue},
	)

	require.Equal(t, http.StatusConflict, response.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, "refresh_in_progress", body["code"])
}

// TestGivenScopedPrincipalThenHandlerThreadsScopeToService is the regression
// test Phase 1's final review asked for on the todoapp side: every route
// must pass principal.ScopeID (not a hardcoded default) through to the
// Service.
func TestGivenScopedPrincipalThenHandlerThreadsScopeToService(t *testing.T) {
	t.Parallel()
	principal := onprem.HumanPrincipal{UserID: "user-1", ScopeID: "other-scope"}

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "list todos", method: http.MethodGet, path: "/v1/todo/todos"},
		{name: "create todo", method: http.MethodPost, path: "/v1/todo/todos", body: `{"title":"Buy milk"}`},
		{name: "complete todo", method: http.MethodPost, path: "/v1/todo/todos/todo-1/complete", body: ""},
		{name: "list suggestions", method: http.MethodGet, path: "/v1/todo/suggestions"},
		{name: "refresh suggestions", method: http.MethodPost, path: "/v1/todo/suggestions/refresh", body: ""},
		{name: "accept suggestion", method: http.MethodPost, path: "/v1/todo/suggestions/suggestion-1/accept", body: ""},
		{name: "dismiss suggestion", method: http.MethodPost, path: "/v1/todo/suggestions/suggestion-1/dismiss", body: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeService{}
			hertz := newTestServer(t, fake, fakeAuthenticator{principal: principal})

			headers := []ut.Header{sessionCookieHeader()}
			if tt.method == http.MethodPost {
				headers = append(headers, ut.Header{Key: "X-CSRF-Token", Value: csrfValue})
			}
			performJSON(hertz, tt.method, tt.path, tt.body, headers...)

			require.Equal(t, "other-scope", fake.lastScopeID)
		})
	}
}

// fakeAuthenticator satisfies httpapi.HumanAuthenticator.
type fakeAuthenticator struct {
	principal onprem.HumanPrincipal
	err       error
}

func (f fakeAuthenticator) AuthenticateSession(context.Context, string) (onprem.HumanPrincipal, error) {
	return f.principal, f.err
}

// fakeService satisfies httpapi.Service.
type fakeService struct {
	todos        []todoapp.Todo
	listErr      error
	listedStatus todoapp.TodoStatus

	createErr       error
	createdByUserID string
	createdTitle    string
	createdBody     string

	completeErr error

	suggestions []todoapp.Suggestion
	listSugErr  error

	refreshCreated int
	refreshErr     error

	acceptTodo todoapp.Todo
	acceptErr  error

	dismissErr error

	// lastScopeID records the scopeID passed to the most recently invoked
	// method, so tests can assert the handler threads principal.ScopeID
	// through to the Service.
	lastScopeID string
}

func (f *fakeService) CreateTodo(_ context.Context, scopeID, userID, title, body string) (todoapp.Todo, error) {
	f.lastScopeID = scopeID
	f.createdByUserID = userID
	f.createdTitle = title
	f.createdBody = body
	if f.createErr != nil {
		return todoapp.Todo{}, f.createErr
	}
	return todoapp.Todo{
		ID:        "new-todo",
		Title:     title,
		Body:      body,
		Status:    todoapp.TodoOpen,
		Source:    todoapp.TodoSourceManual,
		CreatedBy: userID,
	}, nil
}

func (f *fakeService) CompleteTodo(_ context.Context, scopeID, userID, todoID string) (todoapp.Todo, error) {
	f.lastScopeID = scopeID
	if f.completeErr != nil {
		return todoapp.Todo{}, f.completeErr
	}
	return todoapp.Todo{ID: todoID, Status: todoapp.TodoDone, CreatedBy: userID, NoteID: "note-1"}, nil
}

func (f *fakeService) ListTodos(_ context.Context, scopeID string, status todoapp.TodoStatus) ([]todoapp.Todo, error) {
	f.lastScopeID = scopeID
	f.listedStatus = status
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.todos, nil
}

func (f *fakeService) PendingSuggestions(_ context.Context, scopeID string) ([]todoapp.Suggestion, error) {
	f.lastScopeID = scopeID
	if f.listSugErr != nil {
		return nil, f.listSugErr
	}
	return f.suggestions, nil
}

func (f *fakeService) RefreshSuggestions(_ context.Context, scopeID string) (int, error) {
	f.lastScopeID = scopeID
	return f.refreshCreated, f.refreshErr
}

func (f *fakeService) AcceptSuggestion(_ context.Context, scopeID, userID, suggestionID string) (todoapp.Todo, error) {
	f.lastScopeID = scopeID
	if f.acceptErr != nil {
		return todoapp.Todo{}, f.acceptErr
	}
	todo := f.acceptTodo
	todo.CreatedBy = userID
	todo.SuggestionID = suggestionID
	return todo, nil
}

func (f *fakeService) DismissSuggestion(_ context.Context, scopeID, _, _ string) error {
	f.lastScopeID = scopeID
	return f.dismissErr
}
