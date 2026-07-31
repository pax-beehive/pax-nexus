package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	platformpostgres "github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/pax-beehive/pax-nexus/internal/todoapp"
	todoapppostgres "github.com/pax-beehive/pax-nexus/internal/todoapp/postgres"
	"github.com/stretchr/testify/suite"
)

type repositorySuite struct {
	suite.Suite
	ctx     context.Context
	store   *platformpostgres.Store
	scopeID string
	repo    *todoapppostgres.Repository
}

func TestRepositorySuite(t *testing.T) {
	suite.Run(t, new(repositorySuite))
}

func (s *repositorySuite) SetupSuite() {
	dsn := os.Getenv("TEAM_MEMORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		s.T().Skip("TEAM_MEMORY_TEST_POSTGRES_DSN is not configured")
	}
	s.ctx = context.Background()
	var err error
	s.store, err = platformpostgres.Open(s.ctx, dsn)
	s.Require().NoError(err)
	s.Require().NoError(s.store.Migrate(s.ctx))
}

func (s *repositorySuite) TearDownSuite() {
	if s.store != nil {
		s.store.Close()
	}
}

func (s *repositorySuite) SetupTest() {
	s.scopeID = fmt.Sprintf("todoapp-repository-%d", time.Now().UnixNano())
	var err error
	s.repo, err = todoapppostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
}

func (s *repositorySuite) TearDownTest() {
	if s.store == nil || s.scopeID == "" {
		return
	}
	for _, query := range []string{
		"DELETE FROM todoapp_suggestions WHERE scope_id = $1",
		"DELETE FROM todoapp_todos WHERE scope_id = $1",
	} {
		_, err := s.store.Pool().Exec(s.ctx, query, s.scopeID)
		s.Require().NoError(err)
	}
}

func (s *repositorySuite) TestTodoRoundtripAndNotFound() {
	todo := todoapp.Todo{
		ID:        "t1",
		Title:     "Fix provider credential",
		Status:    todoapp.TodoOpen,
		Source:    todoapp.TodoSourceManual,
		CreatedBy: "user-1",
		CreatedAt: time.Unix(100, 0).UTC(),
		UpdatedAt: time.Unix(100, 0).UTC(),
	}
	s.Require().NoError(s.repo.SaveTodo(s.ctx, todo))
	loaded, err := s.repo.TodoByID(s.ctx, "t1")
	s.Require().NoError(err)
	s.Require().Equal(todo, loaded)
	_, err = s.repo.TodoByID(s.ctx, "missing")
	s.Require().ErrorIs(err, todoapp.ErrNotFound)
}

func (s *repositorySuite) TestListTodosFiltersByStatus() {
	s.Require().NoError(s.repo.SaveTodo(s.ctx, todoapp.Todo{
		ID:        "t1",
		Title:     "a",
		Status:    todoapp.TodoOpen,
		UpdatedAt: time.Unix(200, 0).UTC(),
	}))
	s.Require().NoError(s.repo.SaveTodo(s.ctx, todoapp.Todo{
		ID:        "t2",
		Title:     "b",
		Status:    todoapp.TodoDone,
		UpdatedAt: time.Unix(300, 0).UTC(),
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
			listed, err := s.repo.ListTodos(s.ctx, tc.status)
			s.Require().NoError(err)
			ids := make([]string, 0, len(listed))
			for _, item := range listed {
				ids = append(ids, item.ID)
			}
			s.Require().Equal(tc.want, ids)
		})
	}
}

func (s *repositorySuite) TestTodoSaveUpserts() {
	original := todoapp.Todo{
		ID:        "t1",
		Title:     "Original",
		Status:    todoapp.TodoOpen,
		UpdatedAt: time.Unix(100, 0).UTC(),
	}
	s.Require().NoError(s.repo.SaveTodo(s.ctx, original))

	updated := todoapp.Todo{
		ID:        "t1",
		Title:     "Updated",
		Status:    todoapp.TodoDone,
		UpdatedAt: time.Unix(200, 0).UTC(),
	}
	s.Require().NoError(s.repo.SaveTodo(s.ctx, updated))

	loaded, err := s.repo.TodoByID(s.ctx, "t1")
	s.Require().NoError(err)
	s.Require().Equal(updated, loaded)

	listed, err := s.repo.ListTodos(s.ctx, "")
	s.Require().NoError(err)
	s.Require().Len(listed, 1, "upsert must not create a duplicate row")
}

func (s *repositorySuite) TestTodoListOrderingByUpdatedAtAndID() {
	// Insert with same UpdatedAt to test tie-break by ID.
	t := time.Unix(300, 0).UTC()
	s.Require().NoError(s.repo.SaveTodo(s.ctx, todoapp.Todo{ID: "t1", Title: "a", UpdatedAt: t}))
	s.Require().NoError(s.repo.SaveTodo(s.ctx, todoapp.Todo{ID: "t3", Title: "b", UpdatedAt: t}))
	s.Require().NoError(s.repo.SaveTodo(s.ctx, todoapp.Todo{ID: "t2", Title: "c", UpdatedAt: t}))

	listed, err := s.repo.ListTodos(s.ctx, "")
	s.Require().NoError(err)
	ids := make([]string, len(listed))
	for i, item := range listed {
		ids[i] = item.ID
	}
	s.Require().Equal([]string{"t3", "t2", "t1"}, ids, "should sort by ID descending when UpdatedAt is equal")
}

