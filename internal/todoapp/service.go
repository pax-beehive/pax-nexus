package todoapp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

	// refreshLocks keeps RefreshSuggestions single-flight per scope; two
	// scopes must be able to refresh concurrently, while a second refresh
	// for the same scope fails fast with ErrRefreshInProgress.
	refreshLocks sync.Map // scopeID -> *sync.Mutex

	// completeLocks serializes CompleteTodo's read-check-write per scope so
	// concurrent completes of the same todo emit exactly one lake event.
	completeLocks sync.Map // scopeID -> *sync.Mutex
}

// scopeLock returns the per-scope mutex stored in locks for scopeID,
// creating it on first use.
func scopeLock(locks *sync.Map, scopeID string) *sync.Mutex {
	stored, _ := locks.LoadOrStore(scopeID, &sync.Mutex{})
	lock, ok := stored.(*sync.Mutex)
	if !ok {
		// Unreachable: the maps only ever store *sync.Mutex values.
		panic("todoapp: scope lock map contains a non-*sync.Mutex value")
	}
	return lock
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
	if strings.TrimSpace(scopeID) == "" || strings.TrimSpace(title) == "" || strings.TrimSpace(userID) == "" {
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
// It rejects a blank scopeID or userID with ErrInvalidInput.
// If reporting fails, it logs a warning but returns success.
func (s *Service) CompleteTodo(ctx context.Context, scopeID, userID, todoID string) (Todo, error) {
	if strings.TrimSpace(scopeID) == "" || strings.TrimSpace(userID) == "" {
		return Todo{}, fmt.Errorf("complete todo: %w", ErrInvalidInput)
	}

	todo, transitioned, err := s.completeTodoOnce(ctx, scopeID, todoID)
	if err != nil {
		return Todo{}, err
	}
	if !transitioned {
		// Already done: idempotent success without a second lake event.
		return todo, nil
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

// completeTodoOnce performs the open->done transition under the per-scope
// completion lock, so exactly one of any concurrent completes for the same
// todo observes the transition (and therefore reports the lake event). It
// returns transitioned=false when the todo was already done.
func (s *Service) completeTodoOnce(ctx context.Context, scopeID, todoID string) (Todo, bool, error) {
	lock := scopeLock(&s.completeLocks, scopeID)
	lock.Lock()
	defer lock.Unlock()

	todo, err := s.repo.TodoByID(ctx, scopeID, todoID)
	if err != nil {
		return Todo{}, false, err
	}
	if todo.Status == TodoDone {
		return todo, false, nil
	}

	todo.Status = TodoDone
	todo.UpdatedAt = s.clock()
	if err := s.repo.SaveTodo(ctx, scopeID, todo); err != nil {
		return Todo{}, false, err
	}
	return todo, true, nil
}

// ListTodos returns all todos with the given status.
// If status is empty, returns all todos; any value other than the known
// statuses is rejected with ErrInvalidInput.
func (s *Service) ListTodos(ctx context.Context, scopeID string, status TodoStatus) ([]Todo, error) {
	if strings.TrimSpace(scopeID) == "" {
		return nil, fmt.Errorf("list todos: %w", ErrInvalidInput)
	}
	if status != "" && status != TodoOpen && status != TodoDone {
		return nil, fmt.Errorf("list todos: unknown status %q: %w", status, ErrInvalidInput)
	}
	return s.repo.ListTodos(ctx, scopeID, status)
}

// RefreshSuggestions fetches open action items from the NoteDirectory,
// creates pending suggestions for new items (using fingerprint deduplication),
// and returns the count of newly created suggestions.
// Refreshes are single-flight per scope: a refresh can spend minutes inside
// LLM rewrites, so instead of queueing callers behind a held mutex (which
// would block a portal click behind the background sweep), a second refresh
// for the same scope fails fast with ErrRefreshInProgress while different
// scopes still refresh concurrently.
func (s *Service) RefreshSuggestions(ctx context.Context, scopeID string) (int, error) {
	if strings.TrimSpace(scopeID) == "" {
		return 0, fmt.Errorf("refresh suggestions: %w", ErrInvalidInput)
	}

	lock := scopeLock(&s.refreshLocks, scopeID)
	if !lock.TryLock() {
		return 0, fmt.Errorf("refresh suggestions for scope %s: %w", scopeID, ErrRefreshInProgress)
	}
	defer lock.Unlock()

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
	if strings.TrimSpace(scopeID) == "" {
		return nil, fmt.Errorf("pending suggestions: %w", ErrInvalidInput)
	}
	return s.repo.ListSuggestions(ctx, scopeID, SuggestionPending)
}

// AcceptSuggestion converts a pending suggestion into a todo.
// The suggestion must be in pending status, otherwise returns ErrInvalidTransition.
// It rejects a blank scopeID or userID with ErrInvalidInput.
// Creates a todo with source TodoSourceSuggestion and reports EventSuggestionAccepted.
// The accepted todo's ID is derived deterministically from the suggestion ID,
// so a retry after a partial failure (todo saved, suggestion update failed)
// converges on the same todo row instead of creating a duplicate.
// If reporting fails, logs a warning but succeeds.
func (s *Service) AcceptSuggestion(ctx context.Context, scopeID, userID, suggestionID string) (Todo, error) {
	if strings.TrimSpace(scopeID) == "" || strings.TrimSpace(userID) == "" {
		return Todo{}, fmt.Errorf("accept suggestion: %w", ErrInvalidInput)
	}

	// Get the suggestion
	suggestion, err := s.repo.SuggestionByID(ctx, scopeID, suggestionID)
	if err != nil {
		return Todo{}, err
	}

	// Verify it's pending
	if suggestion.Status != SuggestionPending {
		return Todo{}, fmt.Errorf("accept suggestion %s: %w", suggestionID, ErrInvalidTransition)
	}

	now := s.clock()
	todo, err := s.acceptedTodo(ctx, scopeID, userID, suggestion, now)
	if err != nil {
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

// acceptedTodo returns the todo an accept converges on: if a previous accept
// already persisted the todo but failed before updating the suggestion, the
// existing row is reused as-is (preserving any completion that happened in
// between); otherwise a fresh todo is created and saved under the
// deterministic accepted-todo ID.
func (s *Service) acceptedTodo(
	ctx context.Context,
	scopeID, userID string,
	suggestion Suggestion,
	now time.Time,
) (Todo, error) {
	todoID := acceptedTodoID(suggestion.ID)
	existing, err := s.repo.TodoByID(ctx, scopeID, todoID)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Todo{}, err
	}

	todo := Todo{
		ID:           todoID,
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
	if err := s.repo.SaveTodo(ctx, scopeID, todo); err != nil {
		return Todo{}, err
	}
	return todo, nil
}

// acceptedTodoID derives the accepted todo's ID deterministically from the
// suggestion ID: SaveTodo upserts by ID in both repository adapters, so a
// retried accept lands on the same todo row instead of minting a duplicate.
func acceptedTodoID(suggestionID string) string {
	digest := sha256.Sum256([]byte("todoapp:accepted-todo:" + suggestionID))
	return hex.EncodeToString(digest[:16])
}

// DismissSuggestion marks a pending suggestion as dismissed.
// The suggestion must be in pending status, otherwise returns ErrInvalidTransition.
// It rejects a blank scopeID or userID with ErrInvalidInput.
// Reports EventSuggestionDismissed.
// If reporting fails, logs a warning but succeeds.
func (s *Service) DismissSuggestion(ctx context.Context, scopeID, userID, suggestionID string) error {
	if strings.TrimSpace(scopeID) == "" || strings.TrimSpace(userID) == "" {
		return fmt.Errorf("dismiss suggestion: %w", ErrInvalidInput)
	}

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
