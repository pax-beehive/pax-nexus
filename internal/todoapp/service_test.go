package todoapp_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/todoapp"
	"github.com/stretchr/testify/suite"
)

type fakeReporter struct {
	events []todoapp.ReportEvent
	err    error
}

func (f *fakeReporter) Report(_ context.Context, _ string, event todoapp.ReportEvent) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, event)
	return nil
}

type fakeNotes struct{ items []todoapp.ActionItem }

func (f *fakeNotes) ListOpenActionItems(context.Context, string, int) ([]todoapp.ActionItem, error) {
	return f.items, nil
}

type ServiceSuite struct {
	suite.Suite
	repo     *fakeRepository
	notes    *fakeNotes
	reporter *fakeReporter
	service  *todoapp.Service
	ctx      context.Context
	nextID   int
	clock    func() time.Time
}

// fakeRepository implements todoapp.Repository for testing
type fakeRepository struct {
	todos       map[string]todoapp.Todo
	suggestions map[string]todoapp.Suggestion
}

func (f *fakeRepository) SaveTodo(_ context.Context, _ string, todo todoapp.Todo) error {
	f.todos[todo.ID] = todo
	return nil
}

func (f *fakeRepository) TodoByID(_ context.Context, _ string, todoID string) (todoapp.Todo, error) {
	todo, ok := f.todos[todoID]
	if !ok {
		return todoapp.Todo{}, todoapp.ErrNotFound
	}
	return todo, nil
}

func (f *fakeRepository) ListTodos(_ context.Context, _ string, status todoapp.TodoStatus) ([]todoapp.Todo, error) {
	var result []todoapp.Todo
	for _, todo := range f.todos {
		if status == "" || todo.Status == status {
			result = append(result, todo)
		}
	}
	// Sort deterministically: UpdatedAt descending, then ID descending
	sort.Slice(result, func(i, j int) bool {
		if result[i].UpdatedAt != result[j].UpdatedAt {
			return result[i].UpdatedAt.After(result[j].UpdatedAt)
		}
		return result[i].ID > result[j].ID
	})
	return result, nil
}

func (f *fakeRepository) SaveSuggestion(_ context.Context, _ string, suggestion todoapp.Suggestion) error {
	f.suggestions[suggestion.ID] = suggestion
	return nil
}

func (f *fakeRepository) SuggestionByID(_ context.Context, _ string, suggestionID string) (todoapp.Suggestion, error) {
	suggestion, ok := f.suggestions[suggestionID]
	if !ok {
		return todoapp.Suggestion{}, todoapp.ErrNotFound
	}
	return suggestion, nil
}

func (f *fakeRepository) ListSuggestions(_ context.Context, _ string, status todoapp.SuggestionStatus) ([]todoapp.Suggestion, error) {
	var result []todoapp.Suggestion
	for _, suggestion := range f.suggestions {
		if status == "" || suggestion.Status == status {
			result = append(result, suggestion)
		}
	}
	// Sort deterministically: UpdatedAt descending, then ID descending
	sort.Slice(result, func(i, j int) bool {
		if result[i].UpdatedAt != result[j].UpdatedAt {
			return result[i].UpdatedAt.After(result[j].UpdatedAt)
		}
		return result[i].ID > result[j].ID
	})
	return result, nil
}

func (f *fakeRepository) SuggestionFingerprints(_ context.Context, _ string) (map[string]struct{}, error) {
	result := make(map[string]struct{})
	for _, suggestion := range f.suggestions {
		result[suggestion.Fingerprint] = struct{}{}
	}
	return result, nil
}

func TestServiceSuite(t *testing.T) { suite.Run(t, new(ServiceSuite)) }

func (s *ServiceSuite) SetupTest() {
	s.repo = &fakeRepository{
		todos:       make(map[string]todoapp.Todo),
		suggestions: make(map[string]todoapp.Suggestion),
	}
	s.notes = &fakeNotes{items: []todoapp.ActionItem{}}
	s.reporter = &fakeReporter{events: []todoapp.ReportEvent{}}
	s.ctx = context.Background()
	s.nextID = 0
	s.clock = func() time.Time { return time.Unix(1000, 0).UTC() }

	newID := func() string {
		id := s.nextID
		s.nextID++
		return string(rune(int('a') + id))
	}

	service, err := todoapp.NewService(todoapp.ServiceConfig{
		Repository: s.repo,
		Notes:      s.notes,
		Reporter:   s.reporter,
		Clock:      s.clock,
		NewID:      newID,
	})
	s.Require().NoError(err)
	s.service = service
}

