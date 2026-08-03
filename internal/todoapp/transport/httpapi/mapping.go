package httpapi

import (
	"errors"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/todoapp"
	api "github.com/pax-beehive/pax-nexus/internal/todoapp/transport/httpapi/model/todoapp/api"
)

// writeDomainError maps todoapp domain sentinels to the {code, message}
// wire error shape shared with the teamnote human API handler. Unknown
// errors (e.g. raw Postgres failures) are logged with full detail and
// answered with a generic message so internals never reach the browser,
// mirroring the teamnote handler's writeHumanError.
func (h *Handler) writeDomainError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, todoapp.ErrNotFound):
		writeAPIError(c, consts.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, todoapp.ErrInvalidTransition):
		writeAPIError(c, consts.StatusConflict, "invalid_transition", err.Error())
	case errors.Is(err, todoapp.ErrRefreshInProgress):
		writeAPIError(c, consts.StatusConflict, "refresh_in_progress",
			"a suggestion refresh is already running for this team")
	case errors.Is(err, todoapp.ErrInvalidInput):
		writeAPIError(c, consts.StatusBadRequest, "invalid_input", err.Error())
	default:
		h.logger.Error("todo app request failed", "error", err)
		writeAPIError(c, consts.StatusInternalServerError, "internal_error",
			"the request could not be completed")
	}
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func todoToAPI(todo todoapp.Todo) *api.TodoItem {
	return &api.TodoItem{
		TodoID:       todo.ID,
		Title:        todo.Title,
		Body:         todo.Body,
		Status:       string(todo.Status),
		Source:       string(todo.Source),
		SuggestionID: optionalString(todo.SuggestionID),
		NoteID:       optionalString(todo.NoteID),
		CreatedBy:    todo.CreatedBy,
		CreatedAt:    formatTime(todo.CreatedAt),
		UpdatedAt:    formatTime(todo.UpdatedAt),
	}
}

func todosToAPI(todos []todoapp.Todo) *api.ListTodosResponse {
	items := make([]*api.TodoItem, 0, len(todos))
	for _, todo := range todos {
		items = append(items, todoToAPI(todo))
	}
	return &api.ListTodosResponse{Todos: items}
}

func suggestionToAPI(suggestion todoapp.Suggestion) *api.TodoSuggestionItem {
	return &api.TodoSuggestionItem{
		SuggestionID: suggestion.ID,
		NoteID:       suggestion.NoteID,
		Kind:         suggestion.Kind,
		Title:        suggestion.Title,
		Body:         suggestion.Body,
		Status:       string(suggestion.Status),
		CreatedAt:    formatTime(suggestion.CreatedAt),
	}
}

func suggestionsToAPI(suggestions []todoapp.Suggestion) *api.ListTodoSuggestionsResponse {
	items := make([]*api.TodoSuggestionItem, 0, len(suggestions))
	for _, suggestion := range suggestions {
		items = append(items, suggestionToAPI(suggestion))
	}
	return &api.ListTodoSuggestionsResponse{Suggestions: items}
}
