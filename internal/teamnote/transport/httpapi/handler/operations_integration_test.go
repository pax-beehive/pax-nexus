package handler_test

import (
	"context"
	"encoding/json"
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
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/pax-beehive/pax-nexus/internal/recall"
	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractionqueue"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
	teamruntime "github.com/pax-beehive/pax-nexus/internal/teamnote/runtime"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/handler"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/router"
	"github.com/stretchr/testify/suite"
)

type operationsHTTPIntegrationSuite struct {
	suite.Suite
	adminPool    *pgxpool.Pool
	store        *postgres.Store
	schema       string
	sessionToken string
	handler      *handler.Handler
	queue        *extractionqueue.Queue
}

func TestOperationsHTTPIntegrationSuite(t *testing.T) {
	suite.Run(t, new(operationsHTTPIntegrationSuite))
}

func (s *operationsHTTPIntegrationSuite) SetupSuite() {
	dsn := os.Getenv("TEAM_MEMORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		s.T().Skip("TEAM_MEMORY_TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	adminPool, err := pgxpool.New(ctx, dsn)
	s.Require().NoError(err)
	s.adminPool = adminPool
	s.schema = fmt.Sprintf("operations_http_%d", time.Now().UnixNano())
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

	identity, err := onprem.NewIdentityService(store.Identity(), onprem.IdentityConfig{
		BootstrapSecret: "bootstrap", SecretPepper: "0123456789abcdef0123456789abcdef",
		SessionTTL: time.Hour, InvitationTTL: time.Hour,
	})
	s.Require().NoError(err)
	session, err := identity.Login(ctx, onprem.ExternalIdentity{
		Issuer: "https://identity.test", Subject: "operations-owner", Email: "owner@example.com", EmailVerified: true,
	})
	s.Require().NoError(err)
	_, err = identity.ClaimBootstrap(ctx, session.Principal, "bootstrap")
	s.Require().NoError(err)
	s.sessionToken = session.Token
	operationsService, err := onprem.NewOperationsService(store.Operations(), onprem.OperationsConfig{
		EventRetention: 7 * 24 * time.Hour, StorageRetention: 90 * 24 * time.Hour,
	})
	s.Require().NoError(err)
	noteStore, err := postgres.NewNoteStore(
		store, teamnote.DefaultTTLPolicy(), teamnote.SystemClock{}, postgres.RetrievalConfig{},
	)
	s.Require().NoError(err)
	runtime, err := teamruntime.New(
		sessionlake.New(store.Sessions()), operationsIntegrationExtractor{},
		teamruntime.Config{
			NoteStore: noteStore, Logger: slog.New(slog.DiscardHandler),
			ExtractionObserver: onprem.NewExtractionObserver(store.Operations(), slog.New(slog.DiscardHandler)),
		},
	)
	s.Require().NoError(err)
	s.Require().NoError(extractionqueue.Migrate(ctx, store.Pool()))
	queue, err := extractionqueue.New(store.Pool(), runtime, extractionqueue.Config{
		QueuePrefix: fmt.Sprintf("operations_http_extract_%d", time.Now().UnixNano()),
		Shards:      1, MaxAttempts: 1, Debounce: 5 * time.Millisecond, BatchTimeout: 10 * time.Millisecond,
		JobTimeout: 10 * time.Second, SoftStopTimeout: 5 * time.Second,
		Logger: slog.New(slog.DiscardHandler),
	})
	s.Require().NoError(err)
	s.Require().NoError(store.Sessions().ConfigureExtractionEnqueuer(queue))
	s.Require().NoError(queue.Start(ctx))
	s.queue = queue
	memory, err := recall.NewRouter(runtime, nil, recall.Config{})
	s.Require().NoError(err)
	configured, err := handler.NewOnPrem(
		runtime, &credentialService{}, memory, &channelService{},
		slog.New(slog.DiscardHandler),
		handler.WithHumanIdentity(identity, &oidcService{}, "/portal", false),
		handler.WithOperations(operationsService, store.Operations()),
	)
	s.Require().NoError(err)
	s.handler = configured
}

func (s *operationsHTTPIntegrationSuite) TearDownSuite() {
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

func (s *operationsHTTPIntegrationSuite) TestWriteExtractionRecallAndAdminOperationsEndToEnd() {
	now := time.Now().UTC()
	observation := s.requestJSON(http.MethodPost, "/v1/observations", fmt.Sprintf(`{
  "session_id":"session-e2e",
  "events":[{
    "id":"event-e2e",
    "sequence":1,
    "type":"message",
    "content":"secret deployment blocker",
    "task_ref":"release-e2e",
    "occurred_at":%q
  }],
  "complete":true
}`, now.Format(time.RFC3339Nano)), "Authorization", "Bearer agent")
	s.InDelta(1, observation["accepted"], 0)
	s.Require().Eventually(func() bool {
		var noteCount int64
		var extractionEvents int64
		err := s.store.Pool().QueryRow(context.Background(), `SELECT count(*) FROM team_notes`).Scan(&noteCount)
		if err != nil {
			return false
		}
		err = s.store.Pool().QueryRow(context.Background(), `
SELECT count(*) FROM onprem_operation_events
WHERE operation_kind = 'extraction.run' AND outcome = 'succeeded'`).Scan(&extractionEvents)
		return err == nil && noteCount == 1 && extractionEvents == 1
	}, 10*time.Second, 25*time.Millisecond, "River worker did not extract and record its slice")

	search := s.requestJSON(http.MethodPost, "/v1/memory/search", `{
  "intent":"passive",
  "session_id":"consumer-session",
  "task_ref":"release-e2e",
	  "query":"secret deployment blocker",
  "token_budget":256,
  "max_items":3
}`, "Authorization", "Bearer agent")
	hits := operationsArrayField(s.T(), search, "hits")
	s.Require().NotEmpty(hits)
	s.Contains(fmt.Sprint(hits[0]), "secret deployment blocker")

	compatibilityRecall := s.requestJSON(http.MethodPost, "/v1/notes/recall", `{
  "actor":{"user_id":"spoofed","agent_id":"spoofed","session_id":"compat-session"},
  "task_ref":"release-e2e",
  "query":"deployment blocker",
  "token_budget":256,
  "max_items":3
}`, "Authorization", "Bearer agent")
	s.Contains(compatibilityRecall, "items")

	_, err := s.store.Operations().CaptureStorage(context.Background(), time.Now().UTC())
	s.Require().NoError(err)

	summary := s.request("/v1/admin/operations/summary")
	observations := operationsObjectField(s.T(), summary, "observations")
	s.InDelta(1, observations["requests"], 0)
	s.InDelta(1, observations["events_written"], 0)
	recalls := operationsObjectField(s.T(), summary, "recalls")
	s.InDelta(2, recalls["requests"], 0)
	s.InDelta(1, recalls["memory_search_requests"], 0)
	s.InDelta(1, recalls["team_note_recall_requests"], 0)
	s.InDelta(1, recalls["evidence_hits"], 0)
	extraction := operationsObjectField(s.T(), summary, "extraction")
	s.InDelta(1, extraction["runs"], 0)
	latency := operationsObjectField(s.T(), summary, "latency")
	s.InDelta(2, latency["sample_count"], 0)
	s.Contains(latency, "p50_ms")
	s.NotContains(latency, "p95_ms")

	events := s.request("/v1/admin/operations/events")
	s.NotEmpty(events["generated_at"])
	eventRows := operationsArrayField(s.T(), events, "events")
	s.GreaterOrEqual(len(eventRows), 3)
	observationID := operationDetailID(s.T(), eventRows, "memory.search")
	s.NotEmpty(observationID)

	storage := operationsObjectField(s.T(), s.request("/v1/admin/operations/storage"), "storage")
	s.NotEmpty(operationsArrayField(s.T(), storage, "components"))

	recall := s.request("/v1/admin/operations/recalls/" + observationID)
	encoded, err := json.Marshal(recall)
	s.Require().NoError(err)
	s.NotContains(string(encoded), "secret deployment blocker")
	s.NotContains(string(encoded), "secret deployment blocker")
	s.Contains(string(encoded), `"candidates":`)
}

func (s *operationsHTTPIntegrationSuite) request(path string) map[string]any {
	return s.requestJSON(http.MethodGet, path, "", "Cookie", "tm_human_session="+s.sessionToken)
}

func (s *operationsHTTPIntegrationSuite) requestJSON(
	method string,
	path string,
	body string,
	headerKey string,
	headerValue string,
) map[string]any {
	hertz := server.New()
	hertz.Use(handler.InstanceMiddleware(s.handler))
	router.GeneratedRegister(hertz)
	var requestBody *ut.Body
	if body != "" {
		requestBody = &ut.Body{Body: strings.NewReader(body), Len: len(body)}
	}
	response := ut.PerformRequest(hertz.Engine, method, path, requestBody,
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: headerKey, Value: headerValue})
	s.Require().Equal(http.StatusOK, response.Code, response.Body.String())
	result := make(map[string]any)
	s.Require().NoError(json.Unmarshal(response.Body.Bytes(), &result))
	return result
}

type operationsIntegrationExtractor struct{}

func (operationsIntegrationExtractor) Extract(
	ctx context.Context,
	slice sessionlake.Slice,
) (extractor.Result, error) {
	if err := ctx.Err(); err != nil {
		return extractor.Result{}, fmt.Errorf("extract operations integration fixture: %w", err)
	}
	if len(slice.Events) == 0 {
		return extractor.Result{}, nil
	}
	event := slice.Events[0]
	return extractor.Result{Candidates: []teamnote.Candidate{{
		ID: "candidate-e2e", Action: teamnote.ActionCreate, Kind: teamnote.KindBlocker,
		Subject: "deployment", Body: event.Content, TaskRef: event.TaskRef,
		Origin: event.Actor, EvidenceEventIDs: []string{event.ID},
	}}}, nil
}

func operationDetailID(t *testing.T, events []any, operationKind string) string {
	t.Helper()
	for _, value := range events {
		event, ok := value.(map[string]any)
		if ok && event["operation_kind"] == operationKind {
			return fmt.Sprint(event["detail_id"])
		}
	}
	t.Fatalf("operation %s has no diagnostic detail: %#v", operationKind, events)
	return ""
}

func operationsObjectField(t *testing.T, value map[string]any, name string) map[string]any {
	t.Helper()
	result, ok := value[name].(map[string]any)
	if !ok {
		t.Fatalf("field %s is not an object: %#v", name, value[name])
	}
	return result
}

func operationsArrayField(t *testing.T, value map[string]any, name string) []any {
	t.Helper()
	result, ok := value[name].([]any)
	if !ok {
		t.Fatalf("field %s is not an array: %#v", name, value[name])
	}
	return result
}
