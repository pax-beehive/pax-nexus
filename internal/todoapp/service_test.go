package todoapp_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/todoapp"
	"github.com/stretchr/testify/suite"
)

type fakeReporter struct {
	events []todoapp.ReportEvent
	err    error
}

func (f *fakeReporter) Report(_ context.Context, event todoapp.ReportEvent) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, event)
	return nil
}

type fakeNotes struct{ items []todoapp.ActionItem }

func (f *fakeNotes) ListOpenActionItems(context.Context, int) ([]todoapp.ActionItem, error) {
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

func (f *fakeRepository) SaveTodo(_ context.Context, todo todoapp.Todo) error {
	f.todos[todo.ID] = todo
	return nil
}

func (f *fakeRepository) TodoByID(_ context.Context, todoID string) (todoapp.Todo, error) {
	todo, ok := f.todos[todoID]
	if !ok {
		return todoapp.Todo{}, todoapp.ErrNotFound
	}
	return todo, nil
}

func (f *fakeRepository) ListTodos(_ context.Context, status todoapp.TodoStatus) ([]todoapp.Todo, error) {
	var result []todoapp.Todo
	for _, todo := range f.todos {
		if status == "" || todo.Status == status {
			result = append(result, todo)
		}
	}
	return result, nil
}

func (f *fakeRepository) SaveSuggestion(_ context.Context, suggestion todoapp.Suggestion) error {
	f.suggestions[suggestion.ID] = suggestion
	return nil
}

func (f *fakeRepository) SuggestionByID(_ context.Context, suggestionID string) (todoapp.Suggestion, error) {
	suggestion, ok := f.suggestions[suggestionID]
	if !ok {
		return todoapp.Suggestion{}, todoapp.ErrNotFound
	}
	return suggestion, nil
}

func (f *fakeRepository) ListSuggestions(_ context.Context, status todoapp.SuggestionStatus) ([]todoapp.Suggestion, error) {
	var result []todoapp.Suggestion
	for _, suggestion := range f.suggestions {
		if status == "" || suggestion.Status == status {
			result = append(result, suggestion)
		}
	}
	return result, nil
}

func (f *fakeRepository) SuggestionFingerprints(_ context.Context) (map[string]struct{}, error) {
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
			todo, err := s.service.CreateTodo(s.ctx, tc.userID, tc.title, tc.body)
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
				loaded, err := s.repo.TodoByID(s.ctx, todo.ID)
				s.Require().NoError(err)
				s.Require().Equal(todo, loaded)
			}
		})
	}
}

func (s *ServiceSuite) TestCompleteTodoEmitsReportEvent() {
	// Create a todo
	todo, err := s.service.CreateTodo(s.ctx, "user-1", "Test Title", "Test body")
	s.Require().NoError(err)

	// Complete it
	completed, err := s.service.CompleteTodo(s.ctx, "user-1", todo.ID)
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
	loaded, err := s.repo.TodoByID(s.ctx, todo.ID)
	s.Require().NoError(err)
	s.Require().Equal(todoapp.TodoDone, loaded.Status)
}

func (s *ServiceSuite) TestCompleteTodoIsIdempotent() {
	// Create a todo
	todo, err := s.service.CreateTodo(s.ctx, "user-1", "Test Title", "Test body")
	s.Require().NoError(err)

	// Complete it twice
	_, err = s.service.CompleteTodo(s.ctx, "user-1", todo.ID)
	s.Require().NoError(err)

	_, err = s.service.CompleteTodo(s.ctx, "user-1", todo.ID)
	s.Require().NoError(err)

	// Verify only one event was emitted
	s.Require().Len(s.reporter.events, 1)
}

func (s *ServiceSuite) TestCompleteTodoSurvivesReportFailure() {
	// Create a todo
	todo, err := s.service.CreateTodo(s.ctx, "user-1", "Test Title", "Test body")
	s.Require().NoError(err)

	// Set reporter to fail
	s.reporter.err = errors.New("report failed")

	// Complete should still succeed
	completed, err := s.service.CompleteTodo(s.ctx, "user-1", todo.ID)
	s.Require().NoError(err)
	s.Require().Equal(todoapp.TodoDone, completed.Status)

	// Verify repo state is done
	loaded, err := s.repo.TodoByID(s.ctx, todo.ID)
	s.Require().NoError(err)
	s.Require().Equal(todoapp.TodoDone, loaded.Status)
}

func (s *ServiceSuite) TestCompleteTodoUnknownIDReturnsNotFound() {
	_, err := s.service.CompleteTodo(s.ctx, "user-1", "unknown-id")
	s.Require().ErrorIs(err, todoapp.ErrNotFound)
}
