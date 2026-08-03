package todoapp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
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
	mu       sync.Mutex // protects RefreshSuggestions
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
func (s *Service) CreateTodo(ctx context.Context, scopeID, userID, title, body string) (Todo, error) {
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

	if err := s.repo.SaveTodo(ctx, scopeID, todo); err != nil {
		return Todo{}, err
	}

	return todo, nil
}

// CompleteTodo marks a todo as done and reports the completion.
// If the todo is already done, it returns unchanged (idempotent).
// If the todo is not found, it returns ErrNotFound.
// If reporting fails, it logs a warning but returns success.
func (s *Service) CompleteTodo(ctx context.Context, scopeID, userID, todoID string) (Todo, error) {
	todo, err := s.repo.TodoByID(ctx, scopeID, todoID)
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

	if err := s.repo.SaveTodo(ctx, scopeID, todo); err != nil {
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

	if err := s.reporter.Report(ctx, scopeID, event); err != nil {
		s.logger.Warn("todo report failed", "error", err, "todo_id", todoID)
		// Report failure must not fail the call
	}

	return todo, nil
}

// ListTodos returns all todos with the given status.
// If status is empty, returns all todos.
func (s *Service) ListTodos(ctx context.Context, scopeID string, status TodoStatus) ([]Todo, error) {
	return s.repo.ListTodos(ctx, scopeID, status)
}

// RefreshSuggestions fetches open action items from the NoteDirectory,
// creates pending suggestions for new items (using fingerprint deduplication),
// and returns the count of newly created suggestions.
// Serialized with a sync.Mutex to prevent concurrent updates.
func (s *Service) RefreshSuggestions(ctx context.Context, scopeID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Load open action items from notes
	items, err := s.notes.ListOpenActionItems(ctx, scopeID, 50)
	if err != nil {
		return 0, fmt.Errorf("refresh suggestions: %w", err)
	}

	// Load existing fingerprints to avoid duplicates
	fingerprints, err := s.repo.SuggestionFingerprints(ctx, scopeID)
	if err != nil {
		return 0, fmt.Errorf("refresh suggestions: %w", err)
	}

	var created int
	now := s.clock()

	// Process each action item
	for _, item := range items {
		// Skip if fingerprint already exists
		if _, exists := fingerprints[item.NoteID]; exists {
			continue
		}

		// Get title and body: use Rewriter if available, else verbatim
		title := item.Subject
		body := item.Body

		if s.rewriter != nil {
			var err error
			title, body, err = s.rewriter.Rewrite(ctx, item)
			if err != nil {
				s.logger.Warn("rewriter failed", "error", err, "note_id", item.NoteID)
				continue
			}
		}

		// Create and save suggestion
		suggestion := Suggestion{
			ID:          s.newID(),
			Fingerprint: item.NoteID,
			NoteID:      item.NoteID,
			Kind:        item.Kind,
			Title:       title,
			Body:        body,
			Status:      SuggestionPending,
			CreatedAt:   now,
			UpdatedAt:   now,
		}

		if err := s.repo.SaveSuggestion(ctx, scopeID, suggestion); err != nil {
			s.logger.Warn("failed to save suggestion", "error", err, "note_id", item.NoteID)
			continue
		}

		created++
	}

	return created, nil
}

// PendingSuggestions returns all pending suggestions.
func (s *Service) PendingSuggestions(ctx context.Context, scopeID string) ([]Suggestion, error) {
	return s.repo.ListSuggestions(ctx, scopeID, SuggestionPending)
}

// AcceptSuggestion converts a pending suggestion into a todo.
// The suggestion must be in pending status, otherwise returns ErrInvalidTransition.
// Creates a todo with source TodoSourceSuggestion and reports EventSuggestionAccepted.
// If reporting fails, logs a warning but succeeds.
func (s *Service) AcceptSuggestion(ctx context.Context, scopeID, userID, suggestionID string) (Todo, error) {
	// Get the suggestion
	suggestion, err := s.repo.SuggestionByID(ctx, scopeID, suggestionID)
	if err != nil {
		return Todo{}, err
	}

	// Verify it's pending
	if suggestion.Status != SuggestionPending {
		return Todo{}, fmt.Errorf("accept suggestion %s: %w", suggestionID, ErrInvalidTransition)
	}

	// Create the todo
	now := s.clock()
	todo := Todo{
		ID:           s.newID(),
		Title:        suggestion.Title,
		Body:         suggestion.Body,
		Status:       TodoOpen,
		Source:       TodoSourceSuggestion,
		SuggestionID: suggestion.ID,
		NoteID:       suggestion.NoteID,
		CreatedBy:    userID,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	// Save the todo
	if err := s.repo.SaveTodo(ctx, scopeID, todo); err != nil {
		return Todo{}, err
	}

	// Mark suggestion as accepted
	suggestion.Status = SuggestionAccepted
	suggestion.UpdatedAt = now
	if err := s.repo.SaveSuggestion(ctx, scopeID, suggestion); err != nil {
		return Todo{}, err
	}

	// Report the event
	event := ReportEvent{
		Type:         EventSuggestionAccepted,
		UserID:       userID,
		SuggestionID: suggestion.ID,
		NoteID:       suggestion.NoteID,
		Summary:      fmt.Sprintf("User accepted suggested todo %q from team memory.", suggestion.Title),
		OccurredAt:   now,
	}

	if err := s.reporter.Report(ctx, scopeID, event); err != nil {
		s.logger.Warn("suggestion report failed", "error", err, "suggestion_id", suggestionID)
		// Report failure must not fail the call
	}

	return todo, nil
}

// DismissSuggestion marks a pending suggestion as dismissed.
// The suggestion must be in pending status, otherwise returns ErrInvalidTransition.
// Reports EventSuggestionDismissed.
// If reporting fails, logs a warning but succeeds.
func (s *Service) DismissSuggestion(ctx context.Context, scopeID, userID, suggestionID string) error {
	// Get the suggestion
	suggestion, err := s.repo.SuggestionByID(ctx, scopeID, suggestionID)
	if err != nil {
		return err
	}

	// Verify it's pending
	if suggestion.Status != SuggestionPending {
		return fmt.Errorf("dismiss suggestion %s: %w", suggestionID, ErrInvalidTransition)
	}

	// Mark as dismissed
	now := s.clock()
	suggestion.Status = SuggestionDismissed
	suggestion.UpdatedAt = now

	if err := s.repo.SaveSuggestion(ctx, scopeID, suggestion); err != nil {
		return err
	}

	// Report the event
	event := ReportEvent{
		Type:         EventSuggestionDismissed,
		UserID:       userID,
		SuggestionID: suggestion.ID,
		NoteID:       suggestion.NoteID,
		Summary:      fmt.Sprintf("User dismissed suggested todo %q as not useful.", suggestion.Title),
		OccurredAt:   now,
	}

	if err := s.reporter.Report(ctx, scopeID, event); err != nil {
		s.logger.Warn("suggestion report failed", "error", err, "suggestion_id", suggestionID)
		// Report failure must not fail the call
	}

	return nil
}

// defaultNewID generates a random 16-byte hex ID.
func defaultNewID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("id-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
