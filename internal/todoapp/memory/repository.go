package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/pax-beehive/pax-nexus/internal/todoapp"
)

// Repository is the in-memory twin of the Postgres adapter, for domain tests.
// Each scope gets its own isolated set of todos and suggestions, mirroring
// the scope_id-per-row isolation the Postgres adapter enforces via SQL.
type Repository struct {
	mu          sync.Mutex
	todos       map[string]map[string]todoapp.Todo
	suggestions map[string]map[string]todoapp.Suggestion
}

// NewRepository creates a new in-memory repository.
func NewRepository() *Repository {
	return &Repository{
		todos:       map[string]map[string]todoapp.Todo{},
		suggestions: map[string]map[string]todoapp.Suggestion{},
	}
}

// SaveTodo saves or updates a todo by ID within scopeID.
func (r *Repository) SaveTodo(ctx context.Context, scopeID string, todo todoapp.Todo) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.todos[scopeID] == nil {
		r.todos[scopeID] = map[string]todoapp.Todo{}
	}
	r.todos[scopeID][todo.ID] = todo
	return nil
}

// TodoByID retrieves a todo by ID within scopeID, returning ErrNotFound if not present.
func (r *Repository) TodoByID(ctx context.Context, scopeID string, todoID string) (todoapp.Todo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	todo, ok := r.todos[scopeID][todoID]
	if !ok {
		return todoapp.Todo{}, todoapp.ErrNotFound
	}
	return todo, nil
}

// ListTodos returns all todos within scopeID matching the status (empty string = all), sorted by UpdatedAt desc, then ID desc.
func (r *Repository) ListTodos(ctx context.Context, scopeID string, status todoapp.TodoStatus) ([]todoapp.Todo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var todos []todoapp.Todo
	for _, todo := range r.todos[scopeID] {
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

// SaveSuggestion saves or updates a suggestion by ID within scopeID.
func (r *Repository) SaveSuggestion(ctx context.Context, scopeID string, suggestion todoapp.Suggestion) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.suggestions[scopeID] == nil {
		r.suggestions[scopeID] = map[string]todoapp.Suggestion{}
	}
	r.suggestions[scopeID][suggestion.ID] = suggestion
	return nil
}

// SuggestionByID retrieves a suggestion by ID within scopeID, returning ErrNotFound if not present.
func (r *Repository) SuggestionByID(ctx context.Context, scopeID string, suggestionID string) (todoapp.Suggestion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	suggestion, ok := r.suggestions[scopeID][suggestionID]
	if !ok {
		return todoapp.Suggestion{}, todoapp.ErrNotFound
	}
	return suggestion, nil
}

// ListSuggestions returns all suggestions within scopeID matching the status (empty string = all), sorted by UpdatedAt desc, then ID desc.
func (r *Repository) ListSuggestions(ctx context.Context, scopeID string, status todoapp.SuggestionStatus) ([]todoapp.Suggestion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var suggestions []todoapp.Suggestion
	for _, suggestion := range r.suggestions[scopeID] {
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

// SuggestionFingerprints returns all unique fingerprints ever stored within scopeID, regardless of status.
func (r *Repository) SuggestionFingerprints(ctx context.Context, scopeID string) (map[string]struct{}, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	prints := make(map[string]struct{})
	for _, suggestion := range r.suggestions[scopeID] {
		prints[suggestion.Fingerprint] = struct{}{}
	}
	return prints, nil
}

// Verify interface compliance.
var _ todoapp.Repository = (*Repository)(nil)