func (s *ServiceSuite) TestCreateTodoValidatesInput() {
	cases := []struct {
		name      string
		userID    string
		title     string
		body      string
		wantError bool
	}{
		{name: "blank title", userID: "user-1", title: "", body: "body", wantError: true},
		{name: "blank user", userID: "", title: "title", body: "body", wantError: true},
		{name: "valid", userID: "user-1", title: "Title", body: "body", wantError: false},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			todo, err := s.service.CreateTodo(s.ctx, "local-team", tc.userID, tc.title, tc.body)
			if tc.wantError {
				s.Require().ErrorIs(err, todoapp.ErrInvalidInput)
			} else {
				s.Require().NoError(err)
				s.Require().Equal(tc.title, todo.Title)
				s.Require().Equal(tc.body, todo.Body)
				s.Require().Equal(tc.userID, todo.CreatedBy)
				s.Require().Equal(todoapp.TodoOpen, todo.Status)
				s.Require().Equal(todoapp.TodoSourceManual, todo.Source)

				// Verify persisted in repo
				loaded, err := s.repo.TodoByID(s.ctx, "local-team", todo.ID)
				s.Require().NoError(err)
				s.Require().Equal(todo, loaded)
			}
		})
	}
}

func (s *ServiceSuite) TestCompleteTodoEmitsReportEvent() {
	// Create a todo
	todo, err := s.service.CreateTodo(s.ctx, "local-team", "user-1", "Test Title", "Test body")
	s.Require().NoError(err)

	// Complete it
	completed, err := s.service.CompleteTodo(s.ctx, "local-team", "user-1", todo.ID)
	s.Require().NoError(err)

	// Verify status is done
	s.Require().Equal(todoapp.TodoDone, completed.Status)

	// Verify one event was emitted
	s.Require().Len(s.reporter.events, 1)
	event := s.reporter.events[0]
	s.Require().Equal(todoapp.EventTodoCompleted, event.Type)
	s.Require().Equal("user-1", event.UserID)
	s.Require().Equal(todo.ID, event.TodoID)
	s.Require().NotEmpty(event.Summary)
	s.Require().Contains(event.Summary, "Test Title")

	// Verify repo state
	loaded, err := s.repo.TodoByID(s.ctx, "local-team", todo.ID)
	s.Require().NoError(err)
	s.Require().Equal(todoapp.TodoDone, loaded.Status)
}

func (s *ServiceSuite) TestCompleteTodoIsIdempotent() {
	// Create a todo
	todo, err := s.service.CreateTodo(s.ctx, "local-team", "user-1", "Test Title", "Test body")
	s.Require().NoError(err)

	// Complete it twice
	_, err = s.service.CompleteTodo(s.ctx, "local-team", "user-1", todo.ID)
	s.Require().NoError(err)

	_, err = s.service.CompleteTodo(s.ctx, "local-team", "user-1", todo.ID)
	s.Require().NoError(err)

	// Verify only one event was emitted
	s.Require().Len(s.reporter.events, 1)
}

func (s *ServiceSuite) TestCompleteTodoSurvivesReportFailure() {
	// Create a todo
	todo, err := s.service.CreateTodo(s.ctx, "local-team", "user-1", "Test Title", "Test body")
	s.Require().NoError(err)

	// Set reporter to fail
	s.reporter.err = errors.New("report failed")

	// Complete should still succeed
	completed, err := s.service.CompleteTodo(s.ctx, "local-team", "user-1", todo.ID)
	s.Require().NoError(err)
	s.Require().Equal(todoapp.TodoDone, completed.Status)

	// Verify repo state is done
	loaded, err := s.repo.TodoByID(s.ctx, "local-team", todo.ID)
	s.Require().NoError(err)
	s.Require().Equal(todoapp.TodoDone, loaded.Status)
}

func (s *ServiceSuite) TestCompleteTodoUnknownIDReturnsNotFound() {
	_, err := s.service.CompleteTodo(s.ctx, "local-team", "user-1", "unknown-id")
	s.Require().ErrorIs(err, todoapp.ErrNotFound)
}

