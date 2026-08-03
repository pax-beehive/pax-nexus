package memory_test

import (
	"context"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/todoapp"
	"github.com/pax-beehive/pax-nexus/internal/todoapp/memory"
	"github.com/stretchr/testify/suite"
)

type RepositorySuite struct {
	suite.Suite
	repo *memory.Repository
	ctx  context.Context
}

func TestRepositorySuite(t *testing.T) { suite.Run(t, new(RepositorySuite)) }

func (s *RepositorySuite) SetupTest() {
	s.repo = memory.NewRepository()
	s.ctx = context.Background()
}

func (s *RepositorySuite) TestTodoRoundtripAndNotFound() {
	todo := todoapp.Todo{
		ID:        "t1",
		Title:     "Fix provider credential",
		Status:    todoapp.TodoOpen,
		Source:    todoapp.TodoSourceManual,
		CreatedBy: "user-1",
		CreatedAt: time.Unix(100, 0).UTC(),
		UpdatedAt: time.Unix(100, 0).UTC(),
	}
	s.Require().NoError(s.repo.SaveTodo(s.ctx, "local-team", todo))
	loaded, err := s.repo.TodoByID(s.ctx, "local-team", "t1")
	s.Require().NoError(err)
	s.Require().Equal(todo, loaded)
	_, err = s.repo.TodoByID(s.ctx, "local-team", "missing")
	s.Require().ErrorIs(err, todoapp.ErrNotFound)
}

func (s *RepositorySuite) TestListTodosFiltersByStatus() {
	s.Require().NoError(s.repo.SaveTodo(s.ctx, "local-team", todoapp.Todo{
		ID:        "t1",
		Title:     "a",
		Status:    todoapp.TodoOpen,
		UpdatedAt: time.Unix(200, 0),
	}))
	s.Require().NoError(s.repo.SaveTodo(s.ctx, "local-team", todoapp.Todo{
		ID:        "t2",
		Title:     "b",
		Status:    todoapp.TodoDone,
		UpdatedAt: time.Unix(300, 0),
	}))
	cases := []struct {
		name   string
		status todoapp.TodoStatus
		want   []string
	}{
		{name: "all newest first", status: "", want: []string{"t2", "t1"}},
		{name: "open only", status: todoapp.TodoOpen, want: []string{"t1"}},
		{name: "done only", status: todoapp.TodoDone, want: []string{"t2"}},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			listed, err := s.repo.ListTodos(s.ctx, "local-team", tc.status)
			s.Require().NoError(err)
			ids := make([]string, 0, len(listed))
			for _, item := range listed {
				ids = append(ids, item.ID)
			}
			s.Require().Equal(tc.want, ids)
		})
	}
}

func (s *RepositorySuite) TestTodoSaveUpserts() {
	original := todoapp.Todo{
		ID:        "t1",
		Title:     "Original",
		Status:    todoapp.TodoOpen,
		UpdatedAt: time.Unix(100, 0),
	}
	s.Require().NoError(s.repo.SaveTodo(s.ctx, "local-team", original))

	updated := todoapp.Todo{
		ID:        "t1",
		Title:     "Updated",
		Status:    todoapp.TodoDone,
		UpdatedAt: time.Unix(200, 0),
	}
	s.Require().NoError(s.repo.SaveTodo(s.ctx, "local-team", updated))

	loaded, err := s.repo.TodoByID(s.ctx, "local-team", "t1")
	s.Require().NoError(err)
	s.Require().Equal(updated, loaded)
}

