package handler_test

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/operations"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/mocks"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/handler"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/router"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"
)

type operationsHandlerSuite struct {
	suite.Suite
	controller *gomock.Controller
	identity   *humanIdentityService
	operations *operationsLifecycle
	handler    *handler.Handler
}

func TestOperationsHandlerSuite(t *testing.T) {
	suite.Run(t, new(operationsHandlerSuite))
}

func (s *operationsHandlerSuite) SetupTest() {
	s.controller = gomock.NewController(s.T())
	s.identity = &humanIdentityService{principal: onprem.HumanPrincipal{
		UserID: "admin-user", MembershipID: "admin-membership", Role: onprem.RoleAdmin,
		MembershipStatus: onprem.MembershipStatusActive,
	}}
	s.operations = &operationsLifecycle{now: time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)}
	configured, err := handler.NewOnPrem(
		mocks.NewMockRuntime(s.controller), &credentialService{}, &memoryService{}, &channelService{},
		slog.New(slog.DiscardHandler),
		handler.WithAgentRegistry(&agentRegistryService{}),
		handler.WithHumanIdentity(s.identity, &oidcService{}, "/portal", false),
		handler.WithOperations(s.operations, &operationsRecorder{}),
	)
	s.Require().NoError(err)
	s.handler = configured
}

func (s *operationsHandlerSuite) TestAdminReadsAllOperationsViewsThroughGeneratedRoutes() {
	summary := s.perform(http.MethodGet,
		"/v1/admin/operations/summary?from=2026-07-22T10:00:00Z&to=2026-07-22T12:00:00Z&agent_id=agent-1")
	s.Equal(consts.StatusOK, summary.Code)
	s.Contains(summary.Body.String(), `"events_written":3`)
	s.Equal("agent-1", s.operations.summaryFilter.AgentID)

	events := s.perform(http.MethodGet, "/v1/admin/operations/events?operation_kind=memory.search&outcome=succeeded&limit=1")
	s.Equal(consts.StatusOK, events.Code)
	s.Contains(events.Body.String(), `"operation_kind":"memory.search"`)
	s.Contains(events.Body.String(), `"next_cursor":`)
	s.Contains(events.Body.String(), `"generated_at":`)
	s.Equal(operations.KindMemorySearch, s.operations.eventFilter.Kind)

	recall := s.perform(http.MethodGet, "/v1/admin/operations/recalls/41")
	s.Equal(consts.StatusOK, recall.Code)
	s.Contains(recall.Body.String(), `"observation_id":41`)
	s.Contains(recall.Body.String(), `"budget_drop_counts":{"token_budget":1}`)
	s.NotContains(recall.Body.String(), "secret query")
	s.NotContains(recall.Body.String(), `"text"`)

	storage := s.perform(http.MethodGet, "/v1/admin/operations/storage")
	s.Equal(consts.StatusOK, storage.Code)
	s.Contains(storage.Body.String(), `"component":"session_lake"`)
	s.Contains(storage.Body.String(), `"component":"team_memory"`)

	history := s.perform(http.MethodGet, "/v1/admin/operations/storage/history?limit=1")
	s.Equal(consts.StatusOK, history.Code)
	s.Contains(history.Body.String(), `"next_cursor":`)
}

func (s *operationsHandlerSuite) TestExpiredRecallDiagnosticUsesStableGoneResponse() {
	s.operations.recallErr = operations.ErrRecallExpired

	response := s.perform(http.MethodGet, "/v1/admin/operations/recalls/41")

	s.Equal(consts.StatusGone, response.Code)
	s.JSONEq(
		`{"code":"diagnostic_expired","message":"the requested diagnostic has expired"}`,
		response.Body.String(),
	)
}

func (s *operationsHandlerSuite) TestOperationsRoutesValidateFiltersAndRole() {
	tests := []struct {
		name string
		path string
	}{
		{name: "invalid time", path: "/v1/admin/operations/summary?from=tomorrow"},
		{name: "unknown kind", path: "/v1/admin/operations/events?operation_kind=memory.erase"},
		{name: "unknown outcome", path: "/v1/admin/operations/events?outcome=partial"},
		{name: "invalid limit", path: "/v1/admin/operations/storage/history?limit=101"},
		{name: "invalid cursor", path: "/v1/admin/operations/events?cursor=not-a-cursor"},
		{name: "invalid observation", path: "/v1/admin/operations/recalls/not-a-number"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			response := s.perform(http.MethodGet, test.path)
			s.Equal(consts.StatusBadRequest, response.Code)
			s.JSONEq(`{"code":"invalid_request","message":"the request is invalid"}`, response.Body.String())
		})
	}

	s.identity.principal.Role = onprem.RoleMember
	response := s.perform(http.MethodGet, "/v1/admin/operations/summary")
	s.Equal(consts.StatusForbidden, response.Code)
	s.Contains(response.Body.String(), `"code":"forbidden"`)

	hertz := server.New()
	hertz.Use(handler.InstanceMiddleware(s.handler))
	router.GeneratedRegister(hertz)
	agentResponse := ut.PerformRequest(hertz.Engine, http.MethodGet, "/v1/admin/operations/summary", nil,
		ut.Header{Key: "Authorization", Value: "Bearer agent"})
	s.Equal(consts.StatusUnauthorized, agentResponse.Code)
}