type scriptedRewriter struct{ prefix string }

func (r scriptedRewriter) Rewrite(_ context.Context, item todoapp.ActionItem) (string, string, error) {
	return r.prefix + item.Subject, "rewritten: " + item.Body, nil
}

func (s *ServiceSuite) TestRefreshCreatesPendingSuggestionsWithCitation() {
	rewriter := scriptedRewriter{prefix: "[SUGGESTED] "}
	service, err := todoapp.NewService(todoapp.ServiceConfig{
		Repository: s.repo,
		Notes:      s.notes,
		Rewriter:   rewriter,
		Reporter:   s.reporter,
		Clock:      s.clock,
		NewID: func() string {
			id := s.nextID
			s.nextID++
			return string(rune(int('a') + id))
		},
	})
	s.Require().NoError(err)

	s.notes.items = []todoapp.ActionItem{
		{NoteID: "note-1", Kind: "action", Subject: "Fix bug", Body: "Critical"},
		{NoteID: "note-2", Kind: "followup", Subject: "Review PR", Body: "Urgent"},
	}

	count, err := service.RefreshSuggestions(s.ctx, "local-team")
	s.Require().NoError(err)
	s.Require().Equal(2, count)

	// Verify both suggestions are created as pending
	suggestions, err := s.repo.ListSuggestions(s.ctx, "local-team", todoapp.SuggestionPending)
	s.Require().NoError(err)
	s.Require().Len(suggestions, 2)

	// Map suggestions by NoteID for order-independent verification
	suggestionsByNoteID := make(map[string]todoapp.Suggestion)
	for _, sugg := range suggestions {
		suggestionsByNoteID[sugg.NoteID] = sugg
	}

	// Verify both pending with correct data
	for _, sugg := range suggestions {
		s.Require().Equal(todoapp.SuggestionPending, sugg.Status)
		s.Require().NotEmpty(sugg.ID)
	}

	// Verify note-1 suggestion
	sugg1 := suggestionsByNoteID["note-1"]
	s.Require().Equal("note-1", sugg1.Fingerprint)
	s.Require().Equal("action", sugg1.Kind)
	s.Require().Equal("[SUGGESTED] Fix bug", sugg1.Title)
	s.Require().Equal("rewritten: Critical", sugg1.Body)

	// Verify note-2 suggestion
	sugg2 := suggestionsByNoteID["note-2"]
	s.Require().Equal("note-2", sugg2.Fingerprint)
	s.Require().Equal("followup", sugg2.Kind)
	s.Require().Equal("[SUGGESTED] Review PR", sugg2.Title)
	s.Require().Equal("rewritten: Urgent", sugg2.Body)
}

func (s *ServiceSuite) TestRefreshDeduplicatesByFingerprint() {
	rewriter := scriptedRewriter{prefix: "[SUGGESTED] "}
	service, err := todoapp.NewService(todoapp.ServiceConfig{
		Repository: s.repo,
		Notes:      s.notes,
		Rewriter:   rewriter,
		Reporter:   s.reporter,
		Clock:      s.clock,
		NewID: func() string {
			id := s.nextID
			s.nextID++
			return string(rune(int('a') + id))
		},
	})
	s.Require().NoError(err)

	s.notes.items = []todoapp.ActionItem{
		{NoteID: "note-1", Kind: "action", Subject: "Fix bug", Body: "Critical"},
	}

	// First refresh
	count1, err := service.RefreshSuggestions(s.ctx, "local-team")
	s.Require().NoError(err)
	s.Require().Equal(1, count1)

	// Second refresh with same items
	count2, err := service.RefreshSuggestions(s.ctx, "local-team")
	s.Require().NoError(err)
	s.Require().Equal(0, count2)

	// Verify still only one suggestion
	suggestions, err := s.repo.ListSuggestions(s.ctx, "local-team", "")
	s.Require().NoError(err)
	s.Require().Len(suggestions, 1)
}