func (s *repositorySuite) TestTodoListOrderingPreservesSubsecondPrecision() {
	t1 := time.Unix(200, 0).UTC()
	t2 := time.Unix(200, 500_000_000).UTC() // 200.5 seconds, exactly representable at microsecond precision
	s.Require().NoError(s.repo.SaveTodo(s.ctx, todoapp.Todo{ID: "t1", Title: "older", UpdatedAt: t1}))
	s.Require().NoError(s.repo.SaveTodo(s.ctx, todoapp.Todo{ID: "t2", Title: "newer", UpdatedAt: t2}))

	listed, err := s.repo.ListTodos(s.ctx, "")
	s.Require().NoError(err)
	s.Require().Len(listed, 2)
	s.Require().Equal("t2", listed[0].ID)
	s.Require().Equal("t1", listed[1].ID)
}

func (s *repositorySuite) TestSuggestionRoundtrip() {
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
	s.Require().NoError(s.repo.SaveSuggestion(s.ctx, suggestion))
	loaded, err := s.repo.SuggestionByID(s.ctx, "s1")
	s.Require().NoError(err)
	s.Require().Equal(suggestion, loaded)
	_, err = s.repo.SuggestionByID(s.ctx, "missing")
	s.Require().ErrorIs(err, todoapp.ErrNotFound)
}

func (s *repositorySuite) TestListSuggestionsFiltersByStatus() {
	s.Require().NoError(s.repo.SaveSuggestion(s.ctx, todoapp.Suggestion{
		ID: "s1", Fingerprint: "n1", Status: todoapp.SuggestionPending, UpdatedAt: time.Unix(200, 0).UTC(),
	}))
	s.Require().NoError(s.repo.SaveSuggestion(s.ctx, todoapp.Suggestion{
		ID: "s2", Fingerprint: "n2", Status: todoapp.SuggestionAccepted, UpdatedAt: time.Unix(300, 0).UTC(),
	}))
	s.Require().NoError(s.repo.SaveSuggestion(s.ctx, todoapp.Suggestion{
		ID: "s3", Fingerprint: "n3", Status: todoapp.SuggestionDismissed, UpdatedAt: time.Unix(250, 0).UTC(),
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
			listed, err := s.repo.ListSuggestions(s.ctx, tc.status)
			s.Require().NoError(err)
			ids := make([]string, 0, len(listed))
			for _, item := range listed {
				ids = append(ids, item.ID)
			}
			s.Require().Equal(tc.want, ids)
		})
	}
}

func (s *repositorySuite) TestSuggestionSaveUpserts() {
	original := todoapp.Suggestion{
		ID: "s1", Fingerprint: "n1", Status: todoapp.SuggestionPending, UpdatedAt: time.Unix(100, 0).UTC(),
	}
	s.Require().NoError(s.repo.SaveSuggestion(s.ctx, original))

	updated := todoapp.Suggestion{
		ID: "s1", Fingerprint: "n1", Status: todoapp.SuggestionAccepted, UpdatedAt: time.Unix(200, 0).UTC(),
	}
	s.Require().NoError(s.repo.SaveSuggestion(s.ctx, updated))

	loaded, err := s.repo.SuggestionByID(s.ctx, "s1")
	s.Require().NoError(err)
	s.Require().Equal(updated, loaded)

	listed, err := s.repo.ListSuggestions(s.ctx, "")
	s.Require().NoError(err)
	s.Require().Len(listed, 1, "upsert must not create a duplicate row")
}

func (s *repositorySuite) TestSuggestionFingerprintsIncludeAllStatuses() {
	s.Require().NoError(s.repo.SaveSuggestion(s.ctx, todoapp.Suggestion{
		ID: "s1", Fingerprint: "n1", Status: todoapp.SuggestionPending,
	}))
	s.Require().NoError(s.repo.SaveSuggestion(s.ctx, todoapp.Suggestion{
		ID: "s2", Fingerprint: "n2", Status: todoapp.SuggestionDismissed,
	}))
	s.Require().NoError(s.repo.SaveSuggestion(s.ctx, todoapp.Suggestion{
		ID: "s3", Fingerprint: "n3", Status: todoapp.SuggestionAccepted,
	}))
	prints, err := s.repo.SuggestionFingerprints(s.ctx)
	s.Require().NoError(err)
	s.Require().Len(prints, 3)
	for _, fp := range []string{"n1", "n2", "n3"} {
		_, ok := prints[fp]
		s.Require().True(ok, "expected fingerprint %q to be present", fp)
	}
}

func (s *repositorySuite) TestNewRepositoryRequiresPoolAndScope() {
	_, err := todoapppostgres.NewRepository(s.ctx, nil, "scope")
	s.Require().Error(err)
	_, err = todoapppostgres.NewRepository(s.ctx, s.store.Pool(), "")
	s.Require().Error(err)
}
