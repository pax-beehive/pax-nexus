package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	pagewikipostgres "github.com/pax-beehive/pax-nexus/internal/pagewiki/postgres"
	platformpostgres "github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/stretchr/testify/suite"
)

type repositoryManagerSuite struct {
	suite.Suite
	ctx    context.Context
	store  *platformpostgres.Store
	scopeA string
	scopeB string
}

func TestRepositoryManagerSuite(t *testing.T) {
	suite.Run(t, new(repositoryManagerSuite))
}

func (s *repositoryManagerSuite) SetupSuite() {
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

func (s *repositoryManagerSuite) TearDownSuite() {
	if s.store != nil {
		s.store.Close()
	}
}

func (s *repositoryManagerSuite) SetupTest() {
	nonce := time.Now().UnixNano()
	s.scopeA = fmt.Sprintf("pagewiki-repository-manager-a-%d", nonce)
	s.scopeB = fmt.Sprintf("pagewiki-repository-manager-b-%d", nonce)
}

func (s *repositoryManagerSuite) TearDownTest() {
	if s.store == nil {
		return
	}
	for _, scopeID := range []string{s.scopeA, s.scopeB} {
		for _, query := range []string{
			"DELETE FROM pagewiki_maintenance_runs WHERE scope_id = $1",
			"DELETE FROM pagewiki_publications WHERE scope_id = $1",
			"DELETE FROM pagewiki_source_revisions WHERE scope_id = $1",
			"DELETE FROM pagewiki_topic_trees WHERE scope_id = $1",
			"DELETE FROM pagewiki_page_lifecycle WHERE scope_id = $1",
			"DELETE FROM pagewiki_curation_runs WHERE scope_id = $1",
			"DELETE FROM pagewiki_page_embeddings WHERE scope_id = $1",
			"DELETE FROM pagewiki_type_registry WHERE scope_id = $1",
			"DELETE FROM session_processor_cursors WHERE scope_id = $1",
			"DELETE FROM session_events WHERE scope_id = $1",
			"DELETE FROM session_streams WHERE scope_id = $1",
		} {
			_, err := s.store.Pool().Exec(s.ctx, query, scopeID)
			s.Require().NoError(err)
		}
	}
}

func (s *repositoryManagerSuite) TestForScopeReturnsSameInstanceForSameScope() {
	manager, err := pagewikipostgres.NewRepositoryManager(s.store.Pool())
	s.Require().NoError(err)

	first, err := manager.ForScope(s.ctx, s.scopeA)
	s.Require().NoError(err)
	second, err := manager.ForScope(s.ctx, s.scopeA)
	s.Require().NoError(err)

	s.Same(first, second)
}

func (s *repositoryManagerSuite) TestForScopeReturnsDifferentInstancePerScope() {
	manager, err := pagewikipostgres.NewRepositoryManager(s.store.Pool())
	s.Require().NoError(err)

	a, err := manager.ForScope(s.ctx, s.scopeA)
	s.Require().NoError(err)
	b, err := manager.ForScope(s.ctx, s.scopeB)
	s.Require().NoError(err)

	s.NotSame(a, b)
}

func (s *repositoryManagerSuite) TestForScopeRejectsBlankScope() {
	manager, err := pagewikipostgres.NewRepositoryManager(s.store.Pool())
	s.Require().NoError(err)

	_, err = manager.ForScope(s.ctx, "")
	s.Require().Error(err)
}

func (s *repositoryManagerSuite) TestForScopeIsolatesMirrorPerScope() {
	manager, err := pagewikipostgres.NewRepositoryManager(s.store.Pool())
	s.Require().NoError(err)

	repositoryA, err := manager.ForScope(s.ctx, s.scopeA)
	s.Require().NoError(err)
	repositoryB, err := manager.ForScope(s.ctx, s.scopeB)
	s.Require().NoError(err)

	service := pagewiki.NewService(
		repositoryA,
		pagewiki.SessionDocumentPlanner{},
		pagewiki.SessionDocumentEditor{},
	)
	raw := "[event:runtime-event sequence:1 type:assistant] Runtime verification passed."
	_, err = service.InjectSession(s.ctx, pagewiki.InjectSessionRequest{
		SourceID:       fmt.Sprintf("session:%s:runtime-agent:runtime-session", s.scopeA),
		IdempotencyKey: "manual-1",
		Raw:            []byte(raw),
		Events: []pagewiki.SourceEventInput{{
			ID: "runtime-event", StartByte: 0, EndByte: len(raw),
		}},
	})
	s.Require().NoError(err)

	catalogA, err := repositoryA.PageCatalog(s.ctx)
	s.Require().NoError(err)
	s.Require().Len(catalogA, 1)

	catalogB, err := repositoryB.PageCatalog(s.ctx)
	s.Require().NoError(err)
	s.Require().Empty(catalogB)
}

func TestNewRepositoryManagerRequiresPool(t *testing.T) {
	_, err := pagewikipostgres.NewRepositoryManager(nil)
	if err == nil {
		t.Fatal("expected error when pool is nil")
	}
}