func (s *operationsHandlerSuite) perform(method, path string) *ut.ResponseRecorder {
	hertz := server.New()
	hertz.Use(handler.InstanceMiddleware(s.handler))
	router.GeneratedRegister(hertz)
	return ut.PerformRequest(hertz.Engine, method, path, nil,
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "Cookie", Value: "tm_human_session=session"})
}

type operationsLifecycle struct {
	now           time.Time
	summaryFilter operations.TimeFilter
	eventFilter   operations.EventFilter
	recallErr     error
}

func (s *operationsLifecycle) Summary(
	_ context.Context,
	principal onprem.HumanPrincipal,
	filter operations.TimeFilter,
) (operations.Summary, error) {
	if !operationsRoleAllowed(principal) {
		return operations.Summary{}, onprem.ErrForbidden
	}
	s.summaryFilter = filter
	return operations.Summary{
		From: filter.From, To: filter.To, GeneratedAt: s.now,
		Observations: operations.ObservationSummary{Requests: 4, Succeeded: 4, EventsWritten: 3, DuplicateEvents: 1},
		Extraction:   operations.ExtractionSummary{Runs: 2, Completed: 1, UnextractedEvents: 2},
		Recalls:      operations.RecallSummary{Requests: 5, Succeeded: 4, WithEvidence: 3, MemoryHits: 7},
		Latency:      operations.LatencySummary{SampleCount: 9}, Errors: 1,
	}, nil
}

func (s *operationsLifecycle) ListEvents(
	_ context.Context,
	principal onprem.HumanPrincipal,
	filter operations.EventFilter,
) ([]operations.Event, error) {
	if !operationsRoleAllowed(principal) {
		return nil, onprem.ErrForbidden
	}
	s.eventFilter = filter
	if filter.Cursor != "" {
		if _, _, err := operations.DecodeCursor(filter.Cursor); err != nil {
			return nil, err
		}
	}
	return []operations.Event{
		operationsTestEvent(12, s.now), operationsTestEvent(11, s.now.Add(-time.Minute)),
	}, nil
}

func (s *operationsLifecycle) GetRecallDiagnostic(
	_ context.Context,
	principal onprem.HumanPrincipal,
	observationID int64,
) (operations.RecallDiagnostic, error) {
	if !operationsRoleAllowed(principal) {
		return operations.RecallDiagnostic{}, onprem.ErrForbidden
	}
	if s.recallErr != nil {
		return operations.RecallDiagnostic{}, s.recallErr
	}
	return operations.RecallDiagnostic{
		ObservationID: observationID, OccurredAt: s.now, AgentID: "agent-1", SessionID: "session-1",
		EvidenceSufficient: true, ReasonCodes: []string{"fact_coverage"}, LanesExecuted: []string{"lexical"},
		Candidates: 4, FusionKept: 2, PlannedNotes: 1, PlannedTokens: 32, DeliveredItems: 1,
		DispositionCounts: map[string]int64{"evidence": 1}, BudgetDropCounts: map[string]int64{"token_budget": 1},
	}, nil
}

func (s *operationsLifecycle) LatestStorage(
	_ context.Context,
	principal onprem.HumanPrincipal,
) (operations.StorageSnapshot, error) {
	if !operationsRoleAllowed(principal) {
		return operations.StorageSnapshot{}, onprem.ErrForbidden
	}
	return operationsTestStorage(7, s.now), nil
}

func (s *operationsLifecycle) ListStorage(
	_ context.Context,
	principal onprem.HumanPrincipal,
	_ operations.StorageFilter,
) ([]operations.StorageSnapshot, error) {
	if !operationsRoleAllowed(principal) {
		return nil, onprem.ErrForbidden
	}
	return []operations.StorageSnapshot{
		operationsTestStorage(7, s.now), operationsTestStorage(6, s.now.Add(-time.Hour)),
	}, nil
}

func operationsRoleAllowed(principal onprem.HumanPrincipal) bool {
	return principal.Role == onprem.RoleOwner || principal.Role == onprem.RoleAdmin
}

func operationsTestEvent(id int64, startedAt time.Time) operations.Event {
	return operations.Event{
		OperationEventID: id, AttemptID: "op-attempt", Kind: operations.KindMemorySearch,
		Outcome: operations.OutcomeSucceeded, Actor: operations.Actor{Kind: "agent", AgentID: "agent-1"},
		StartedAt: startedAt, CompletedAt: startedAt.Add(10 * time.Millisecond), DurationMS: 10,
		ResultItems: 1, EvidenceItems: 1,
	}
}

func operationsTestStorage(id int64, capturedAt time.Time) operations.StorageSnapshot {
	return operations.StorageSnapshot{
		SnapshotID: id, SchemaVersion: 1, CapturedAt: capturedAt, Status: "complete",
		DatabasePhysicalBytes: 1000, Components: []operations.StorageComponent{
			{Component: "session_lake", Counts: map[string]int64{"events": 4}, LogicalBytes: 100},
			{Component: "team_memory", Counts: map[string]int64{"notes": 2}, LogicalBytes: 200},
		},
	}
}

type operationsRecorder struct {
	events []operations.Event
	err    error
}

func (r *operationsRecorder) Record(_ context.Context, event operations.Event) (operations.Event, error) {
	if r.err != nil {
		return operations.Event{}, r.err
	}
	event.OperationEventID = int64(len(r.events) + 1)
	r.events = append(r.events, event)
	return event, nil
}
