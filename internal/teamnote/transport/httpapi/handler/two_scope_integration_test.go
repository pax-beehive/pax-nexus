package handler_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/evidencelake"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractionqueue"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
	teamruntime "github.com/pax-beehive/pax-nexus/internal/teamnote/runtime"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/handler"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/router"
	"github.com/stretchr/testify/suite"
)

// twoScopeSuite is the M2 acceptance test for the SaaS Phase 2 satellite
// descope: one process, one *handler.Handler built with API-key mode
// (handler.StaticAPIKeys, the same constructor internal/app/wiring.go uses
// for legacy/cloud multi-tenant deployments), serving two scopes through two
// API keys. It extends the existing StaticAPIKeys integration harness
// (operationsHTTPIntegrationSuite in operations_integration_test.go) rather
// than building a new one: same per-suite Postgres schema, same
// teamruntime.New/extractionqueue wiring, same GeneratedRegister + ut request
// pattern. The only difference is the handler constructor (handler.New with
// two keys instead of handler.NewOnPrem with one human-session identity).
//
// Todo App and Page Wiki are deliberately not exercised here:
//   - buildTodoApp (internal/app/wiring.go) passes a nil HumanAuthenticator
//     in legacy API-key mode, and todoapphttp.New documents that a nil
//     authenticator makes every Todo App route answer 501 — there is no
//     API-key-mode path into Todo App to assert against.
//   - buildPageWikiHTTPHandler pins the Page Wiki HTTP transport's
//     Injector/Reader resolution to onprem.LocalScopeID as a deliberate
//     Phase 2 profile pin (per-request tenant resolution on that transport
//     is Phase 3 work), so even if it were wired into this harness it would
//     not demonstrate scope separation.
type twoScopeSuite struct {
	suite.Suite
	adminPool *pgxpool.Pool
	store     *postgres.Store
	schema    string
	handler   *handler.Handler
	queue     *extractionqueue.Queue
}

func TestTwoScopesAreIsolatedThroughAPIKeyMode(t *testing.T) {
	suite.Run(t, new(twoScopeSuite))
}

func (s *twoScopeSuite) SetupSuite() {
	dsn := os.Getenv("TEAM_MEMORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		s.T().Skip("TEAM_MEMORY_TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	adminPool, err := pgxpool.New(ctx, dsn)
	s.Require().NoError(err)
	s.adminPool = adminPool
	s.schema = fmt.Sprintf("two_scope_%d", time.Now().UnixNano())
	_, err = adminPool.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{s.schema}.Sanitize())
	s.Require().NoError(err)
	parsed, err := url.Parse(dsn)
	s.Require().NoError(err)
	query := parsed.Query()
	query.Set("search_path", s.schema+",public")
	parsed.RawQuery = query.Encode()
	store, err := postgres.Open(ctx, parsed.String())
	s.Require().NoError(err)
	s.Require().NoError(store.Migrate(ctx))
	s.store = store

	noteStore, err := postgres.NewNoteStore(
		store, teamnote.DefaultTTLPolicy(), teamnote.SystemClock{}, postgres.RetrievalConfig{},
	)
	s.Require().NoError(err)
	runtime, err := teamruntime.New(
		evidencelake.New(store.Sessions()), twoScopeExtractor{},
		teamruntime.Config{NoteStore: noteStore, Logger: slog.New(slog.DiscardHandler)},
	)
	s.Require().NoError(err)
	s.Require().NoError(extractionqueue.Migrate(ctx, store.Pool()))
	queue, err := extractionqueue.New(store.Pool(), runtime, extractionqueue.Config{
		QueuePrefix: fmt.Sprintf("two_scope_extract_%d", time.Now().UnixNano()),
		Shards:      1, MaxAttempts: 1, Debounce: 5 * time.Millisecond, BatchTimeout: 10 * time.Millisecond,
		JobTimeout: 10 * time.Second, SoftStopTimeout: 5 * time.Second,
		Logger: slog.New(slog.DiscardHandler),
	})
	s.Require().NoError(err)
	s.Require().NoError(store.Sessions().ConfigureExtractionEnqueuer(queue))
	s.Require().NoError(queue.Start(ctx))
	s.queue = queue

	// One handler instance, two API keys mapped to two scopes: this is the
	// exact shape internal/app/wiring.go's buildHTTPHandler uses for
	// TEAM_MEMORY_API_KEYS in production.
	configured, err := handler.New(
		runtime,
		handler.StaticAPIKeys{"key-a": "scope-a", "key-b": "scope-b"},
		slog.New(slog.DiscardHandler),
	)
	s.Require().NoError(err)
	s.handler = configured
}