func (s *RepositorySuite) TestTodoListOrderingByUpdatedAtAndID() {
	// Insert with same UpdatedAt to test tie-break by ID
	t := time.Unix(300, 0)
	s.Require().NoError(s.repo.SaveTodo(s.ctx, "local-team", todoapp.Todo{
		ID:        "t1",
		Title:     "a",
		UpdatedAt: t,
	}))
	s.Require().NoError(s.repo.SaveTodo(s.ctx, "local-team", todoapp.Todo{
		ID:        "t3",
		Title:     "b",
		UpdatedAt: t,
	}))
	s.Require().NoError(s.repo.SaveTodo(s.ctx, "local-team", todoapp.Todo{
		ID:        "t2",
		Title:     "c",
		UpdatedAt: t,
	}))

	listed, err := s.repo.ListTodos(s.ctx, "local-team", "")
	s.Require().NoError(err)
	ids := make([]string, len(listed))
	for i, item := range listed {
		ids[i] = item.ID
	}
	s.Require().Equal([]string{"t3", "t2", "t1"}, ids, "should sort by ID descending when UpdatedAt is equal")
}

func (s *RepositorySuite) TestTodoListOrderingPreservesSubsecondPrecision() {
	// Test that sorting preserves sub-second timestamp precision
	t1 := time.Unix(200, 0)
	t2 := time.Unix(200, 500_000_000) // 200.5 seconds
	s.Require().NoError(s.repo.SaveTodo(s.ctx, "local-team", todoapp.Todo{
		ID:        "t1",
		Title:     "older",
		UpdatedAt: t1,
	}))
	s.Require().NoError(s.repo.SaveTodo(s.ctx, "local-team", todoapp.Todo{
		ID:        "t2",
		Title:     "newer",
		UpdatedAt: t2,
	}))

	listed, err := s.repo.ListTodos(s.ctx, "local-team", "")
	s.Require().NoError(err)
	s.Require().Len(listed, 2)
	// t2 has later UpdatedAt (200.5s > 200.0s), should come first
	s.Require().Equal("t2", listed[0].ID)
	s.Require().Equal("t1", listed[1].ID)
}

func (s *RepositorySuite) TestSuggestionRoundtrip() {
	suggestion := todoapp.Suggestion{
		ID:          "s1",
		Fingerprint: "n1",
		NoteID:      "note-1",
		Kind:        "blocker",
		Title:       "Fix issue",
		Body:        "Action needed",
		Status:      todoapp.SuggestionPending,
		CreatedAt:   time.Unix(100, 0).UTC(),
		UpdatedAt:   time.Unix(100, 0).UTC(),
	}
	s.Require().NoError(s.repo.SaveSuggestion(s.ctx, "local-team", suggestion))
	loaded, err := s.repo.SuggestionByID(s.ctx, "local-team", "s1")
	s.Require().NoError(err)
	s.Require().Equal(suggestion, loaded)
	_, err = s.repo.SuggestionByID(s.ctx, "local-team", "missing")
	s.Require().ErrorIs(err, todoapp.ErrNotFound)
}

func (s *RepositorySuite) TestListSuggestionsFiltersByStatus() {
	s.Require().NoError(s.repo.SaveSuggestion(s.ctx, "local-team", todoapp.Suggestion{
		ID:        "s1",
		Status:    todoapp.SuggestionPending,
		UpdatedAt: time.Unix(200, 0),
	}))
	s.Require().NoError(s.repo.SaveSuggestion(s.ctx, "local-team", todoapp.Suggestion{
		ID:        "s2",
		Status:    todoapp.SuggestionAccepted,
		UpdatedAt: time.Unix(300, 0),
	}))
	s.Require().NoError(s.repo.SaveSuggestion(s.ctx, "local-team", todoapp.Suggestion{
		ID:        "s3",
		Status:    todoapp.SuggestionDismissed,
		UpdatedAt: time.Unix(250, 0),
	}))

	cases := []struct {
		name   string
		status todoapp.SuggestionStatus
		want   []string
	}{
		{name: "all newest first", status: "", want: []string{"s2", "s3", "s1"}},
		{name: "pending only", status: todoapp.SuggestionPending, want: []string{"s1"}},
		{name: "accepted only", status: todoapp.SuggestionAccepted, want: []string{"s2"}},
		{name: "dismissed only", status: todoapp.SuggestionDismissed, want: []string{"s3"}},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			listed, err := s.repo.ListSuggestions(s.ctx, "local-team", tc.status)
			s.Require().NoError(err)
			ids := make([]string, 0, len(listed))
			for _, item := range listed {
				ids = append(ids, item.ID)
			}
			s.Require().Equal(tc.want, ids)
		})
	}
}

