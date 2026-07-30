// Package postgres provides the Postgres-backed adapter for the todoapp
// Repository port: each domain record is marshalled whole into a JSONB
// payload column, with status/fingerprint mirrored into plain columns for
// filtering and ordering.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/todoapp"
)

// Repository is the Postgres-backed implementation of todoapp.Repository.
type Repository struct {
	pool    *pgxpool.Pool
	scopeID string
}

// NewRepository creates a Postgres-backed todoapp repository scoped to scopeID.
func NewRepository(ctx context.Context, pool *pgxpool.Pool, scopeID string) (*Repository, error) {
	if pool == nil || scopeID == "" {
		return nil, fmt.Errorf("create todo app postgres repository: pool and scope are required")
	}
	return &Repository{pool: pool, scopeID: scopeID}, nil
}

// SaveTodo inserts or updates a todo by ID.
func (r *Repository) SaveTodo(ctx context.Context, todo todoapp.Todo) error {
	payload, err := json.Marshal(todo)
	if err != nil {
		return fmt.Errorf("marshal todo %q: %w", todo.ID, err)
	}
	if _, err := r.pool.Exec(ctx, `
INSERT INTO todoapp_todos (scope_id, todo_id, status, payload, updated_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (scope_id, todo_id)
DO UPDATE SET status = EXCLUDED.status, payload = EXCLUDED.payload, updated_at = EXCLUDED.updated_at`,
		r.scopeID, todo.ID, string(todo.Status), payload, todo.UpdatedAt,
	); err != nil {
		return fmt.Errorf("save todo %q: %w", todo.ID, err)
	}
	return nil
}

// TodoByID retrieves a todo by ID, returning todoapp.ErrNotFound if not present.
func (r *Repository) TodoByID(ctx context.Context, todoID string) (todoapp.Todo, error) {
	var payload []byte
	err := r.pool.QueryRow(ctx, `
SELECT payload FROM todoapp_todos WHERE scope_id = $1 AND todo_id = $2`,
		r.scopeID, todoID,
	).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return todoapp.Todo{}, todoapp.ErrNotFound
	}
	if err != nil {
		return todoapp.Todo{}, fmt.Errorf("load todo %q: %w", todoID, err)
	}
	var todo todoapp.Todo
	if err := json.Unmarshal(payload, &todo); err != nil {
		return todoapp.Todo{}, fmt.Errorf("unmarshal todo %q: %w", todoID, err)
	}
	return todo, nil
}

// ListTodos returns todos matching status (empty string = all), ordered by
// UpdatedAt descending, then ID descending.
func (r *Repository) ListTodos(ctx context.Context, status todoapp.TodoStatus) ([]todoapp.Todo, error) {
	rows, err := r.pool.Query(ctx, `
SELECT payload FROM todoapp_todos
WHERE scope_id = $1 AND ($2 = '' OR status = $2)
ORDER BY updated_at DESC, todo_id DESC`,
		r.scopeID, string(status),
	)
	if err != nil {
		return nil, fmt.Errorf("list todos: %w", err)
	}
	defer rows.Close()
	var todos []todoapp.Todo
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("scan todo row: %w", err)
		}
		var todo todoapp.Todo
		if err := json.Unmarshal(payload, &todo); err != nil {
			return nil, fmt.Errorf("unmarshal todo row: %w", err)
		}
		todos = append(todos, todo)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list todos: %w", err)
	}
	return todos, nil
}

// SaveSuggestion inserts or updates a suggestion by ID.
func (r *Repository) SaveSuggestion(ctx context.Context, suggestion todoapp.Suggestion) error {
	payload, err := json.Marshal(suggestion)
	if err != nil {
		return fmt.Errorf("marshal suggestion %q: %w", suggestion.ID, err)
	}
	if _, err := r.pool.Exec(ctx, `
INSERT INTO todoapp_suggestions (scope_id, suggestion_id, fingerprint, status, payload, updated_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (scope_id, suggestion_id)
DO UPDATE SET fingerprint = EXCLUDED.fingerprint, status = EXCLUDED.status,
    payload = EXCLUDED.payload, updated_at = EXCLUDED.updated_at`,
		r.scopeID, suggestion.ID, suggestion.Fingerprint, string(suggestion.Status), payload, suggestion.UpdatedAt,
	); err != nil {
		return fmt.Errorf("save suggestion %q: %w", suggestion.ID, err)
	}
	return nil
}

// SuggestionByID retrieves a suggestion by ID, returning todoapp.ErrNotFound if not present.
func (r *Repository) SuggestionByID(ctx context.Context, suggestionID string) (todoapp.Suggestion, error) {
	var payload []byte
	err := r.pool.QueryRow(ctx, `
SELECT payload FROM todoapp_suggestions WHERE scope_id = $1 AND suggestion_id = $2`,
		r.scopeID, suggestionID,
	).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return todoapp.Suggestion{}, todoapp.ErrNotFound
	}
	if err != nil {
		return todoapp.Suggestion{}, fmt.Errorf("load suggestion %q: %w", suggestionID, err)
	}
	var suggestion todoapp.Suggestion
	if err := json.Unmarshal(payload, &suggestion); err != nil {
		return todoapp.Suggestion{}, fmt.Errorf("unmarshal suggestion %q: %w", suggestionID, err)
	}
	return suggestion, nil
}

// ListSuggestions returns suggestions matching status (empty string = all), ordered by
// UpdatedAt descending, then ID descending.
func (r *Repository) ListSuggestions(ctx context.Context, status todoapp.SuggestionStatus) ([]todoapp.Suggestion, error) {
	rows, err := r.pool.Query(ctx, `
SELECT payload FROM todoapp_suggestions
WHERE scope_id = $1 AND ($2 = '' OR status = $2)
ORDER BY updated_at DESC, suggestion_id DESC`,
		r.scopeID, string(status),
	)
	if err != nil {
		return nil, fmt.Errorf("list suggestions: %w", err)
	}
	defer rows.Close()
	var suggestions []todoapp.Suggestion
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("scan suggestion row: %w", err)
		}
		var suggestion todoapp.Suggestion
		if err := json.Unmarshal(payload, &suggestion); err != nil {
			return nil, fmt.Errorf("unmarshal suggestion row: %w", err)
		}
		suggestions = append(suggestions, suggestion)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list suggestions: %w", err)
	}
	return suggestions, nil
}

// SuggestionFingerprints returns the set of all suggestion fingerprints ever stored,
// regardless of status.
func (r *Repository) SuggestionFingerprints(ctx context.Context) (map[string]struct{}, error) {
	rows, err := r.pool.Query(ctx, `
SELECT fingerprint FROM todoapp_suggestions WHERE scope_id = $1`, r.scopeID)
	if err != nil {
		return nil, fmt.Errorf("list suggestion fingerprints: %w", err)
	}
	defer rows.Close()
	fingerprints := make(map[string]struct{})
	for rows.Next() {
		var fingerprint string
		if err := rows.Scan(&fingerprint); err != nil {
			return nil, fmt.Errorf("scan suggestion fingerprint: %w", err)
		}
		fingerprints[fingerprint] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list suggestion fingerprints: %w", err)
	}
	return fingerprints, nil
}

var _ todoapp.Repository = (*Repository)(nil)