func (s *ServiceSuite) TestRefreshSkipsDismissedForever() {
	rewriter := scriptedRewriter{prefix: "[SUGGESTED] "}
	service, err := todoapp.NewService(todoapp.ServiceConfig{
		Repository: s.repo,
		Notes:      s.notes,
		Rewriter:   rewriter,
		Reporter:   s.reporter,
		Clock:      s.clock,
		NewID: func() string {
			id := s.nextID
			s.nextID++
			return string(rune(int('a') + id))
		},
	})
	s.Require().NoError(err)

	s.notes.items = []todoapp.ActionItem{
		{NoteID: "note-1", Kind: "action", Subject: "Fix bug", Body: "Critical"},
	}

	// First refresh
	count1, err := service.RefreshSuggestions(s.ctx, "local-team")
	s.Require().NoError(err)
	s.Require().Equal(1, count1)

	// Dismiss the suggestion
	suggestions, err := s.repo.ListSuggestions(s.ctx, "local-team", todoapp.SuggestionPending)
	s.Require().NoError(err)
	s.Require().Len(suggestions, 1)
	sugg := suggestions[0]

	err = service.DismissSuggestion(s.ctx, "local-team", "user-1", sugg.ID)
	s.Require().NoError(err)

	// Second refresh with same items
	count2, err := service.RefreshSuggestions(s.ctx, "local-team")
	s.Require().NoError(err)
	s.Require().Equal(0, count2)

	// Verify still only one suggestion (dismissed)
	allSuggestions, err := s.repo.ListSuggestions(s.ctx, "local-team", "")
	s.Require().NoError(err)
	s.Require().Len(allSuggestions, 1)
	s.Require().Equal(todoapp.SuggestionDismissed, allSuggestions[0].Status)
}

func (s *ServiceSuite) TestRefreshWithoutRewriterCopiesVerbatim() {
	service, err := todoapp.NewService(todoapp.ServiceConfig{
		Repository: s.repo,
		Notes:      s.notes,
		Rewriter:   nil, // No rewriter
		Reporter:   s.reporter,
		Clock:      s.clock,
		NewID: func() string {
			id := s.nextID
			s.nextID++
			return string(rune(int('a') + id))
		},
	})
	s.Require().NoError(err)

	s.notes.items = []todoapp.ActionItem{
		{NoteID: "note-1", Kind: "action", Subject: "Original Title", Body: "Original Body"},
	}

	count, err := service.RefreshSuggestions(s.ctx, "local-team")
	s.Require().NoError(err)
	s.Require().Equal(1, count)

	suggestions, err := s.repo.ListSuggestions(s.ctx, "local-team", todoapp.SuggestionPending)
	s.Require().NoError(err)
	s.Require().Len(suggestions, 1)

	// Verify verbatim copy
	s.Require().Equal("Original Title", suggestions[0].Title)
	s.Require().Equal("Original Body", suggestions[0].Body)
}

func (s *ServiceSuite) TestAcceptSuggestionCreatesTodoAndReports() {
	rewriter := scriptedRewriter{prefix: "[SUGGESTED] "}
	service, err := todoapp.NewService(todoapp.ServiceConfig{
		Repository: s.repo,
		Notes:      s.notes,
		Rewriter:   rewriter,
		Reporter:   s.reporter,
		Clock:      s.clock,
		NewID: func() string {
			id := s.nextID
			s.nextID++
			return string(rune(int('a') + id))
		},
	})
	s.Require().NoError(err)

	s.notes.items = []todoapp.ActionItem{
		{NoteID: "note-1", Kind: "action", Subject: "Fix bug", Body: "Critical"},
	}

	// Create a suggestion
	count, err := service.RefreshSuggestions(s.ctx, "local-team")
	s.Require().NoError(err)
	s.Require().Equal(1, count)

	suggestions, err := s.repo.ListSuggestions(s.ctx, "local-team", todoapp.SuggestionPending)
	s.Require().NoError(err)
	s.Require().Len(suggestions, 1)
	sugg := suggestions[0]

	// Accept the suggestion
	todo, err := service.AcceptSuggestion(s.ctx, "local-team", "user-1", sugg.ID)
	s.Require().NoError(err)

	// Verify todo is created
	s.Require().Equal(todoapp.TodoOpen, todo.Status)
	s.Require().Equal(todoapp.TodoSourceSuggestion, todo.Source)
	s.Require().Equal(sugg.ID, todo.SuggestionID)
	s.Require().Equal("note-1", todo.NoteID)
	s.Require().Equal("[SUGGESTED] Fix bug", todo.Title)
	s.Require().Equal("rewritten: Critical", todo.Body)
	s.Require().Equal("user-1", todo.CreatedBy)

	// Verify suggestion is marked accepted
	acceptedSugg, err := s.repo.SuggestionByID(s.ctx, "local-team", sugg.ID)
	s.Require().NoError(err)
	s.Require().Equal(todoapp.SuggestionAccepted, acceptedSugg.Status)

	// Verify event was reported
	s.Require().Len(s.reporter.events, 1)
	event := s.reporter.events[0]
	s.Require().Equal(todoapp.EventSuggestionAccepted, event.Type)
	s.Require().Equal("user-1", event.UserID)
	s.Require().Equal(sugg.ID, event.SuggestionID)
	s.Require().Equal("note-1", event.NoteID)
	s.Require().Equal(fmt.Sprintf("User accepted suggested todo %q from team memory.", "[SUGGESTED] Fix bug"), event.Summary)
}