func (s *twoScopeSuite) TearDownSuite() {
	if s.queue != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		s.NoError(s.queue.Stop(ctx))
		cancel()
	}
	if s.store != nil {
		s.store.Close()
	}
	if s.adminPool != nil {
		_, err := s.adminPool.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{s.schema}.Sanitize()+" CASCADE")
		s.NoError(err)
		s.adminPool.Close()
	}
}

func (s *twoScopeSuite) TestTwoScopesAreIsolatedThroughAPIKeyMode() {
	now := time.Now().UTC()

	// Observe a session under each key. Both use the same task_ref so that,
	// if the Phase 2 scope-per-call refactor regressed and leaked across
	// scopes, key-a's recall below would become eligible to see key-b's
	// note (and vice versa) purely on task_ref/thread_ref match.
	s.observe("key-a", "session-a", "scope-a-secret-alpha", now)
	s.observe("key-b", "session-b", "scope-b-secret-beta", now)

	s.Require().Eventually(func() bool {
		var countA, countB int64
		if err := s.store.Pool().QueryRow(context.Background(),
			`SELECT count(*) FROM team_notes WHERE scope_id = 'scope-a'`).Scan(&countA); err != nil {
			return false
		}
		if err := s.store.Pool().QueryRow(context.Background(),
			`SELECT count(*) FROM team_notes WHERE scope_id = 'scope-b'`).Scan(&countB); err != nil {
			return false
		}
		return countA == 1 && countB == 1
	}, 10*time.Second, 25*time.Millisecond, "each scope's extraction did not land exactly one note")

	// Recall under key-a must surface scope-a's secret and never scope-b's.
	recallA := s.recall("key-a", "recall-session-a")
	s.Contains(recallA, "scope-a-secret-alpha")
	s.NotContains(recallA, "scope-b-secret-beta")

	// Recall under key-b must surface scope-b's secret and never scope-a's.
	recallB := s.recall("key-b", "recall-session-b")
	s.Contains(recallB, "scope-b-secret-beta")
	s.NotContains(recallB, "scope-a-secret-alpha")
}

func (s *twoScopeSuite) observe(apiKey, sessionID, secret string, now time.Time) {
	body := fmt.Sprintf(`{
  "events":[{
    "id":%q,
    "actor":{"user_id":"owner","agent_id":"agent","session_id":%q},
    "sequence":1,
    "type":"message",
    "content":%q,
    "task_ref":"shared-release",
    "occurred_at":%q
  }],
  "complete":true
}`, "event-"+sessionID, sessionID, secret, now.Format(time.RFC3339Nano))
	response := s.request(http.MethodPost, "/v1/session-batches", body, apiKey)
	s.Require().Equal(http.StatusOK, response.Code, response.Body.String())
}

func (s *twoScopeSuite) recall(apiKey, sessionID string) string {
	body := fmt.Sprintf(`{
  "actor":{"user_id":"owner","agent_id":"agent","session_id":%q},
  "task_ref":"shared-release",
  "token_budget":256,
  "query":"secret",
  "max_items":3
}`, sessionID)
	response := s.request(http.MethodPost, "/v1/notes/recall", body, apiKey)
	s.Require().Equal(http.StatusOK, response.Code, response.Body.String())
	return response.Body.String()
}

func (s *twoScopeSuite) request(method, path, body, apiKey string) *ut.ResponseRecorder {
	hertz := server.New()
	hertz.Use(handler.InstanceMiddleware(s.handler))
	router.GeneratedRegister(hertz)
	requestBody := &ut.Body{Body: strings.NewReader(body), Len: len(body)}
	return ut.PerformRequest(hertz.Engine, method, path, requestBody,
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "Authorization", Value: "Bearer " + apiKey})
}

// twoScopeExtractor mirrors operationsIntegrationExtractor: it turns the
// slice's first event into a single blocker candidate carrying the event's
// own content and task_ref, so the resulting note is deterministically
// eligible for recall under the same task_ref.
type twoScopeExtractor struct{}

func (twoScopeExtractor) Extract(ctx context.Context, slice evidencelake.Slice) (extractor.Result, error) {
	if err := ctx.Err(); err != nil {
		return extractor.Result{}, fmt.Errorf("extract two-scope fixture: %w", err)
	}
	if len(slice.Events) == 0 {
		return extractor.Result{}, nil
	}
	event := slice.Events[0]
	return extractor.Result{Candidates: []teamnote.Candidate{{
		ID: "candidate-" + event.ID, Action: teamnote.ActionCreate, Kind: teamnote.KindBlocker,
		Subject: "deployment", Body: event.Content, TaskRef: event.TaskRef,
		Origin: event.Actor, EvidenceEventIDs: []string{event.ID},
	}}}, nil
}