func (s *RepositorySuite) TestSuggestionSaveUpserts() {
	original := todoapp.Suggestion{
		ID:        "s1",
		Status:    todoapp.SuggestionPending,
		UpdatedAt: time.Unix(100, 0),
	}
	s.Require().NoError(s.repo.SaveSuggestion(s.ctx, "local-team", original))

	updated := todoapp.Suggestion{
		ID:        "s1",
		Status:    todoapp.SuggestionAccepted,
		UpdatedAt: time.Unix(200, 0),
	}
	s.Require().NoError(s.repo.SaveSuggestion(s.ctx, "local-team", updated))

	loaded, err := s.repo.SuggestionByID(s.ctx, "local-team", "s1")
	s.Require().NoError(err)
	s.Require().Equal(updated, loaded)
}

func (s *RepositorySuite) TestSuggestionFingerprintsIncludeAllStatuses() {
	s.Require().NoError(s.repo.SaveSuggestion(s.ctx, "local-team", todoapp.Suggestion{
		ID:          "s1",
		Fingerprint: "n1",
		Status:      todoapp.SuggestionPending,
	}))
	s.Require().NoError(s.repo.SaveSuggestion(s.ctx, "local-team", todoapp.Suggestion{
		ID:          "s2",
		Fingerprint: "n2",
		Status:      todoapp.SuggestionDismissed,
	}))
	prints, err := s.repo.SuggestionFingerprints(s.ctx, "local-team")
	s.Require().NoError(err)
	s.Require().Len(prints, 2)
	_, ok := prints["n2"]
	s.Require().True(ok)
}

// TestScopeIsolationPerCall mirrors the Postgres adapter's isolation test:
// each scope's todos are only visible to calls made with that scope, since
// the in-memory repository keys its maps by scope to mimic the Postgres
// adapter's scope_id-per-row isolation.
func (s *RepositorySuite) TestScopeIsolationPerCall() {
	s.Require().NoError(s.repo.SaveTodo(s.ctx, "scope-a", todoapp.Todo{
		ID: "todo-a", Title: "A", Status: todoapp.TodoOpen,
	}))
	s.Require().NoError(s.repo.SaveTodo(s.ctx, "scope-b", todoapp.Todo{
		ID: "todo-b", Title: "B", Status: todoapp.TodoOpen,
	}))

	scopeATodos, err := s.repo.ListTodos(s.ctx, "scope-a", todoapp.TodoOpen)
	s.Require().NoError(err)
	s.Require().Len(scopeATodos, 1)
	s.Require().Equal("todo-a", scopeATodos[0].ID)

	scopeBTodos, err := s.repo.ListTodos(s.ctx, "scope-b", todoapp.TodoOpen)
	s.Require().NoError(err)
	s.Require().Len(scopeBTodos, 1)
	s.Require().Equal("todo-b", scopeBTodos[0].ID)
}

func (s *RepositorySuite) TestSuggestionFingerprintsDeduplicates() {
	// Same fingerprint saved twice should result in one entry
	s.Require().NoError(s.repo.SaveSuggestion(s.ctx, "local-team", todoapp.Suggestion{
		ID:          "s1",
		Fingerprint: "n1",
		Status:      todoapp.SuggestionPending,
	}))
	s.Require().NoError(s.repo.SaveSuggestion(s.ctx, "local-team", todoapp.Suggestion{
		ID:          "s2",
		Fingerprint: "n1",
		Status:      todoapp.SuggestionAccepted,
	}))
	prints, err := s.repo.SuggestionFingerprints(s.ctx, "local-team")
	s.Require().NoError(err)
	s.Require().Len(prints, 1)
	_, ok := prints["n1"]
	s.Require().True(ok)
}