func (s *ServiceSuite) TestAcceptRejectsNonPending() {
	rewriter := scriptedRewriter{prefix: "[SUGGESTED] "}
	service, err := todoapp.NewService(todoapp.ServiceConfig{
		Repository: s.repo,
		Notes:      s.notes,
		Rewriter:   rewriter,
		Reporter:   s.reporter,
		Clock:      s.clock,
		NewID: func() string {
			id := s.nextID
			s.nextID++
			return string(rune(int('a') + id))
		},
	})
	s.Require().NoError(err)

	s.notes.items = []todoapp.ActionItem{
		{NoteID: "note-1", Kind: "action", Subject: "Fix bug", Body: "Critical"},
	}

	// Create a suggestion
	count, err := service.RefreshSuggestions(s.ctx, "local-team")
	s.Require().NoError(err)
	s.Require().Equal(1, count)

	suggestions, err := s.repo.ListSuggestions(s.ctx, "local-team", todoapp.SuggestionPending)
	s.Require().NoError(err)
	s.Require().Len(suggestions, 1)
	sugg := suggestions[0]

	// Accept it first time
	_, err = service.AcceptSuggestion(s.ctx, "local-team", "user-1", sugg.ID)
	s.Require().NoError(err)

	// Try to accept again
	_, err = service.AcceptSuggestion(s.ctx, "local-team", "user-1", sugg.ID)
	s.Require().Error(err)
	s.Require().ErrorIs(err, todoapp.ErrInvalidTransition)
}

func (s *ServiceSuite) TestDismissReports() {
	rewriter := scriptedRewriter{prefix: "[SUGGESTED] "}
	service, err := todoapp.NewService(todoapp.ServiceConfig{
		Repository: s.repo,
		Notes:      s.notes,
		Rewriter:   rewriter,
		Reporter:   s.reporter,
		Clock:      s.clock,
		NewID: func() string {
			id := s.nextID
			s.nextID++
			return string(rune(int('a') + id))
		},
	})
	s.Require().NoError(err)

	s.notes.items = []todoapp.ActionItem{
		{NoteID: "note-1", Kind: "action", Subject: "Fix bug", Body: "Critical"},
	}

	// Create a suggestion
	count, err := service.RefreshSuggestions(s.ctx, "local-team")
	s.Require().NoError(err)
	s.Require().Equal(1, count)

	suggestions, err := s.repo.ListSuggestions(s.ctx, "local-team", todoapp.SuggestionPending)
	s.Require().NoError(err)
	s.Require().Len(suggestions, 1)
	sugg := suggestions[0]

	// Dismiss the suggestion
	err = service.DismissSuggestion(s.ctx, "local-team", "user-1", sugg.ID)
	s.Require().NoError(err)

	// Verify suggestion is marked dismissed
	dismissedSugg, err := s.repo.SuggestionByID(s.ctx, "local-team", sugg.ID)
	s.Require().NoError(err)
	s.Require().Equal(todoapp.SuggestionDismissed, dismissedSugg.Status)

	// Verify event was reported
	s.Require().Len(s.reporter.events, 1)
	event := s.reporter.events[0]
	s.Require().Equal(todoapp.EventSuggestionDismissed, event.Type)
	s.Require().Equal("user-1", event.UserID)
	s.Require().Equal(sugg.ID, event.SuggestionID)
	s.Require().Equal("note-1", event.NoteID)
	s.Require().Equal(fmt.Sprintf("User dismissed suggested todo %q as not useful.", "[SUGGESTED] Fix bug"), event.Summary)
}

