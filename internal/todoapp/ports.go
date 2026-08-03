package todoapp

import (
	"context"
	"errors"
)

var (
	ErrNotFound          = errors.New("todoapp: not found")
	ErrInvalidInput      = errors.New("todoapp: invalid input")
	ErrInvalidTransition = errors.New("todoapp: invalid transition")
)

type Repository interface {
	SaveTodo(ctx context.Context, scopeID string, todo Todo) error
	TodoByID(ctx context.Context, scopeID string, todoID string) (Todo, error)
	ListTodos(ctx context.Context, scopeID string, status TodoStatus) ([]Todo, error)
	SaveSuggestion(ctx context.Context, scopeID string, suggestion Suggestion) error
	SuggestionByID(ctx context.Context, scopeID string, suggestionID string) (Suggestion, error)
	ListSuggestions(ctx context.Context, scopeID string, status SuggestionStatus) ([]Suggestion, error)
	SuggestionFingerprints(ctx context.Context, scopeID string) (map[string]struct{}, error)
}

type NoteDirectory interface {
	ListOpenActionItems(ctx context.Context, scopeID string, limit int) ([]ActionItem, error)
}

type Rewriter interface {
	Rewrite(ctx context.Context, item ActionItem) (title string, body string, err error)
}

type Reporter interface {
	Report(ctx context.Context, scopeID string, event ReportEvent) error
}
