package todoapp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
)

// ServiceConfig holds the configuration for the Service.
type ServiceConfig struct {
	Repository Repository
	Notes      NoteDirectory
	Rewriter   Rewriter         // optional; nil means verbatim copy
	Reporter   Reporter         // required
	Logger     *slog.Logger     // optional; defaults to observability.DiscardLogger()
	Clock      func() time.Time // optional; defaults to time.Now().UTC()
	NewID      func() string    // optional; defaults to crypto/rand 16-byte hex
}

// Service handles todo CRUD and completion reporting.
type Service struct {
	repo     Repository
	notes    NoteDirectory
	rewriter Rewriter
	reporter Reporter
	logger   *slog.Logger
	clock    func() time.Time
	newID    func() string
}

// NewService creates a new Service with the given configuration.
// It returns an error if Repository, Notes, or Reporter are nil.
func NewService(config ServiceConfig) (*Service, error) {
	if config.Repository == nil {
		return nil, fmt.Errorf("new service: %w", ErrInvalidInput)
	}
	if config.Notes == nil {
		return nil, fmt.Errorf("new service: %w", ErrInvalidInput)
	}
	if config.Reporter == nil {
		return nil, fmt.Errorf("new service: %w", ErrInvalidInput)
	}

	logger := config.Logger
	if logger == nil {
		logger = observability.DiscardLogger()
	}

	clock := config.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}

	newID := config.NewID
	if newID == nil {
		newID = defaultNewID
	}

	return &Service{
		repo:     config.Repository,
		notes:    config.Notes,
		rewriter: config.Rewriter,
		reporter: config.Reporter,
		logger:   logger,
		clock:    clock,
		newID:    newID,
	}, nil
}

// CreateTodo creates a new todo with the given title and body.
// It rejects blank title or userID with ErrInvalidInput.
// Status is set to TodoOpen, source to TodoSourceManual.
func (s *Service) CreateTodo(ctx context.Context, userID, title, body string) (Todo, error) {
	if strings.TrimSpace(title) == "" || strings.TrimSpace(userID) == "" {
		return Todo{}, fmt.Errorf("create todo: %w", ErrInvalidInput)
	}

	now := s.clock()
	todo := Todo{
		ID:        s.newID(),
		Title:     title,
		Body:      body,
		Status:    TodoOpen,
		Source:    TodoSourceManual,
		CreatedBy: userID,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.repo.SaveTodo(ctx, todo); err != nil {
		return Todo{}, err
	}

	return todo, nil
}

// CompleteTodo marks a todo as done and reports the completion.
// If the todo is already done, it returns unchanged (idempotent).
// If the todo is not found, it returns ErrNotFound.
// If reporting fails, it logs a warning but returns success.
func (s *Service) CompleteTodo(ctx context.Context, userID, todoID string) (Todo, error) {
	todo, err := s.repo.TodoByID(ctx, todoID)
	if err != nil {
		return Todo{}, err
	}

	// If already done, return unchanged (idempotent)
	if todo.Status == TodoDone {
		return todo, nil
	}

	// Mark as done
	todo.Status = TodoDone
	todo.UpdatedAt = s.clock()

	if err := s.repo.SaveTodo(ctx, todo); err != nil {
		return Todo{}, err
	}

	// Report the completion
	event := ReportEvent{
		Type:       EventTodoCompleted,
		UserID:     userID,
		TodoID:     todoID,
		NoteID:     todo.NoteID,
		Summary:    fmt.Sprintf("User completed todo %q.", todo.Title),
		OccurredAt: s.clock(),
	}

	if err := s.reporter.Report(ctx, event); err != nil {
		s.logger.Warn("todo report failed", "error", err, "todo_id", todoID)
		// Report failure must not fail the call
	}

	return todo, nil
}

// ListTodos returns all todos with the given status.
// If status is empty, returns all todos.
func (s *Service) ListTodos(ctx context.Context, status TodoStatus) ([]Todo, error) {
	return s.repo.ListTodos(ctx, status)
}

// defaultNewID generates a random 16-byte hex ID.
func defaultNewID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("id-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