func (s *ServiceSuite) TestDismissRejectsNonPending() {
	rewriter := scriptedRewriter{prefix: "[SUGGESTED] "}
	service, err := todoapp.NewService(todoapp.ServiceConfig{
		Repository: s.repo,
		Notes:      s.notes,
		Rewriter:   rewriter,
		Reporter:   s.reporter,
		Clock:      s.clock,
		NewID: func() string {
			id := s.nextID
			s.nextID++
			return string(rune(int('a') + id))
		},
	})
	s.Require().NoError(err)

	s.notes.items = []todoapp.ActionItem{
		{NoteID: "note-1", Kind: "action", Subject: "Fix bug", Body: "Critical"},
	}

	// Create a suggestion
	count, err := service.RefreshSuggestions(s.ctx, "local-team")
	s.Require().NoError(err)
	s.Require().Equal(1, count)

	suggestions, err := s.repo.ListSuggestions(s.ctx, "local-team", todoapp.SuggestionPending)
	s.Require().NoError(err)
	s.Require().Len(suggestions, 1)
	sugg := suggestions[0]

	// Test 1: Dismiss twice → second call should return ErrInvalidTransition
	err = service.DismissSuggestion(s.ctx, "local-team", "user-1", sugg.ID)
	s.Require().NoError(err)
	s.Require().Len(s.reporter.events, 1)

	// Try to dismiss again
	err = service.DismissSuggestion(s.ctx, "local-team", "user-1", sugg.ID)
	s.Require().Error(err)
	s.Require().ErrorIs(err, todoapp.ErrInvalidTransition)
	// Verify no extra event was fired
	s.Require().Len(s.reporter.events, 1)

	// Test 2: Accept then dismiss → should return ErrInvalidTransition
	// Create another suggestion
	s.reporter.events = []todoapp.ReportEvent{}
	s.notes.items = []todoapp.ActionItem{
		{NoteID: "note-2", Kind: "action", Subject: "Review PR", Body: "Urgent"},
	}

	count, err = service.RefreshSuggestions(s.ctx, "local-team")
	s.Require().NoError(err)
	s.Require().Equal(1, count)

	suggestions, err = s.repo.ListSuggestions(s.ctx, "local-team", todoapp.SuggestionPending)
	s.Require().NoError(err)
	sugg2 := suggestions[0]

	// Accept the suggestion
	_, err = service.AcceptSuggestion(s.ctx, "local-team", "user-1", sugg2.ID)
	s.Require().NoError(err)
	s.Require().Len(s.reporter.events, 1) // One accept event

	// Try to dismiss after accepting
	err = service.DismissSuggestion(s.ctx, "local-team", "user-1", sugg2.ID)
	s.Require().Error(err)
	s.Require().ErrorIs(err, todoapp.ErrInvalidTransition)
	// Verify no dismiss event was fired
	s.Require().Len(s.reporter.events, 1)
}

func (s *ServiceSuite) TestPendingSuggestions() {
	rewriter := scriptedRewriter{prefix: "[SUGGESTED] "}
	service, err := todoapp.NewService(todoapp.ServiceConfig{
		Repository: s.repo,
		Notes:      s.notes,
		Rewriter:   rewriter,
		Reporter:   s.reporter,
		Clock:      s.clock,
		NewID: func() string {
			id := s.nextID
			s.nextID++
			return string(rune(int('a') + id))
		},
	})
	s.Require().NoError(err)

	s.notes.items = []todoapp.ActionItem{
		{NoteID: "note-1", Kind: "action", Subject: "Fix bug", Body: "Critical"},
		{NoteID: "note-2", Kind: "followup", Subject: "Review PR", Body: "Urgent"},
	}

	// Create suggestions
	count, err := service.RefreshSuggestions(s.ctx, "local-team")
	s.Require().NoError(err)
	s.Require().Equal(2, count)

	// Get pending suggestions
	pending, err := service.PendingSuggestions(s.ctx, "local-team")
	s.Require().NoError(err)
	s.Require().Len(pending, 2)

	for _, sugg := range pending {
		s.Require().Equal(todoapp.SuggestionPending, sugg.Status)
	}
}
