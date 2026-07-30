package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/pax-beehive/pax-nexus/internal/todoapp"
)

// Repository is the in-memory twin of the Postgres adapter, for domain tests.
type Repository struct {
	mu          sync.Mutex
	todos       map[string]todoapp.Todo
	suggestions map[string]todoapp.Suggestion
}

// NewRepository creates a new in-memory repository.
func NewRepository() *Repository {
	return &Repository{
		todos:       map[string]todoapp.Todo{},
		suggestions: map[string]todoapp.Suggestion{},
	}
}

// SaveTodo saves or updates a todo by ID.
func (r *Repository) SaveTodo(ctx context.Context, todo todoapp.Todo) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.todos[todo.ID] = todo
	return nil
}

// TodoByID retrieves a todo by ID, returning ErrNotFound if not present.
func (r *Repository) TodoByID(ctx context.Context, todoID string) (todoapp.Todo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	todo, ok := r.todos[todoID]
	if !ok {
		return todoapp.Todo{}, todoapp.ErrNotFound
	}
	return todo, nil
}

// ListTodos returns all todos matching the status (empty string = all), sorted by UpdatedAt desc, then ID desc.
func (r *Repository) ListTodos(ctx context.Context, status todoapp.TodoStatus) ([]todoapp.Todo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var todos []todoapp.Todo
	for _, todo := range r.todos {
		if status == "" || todo.Status == status {
			todos = append(todos, todo)
		}
	}

	// Sort by UpdatedAt descending, then by ID descending for determinism.
	sort.Slice(todos, func(i, j int) bool {
		if !todos[i].UpdatedAt.Equal(todos[j].UpdatedAt) {
			return todos[i].UpdatedAt.After(todos[j].UpdatedAt)
		}
		return todos[i].ID > todos[j].ID
	})

	return todos, nil
}

// SaveSuggestion saves or updates a suggestion by ID.
func (r *Repository) SaveSuggestion(ctx context.Context, suggestion todoapp.Suggestion) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.suggestions[suggestion.ID] = suggestion
	return nil
}

// SuggestionByID retrieves a suggestion by ID, returning ErrNotFound if not present.
func (r *Repository) SuggestionByID(ctx context.Context, suggestionID string) (todoapp.Suggestion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	suggestion, ok := r.suggestions[suggestionID]
	if !ok {
		return todoapp.Suggestion{}, todoapp.ErrNotFound
	}
	return suggestion, nil
}

// ListSuggestions returns all suggestions matching the status (empty string = all), sorted by UpdatedAt desc, then ID desc.
func (r *Repository) ListSuggestions(ctx context.Context, status todoapp.SuggestionStatus) ([]todoapp.Suggestion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var suggestions []todoapp.Suggestion
	for _, suggestion := range r.suggestions {
		if status == "" || suggestion.Status == status {
			suggestions = append(suggestions, suggestion)
		}
	}

	// Sort by UpdatedAt descending, then by ID descending for determinism.
	sort.Slice(suggestions, func(i, j int) bool {
		if !suggestions[i].UpdatedAt.Equal(suggestions[j].UpdatedAt) {
			return suggestions[i].UpdatedAt.After(suggestions[j].UpdatedAt)
		}
		return suggestions[i].ID > suggestions[j].ID
	})

	return suggestions, nil
}

// SuggestionFingerprints returns all unique fingerprints ever stored, regardless of status.
func (r *Repository) SuggestionFingerprints(ctx context.Context) (map[string]struct{}, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	prints := make(map[string]struct{})
	for _, suggestion := range r.suggestions {
		prints[suggestion.Fingerprint] = struct{}{}
	}
	return prints, nil
}

// Verify interface compliance.
var _ todoapp.Repository = (*Repository)(nil)
