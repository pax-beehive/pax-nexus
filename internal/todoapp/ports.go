package todoapp

import (
	"context"
	"errors"
)

var (
	ErrNotFound         = errors.New("todoapp: not found")
	ErrInvalidInput     = errors.New("todoapp: invalid input")
	ErrInvalidTransition = errors.New("todoapp: invalid transition")
)

type Repository interface {
	SaveTodo(ctx context.Context, todo Todo) error
	TodoByID(ctx context.Context, todoID string) (Todo, error)
	ListTodos(ctx context.Context, status TodoStatus) ([]Todo, error)
	SaveSuggestion(ctx context.Context, suggestion Suggestion) error
	SuggestionByID(ctx context.Context, suggestionID string) (Suggestion, error)
	ListSuggestions(ctx context.Context, status SuggestionStatus) ([]Suggestion, error)
	SuggestionFingerprints(ctx context.Context) (map[string]struct{}, error)
}

type NoteDirectory interface {
	ListOpenActionItems(ctx context.Context, limit int) ([]ActionItem, error)
}

type Rewriter interface {
	Rewrite(ctx context.Context, item ActionItem) (title string, body string, err error)
}

type Reporter interface {
	Report(ctx context.Context, event ReportEvent) error
}
